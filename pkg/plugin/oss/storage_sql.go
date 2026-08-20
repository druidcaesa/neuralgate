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
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// SQLStorage 共享 SQL 存储实现(MySQL/SQLite 共用 CRUD 逻辑)
type SQLStorage struct {
	db         *sql.DB
	encryptKey string
}

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
	s.db = db
	if driver == "mysql" {
		return mysqlCreateTables(db)
	}
	return sqliteCreateTables(db)
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
	_, err := s.db.Exec(
		`INSERT INTO api_keys (id, key_hash, key_prefix, tenant_id, name, status, quota, used_quota, rate_limit, allowed_models, expires_at, created_at, updated_at, created_by, deleted)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,0)
		 ON CONFLICT(id) DO UPDATE SET key_hash=excluded.key_hash, key_prefix=excluded.key_prefix, tenant_id=excluded.tenant_id,
		   name=excluded.name, status=excluded.status, quota=excluded.quota, used_quota=excluded.used_quota,
		   rate_limit=excluded.rate_limit, allowed_models=excluded.allowed_models, expires_at=excluded.expires_at,
		   updated_at=excluded.updated_at, created_by=excluded.created_by`,
		key.ID, key.KeyHash, key.KeyPrefix, key.TenantID, key.Name, string(key.Status),
		key.Quota, key.UsedQuota, key.RateLimit, allowed, expiresAt, created, updated, key.CreatedBy)
	return err
}

func (s *SQLStorage) UpdateAPIKeyQuota(keyID string, usedQuota int64) error {
	_, err := s.db.Exec("UPDATE api_keys SET used_quota = ? WHERE id = ? AND deleted = 0", usedQuota, keyID)
	return err
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
