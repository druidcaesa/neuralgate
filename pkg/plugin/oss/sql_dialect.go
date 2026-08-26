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

import "strings"

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

// ph 占位符适配：当前全部方言使用 "?"；金仓($n)接线时在此做运行时重写，
// 使既有语句零改动获得多方言能力。前提：库内 SQL 字符串字面量不含 "?"（已审计）
func (s *SQLStorage) ph(query string) string {
	return query
}
