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
			request_headers MEDIUMTEXT NOT NULL,
			request_body MEDIUMTEXT NOT NULL,
			response_status INT NOT NULL DEFAULT 0,
			response_body MEDIUMTEXT NOT NULL,
			sse_chunks MEDIUMTEXT NOT NULL,
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
		`CREATE TABLE IF NOT EXISTS admin_users (
			id VARCHAR(64) PRIMARY KEY,
			username VARCHAR(64) NOT NULL UNIQUE,
			password_hash VARCHAR(255) NOT NULL,
			tenant_id VARCHAR(64) NOT NULL DEFAULT '',
			role_id VARCHAR(64) NOT NULL DEFAULT '',
			status VARCHAR(16) NOT NULL DEFAULT 'active',
			created_at BIGINT NOT NULL,
			updated_at BIGINT NOT NULL,
			last_login_at BIGINT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS tenants (
			id VARCHAR(64) PRIMARY KEY,
			name VARCHAR(64) NOT NULL,
			code VARCHAR(32) NOT NULL UNIQUE,
			status VARCHAR(16) NOT NULL DEFAULT 'active',
			config TEXT NOT NULL,
			created_at BIGINT NOT NULL,
			updated_at BIGINT NOT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS roles (
			id VARCHAR(64) PRIMARY KEY,
			name VARCHAR(64) NOT NULL,
			tenant_id VARCHAR(64) NOT NULL DEFAULT '',
			permissions TEXT NOT NULL,
			created_at BIGINT NOT NULL,
			updated_at BIGINT NOT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS admin_operation_logs (
			id VARCHAR(64) PRIMARY KEY,
			user_id VARCHAR(64) NOT NULL,
			username VARCHAR(64) NOT NULL DEFAULT '',
			method VARCHAR(16) NOT NULL,
			path VARCHAR(255) NOT NULL DEFAULT '',
			target_id VARCHAR(64) NOT NULL DEFAULT '',
			status_code INT NOT NULL DEFAULT 0,
			client_ip VARCHAR(64) NOT NULL DEFAULT '',
			created_at BIGINT NOT NULL,
			KEY idx_admin_oplogs_created (created_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS privacy_rules (
			id VARCHAR(64) PRIMARY KEY,
			rule_type VARCHAR(16) NOT NULL,
			name VARCHAR(64) NOT NULL,
			pattern TEXT NOT NULL,
			replacement VARCHAR(128) NOT NULL DEFAULT '',
			scope VARCHAR(16) NOT NULL DEFAULT 'both',
			enabled TINYINT NOT NULL DEFAULT 1,
			created_at BIGINT NOT NULL,
			updated_at BIGINT NOT NULL,
			KEY idx_privacy_rules_type (rule_type)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS privacy_whitelist (
			id VARCHAR(64) PRIMARY KEY,
			pattern TEXT NOT NULL,
			note VARCHAR(255) NOT NULL DEFAULT '',
			enabled TINYINT NOT NULL DEFAULT 1,
			created_at BIGINT NOT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS security_events (
			id VARCHAR(64) PRIMARY KEY,
			request_id VARCHAR(64) NOT NULL,
			rule_name VARCHAR(64) NOT NULL DEFAULT '',
			snippet VARCHAR(1024) NOT NULL DEFAULT '',
			client_ip VARCHAR(64) NOT NULL DEFAULT '',
			model_name VARCHAR(64) NOT NULL DEFAULT '',
			created_at BIGINT NOT NULL,
			KEY idx_security_events_created (created_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS compliance_reports (
			id VARCHAR(64) PRIMARY KEY,
			period_type VARCHAR(16) NOT NULL,
			period_start BIGINT NOT NULL,
			period_end BIGINT NOT NULL,
			generated_at BIGINT NOT NULL,
			content MEDIUMTEXT NOT NULL,
			UNIQUE KEY uq_period (period_type, period_start)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS mcp_servers (
			id VARCHAR(64) PRIMARY KEY,
			name VARCHAR(128) NOT NULL,
			endpoint VARCHAR(512) NOT NULL,
			headers MEDIUMTEXT NOT NULL,
			enabled TINYINT NOT NULL DEFAULT 1,
			created_at BIGINT NOT NULL,
			updated_at BIGINT NOT NULL,
			UNIQUE KEY uq_mcp_server_name (name)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return ensureAuditColumnSizes(db)
}

// ensureAuditColumnSizes 存量库审计正文列从 TEXT 扩到 MEDIUMTEXT(16MB)。
// 仅当列类型仍为 text 时 ALTER，避免每次启动对大表加锁
func ensureAuditColumnSizes(db *sql.DB) error {
	rows, err := db.Query(`SELECT COLUMN_NAME FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'audit_logs' AND DATA_TYPE = 'text'`)
	if err != nil {
		return err
	}
	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			rows.Close()
			return err
		}
		cols = append(cols, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, c := range cols {
		switch c { // 只动本产品定义的列，防 information_schema 异常数据拼进 DDL
		case "request_headers", "request_body", "response_body", "sse_chunks":
		default:
			continue
		}
		if _, err := db.Exec(fmt.Sprintf("ALTER TABLE audit_logs MODIFY `%s` MEDIUMTEXT NOT NULL", c)); err != nil {
			return fmt.Errorf("widen column %s: %w", c, err)
		}
	}
	return nil
}

// migrateMySQLAdminUserColumns 存量库 admin_users 补 tenant_id/role_id 列（已存在则跳过）
func migrateMySQLAdminUserColumns(db *sql.DB) error {
	rows, err := db.Query(`SELECT COLUMN_NAME FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'admin_users' AND COLUMN_NAME IN ('tenant_id', 'role_id')`)
	if err != nil {
		return err
	}
	existing := map[string]bool{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			rows.Close()
			return err
		}
		existing[c] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, col := range []string{"tenant_id", "role_id"} {
		if existing[col] {
			continue
		}
		if _, err := db.Exec("ALTER TABLE admin_users ADD COLUMN `" + col + "` VARCHAR(64) NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("add column %s: %w", col, err)
		}
	}
	return nil
}
