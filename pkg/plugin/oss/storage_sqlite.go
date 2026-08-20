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
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

// mysqlCreateTables 建 MySQL 表。
// 临时版本(任务 4):占位实现,保证 Init 可编译;任务 6 将实现完整建表与 UPSERT 方言。
func mysqlCreateTables(db *sql.DB) error {
	_ = db
	return fmt.Errorf("mysqlCreateTables: not implemented until task 6")
}
