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
	"fmt"

	_ "modernc.org/sqlite" // 注册 sqlite 驱动(驱动名 "sqlite",DSN 为文件路径)
)

// sqliteCreateTables 建 SQLite 表(与 storage_sql.go 中 CRUD 列完全对应)
func sqliteCreateTables(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS api_keys (
			id TEXT PRIMARY KEY,
			key_hash TEXT NOT NULL UNIQUE,
			key_prefix TEXT NOT NULL DEFAULT '',
			tenant_id TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'active',
			quota INTEGER NOT NULL DEFAULT -1,
			used_quota INTEGER NOT NULL DEFAULT 0,
			rate_limit INTEGER NOT NULL DEFAULT 10,
			allowed_models TEXT NOT NULL DEFAULT '[]',
			expires_at INTEGER,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			created_by TEXT NOT NULL DEFAULT '',
			deleted INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS model_configs (
			id TEXT PRIMARY KEY,
			model_name TEXT NOT NULL UNIQUE,
			provider TEXT NOT NULL,
			provider_model TEXT NOT NULL,
			base_url TEXT NOT NULL,
			api_key TEXT NOT NULL,
			encrypted INTEGER NOT NULL DEFAULT 1,
			timeout INTEGER NOT NULL DEFAULT 60,
			max_retries INTEGER NOT NULL DEFAULT 2,
			retry_interval INTEGER NOT NULL DEFAULT 3,
			weight INTEGER NOT NULL DEFAULT 1,
			enabled INTEGER NOT NULL DEFAULT 1,
			tags TEXT NOT NULL DEFAULT '{}',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS audit_logs (
			id TEXT PRIMARY KEY,
			request_id TEXT NOT NULL,
			tenant_id TEXT NOT NULL DEFAULT '',
			api_key_id TEXT NOT NULL DEFAULT '',
			model_name TEXT NOT NULL DEFAULT '',
			provider TEXT NOT NULL DEFAULT '',
			request_method TEXT NOT NULL DEFAULT '',
			request_path TEXT NOT NULL DEFAULT '',
			request_headers TEXT NOT NULL DEFAULT '{}',
			request_body TEXT NOT NULL DEFAULT '',
			response_status INTEGER NOT NULL DEFAULT 0,
			response_body TEXT NOT NULL DEFAULT '',
			sse_chunks TEXT NOT NULL DEFAULT '[]',
			prompt_tokens INTEGER NOT NULL DEFAULT 0,
			completion_tokens INTEGER NOT NULL DEFAULT 0,
			total_tokens INTEGER NOT NULL DEFAULT 0,
			duration_ms INTEGER NOT NULL DEFAULT 0,
			client_ip TEXT NOT NULL DEFAULT '',
			is_stream INTEGER NOT NULL DEFAULT 0,
			disconnected INTEGER NOT NULL DEFAULT 0,
			disconnect_reason TEXT NOT NULL DEFAULT '',
			sha256_fingerprint TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_created ON audit_logs(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_tenant ON audit_logs(tenant_id)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_model ON audit_logs(model_name)`,
		`CREATE TABLE IF NOT EXISTS rate_limit_configs (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL DEFAULT '',
			model_name TEXT NOT NULL DEFAULT '',
			requests_per_sec INTEGER NOT NULL,
			tokens_per_min INTEGER NOT NULL,
			strategy TEXT NOT NULL DEFAULT 'token_bucket',
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE(tenant_id, model_name)
		)`,
		`CREATE TABLE IF NOT EXISTS upstreams (
			id TEXT PRIMARY KEY,
			model_config_id TEXT NOT NULL,
			base_url TEXT NOT NULL,
			api_key TEXT NOT NULL,
			encrypted INTEGER NOT NULL DEFAULT 1,
			weight INTEGER NOT NULL DEFAULT 1,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_upstreams_model ON upstreams(model_config_id)`,
		`CREATE TABLE IF NOT EXISTS audit_tamper_alerts (
			id TEXT PRIMARY KEY,
			audit_log_id TEXT NOT NULL,
			reason TEXT NOT NULL,
			resolved INTEGER NOT NULL DEFAULT 0,
			first_seen_at INTEGER NOT NULL,
			last_checked_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tamper_alerts_log ON audit_tamper_alerts(audit_log_id)`,
		`CREATE TABLE IF NOT EXISTS admin_users (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			tenant_id TEXT NOT NULL DEFAULT '',
			role_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'active',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			last_login_at INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS tenants (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			code TEXT NOT NULL UNIQUE,
			status TEXT NOT NULL DEFAULT 'active',
			config TEXT NOT NULL DEFAULT '{}',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS roles (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			tenant_id TEXT NOT NULL DEFAULT '',
			permissions TEXT NOT NULL DEFAULT '[]',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS admin_operation_logs (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			username TEXT NOT NULL DEFAULT '',
			method TEXT NOT NULL,
			path TEXT NOT NULL DEFAULT '',
			target_id TEXT NOT NULL DEFAULT '',
			status_code INTEGER NOT NULL DEFAULT 0,
			client_ip TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_admin_oplogs_created ON admin_operation_logs(created_at)`,
		`CREATE TABLE IF NOT EXISTS privacy_rules (
			id TEXT PRIMARY KEY,
			rule_type TEXT NOT NULL,
			name TEXT NOT NULL,
			pattern TEXT NOT NULL,
			replacement TEXT NOT NULL DEFAULT '',
			scope TEXT NOT NULL DEFAULT 'both',
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_privacy_rules_type ON privacy_rules(rule_type)`,
		`CREATE TABLE IF NOT EXISTS privacy_whitelist (
			id TEXT PRIMARY KEY,
			pattern TEXT NOT NULL,
			note TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS security_events (
			id TEXT PRIMARY KEY,
			request_id TEXT NOT NULL,
			rule_name TEXT NOT NULL DEFAULT '',
			snippet TEXT NOT NULL DEFAULT '',
			client_ip TEXT NOT NULL DEFAULT '',
			model_name TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_security_events_created ON security_events(created_at)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

// migrateSQLiteAdminUserColumns 存量库 admin_users 补 tenant_id/role_id 列（已存在则跳过）
func migrateSQLiteAdminUserColumns(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(admin_users)`)
	if err != nil {
		return err
	}
	existing := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull, pk int
		var dfltValue interface{}
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dfltValue, &pk); err != nil {
			rows.Close()
			return err
		}
		existing[name] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, col := range []string{"tenant_id", "role_id"} {
		if existing[col] {
			continue
		}
		if _, err := db.Exec("ALTER TABLE admin_users ADD COLUMN " + col + " TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("add column %s: %w", col, err)
		}
	}
	return nil
}
