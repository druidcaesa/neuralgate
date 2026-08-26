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

import "testing"

// TestUpsertSuffixMySQL MySQL 形态：VALUES 引用，忽略冲突键
func TestUpsertSuffixMySQL(t *testing.T) {
	got := dialectFor("mysql").upsert([]string{"id"}, []string{"name", "status"})
	want := " ON DUPLICATE KEY UPDATE name=VALUES(name), status=VALUES(status)"
	if got != want {
		t.Errorf("mysql 后缀不符:\n got %q\nwant %q", got, want)
	}
}

// TestUpsertSuffixSQLite SQLite 形态：显式冲突键 + excluded 引用
func TestUpsertSuffixSQLite(t *testing.T) {
	got := dialectFor("sqlite").upsert([]string{"id"}, []string{"name", "status"})
	want := " ON CONFLICT(id) DO UPDATE SET name=excluded.name, status=excluded.status"
	if got != want {
		t.Errorf("sqlite 后缀不符:\n got %q\nwant %q", got, want)
	}
}

// TestUpsertSuffixBusinessKey 业务键冲突(合规报表 period_type+period_start)同样成立
func TestUpsertSuffixBusinessKey(t *testing.T) {
	got := dialectFor("sqlite").upsert([]string{"period_type", "period_start"}, []string{"content"})
	want := " ON CONFLICT(period_type, period_start) DO UPDATE SET content=excluded.content"
	if got != want {
		t.Errorf("业务键后缀不符:\n got %q\nwant %q", got, want)
	}
}

// TestPhPlaceholderPassthrough 本阶段 ph 恒原样返回(? 重写在金仓接线时启用)
func TestPhPlaceholderPassthrough(t *testing.T) {
	q := "SELECT * FROM t WHERE a = ? AND b = ?"
	if got := NewSQLStorage().ph(q); got != q {
		t.Errorf("ph 应原样返回, got %q", got)
	}
}
