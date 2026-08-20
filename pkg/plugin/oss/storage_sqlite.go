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

// sqliteCreateTables 建 SQLite 表。
// 临时版本(任务 4):仅创建 api_keys 表;任务 6 将扩展为完整建表(api_keys/model_configs/audit_logs 三表)。
func sqliteCreateTables(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS api_keys (
    id             TEXT PRIMARY KEY,
    key_hash       TEXT NOT NULL,
    key_prefix     TEXT,
    tenant_id      TEXT,
    name           TEXT,
    status         TEXT,
    quota          INTEGER,
    used_quota     INTEGER,
    rate_limit     INTEGER,
    allowed_models TEXT,
    expires_at     INTEGER,
    created_at     INTEGER,
    updated_at     INTEGER,
    created_by     TEXT,
    deleted        INTEGER DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_api_keys_key_hash ON api_keys(key_hash);
CREATE INDEX IF NOT EXISTS idx_api_keys_tenant ON api_keys(tenant_id);
`)
	return err
}

// mysqlCreateTables 建 MySQL 表。
// 临时版本(任务 4):占位实现,保证 Init 可编译;任务 6 将实现完整建表与 UPSERT 方言。
func mysqlCreateTables(db *sql.DB) error {
	_ = db
	return fmt.Errorf("mysqlCreateTables: not implemented until task 6")
}
