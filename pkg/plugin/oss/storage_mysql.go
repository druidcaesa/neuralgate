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

	_ "github.com/go-sql-driver/mysql" // 注册 mysql 驱动(驱动名 "mysql")
)

// mysqlCreateTables MySQL 建表(与 storage_sql.go 中 CRUD 列完全对应)
func mysqlCreateTables(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS api_keys (
			id VARCHAR(64) PRIMARY KEY,
			key_hash VARCHAR(64) NOT NULL UNIQUE,
			key_prefix VARCHAR(32) NOT NULL DEFAULT '',
			tenant_id VARCHAR(64) NOT NULL DEFAULT '',
			name VARCHAR(64) NOT NULL DEFAULT '',
			status VARCHAR(16) NOT NULL DEFAULT 'active',
			quota BIGINT NOT NULL DEFAULT -1,
			used_quota BIGINT NOT NULL DEFAULT 0,
			rate_limit INT NOT NULL DEFAULT 10,
			allowed_models TEXT NOT NULL,
			expires_at BIGINT NULL,
			created_at BIGINT NOT NULL,
			updated_at BIGINT NOT NULL,
			created_by VARCHAR(64) NOT NULL DEFAULT '',
			deleted TINYINT NOT NULL DEFAULT 0
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS model_configs (
			id VARCHAR(64) PRIMARY KEY,
			model_name VARCHAR(64) NOT NULL UNIQUE,
			provider VARCHAR(32) NOT NULL,
			provider_model VARCHAR(128) NOT NULL,
			base_url VARCHAR(512) NOT NULL,
			api_key VARCHAR(1024) NOT NULL,
			encrypted TINYINT NOT NULL DEFAULT 1,
			timeout INT NOT NULL DEFAULT 60,
			max_retries INT NOT NULL DEFAULT 2,
			retry_interval INT NOT NULL DEFAULT 3,
			weight INT NOT NULL DEFAULT 1,
			enabled TINYINT NOT NULL DEFAULT 1,
			tags TEXT NOT NULL,
			created_at BIGINT NOT NULL,
			updated_at BIGINT NOT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS audit_logs (
			id VARCHAR(64) PRIMARY KEY,
			request_id VARCHAR(64) NOT NULL,
			tenant_id VARCHAR(64) NOT NULL DEFAULT '',
			api_key_id VARCHAR(64) NOT NULL DEFAULT '',
			model_name VARCHAR(64) NOT NULL DEFAULT '',
			provider VARCHAR(32) NOT NULL DEFAULT '',
			request_method VARCHAR(16) NOT NULL DEFAULT '',
			request_path VARCHAR(255) NOT NULL DEFAULT '',
			request_headers TEXT NOT NULL,
			request_body TEXT NOT NULL,
			response_status INT NOT NULL DEFAULT 0,
			response_body TEXT NOT NULL,
			sse_chunks TEXT NOT NULL,
			prompt_tokens INT NOT NULL DEFAULT 0,
			completion_tokens INT NOT NULL DEFAULT 0,
			total_tokens INT NOT NULL DEFAULT 0,
			duration_ms BIGINT NOT NULL DEFAULT 0,
			client_ip VARCHAR(64) NOT NULL DEFAULT '',
			is_stream TINYINT NOT NULL DEFAULT 0,
			disconnected TINYINT NOT NULL DEFAULT 0,
			disconnect_reason VARCHAR(255) NOT NULL DEFAULT '',
			sha256_fingerprint VARCHAR(64) NOT NULL DEFAULT '',
			created_at BIGINT NOT NULL,
			KEY idx_audit_created (created_at),
			KEY idx_audit_tenant (tenant_id),
			KEY idx_audit_model (model_name)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS rate_limit_configs (
			id VARCHAR(64) PRIMARY KEY,
			tenant_id VARCHAR(64) NOT NULL DEFAULT '',
			model_name VARCHAR(64) NOT NULL DEFAULT '',
			requests_per_sec INT NOT NULL,
			tokens_per_min BIGINT NOT NULL,
			strategy VARCHAR(32) NOT NULL DEFAULT 'token_bucket',
			enabled TINYINT NOT NULL DEFAULT 1,
			created_at BIGINT NOT NULL,
			updated_at BIGINT NOT NULL,
			UNIQUE KEY uq_tenant_model (tenant_id, model_name)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS upstreams (
			id VARCHAR(64) PRIMARY KEY,
			model_config_id VARCHAR(64) NOT NULL,
			base_url VARCHAR(512) NOT NULL,
			api_key VARCHAR(1024) NOT NULL,
			encrypted TINYINT NOT NULL DEFAULT 1,
			weight INT NOT NULL DEFAULT 1,
			enabled TINYINT NOT NULL DEFAULT 1,
			created_at BIGINT NOT NULL,
			updated_at BIGINT NOT NULL,
			KEY idx_upstreams_model (model_config_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS audit_tamper_alerts (
			id VARCHAR(36) PRIMARY KEY,
			audit_log_id VARCHAR(64) NOT NULL,
			reason TEXT NOT NULL,
			resolved TINYINT NOT NULL DEFAULT 0,
			first_seen_at BIGINT NOT NULL,
			last_checked_at BIGINT NOT NULL,
			KEY idx_tamper_alerts_log (audit_log_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}
