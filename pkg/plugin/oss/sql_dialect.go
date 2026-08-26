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
	"strconv"
	"strings"
)

// sqlDialect SQL 方言差异收拢点：UPSERT 子句与占位符风格。
// 达梦/金仓两方言由信创迭代在同一处扩展，CRUD 主体保持驱动无关
type sqlDialect struct {
	name string
}

func dialectFor(driver string) sqlDialect { return sqlDialect{name: driver} }

// Name 暴露驱动名（MERGE 分派等需要按名判断的场景）
func (d sqlDialect) Name() string { return d.name }

// upsert 生成 INSERT 之后的 UPSERT 子句。
// conflictKeys 为冲突键列（主键 id 或业务键）；updateCols 为冲突时更新的列。
// mysql 形态不使用 conflictKeys（ON DUPLICATE KEY 隐式覆盖全部唯一键）；
// dm 的 UPSERT 是完整 MERGE 语句而非后缀——调用方据 Name 分派 MergeInto
func (d sqlDialect) upsert(conflictKeys, updateCols []string) string {
	if d.name == "mysql" {
		pairs := make([]string, len(updateCols))
		for i, c := range updateCols {
			pairs[i] = c + "=VALUES(" + c + ")"
		}
		return " ON DUPLICATE KEY UPDATE " + strings.Join(pairs, ", ")
	}
	pairs := make([]string, len(updateCols))
	for i, c := range updateCols {
		pairs[i] = c + "=excluded." + c
	}
	return " ON CONFLICT(" + strings.Join(conflictKeys, ", ") + ") DO UPDATE SET " + strings.Join(pairs, ", ")
}

// ph 占位符适配：金仓(PG 协议)要求 $n 位置参数，运行时重写使既有语句零改动；
// 其余方言(dm/mysql/sqlite)均使用 "?" 原样返回。
// 前提：库内 SQL 字符串字面量不含 "?"（已审计）
func (s *SQLStorage) ph(query string) string {
	if s.dialect.Name() != "kingbase" {
		return query
	}
	n := 0
	var b strings.Builder
	b.Grow(len(query) + 8)
	for _, r := range query {
		if r == '?' {
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// exec/query/queryRow 占位符感知的访问包装：全部 CRUD 经此三入口，
// 金仓重写只需一处生效。达梦走两段式 UPSERT 时亦经 exec
func (s *SQLStorage) exec(query string, args ...interface{}) (sql.Result, error) {
	return s.db.Exec(s.ph(query), args...)
}

func (s *SQLStorage) query(query string, args ...interface{}) (*sql.Rows, error) {
	return s.db.Query(s.ph(query), args...)
}

func (s *SQLStorage) queryRow(query string, args ...interface{}) *sql.Row {
	return s.db.QueryRow(s.ph(query), args...)
}

// driverNameForOpen sql.Open 的注册名：金仓复用 postgres 驱动
// （enterprise 侧已将 pq.Driver 以 "kingbase" 别名注册，二名皆可）
func driverNameForOpen(driver string) string { return driver }

// saveUpsert 统一 UPSERT 入口。dm 无 ON CONFLICT/ON DUPLICATE 语法——
// 两段式：先按冲突键 UPDATE（参数自 INSERT 列序解析重排），未命中再回退 INSERT；
// 其余方言单语句 INSERT+方言后缀。
// insertSQL 必须为不含后缀的裸 INSERT，且列清单与 VALUES 占位符一一对应
func (s *SQLStorage) saveUpsert(insertSQL string, args []interface{}, conflictKeys, updateCols []string) error {
	if s.dialect.Name() == "dm" {
		cols := extractInsertColumns(insertSQL)
		pos := make(map[string]int, len(cols))
		for i, c := range cols {
			pos[strings.ToLower(c)] = i
		}
		setParts := make([]string, 0, len(updateCols))
		setArgs := make([]interface{}, 0, len(updateCols))
		for _, c := range updateCols {
			setParts = append(setParts, c+" = ?")
			setArgs = append(setArgs, args[pos[strings.ToLower(c)]])
		}
		whereParts := make([]string, 0, len(conflictKeys))
		whereArgs := make([]interface{}, 0, len(conflictKeys))
		for _, k := range conflictKeys {
			whereParts = append(whereParts, k+" = ?")
			whereArgs = append(whereArgs, args[pos[strings.ToLower(k)]])
		}
		updateSQL := "UPDATE " + extractTable(insertSQL) +
			" SET " + strings.Join(setParts, ", ") +
			" WHERE " + strings.Join(whereParts, " AND ")
		res, err := s.exec(updateSQL, append(setArgs, whereArgs...)...)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			return nil
		}
	}
	_, err := s.exec(insertSQL+s.dialect.upsert(conflictKeys, updateCols), args...)
	return err
}

// extractTable 从 INSERT INTO <table> 提取表名（两段式 UPDATE 复用同一目标）
func extractTable(insertSQL string) string {
	s := strings.TrimSpace(insertSQL)
	if !strings.HasPrefix(strings.ToUpper(s), "INSERT INTO") {
		return ""
	}
	rest := strings.TrimSpace(s[len("INSERT INTO"):])
	if sp := strings.IndexAny(rest, " \t\n("); sp >= 0 {
		return rest[:sp]
	}
	return rest
}

// extractInsertColumns 从 "INSERT INTO t (a,b,c) VALUES ..." 解析列清单（保序）
func extractInsertColumns(insertSQL string) []string {
	open := strings.Index(insertSQL, "(")
	close := strings.Index(insertSQL, ")")
	if open < 0 || close < open {
		return nil
	}
	parts := strings.Split(insertSQL[open+1:close], ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}
