// Copyright 2026 FanYaNan. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package oss

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/google/uuid"
)

// SQLStorage 共享 SQL 存储实现(MySQL/SQLite 共用 CRUD 逻辑)
type SQLStorage struct {
	db         *sql.DB
	driver     string // mysql / sqlite,记录以区分 UPSERT 等 SQL 方言
	encryptKey string
}

// isMySQL 判断当前驱动是否为 MySQL(用于 UPSERT 等方言分支)
func (s *SQLStorage) isMySQL() bool { return s.driver == "mysql" }

// NewSQLStorage 创建 SQL 存储(不含连接,连接由 Init 建立)
func NewSQLStorage() *SQLStorage { return &SQLStorage{} }

// appendSQLitePragmas 向 DSN 追加并发写所需的连接级 PRAGMA：
// busy_timeout 缓解写锁冲突，WAL 提升读写并行度，NORMAL 同步级别为 WAL 推荐值
func appendSQLitePragmas(dsn string) string {
	pragmas := "_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	if strings.Contains(dsn, "?") {
		return dsn + "&" + pragmas
	}
	return dsn + "?" + pragmas
}

// Init 按 driver 打开连接并建表: driver ∈ {mysql, sqlite}
func (s *SQLStorage) Init(config map[string]interface{}) error {
	driver, _ := config["driver"].(string)
	dsn, _ := config["dsn"].(string)
	s.encryptKey, _ = config["encrypt_key"].(string)
	if driver != "mysql" && driver != "sqlite" {
		return fmt.Errorf("unsupported sql driver: %s", driver)
	}
	if driver == "sqlite" {
		// busy_timeout/WAL 为连接级参数,须经 DSN 传入才能覆盖连接池中每个新连接
		dsn = appendSQLitePragmas(dsn)
	}
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return fmt.Errorf("open %s: %w", driver, err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return fmt.Errorf("ping %s: %w", driver, err)
	}
	s.driver = driver
	s.db = db
	if driver == "mysql" {
		err = mysqlCreateTables(db)
	} else {
		err = sqliteCreateTables(db)
	}
	if err != nil {
		_ = db.Close()
		s.driver = ""
		s.db = nil
		return fmt.Errorf("create tables: %w", err)
	}
	if driver == "mysql" {
		err = migrateMySQLAdminUserColumns(db)
	} else {
		err = migrateSQLiteAdminUserColumns(db)
	}
	if err != nil {
		_ = db.Close()
		s.driver = ""
		s.db = nil
		return fmt.Errorf("migrate admin users: %w", err)
	}
	if err := seedPrivacyRules(db); err != nil {
		_ = db.Close()
		s.driver = ""
		s.db = nil
		return fmt.Errorf("seed privacy rules: %w", err)
	}
	if err := seedRBAC(db); err != nil {
		_ = db.Close()
		s.driver = ""
		s.db = nil
		return fmt.Errorf("seed rbac: %w", err)
	}
	return nil
}

// seedPrivacyRules privacy_rules 空表时写入内置规则种子；已有数据则跳过（重启不重复插入）
func seedPrivacyRules(db *sql.DB) error {
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM privacy_rules").Scan(&count); err != nil {
		return fmt.Errorf("count privacy rules: %w", err)
	}
	if count > 0 {
		return nil
	}
	now := timeToMS(time.Now())
	for _, r := range plugin.DefaultPrivacyRules() {
		if _, err := db.Exec(
			"INSERT INTO privacy_rules (id, rule_type, name, pattern, replacement, scope, enabled, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
			uuid.NewString(), r.RuleType, r.Name, r.Pattern, r.Replacement, r.Scope, boolToInt(r.Enabled), now, now,
		); err != nil {
			return fmt.Errorf("seed privacy rule %s: %w", r.Name, err)
		}
	}
	return nil
}

// ===== 时间与 JSON 转换 =====

func timeToMS(t time.Time) int64  { return t.UnixMilli() }
func msToTime(ms int64) time.Time { return time.UnixMilli(ms) }
func timePtrToMS(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	v := t.UnixMilli()
	return &v
}
func msToTimePtr(ms *int64) *time.Time {
	if ms == nil {
		return nil
	}
	v := time.UnixMilli(*ms)
	return &v
}

func marshalJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// ===== API Key 管理 =====

const apiKeyCols = "id, key_hash, key_prefix, tenant_id, name, status, quota, used_quota, rate_limit, allowed_models, expires_at, created_at, updated_at, created_by, deleted"

func scanAPIKey(row interface{ Scan(...interface{}) error }) (*plugin.APIKey, error) {
	var k plugin.APIKey
	var allowedModels string
	var expiresAt sql.NullString
	var createdAt, updatedAt string
	var deleted int
	if err := row.Scan(&k.ID, &k.KeyHash, &k.KeyPrefix, &k.TenantID, &k.Name,
		&k.Status, &k.Quota, &k.UsedQuota, &k.RateLimit, &allowedModels,
		&expiresAt, &createdAt, &updatedAt, &k.CreatedBy, &deleted); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	_ = json.Unmarshal([]byte(allowedModels), &k.AllowedModels)
	if expiresAt.Valid {
		var ms int64
		fmt.Sscanf(expiresAt.String, "%d", &ms)
		k.ExpiresAt = msToTimePtr(&ms)
	}
	var cms, ums int64
	fmt.Sscanf(createdAt, "%d", &cms)
	fmt.Sscanf(updatedAt, "%d", &ums)
	k.CreatedAt = msToTime(cms)
	k.UpdatedAt = msToTime(ums)
	return &k, nil
}

func (s *SQLStorage) GetAPIKey(keyHash string) (*plugin.APIKey, error) {
	row := s.db.QueryRow("SELECT "+apiKeyCols+" FROM api_keys WHERE key_hash = ? AND deleted = 0", keyHash)
	return scanAPIKey(row)
}

func (s *SQLStorage) GetAPIKeyByID(id string) (*plugin.APIKey, error) {
	row := s.db.QueryRow("SELECT "+apiKeyCols+" FROM api_keys WHERE id = ? AND deleted = 0", id)
	return scanAPIKey(row)
}

func (s *SQLStorage) SaveAPIKey(key *plugin.APIKey) error {
	allowed := marshalJSON(key.AllowedModels)
	expiresAt := timePtrToMS(key.ExpiresAt)
	created := timeToMS(key.CreatedAt)
	updated := timeToMS(key.UpdatedAt)
	// UPSERT 冲突更新子句:MySQL 用 VALUES(col),SQLite 用 excluded.col;
	// 更新列与 INSERT 列(除 id/created_at)对应,deleted 随重新保存恢复为 0。
	upsert := ""
	if s.isMySQL() {
		upsert = " ON DUPLICATE KEY UPDATE key_hash=VALUES(key_hash), key_prefix=VALUES(key_prefix), tenant_id=VALUES(tenant_id), name=VALUES(name), status=VALUES(status), quota=VALUES(quota), used_quota=VALUES(used_quota), rate_limit=VALUES(rate_limit), allowed_models=VALUES(allowed_models), expires_at=VALUES(expires_at), updated_at=VALUES(updated_at), created_by=VALUES(created_by), deleted=VALUES(deleted)"
	} else {
		upsert = " ON CONFLICT(id) DO UPDATE SET key_hash=excluded.key_hash, key_prefix=excluded.key_prefix, tenant_id=excluded.tenant_id, name=excluded.name, status=excluded.status, quota=excluded.quota, used_quota=excluded.used_quota, rate_limit=excluded.rate_limit, allowed_models=excluded.allowed_models, expires_at=excluded.expires_at, updated_at=excluded.updated_at, created_by=excluded.created_by, deleted=excluded.deleted"
	}
	_, err := s.db.Exec(
		`INSERT INTO api_keys (id, key_hash, key_prefix, tenant_id, name, status, quota, used_quota, rate_limit, allowed_models, expires_at, created_at, updated_at, created_by, deleted)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,0)`+upsert,
		key.ID, key.KeyHash, key.KeyPrefix, key.TenantID, key.Name, string(key.Status),
		key.Quota, key.UsedQuota, key.RateLimit, allowed, expiresAt, created, updated, key.CreatedBy)
	return err
}

// IncrementAPIKeyUsage 原子累加已用额度(SQL 层 used_quota = used_quota + ? 单语句,并发安全)
func (s *SQLStorage) IncrementAPIKeyUsage(keyID string, delta int64) error {
	res, err := s.db.Exec("UPDATE api_keys SET used_quota = used_quota + ? WHERE id = ? AND deleted = 0", delta, keyID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLStorage) ListAPIKeys(tenantID string, page, size int) ([]*plugin.APIKey, int64, error) {
	page, size = normalizePage(page, size)
	where := "deleted = 0"
	args := []interface{}{}
	if tenantID != "" {
		where += " AND tenant_id = ?"
		args = append(args, tenantID)
	}
	var total int64
	if err := s.db.QueryRow("SELECT COUNT(*) FROM api_keys WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query("SELECT "+apiKeyCols+" FROM api_keys WHERE "+where+
		" ORDER BY created_at DESC LIMIT ? OFFSET ?", append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var keys []*plugin.APIKey
	for rows.Next() {
		k, err := scanAPIKey(rows)
		if err != nil {
			return nil, 0, err
		}
		keys = append(keys, k)
	}
	return keys, total, rows.Err()
}

func (s *SQLStorage) DeleteAPIKey(keyID string) error {
	res, err := s.db.Exec("UPDATE api_keys SET deleted = 1 WHERE id = ?", keyID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ===== 管理后台账号 =====

const adminUserCols = "id, username, password_hash, tenant_id, role_id, status, created_at, updated_at, last_login_at"

func scanAdminUser(row interface{ Scan(...interface{}) error }) (*plugin.AdminUser, error) {
	var u plugin.AdminUser
	var status, createdAt, updatedAt string
	var lastLoginAt sql.NullString
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.TenantID, &u.RoleID, &status, &createdAt, &updatedAt, &lastLoginAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	u.Status = plugin.AdminUserStatus(status)
	var cms, ums int64
	fmt.Sscanf(createdAt, "%d", &cms)
	fmt.Sscanf(updatedAt, "%d", &ums)
	u.CreatedAt = msToTime(cms)
	u.UpdatedAt = msToTime(ums)
	if lastLoginAt.Valid {
		var lms int64
		fmt.Sscanf(lastLoginAt.String, "%d", &lms)
		t := msToTime(lms)
		u.LastLoginAt = &t
	}
	return &u, nil
}

func (s *SQLStorage) CountAdminUsers() (int64, error) {
	var total int64
	err := s.db.QueryRow("SELECT COUNT(*) FROM admin_users").Scan(&total)
	return total, err
}

func (s *SQLStorage) GetAdminUserByUsername(username string) (*plugin.AdminUser, error) {
	row := s.db.QueryRow("SELECT "+adminUserCols+" FROM admin_users WHERE username = ?", username)
	return scanAdminUser(row)
}

func (s *SQLStorage) GetAdminUserByID(id string) (*plugin.AdminUser, error) {
	row := s.db.QueryRow("SELECT "+adminUserCols+" FROM admin_users WHERE id = ?", id)
	return scanAdminUser(row)
}

// SaveAdminUser UPSERT：MySQL 用 VALUES(col)，SQLite 用 excluded.col
func (s *SQLStorage) SaveAdminUser(user *plugin.AdminUser) error {
	upsert := ""
	if s.isMySQL() {
		upsert = " ON DUPLICATE KEY UPDATE username=VALUES(username), password_hash=VALUES(password_hash), tenant_id=VALUES(tenant_id), role_id=VALUES(role_id), status=VALUES(status), updated_at=VALUES(updated_at), last_login_at=VALUES(last_login_at)"
	} else {
		upsert = " ON CONFLICT(id) DO UPDATE SET username=excluded.username, password_hash=excluded.password_hash, tenant_id=excluded.tenant_id, role_id=excluded.role_id, status=excluded.status, updated_at=excluded.updated_at, last_login_at=excluded.last_login_at"
	}
	_, err := s.db.Exec(
		`INSERT INTO admin_users (id, username, password_hash, tenant_id, role_id, status, created_at, updated_at, last_login_at)
		 VALUES (?,?,?,?,?,?,?,?,?)`+upsert,
		user.ID, user.Username, user.PasswordHash, user.TenantID, user.RoleID, string(user.Status),
		timeToMS(user.CreatedAt), timeToMS(user.UpdatedAt), timePtrToMS(user.LastLoginAt))
	return err
}

// DeleteAdminUser 物理删除管理后台账号（调用方负责最后一个超管守卫）
func (s *SQLStorage) DeleteAdminUser(id string) error {
	res, err := s.db.Exec("DELETE FROM admin_users WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete admin user: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ===== 模型配置管理 =====

const modelConfigCols = "id, model_name, provider, provider_model, base_url, api_key, encrypted, timeout, max_retries, retry_interval, weight, enabled, tags, created_at, updated_at"

// scanModelConfig 扫描一行模型配置,encrypted=1 时用 s.encryptKey 解密 api_key
func (s *SQLStorage) scanModelConfig(row interface{ Scan(...interface{}) error }) (*plugin.ModelConfig, error) {
	var c plugin.ModelConfig
	var apiKey, tags, createdAt, updatedAt string
	var encrypted, enabled int
	if err := row.Scan(&c.ID, &c.ModelName, &c.Provider, &c.ProviderModel, &c.BaseURL,
		&apiKey, &encrypted, &c.Timeout, &c.MaxRetries, &c.RetryInterval, &c.Weight,
		&enabled, &tags, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if encrypted == 1 {
		plain, err := Decrypt(apiKey, s.encryptKey)
		if err != nil {
			return nil, fmt.Errorf("decrypt api key: %w", err)
		}
		apiKey = plain
	}
	c.APIKey = apiKey
	c.Enabled = enabled == 1
	_ = json.Unmarshal([]byte(tags), &c.Tags)
	var cms, ums int64
	fmt.Sscanf(createdAt, "%d", &cms)
	fmt.Sscanf(updatedAt, "%d", &ums)
	c.CreatedAt = msToTime(cms)
	c.UpdatedAt = msToTime(ums)
	return &c, nil
}

func (s *SQLStorage) GetModelConfig(modelName string) (*plugin.ModelConfig, error) {
	row := s.db.QueryRow("SELECT "+modelConfigCols+" FROM model_configs WHERE model_name = ?", modelName)
	return s.scanModelConfig(row)
}

func (s *SQLStorage) GetModelConfigByID(id string) (*plugin.ModelConfig, error) {
	row := s.db.QueryRow("SELECT "+modelConfigCols+" FROM model_configs WHERE id = ?", id)
	return s.scanModelConfig(row)
}

func (s *SQLStorage) SaveModelConfig(config *plugin.ModelConfig) error {
	encrypted, err := Encrypt(config.APIKey, s.encryptKey)
	if err != nil {
		return fmt.Errorf("encrypt api key: %w", err)
	}
	created := timeToMS(config.CreatedAt)
	updated := timeToMS(config.UpdatedAt)
	// UPSERT 冲突更新子句:MySQL 用 VALUES(col),SQLite 用 excluded.col;
	// 更新列与 INSERT 列(除 id/created_at)对应。
	upsert := ""
	if s.isMySQL() {
		upsert = " ON DUPLICATE KEY UPDATE model_name=VALUES(model_name), provider=VALUES(provider), provider_model=VALUES(provider_model), base_url=VALUES(base_url), api_key=VALUES(api_key), encrypted=VALUES(encrypted), timeout=VALUES(timeout), max_retries=VALUES(max_retries), retry_interval=VALUES(retry_interval), weight=VALUES(weight), enabled=VALUES(enabled), tags=VALUES(tags), updated_at=VALUES(updated_at)"
	} else {
		upsert = " ON CONFLICT(id) DO UPDATE SET model_name=excluded.model_name, provider=excluded.provider, provider_model=excluded.provider_model, base_url=excluded.base_url, api_key=excluded.api_key, encrypted=excluded.encrypted, timeout=excluded.timeout, max_retries=excluded.max_retries, retry_interval=excluded.retry_interval, weight=excluded.weight, enabled=excluded.enabled, tags=excluded.tags, updated_at=excluded.updated_at"
	}
	_, err = s.db.Exec(
		`INSERT INTO model_configs (id, model_name, provider, provider_model, base_url, api_key, encrypted, timeout, max_retries, retry_interval, weight, enabled, tags, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,1,?,?,?,?,?,?,?,?)`+upsert,
		config.ID, config.ModelName, config.Provider, config.ProviderModel, config.BaseURL,
		encrypted, config.Timeout, config.MaxRetries, config.RetryInterval, config.Weight,
		config.Enabled, marshalJSON(config.Tags), created, updated)
	return err
}

func (s *SQLStorage) ListModelConfigs(page, size int) ([]*plugin.ModelConfig, int64, error) {
	page, size = normalizePage(page, size)
	var total int64
	if err := s.db.QueryRow("SELECT COUNT(*) FROM model_configs").Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query("SELECT "+modelConfigCols+" FROM model_configs ORDER BY created_at DESC LIMIT ? OFFSET ?", size, (page-1)*size)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var configs []*plugin.ModelConfig
	for rows.Next() {
		c, err := s.scanModelConfig(rows)
		if err != nil {
			return nil, 0, err
		}
		configs = append(configs, c)
	}
	return configs, total, rows.Err()
}

func (s *SQLStorage) DeleteModelConfig(id string) error {
	res, err := s.db.Exec("DELETE FROM model_configs WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ===== 审计日志 =====

const auditLogCols = "id, request_id, tenant_id, api_key_id, model_name, provider, request_method, request_path, request_headers, request_body, response_status, response_body, sse_chunks, prompt_tokens, completion_tokens, total_tokens, duration_ms, client_ip, is_stream, disconnected, disconnect_reason, sha256_fingerprint, created_at"

func (s *SQLStorage) scanAuditLog(row interface{ Scan(...interface{}) error }) (*plugin.AuditLog, error) {
	var l plugin.AuditLog
	var headers, chunks, createdAt string
	var isStream, disconnected int
	if err := row.Scan(&l.ID, &l.RequestID, &l.TenantID, &l.APIKeyID, &l.ModelName,
		&l.Provider, &l.RequestMethod, &l.RequestPath, &headers, &l.RequestBody,
		&l.ResponseStatus, &l.ResponseBody, &chunks, &l.PromptTokens, &l.CompletionTokens,
		&l.TotalTokens, &l.Duration, &l.ClientIP, &isStream, &disconnected,
		&l.DisconnectReason, &l.SHA256Fingerprint, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	_ = json.Unmarshal([]byte(headers), &l.RequestHeaders)
	_ = json.Unmarshal([]byte(chunks), &l.SSEChunks)
	l.IsStream = isStream == 1
	l.Disconnected = disconnected == 1
	var cms int64
	fmt.Sscanf(createdAt, "%d", &cms)
	l.CreatedAt = msToTime(cms)
	return &l, nil
}

// sqlExecer 统一 sql.DB 与 sql.Tx 的 Exec,使单条与批量写入共用插入逻辑
type sqlExecer interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
}

func insertAuditLog(ex sqlExecer, log *plugin.AuditLog) error {
	_, err := ex.Exec(
		`INSERT INTO audit_logs (id, request_id, tenant_id, api_key_id, model_name, provider, request_method, request_path, request_headers, request_body, response_status, response_body, sse_chunks, prompt_tokens, completion_tokens, total_tokens, duration_ms, client_ip, is_stream, disconnected, disconnect_reason, sha256_fingerprint, created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		log.ID, log.RequestID, log.TenantID, log.APIKeyID, log.ModelName, log.Provider,
		log.RequestMethod, log.RequestPath, marshalJSON(log.RequestHeaders), log.RequestBody,
		log.ResponseStatus, log.ResponseBody, marshalJSON(log.SSEChunks),
		log.PromptTokens, log.CompletionTokens, log.TotalTokens, log.Duration,
		log.ClientIP, log.IsStream, log.Disconnected, log.DisconnectReason,
		log.SHA256Fingerprint, timeToMS(log.CreatedAt))
	return err
}

func (s *SQLStorage) SaveAuditLog(log *plugin.AuditLog) error {
	return insertAuditLog(s.db, log)
}

func (s *SQLStorage) BatchSaveAuditLogs(logs []*plugin.AuditLog) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	for _, l := range logs {
		if err := insertAuditLog(tx, l); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// buildAuditWhere 构建过滤 WHERE 子句与参数(与 MemStorage.matchAuditLog 语义一致)
func buildAuditWhere(filter plugin.AuditLogFilter) (string, []interface{}) {
	conds := []string{}
	args := []interface{}{}
	add := func(cond string, as ...interface{}) {
		conds = append(conds, cond)
		args = append(args, as...)
	}
	if filter.TenantID != "" {
		add("tenant_id = ?", filter.TenantID)
	}
	if filter.APIKeyID != "" {
		add("api_key_id = ?", filter.APIKeyID)
	}
	if filter.ModelName != "" {
		add("model_name = ?", filter.ModelName)
	}
	if filter.RequestID != "" {
		add("request_id = ?", filter.RequestID)
	}
	if filter.Status != 0 {
		add("response_status = ?", filter.Status)
	}
	if filter.IsStream != nil {
		add("is_stream = ?", boolToInt(*filter.IsStream))
	}
	if filter.StartTime != nil {
		add("created_at >= ?", timeToMS(*filter.StartTime))
	}
	if filter.EndTime != nil {
		add("created_at <= ?", timeToMS(*filter.EndTime))
	}
	if filter.Keyword != "" {
		kw := "%" + filter.Keyword + "%"
		add("(request_id LIKE ? OR tenant_id LIKE ? OR api_key_id LIKE ? OR model_name LIKE ? OR request_body LIKE ? OR response_body LIKE ? OR disconnect_reason LIKE ?)", kw, kw, kw, kw, kw, kw, kw)
	}
	if len(conds) == 0 {
		return "1=1", nil
	}
	return strings.Join(conds, " AND "), args
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *SQLStorage) QueryAuditLogs(filter plugin.AuditLogFilter, page, size int) ([]*plugin.AuditLog, int64, error) {
	page, size = normalizePage(page, size)
	where, args := buildAuditWhere(filter)
	var total int64
	if err := s.db.QueryRow("SELECT COUNT(*) FROM audit_logs WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query("SELECT "+auditLogCols+" FROM audit_logs WHERE "+where+
		" ORDER BY created_at DESC LIMIT ? OFFSET ?", append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var logs []*plugin.AuditLog
	for rows.Next() {
		l, err := s.scanAuditLog(rows)
		if err != nil {
			return nil, 0, err
		}
		logs = append(logs, l)
	}
	return logs, total, rows.Err()
}

// ===== 健康检查 =====

func (s *SQLStorage) Ping() error { return s.db.Ping() }

func (s *SQLStorage) Close() error { return s.db.Close() }

// ===== 限流配置 =====

const rateLimitCols = "id, tenant_id, model_name, requests_per_sec, tokens_per_min, strategy, enabled, created_at, updated_at"

func (s *SQLStorage) scanRateLimitConfig(row interface{ Scan(...interface{}) error }) (*plugin.RateLimitConfig, error) {
	var c plugin.RateLimitConfig
	var enabled int
	var createdAt, updatedAt string
	if err := row.Scan(&c.ID, &c.TenantID, &c.ModelName, &c.RequestsPerSec, &c.TokensPerMin,
		&c.Strategy, &enabled, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	c.Enabled = enabled == 1
	var cms, ums int64
	fmt.Sscanf(createdAt, "%d", &cms)
	fmt.Sscanf(updatedAt, "%d", &ums)
	c.CreatedAt = msToTime(cms)
	c.UpdatedAt = msToTime(ums)
	return &c, nil
}

func (s *SQLStorage) GetRateLimitConfig(tenantID, modelName string) (*plugin.RateLimitConfig, error) {
	row := s.db.QueryRow("SELECT "+rateLimitCols+" FROM rate_limit_configs WHERE tenant_id = ? AND model_name = ?", tenantID, modelName)
	return s.scanRateLimitConfig(row)
}

func (s *SQLStorage) SaveRateLimitConfig(cfg *plugin.RateLimitConfig) error {
	var upsert string
	if s.isMySQL() {
		upsert = " ON DUPLICATE KEY UPDATE requests_per_sec=VALUES(requests_per_sec), tokens_per_min=VALUES(tokens_per_min), strategy=VALUES(strategy), enabled=VALUES(enabled), updated_at=VALUES(updated_at)"
	} else {
		upsert = " ON CONFLICT(id) DO UPDATE SET tenant_id=excluded.tenant_id, model_name=excluded.model_name, requests_per_sec=excluded.requests_per_sec, tokens_per_min=excluded.tokens_per_min, strategy=excluded.strategy, enabled=excluded.enabled, updated_at=excluded.updated_at"
	}
	_, err := s.db.Exec(
		`INSERT INTO rate_limit_configs (id, tenant_id, model_name, requests_per_sec, tokens_per_min, strategy, enabled, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?)`+upsert,
		cfg.ID, cfg.TenantID, cfg.ModelName, cfg.RequestsPerSec, cfg.TokensPerMin, cfg.Strategy,
		boolToInt(cfg.Enabled), timeToMS(cfg.CreatedAt), timeToMS(cfg.UpdatedAt))
	return err
}

func (s *SQLStorage) ListRateLimitConfigs(tenantID *string, page, size int) ([]*plugin.RateLimitConfig, int64, error) {
	where := "1=1"
	args := []interface{}{}
	if tenantID != nil {
		where += " AND tenant_id = ?"
		args = append(args, *tenantID)
	}
	page, size = normalizePage(page, size)
	var total int64
	if err := s.db.QueryRow("SELECT COUNT(*) FROM rate_limit_configs WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query("SELECT "+rateLimitCols+" FROM rate_limit_configs WHERE "+where+" ORDER BY created_at DESC LIMIT ? OFFSET ?", append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var configs []*plugin.RateLimitConfig
	for rows.Next() {
		c, err := s.scanRateLimitConfig(rows)
		if err != nil {
			return nil, 0, err
		}
		configs = append(configs, c)
	}
	return configs, total, rows.Err()
}

func (s *SQLStorage) DeleteRateLimitConfig(id string) error {
	res, err := s.db.Exec("DELETE FROM rate_limit_configs WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ===== 上游管理 =====

const upstreamCols = "id, model_config_id, base_url, api_key, encrypted, weight, enabled, created_at, updated_at"

func (s *SQLStorage) scanUpstream(row interface{ Scan(...interface{}) error }) (*plugin.Upstream, error) {
	var u plugin.Upstream
	var apiKey, createdAt, updatedAt string
	var encrypted, enabled int
	if err := row.Scan(&u.ID, &u.ModelConfigID, &u.BaseURL, &apiKey, &encrypted, &u.Weight,
		&enabled, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if encrypted == 1 {
		plain, err := Decrypt(apiKey, s.encryptKey)
		if err != nil {
			return nil, fmt.Errorf("decrypt upstream api key: %w", err)
		}
		apiKey = plain
	}
	u.APIKey = apiKey
	u.Enabled = enabled == 1
	var cms, ums int64
	fmt.Sscanf(createdAt, "%d", &cms)
	fmt.Sscanf(updatedAt, "%d", &ums)
	u.CreatedAt = msToTime(cms)
	u.UpdatedAt = msToTime(ums)
	return &u, nil
}

func (s *SQLStorage) GetUpstreamByID(id string) (*plugin.Upstream, error) {
	row := s.db.QueryRow("SELECT "+upstreamCols+" FROM upstreams WHERE id = ?", id)
	return s.scanUpstream(row)
}

func (s *SQLStorage) ListUpstreams(modelConfigID string) ([]*plugin.Upstream, error) {
	rows, err := s.db.Query("SELECT "+upstreamCols+" FROM upstreams WHERE model_config_id = ? ORDER BY created_at", modelConfigID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ups []*plugin.Upstream
	for rows.Next() {
		u, err := s.scanUpstream(rows)
		if err != nil {
			return nil, err
		}
		ups = append(ups, u)
	}
	return ups, rows.Err()
}

func (s *SQLStorage) SaveUpstream(up *plugin.Upstream) error {
	enc, err := Encrypt(up.APIKey, s.encryptKey)
	if err != nil {
		return fmt.Errorf("encrypt upstream api key: %w", err)
	}
	var upsert string
	if s.isMySQL() {
		upsert = " ON DUPLICATE KEY UPDATE model_config_id=VALUES(model_config_id), base_url=VALUES(base_url), api_key=VALUES(api_key), encrypted=VALUES(encrypted), weight=VALUES(weight), enabled=VALUES(enabled), updated_at=VALUES(updated_at)"
	} else {
		upsert = " ON CONFLICT(id) DO UPDATE SET model_config_id=excluded.model_config_id, base_url=excluded.base_url, api_key=excluded.api_key, encrypted=excluded.encrypted, weight=excluded.weight, enabled=excluded.enabled, updated_at=excluded.updated_at"
	}
	_, err = s.db.Exec(
		`INSERT INTO upstreams (id, model_config_id, base_url, api_key, encrypted, weight, enabled, created_at, updated_at)
		 VALUES (?,?,?,?,1,?,?,?,?)`+upsert,
		up.ID, up.ModelConfigID, up.BaseURL, enc, up.Weight, boolToInt(up.Enabled),
		timeToMS(up.CreatedAt), timeToMS(up.UpdatedAt))
	return err
}

func (s *SQLStorage) DeleteUpstream(id string) error {
	res, err := s.db.Exec("DELETE FROM upstreams WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ===== 留存清理与篡改告警 =====

// DeleteAuditLogsBefore 删除 cutoff 之前的审计日志，返回删除条数
func (s *SQLStorage) DeleteAuditLogsBefore(cutoff time.Time) (int64, error) {
	res, err := s.db.Exec("DELETE FROM audit_logs WHERE created_at < ?", timeToMS(cutoff))
	if err != nil {
		return 0, fmt.Errorf("delete expired audit logs: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// SaveTamperAlerts upsert 篡改告警：同一 AuditLogID 存在未处置告警则更新检查时间，否则插入
func (s *SQLStorage) SaveTamperAlerts(alerts []*plugin.TamperAlert) error {
	now := timeToMS(time.Now())
	for _, a := range alerts {
		var id string
		err := s.db.QueryRow(
			"SELECT id FROM audit_tamper_alerts WHERE audit_log_id = ? AND resolved = 0 LIMIT 1", a.AuditLogID,
		).Scan(&id)
		switch {
		case err == nil:
			if _, err := s.db.Exec(
				"UPDATE audit_tamper_alerts SET reason = ?, last_checked_at = ? WHERE id = ?",
				a.Reason, now, id,
			); err != nil {
				return fmt.Errorf("update tamper alert: %w", err)
			}
		case errors.Is(err, sql.ErrNoRows):
			inserted := *a
			if inserted.ID == "" {
				inserted.ID = uuid.NewString()
			}
			if inserted.FirstSeenAt.IsZero() {
				inserted.FirstSeenAt = msToTime(now)
			}
			inserted.LastCheckedAt = msToTime(now)
			if _, err := s.db.Exec(
				"INSERT INTO audit_tamper_alerts (id, audit_log_id, reason, resolved, first_seen_at, last_checked_at) VALUES (?, ?, ?, 0, ?, ?)",
				inserted.ID, inserted.AuditLogID, inserted.Reason, timeToMS(inserted.FirstSeenAt), now,
			); err != nil {
				return fmt.Errorf("insert tamper alert: %w", err)
			}
		default:
			return fmt.Errorf("query tamper alert: %w", err)
		}
	}
	return nil
}

// ListTamperAlerts 查询篡改告警：resolved nil=全部；按最近检查时间倒序分页
func (s *SQLStorage) ListTamperAlerts(resolved *bool, page, size int) ([]*plugin.TamperAlert, int64, error) {
	where := "1=1"
	args := []interface{}{}
	if resolved != nil {
		where += " AND resolved = ?"
		args = append(args, boolToInt(*resolved))
	}
	var total int64
	if err := s.db.QueryRow("SELECT COUNT(*) FROM audit_tamper_alerts WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count tamper alerts: %w", err)
	}
	page, size = normalizePage(page, size)
	rows, err := s.db.Query(
		"SELECT id, audit_log_id, reason, resolved, first_seen_at, last_checked_at FROM audit_tamper_alerts WHERE "+where+
			" ORDER BY last_checked_at DESC LIMIT ? OFFSET ?", append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, 0, fmt.Errorf("list tamper alerts: %w", err)
	}
	defer rows.Close()
	var alerts []*plugin.TamperAlert
	for rows.Next() {
		a := &plugin.TamperAlert{}
		var firstMS, checkedMS int64
		var resolvedInt int
		if err := rows.Scan(&a.ID, &a.AuditLogID, &a.Reason, &resolvedInt, &firstMS, &checkedMS); err != nil {
			return nil, 0, err
		}
		a.Resolved = resolvedInt != 0
		a.FirstSeenAt = msToTime(firstMS)
		a.LastCheckedAt = msToTime(checkedMS)
		alerts = append(alerts, a)
	}
	return alerts, total, rows.Err()
}

// SetTamperAlertResolved 标记告警处置状态
func (s *SQLStorage) SetTamperAlertResolved(id string, resolved bool) error {
	res, err := s.db.Exec("UPDATE audit_tamper_alerts SET resolved = ? WHERE id = ?", boolToInt(resolved), id)
	if err != nil {
		return fmt.Errorf("resolve tamper alert: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ===== 隐私合规(规则库/白名单/安全事件) =====

const privacyRuleCols = "id, rule_type, name, pattern, replacement, scope, enabled, created_at, updated_at"

func scanPrivacyRule(row interface{ Scan(...interface{}) error }) (*plugin.PrivacyRule, error) {
	r := &plugin.PrivacyRule{}
	var enabledInt int
	var createdMS, updatedMS int64
	if err := row.Scan(&r.ID, &r.RuleType, &r.Name, &r.Pattern, &r.Replacement, &r.Scope, &enabledInt, &createdMS, &updatedMS); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	r.Enabled = enabledInt != 0
	r.CreatedAt = msToTime(createdMS)
	r.UpdatedAt = msToTime(updatedMS)
	return r, nil
}

// SavePrivacyRule UPSERT：MySQL 用 VALUES(col)，SQLite 用 excluded.col
func (s *SQLStorage) SavePrivacyRule(rule *plugin.PrivacyRule) error {
	now := time.Now()
	if rule.ID == "" {
		rule.ID = uuid.NewString()
		rule.CreatedAt = now
	}
	rule.UpdatedAt = now
	upsert := ""
	if s.isMySQL() {
		upsert = " ON DUPLICATE KEY UPDATE rule_type=VALUES(rule_type), name=VALUES(name), pattern=VALUES(pattern), replacement=VALUES(replacement), scope=VALUES(scope), enabled=VALUES(enabled), updated_at=VALUES(updated_at)"
	} else {
		upsert = " ON CONFLICT(id) DO UPDATE SET rule_type=excluded.rule_type, name=excluded.name, pattern=excluded.pattern, replacement=excluded.replacement, scope=excluded.scope, enabled=excluded.enabled, updated_at=excluded.updated_at"
	}
	if _, err := s.db.Exec(
		"INSERT INTO privacy_rules ("+privacyRuleCols+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)"+upsert,
		rule.ID, rule.RuleType, rule.Name, rule.Pattern, rule.Replacement, rule.Scope,
		boolToInt(rule.Enabled), timeToMS(rule.CreatedAt), timeToMS(rule.UpdatedAt),
	); err != nil {
		return fmt.Errorf("save privacy rule: %w", err)
	}
	return nil
}

func (s *SQLStorage) DeletePrivacyRule(id string) error {
	res, err := s.db.Exec("DELETE FROM privacy_rules WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete privacy rule: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListPrivacyRules 全量规则（引擎缓存加载用，不分页）；ruleType nil=全部，按创建序返回保证确定性
func (s *SQLStorage) ListPrivacyRules(ruleType *string) ([]*plugin.PrivacyRule, error) {
	where := "1=1"
	args := []interface{}{}
	if ruleType != nil {
		where += " AND rule_type = ?"
		args = append(args, *ruleType)
	}
	rows, err := s.db.Query(
		"SELECT "+privacyRuleCols+" FROM privacy_rules WHERE "+where+" ORDER BY created_at ASC, id ASC", args...)
	if err != nil {
		return nil, fmt.Errorf("list privacy rules: %w", err)
	}
	defer rows.Close()
	var rules []*plugin.PrivacyRule
	for rows.Next() {
		r, err := scanPrivacyRule(rows)
		if err != nil {
			return nil, err
		}
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

const privacyWhitelistCols = "id, pattern, note, enabled, created_at"

// SavePrivacyWhitelistEntry UPSERT：MySQL 用 VALUES(col)，SQLite 用 excluded.col
func (s *SQLStorage) SavePrivacyWhitelistEntry(entry *plugin.PrivacyWhitelistEntry) error {
	if entry.ID == "" {
		entry.ID = uuid.NewString()
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now()
	}
	upsert := ""
	if s.isMySQL() {
		upsert = " ON DUPLICATE KEY UPDATE pattern=VALUES(pattern), note=VALUES(note), enabled=VALUES(enabled)"
	} else {
		upsert = " ON CONFLICT(id) DO UPDATE SET pattern=excluded.pattern, note=excluded.note, enabled=excluded.enabled"
	}
	if _, err := s.db.Exec(
		"INSERT INTO privacy_whitelist ("+privacyWhitelistCols+") VALUES (?, ?, ?, ?, ?)"+upsert,
		entry.ID, entry.Pattern, entry.Note, boolToInt(entry.Enabled), timeToMS(entry.CreatedAt),
	); err != nil {
		return fmt.Errorf("save privacy whitelist entry: %w", err)
	}
	return nil
}

func (s *SQLStorage) DeletePrivacyWhitelistEntry(id string) error {
	res, err := s.db.Exec("DELETE FROM privacy_whitelist WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete privacy whitelist entry: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLStorage) ListPrivacyWhitelistEntries() ([]*plugin.PrivacyWhitelistEntry, error) {
	rows, err := s.db.Query(
		"SELECT " + privacyWhitelistCols + " FROM privacy_whitelist ORDER BY created_at ASC, id ASC")
	if err != nil {
		return nil, fmt.Errorf("list privacy whitelist: %w", err)
	}
	defer rows.Close()
	var entries []*plugin.PrivacyWhitelistEntry
	for rows.Next() {
		e := &plugin.PrivacyWhitelistEntry{}
		var enabledInt int
		var createdMS int64
		if err := rows.Scan(&e.ID, &e.Pattern, &e.Note, &enabledInt, &createdMS); err != nil {
			return nil, err
		}
		e.Enabled = enabledInt != 0
		e.CreatedAt = msToTime(createdMS)
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

const securityEventCols = "id, request_id, rule_name, snippet, client_ip, model_name, created_at"

func (s *SQLStorage) SaveSecurityEvent(event *plugin.SecurityEvent) error {
	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now()
	}
	if _, err := s.db.Exec(
		"INSERT INTO security_events ("+securityEventCols+") VALUES (?, ?, ?, ?, ?, ?, ?)",
		event.ID, event.RequestID, event.RuleName, event.Snippet, event.ClientIP, event.ModelName,
		timeToMS(event.CreatedAt),
	); err != nil {
		return fmt.Errorf("save security event: %w", err)
	}
	return nil
}

// ListSecurityEvents 安全事件分页查询：按发生时间倒序（最近优先）
func (s *SQLStorage) ListSecurityEvents(page, size int) ([]*plugin.SecurityEvent, int64, error) {
	var total int64
	if err := s.db.QueryRow("SELECT COUNT(*) FROM security_events").Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count security events: %w", err)
	}
	page, size = normalizePage(page, size)
	rows, err := s.db.Query(
		"SELECT "+securityEventCols+" FROM security_events ORDER BY created_at DESC LIMIT ? OFFSET ?",
		size, (page-1)*size)
	if err != nil {
		return nil, 0, fmt.Errorf("list security events: %w", err)
	}
	defer rows.Close()
	events := make([]*plugin.SecurityEvent, 0, size)
	for rows.Next() {
		e := &plugin.SecurityEvent{}
		var createdMS int64
		if err := rows.Scan(&e.ID, &e.RequestID, &e.RuleName, &e.Snippet, &e.ClientIP, &e.ModelName, &createdMS); err != nil {
			return nil, 0, err
		}
		e.CreatedAt = msToTime(createdMS)
		events = append(events, e)
	}
	return events, total, rows.Err()
}

// ===== RBAC 权限体系(租户/角色/操作日志) =====

const tenantCols = "id, name, code, status, config, created_at, updated_at"

func scanTenant(row interface{ Scan(...interface{}) error }) (*plugin.Tenant, error) {
	t := &plugin.Tenant{}
	var configJSON string
	var createdMS, updatedMS int64
	if err := row.Scan(&t.ID, &t.Name, &t.Code, &t.Status, &configJSON, &createdMS, &updatedMS); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	_ = json.Unmarshal([]byte(configJSON), &t.Config)
	t.CreatedAt = msToTime(createdMS)
	t.UpdatedAt = msToTime(updatedMS)
	return t, nil
}

// SaveTenant UPSERT：code 唯一约束冲突由调用方先查 GetTenantByCode 转 409
func (s *SQLStorage) SaveTenant(tenant *plugin.Tenant) error {
	now := time.Now()
	if tenant.ID == "" {
		tenant.ID = uuid.NewString()
		tenant.CreatedAt = now
	}
	tenant.UpdatedAt = now
	upsert := ""
	if s.isMySQL() {
		upsert = " ON DUPLICATE KEY UPDATE name=VALUES(name), status=VALUES(status), config=VALUES(config), updated_at=VALUES(updated_at)"
	} else {
		upsert = " ON CONFLICT(id) DO UPDATE SET name=excluded.name, status=excluded.status, config=excluded.config, updated_at=excluded.updated_at"
	}
	if _, err := s.db.Exec(
		"INSERT INTO tenants ("+tenantCols+") VALUES (?, ?, ?, ?, ?, ?, ?)"+upsert,
		tenant.ID, tenant.Name, tenant.Code, tenant.Status, marshalJSON(tenant.Config),
		timeToMS(tenant.CreatedAt), timeToMS(tenant.UpdatedAt),
	); err != nil {
		return fmt.Errorf("save tenant: %w", err)
	}
	return nil
}

func (s *SQLStorage) GetTenantByID(id string) (*plugin.Tenant, error) {
	row := s.db.QueryRow("SELECT "+tenantCols+" FROM tenants WHERE id = ?", id)
	return scanTenant(row)
}

func (s *SQLStorage) GetTenantByCode(code string) (*plugin.Tenant, error) {
	row := s.db.QueryRow("SELECT "+tenantCols+" FROM tenants WHERE code = ?", code)
	return scanTenant(row)
}

func (s *SQLStorage) ListTenants(page, size int) ([]*plugin.Tenant, int64, error) {
	var total int64
	if err := s.db.QueryRow("SELECT COUNT(*) FROM tenants").Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count tenants: %w", err)
	}
	page, size = normalizePage(page, size)
	rows, err := s.db.Query("SELECT "+tenantCols+" FROM tenants ORDER BY created_at ASC, id ASC LIMIT ? OFFSET ?", size, (page-1)*size)
	if err != nil {
		return nil, 0, fmt.Errorf("list tenants: %w", err)
	}
	defer rows.Close()
	tenants := make([]*plugin.Tenant, 0, size)
	for rows.Next() {
		t, err := scanTenant(rows)
		if err != nil {
			return nil, 0, err
		}
		tenants = append(tenants, t)
	}
	return tenants, total, rows.Err()
}

func (s *SQLStorage) DeleteTenant(id string) error {
	res, err := s.db.Exec("DELETE FROM tenants WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete tenant: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLStorage) CountTenants() (int64, error) {
	var total int64
	err := s.db.QueryRow("SELECT COUNT(*) FROM tenants").Scan(&total)
	return total, err
}

func (s *SQLStorage) CountAPIKeysByTenantID(tenantID string) (int64, error) {
	var total int64
	err := s.db.QueryRow("SELECT COUNT(*) FROM api_keys WHERE tenant_id = ? AND deleted = 0", tenantID).Scan(&total)
	return total, err
}

const roleCols = "id, name, tenant_id, permissions, created_at, updated_at"

func scanRole(row interface{ Scan(...interface{}) error }) (*plugin.Role, error) {
	r := &plugin.Role{}
	var permissionsJSON string
	var createdMS, updatedMS int64
	if err := row.Scan(&r.ID, &r.Name, &r.TenantID, &permissionsJSON, &createdMS, &updatedMS); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	_ = json.Unmarshal([]byte(permissionsJSON), &r.Permissions)
	r.CreatedAt = msToTime(createdMS)
	r.UpdatedAt = msToTime(updatedMS)
	return r, nil
}

// SaveRole UPSERT：MySQL 用 VALUES(col)，SQLite 用 excluded.col
func (s *SQLStorage) SaveRole(role *plugin.Role) error {
	now := time.Now()
	if role.ID == "" {
		role.ID = uuid.NewString()
		role.CreatedAt = now
	}
	role.UpdatedAt = now
	upsert := ""
	if s.isMySQL() {
		upsert = " ON DUPLICATE KEY UPDATE name=VALUES(name), tenant_id=VALUES(tenant_id), permissions=VALUES(permissions), updated_at=VALUES(updated_at)"
	} else {
		upsert = " ON CONFLICT(id) DO UPDATE SET name=excluded.name, tenant_id=excluded.tenant_id, permissions=excluded.permissions, updated_at=excluded.updated_at"
	}
	if _, err := s.db.Exec(
		"INSERT INTO roles ("+roleCols+") VALUES (?, ?, ?, ?, ?, ?)"+upsert,
		role.ID, role.Name, role.TenantID, marshalJSON(role.Permissions),
		timeToMS(role.CreatedAt), timeToMS(role.UpdatedAt),
	); err != nil {
		return fmt.Errorf("save role: %w", err)
	}
	return nil
}

func (s *SQLStorage) GetRoleByID(id string) (*plugin.Role, error) {
	row := s.db.QueryRow("SELECT "+roleCols+" FROM roles WHERE id = ?", id)
	return scanRole(row)
}

func (s *SQLStorage) ListRoles() ([]*plugin.Role, error) {
	rows, err := s.db.Query("SELECT " + roleCols + " FROM roles ORDER BY created_at ASC, id ASC")
	if err != nil {
		return nil, fmt.Errorf("list roles: %w", err)
	}
	defer rows.Close()
	var roles []*plugin.Role
	for rows.Next() {
		r, err := scanRole(rows)
		if err != nil {
			return nil, err
		}
		roles = append(roles, r)
	}
	return roles, rows.Err()
}

func (s *SQLStorage) DeleteRole(id string) error {
	res, err := s.db.Exec("DELETE FROM roles WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete role: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLStorage) CountAdminUsersByRoleID(roleID string) (int64, error) {
	var total int64
	err := s.db.QueryRow("SELECT COUNT(*) FROM admin_users WHERE role_id = ?", roleID).Scan(&total)
	return total, err
}

const adminOpLogCols = "id, user_id, username, method, path, target_id, status_code, client_ip, created_at"

func (s *SQLStorage) SaveAdminOperationLog(log *plugin.AdminOperationLog) error {
	if log.ID == "" {
		log.ID = uuid.NewString()
	}
	if log.CreatedAt.IsZero() {
		log.CreatedAt = time.Now()
	}
	if _, err := s.db.Exec(
		"INSERT INTO admin_operation_logs ("+adminOpLogCols+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		log.ID, log.UserID, log.Username, log.Method, log.Path, log.TargetID,
		log.StatusCode, log.ClientIP, timeToMS(log.CreatedAt),
	); err != nil {
		return fmt.Errorf("save admin operation log: %w", err)
	}
	return nil
}

// ListAdminOperationLogs 操作日志分页：时间倒序（最近优先）
func (s *SQLStorage) ListAdminOperationLogs(filter plugin.AdminOpLogFilter, page, size int) ([]*plugin.AdminOperationLog, int64, error) {
	where := "1=1"
	args := []interface{}{}
	if filter.UserID != "" {
		where += " AND user_id = ?"
		args = append(args, filter.UserID)
	}
	var total int64
	if err := s.db.QueryRow("SELECT COUNT(*) FROM admin_operation_logs WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count operation logs: %w", err)
	}
	page, size = normalizePage(page, size)
	rows, err := s.db.Query(
		"SELECT "+adminOpLogCols+" FROM admin_operation_logs WHERE "+where+" ORDER BY created_at DESC LIMIT ? OFFSET ?",
		append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, 0, fmt.Errorf("list operation logs: %w", err)
	}
	defer rows.Close()
	logs := make([]*plugin.AdminOperationLog, 0, size)
	for rows.Next() {
		l := &plugin.AdminOperationLog{}
		var createdMS int64
		if err := rows.Scan(&l.ID, &l.UserID, &l.Username, &l.Method, &l.Path, &l.TargetID, &l.StatusCode, &l.ClientIP, &createdMS); err != nil {
			return nil, 0, err
		}
		l.CreatedAt = msToTime(createdMS)
		logs = append(logs, l)
	}
	return logs, total, rows.Err()
}

// seedRBAC roles 空表时写入超管角色，并把无角色的存量账号挂载到超管；已有数据则跳过
func seedRBAC(db *sql.DB) error {
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM roles").Scan(&count); err != nil {
		return fmt.Errorf("count roles: %w", err)
	}
	superID := ""
	if count == 0 {
		superID = uuid.NewString()
		now := timeToMS(time.Now())
		if _, err := db.Exec(
			"INSERT INTO roles (id, name, tenant_id, permissions, created_at, updated_at) VALUES (?, ?, '', ?, ?, ?)",
			superID, plugin.SuperRoleName, marshalJSON(plugin.AllPermissions), now, now,
		); err != nil {
			return fmt.Errorf("seed super role: %w", err)
		}
	} else if err := db.QueryRow("SELECT id FROM roles WHERE name = ? AND tenant_id = ''", plugin.SuperRoleName).Scan(&superID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("find super role: %w", err)
	}
	if superID == "" {
		return nil
	}
	if _, err := db.Exec("UPDATE admin_users SET role_id = ? WHERE role_id = ''", superID); err != nil {
		return fmt.Errorf("backfill admin role: %w", err)
	}
	return nil
}

// ListAdminUsers 全量管理账号（按创建序）
func (s *SQLStorage) ListAdminUsers() ([]*plugin.AdminUser, error) {
	rows, err := s.db.Query("SELECT " + adminUserCols + " FROM admin_users ORDER BY created_at ASC, id ASC")
	if err != nil {
		return nil, fmt.Errorf("list admin users: %w", err)
	}
	defer rows.Close()
	var users []*plugin.AdminUser
	for rows.Next() {
		u, err := scanAdminUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// CountActiveAdminUsersByRoleID 统计指定角色下的活跃账号数（最后一个超管守卫用）
func (s *SQLStorage) CountActiveAdminUsersByRoleID(roleID string) (int64, error) {
	var total int64
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM admin_users WHERE role_id = ? AND status = ?", roleID, string(plugin.AdminUserStatusActive),
	).Scan(&total)
	return total, err
}

// ===== 合规报表(E6) =====

const complianceReportCols = "id, period_type, period_start, period_end, generated_at, content"

func scanComplianceReport(row interface{ Scan(...interface{}) error }) (*plugin.ComplianceReport, error) {
	r := &plugin.ComplianceReport{}
	var contentJSON string
	var startMS, endMS, genMS int64
	if err := row.Scan(&r.ID, &r.PeriodType, &startMS, &endMS, &genMS, &contentJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	r.PeriodStart = msToTime(startMS)
	r.PeriodEnd = msToTime(endMS)
	r.GeneratedAt = msToTime(genMS)
	content := &plugin.ReportContent{}
	if err := json.Unmarshal([]byte(contentJSON), content); err != nil {
		return nil, fmt.Errorf("解析报表内容: %w", err)
	}
	r.Content = content
	return r, nil
}

// SaveComplianceReport UPSERT：按 (period_type, period_start) 业务键覆盖，保留原 id。
// MySQL 用 VALUES(col)，SQLite 用 excluded.col
func (s *SQLStorage) SaveComplianceReport(report *plugin.ComplianceReport) error {
	now := time.Now()
	if report.ID == "" {
		report.ID = uuid.NewString()
	}
	if report.GeneratedAt.IsZero() {
		report.GeneratedAt = now
	}
	contentJSON, err := json.Marshal(report.Content)
	if err != nil {
		return fmt.Errorf("序列化报表内容: %w", err)
	}
	upsert := ""
	if s.isMySQL() {
		upsert = " ON DUPLICATE KEY UPDATE id=VALUES(id), period_end=VALUES(period_end), generated_at=VALUES(generated_at), content=VALUES(content)"
	} else {
		upsert = " ON CONFLICT(period_type, period_start) DO UPDATE SET id=excluded.id, period_end=excluded.period_end, generated_at=excluded.generated_at, content=excluded.content"
	}
	if _, err := s.db.Exec(
		"INSERT INTO compliance_reports ("+complianceReportCols+") VALUES (?, ?, ?, ?, ?, ?)"+upsert,
		report.ID, report.PeriodType, timeToMS(report.PeriodStart),
		timeToMS(report.PeriodEnd), timeToMS(report.GeneratedAt), string(contentJSON),
	); err != nil {
		return fmt.Errorf("save compliance report: %w", err)
	}
	return nil
}

// ListComplianceReports 报表分页：period_start 倒序（最近优先）
func (s *SQLStorage) ListComplianceReports(page, size int) ([]*plugin.ComplianceReport, int64, error) {
	page, size = normalizePage(page, size)
	var total int64
	if err := s.db.QueryRow("SELECT COUNT(*) FROM compliance_reports").Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count compliance reports: %w", err)
	}
	rows, err := s.db.Query(
		"SELECT "+complianceReportCols+" FROM compliance_reports ORDER BY period_start DESC LIMIT ? OFFSET ?",
		size, (page-1)*size)
	if err != nil {
		return nil, 0, fmt.Errorf("list compliance reports: %w", err)
	}
	defer rows.Close()
	reports := make([]*plugin.ComplianceReport, 0, size)
	for rows.Next() {
		r, err := scanComplianceReport(rows)
		if err != nil {
			return nil, 0, err
		}
		reports = append(reports, r)
	}
	return reports, total, rows.Err()
}

func (s *SQLStorage) GetComplianceReport(id string) (*plugin.ComplianceReport, error) {
	row := s.db.QueryRow("SELECT "+complianceReportCols+" FROM compliance_reports WHERE id = ?", id)
	return scanComplianceReport(row)
}

func (s *SQLStorage) FindComplianceReportByPeriod(periodType string, periodStart time.Time) (*plugin.ComplianceReport, error) {
	row := s.db.QueryRow(
		"SELECT "+complianceReportCols+" FROM compliance_reports WHERE period_type = ? AND period_start = ?",
		periodType, timeToMS(periodStart))
	return scanComplianceReport(row)
}

func (s *SQLStorage) CountComplianceReports() (int64, error) {
	var total int64
	err := s.db.QueryRow("SELECT COUNT(*) FROM compliance_reports").Scan(&total)
	return total, err
}

// ===== MCP 上游配置(E7) =====

const mcpServerCols = "id, name, endpoint, headers, enabled, created_at, updated_at"

func scanMCPServer(row interface{ Scan(...interface{}) error }) (*plugin.MCPServer, error) {
	srv := &plugin.MCPServer{}
	var headersJSON string
	var enabledInt int
	var createdMS, updatedMS int64
	if err := row.Scan(&srv.ID, &srv.Name, &srv.Endpoint, &headersJSON, &enabledInt, &createdMS, &updatedMS); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	srv.Enabled = enabledInt != 0
	srv.CreatedAt = msToTime(createdMS)
	srv.UpdatedAt = msToTime(updatedMS)
	if err := json.Unmarshal([]byte(headersJSON), &srv.Headers); err != nil {
		return nil, fmt.Errorf("解析 MCP 上游 headers: %w", err)
	}
	return srv, nil
}

// SaveMCPServer UPSERT：按 id 覆盖(保留原 created_at)。MySQL 用 VALUES(col)，SQLite 用 excluded.col
func (s *SQLStorage) SaveMCPServer(server *plugin.MCPServer) error {
	now := time.Now()
	if server.ID == "" {
		server.ID = uuid.NewString()
	}
	if server.CreatedAt.IsZero() {
		server.CreatedAt = now
	}
	server.UpdatedAt = now
	headersJSON, err := json.Marshal(server.Headers)
	if err != nil {
		return fmt.Errorf("序列化 MCP 上游 headers: %w", err)
	}
	upsert := ""
	if s.isMySQL() {
		upsert = " ON DUPLICATE KEY UPDATE name=VALUES(name), endpoint=VALUES(endpoint), headers=VALUES(headers), enabled=VALUES(enabled), updated_at=VALUES(updated_at)"
	} else {
		upsert = " ON CONFLICT(id) DO UPDATE SET name=excluded.name, endpoint=excluded.endpoint, headers=excluded.headers, enabled=excluded.enabled, updated_at=excluded.updated_at"
	}
	if _, err := s.db.Exec(
		"INSERT INTO mcp_servers ("+mcpServerCols+") VALUES (?, ?, ?, ?, ?, ?, ?)"+upsert,
		server.ID, server.Name, server.Endpoint, string(headersJSON),
		boolToInt(server.Enabled), timeToMS(server.CreatedAt), timeToMS(server.UpdatedAt),
	); err != nil {
		return fmt.Errorf("save mcp server: %w", err)
	}
	return nil
}

// ListMCPServers 分页列表：name 升序（与 MemStorage 排序语义一致）
func (s *SQLStorage) ListMCPServers(page, size int) ([]*plugin.MCPServer, int64, error) {
	page, size = normalizePage(page, size)
	var total int64
	if err := s.db.QueryRow("SELECT COUNT(*) FROM mcp_servers").Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count mcp servers: %w", err)
	}
	rows, err := s.db.Query(
		"SELECT "+mcpServerCols+" FROM mcp_servers ORDER BY name ASC LIMIT ? OFFSET ?",
		size, (page-1)*size)
	if err != nil {
		return nil, 0, fmt.Errorf("list mcp servers: %w", err)
	}
	defer rows.Close()
	out := make([]*plugin.MCPServer, 0, size)
	for rows.Next() {
		srv, err := scanMCPServer(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, srv)
	}
	return out, total, rows.Err()
}

func (s *SQLStorage) GetMCPServer(id string) (*plugin.MCPServer, error) {
	row := s.db.QueryRow("SELECT "+mcpServerCols+" FROM mcp_servers WHERE id = ?", id)
	return scanMCPServer(row)
}

// DeleteMCPServer 物理删除；未命中返回 ErrNotFound 与内存实现语义一致
func (s *SQLStorage) DeleteMCPServer(id string) error {
	res, err := s.db.Exec("DELETE FROM mcp_servers WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete mcp server: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ===== MCP 工具调用审计(E7) =====

const mcpAuditCols = "id, request_id, tenant_id, api_key_id, tool_name, tool_arguments, tool_result, caller_agent, duration_ms, status, error_message, client_ip, created_at"

func scanMCPAudit(row interface{ Scan(...interface{}) error }) (*plugin.MCPAuditLog, error) {
	e := &plugin.MCPAuditLog{}
	var createdMS int64
	if err := row.Scan(&e.ID, &e.RequestID, &e.TenantID, &e.APIKeyID, &e.ToolName,
		&e.ToolArguments, &e.ToolResult, &e.CallerAgent, &e.DurationMS,
		&e.Status, &e.ErrorMessage, &e.ClientIP, &createdMS); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	e.CreatedAt = msToTime(createdMS)
	return e, nil
}

// buildMCPAuditWhere 构建过滤条件：等值匹配+闭区间时间(与 AuditLogFilter 时间语义一致)
func buildMCPAuditWhere(filter plugin.MCPAuditLogFilter) (string, []interface{}) {
	conds := []string{}
	args := []interface{}{}
	add := func(cond string, as ...interface{}) {
		conds = append(conds, cond)
		args = append(args, as...)
	}
	if filter.TenantID != "" {
		add("tenant_id = ?", filter.TenantID)
	}
	if filter.RequestID != "" {
		add("request_id = ?", filter.RequestID)
	}
	if filter.ToolName != "" {
		add("tool_name = ?", filter.ToolName)
	}
	if filter.Status != "" {
		add("status = ?", filter.Status)
	}
	if filter.StartTime != nil {
		add("created_at >= ?", timeToMS(*filter.StartTime))
	}
	if filter.EndTime != nil {
		add("created_at <= ?", timeToMS(*filter.EndTime))
	}
	if len(conds) == 0 {
		return "1=1", nil
	}
	return strings.Join(conds, " AND "), args
}

// SaveMCPAuditLog 追加写入审计记录；ID/CreatedAt 零值自动补齐
func (s *SQLStorage) SaveMCPAuditLog(entry *plugin.MCPAuditLog) error {
	now := time.Now()
	if entry.ID == "" {
		entry.ID = uuid.NewString()
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = now
	}
	if _, err := s.db.Exec(
		"INSERT INTO mcp_audit_logs ("+mcpAuditCols+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		entry.ID, entry.RequestID, entry.TenantID, entry.APIKeyID, entry.ToolName,
		entry.ToolArguments, entry.ToolResult, entry.CallerAgent, entry.DurationMS,
		entry.Status, entry.ErrorMessage, entry.ClientIP, timeToMS(entry.CreatedAt),
	); err != nil {
		return fmt.Errorf("save mcp audit log: %w", err)
	}
	return nil
}

// GetMCPAuditLog 按主键取单条审计记录（管理面详情）
func (s *SQLStorage) GetMCPAuditLog(id string) (*plugin.MCPAuditLog, error) {
	row := s.db.QueryRow("SELECT "+mcpAuditCols+" FROM mcp_audit_logs WHERE id = ?", id)
	return scanMCPAudit(row)
}

// ListMCPAuditLogs 过滤+分页：created_at 倒序（最近优先）
func (s *SQLStorage) ListMCPAuditLogs(filter plugin.MCPAuditLogFilter, page, size int) ([]*plugin.MCPAuditLog, int64, error) {
	page, size = normalizePage(page, size)
	where, args := buildMCPAuditWhere(filter)
	var total int64
	if err := s.db.QueryRow("SELECT COUNT(*) FROM mcp_audit_logs WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count mcp audit logs: %w", err)
	}
	rows, err := s.db.Query(
		"SELECT "+mcpAuditCols+" FROM mcp_audit_logs WHERE "+where+
			" ORDER BY created_at DESC LIMIT ? OFFSET ?",
		append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, 0, fmt.Errorf("list mcp audit logs: %w", err)
	}
	defer rows.Close()
	out := make([]*plugin.MCPAuditLog, 0, size)
	for rows.Next() {
		e, err := scanMCPAudit(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, e)
	}
	return out, total, rows.Err()
}
