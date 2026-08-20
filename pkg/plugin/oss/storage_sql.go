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

// Init 按 driver 打开连接并建表: driver ∈ {mysql, sqlite}
func (s *SQLStorage) Init(config map[string]interface{}) error {
	driver, _ := config["driver"].(string)
	dsn, _ := config["dsn"].(string)
	s.encryptKey, _ = config["encrypt_key"].(string)
	if driver != "mysql" && driver != "sqlite" {
		return fmt.Errorf("unsupported sql driver: %s", driver)
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

func (s *SQLStorage) UpdateAPIKeyQuota(keyID string, usedQuota int64) error {
	_, err := s.db.Exec("UPDATE api_keys SET used_quota = ? WHERE id = ? AND deleted = 0", usedQuota, keyID)
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
