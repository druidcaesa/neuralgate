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
	"strings"
)

// 本文件承载达梦/人大金仓的建表逻辑：语句体复用 sqlite 清单（两库对
// INTEGER/BIGINT/VARCHAR/TEXT/表级 UNIQUE 均兼容），差异只在
// 幂等策略——金仓支持 IF NOT EXISTS 直接执行；达梦需存在性检查包装

// kingbaseCreateTables 金仓(PG 兼容模式)建表：IF NOT EXISTS/表级 UNIQUE/
// 独立 CREATE INDEX 语法均与 SQLite 清单兼容，逐条执行即可
func kingbaseCreateTables(db *sql.DB) error {
	for _, stmt := range sqliteTableStmts() {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

// dmCreateTables 达梦建表：不支持 IF NOT EXISTS——
// 表经 user_tables、索引经 user_indexes 存在性检查后逐条创建。
// 标识符不加引号，交由达梦默认大写规范化统一处理
func dmCreateTables(db *sql.DB) error {
	for _, stmt := range sqliteTableStmts() {
		trimmed := strings.TrimSpace(stmt)
		switch {
		case strings.HasPrefix(strings.ToUpper(trimmed), "CREATE TABLE"):
			table := extractTableName(trimmed)
			exists, err := dmObjectExists(db,
				"SELECT COUNT(*) FROM user_tables WHERE table_name = UPPER(?)", table)
			if err != nil {
				return err
			}
			if !exists {
				if _, err := db.Exec(stmt); err != nil {
					return fmt.Errorf("dm create table %s: %w", table, err)
				}
			}
		case strings.HasPrefix(strings.ToUpper(trimmed), "CREATE INDEX"):
			index := extractIndexName(trimmed)
			exists, err := dmObjectExists(db,
				"SELECT COUNT(*) FROM user_indexes WHERE index_name = UPPER(?)", index)
			if err != nil {
				return err
			}
			if !exists {
				if _, err := db.Exec(stmt); err != nil {
					return fmt.Errorf("dm create index %s: %w", index, err)
				}
			}
		default:
			if _, err := db.Exec(stmt); err != nil {
				return err
			}
		}
	}
	return nil
}

func dmObjectExists(db *sql.DB, query string, name string) (bool, error) {
	var count int
	if err := db.QueryRow(query, name).Scan(&count); err != nil {
		return false, fmt.Errorf("dm existence probe: %w", err)
	}
	return count > 0, nil
}

// extractTableName 从 "CREATE TABLE IF NOT EXISTS <name> (" 提取表名
func extractTableName(stmt string) string {
	fields := strings.Fields(stmt)
	for i, f := range fields {
		if strings.EqualFold(f, "TABLE") {
			nameIdx := i + 1
			if nameIdx < len(fields) && strings.EqualFold(fields[nameIdx], "IF") {
				nameIdx += 3 // 跳过 IF NOT EXISTS
			}
			if nameIdx < len(fields) {
				return strings.Trim(fields[nameIdx], "(")
			}
		}
	}
	return ""
}

// extractIndexName 从 "CREATE INDEX IF NOT EXISTS <name> ON ..." 提取索引名
func extractIndexName(stmt string) string {
	fields := strings.Fields(stmt)
	for i, f := range fields {
		if strings.EqualFold(f, "INDEX") {
			nameIdx := i + 1
			if nameIdx < len(fields) && strings.EqualFold(fields[nameIdx], "IF") {
				nameIdx += 3
			}
			if nameIdx < len(fields) {
				return fields[nameIdx]
			}
		}
	}
	return ""
}
