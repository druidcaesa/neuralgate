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

// TestPhKingbaseRewrite 金仓占位符运行时重写 ?→$n；其余驱动原样
func TestPhKingbaseRewrite(t *testing.T) {
	s := NewSQLStorage()
	s.driver = "kingbase"
	s.dialect = dialectFor("kingbase")
	got := s.ph("INSERT INTO t (a,b) VALUES (?, ?) ON CONFLICT(id) DO UPDATE SET a=excluded.a WHERE x = ?")
	want := "INSERT INTO t (a,b) VALUES ($1, $2) ON CONFLICT(id) DO UPDATE SET a=excluded.a WHERE x = $3"
	if got != want {
		t.Errorf("金仓占位符重写不符:\n got %q\nwant %q", got, want)
	}
	if other := "SELECT 1 WHERE a = ?"; s2ph(other) != other {
		t.Error("非金仓驱动不应重写")
	}
}

func s2ph(q string) string {
	s := NewSQLStorage()
	s.driver = "mysql"
	s.dialect = dialectFor("mysql")
	return s.ph(q)
}

// TestSaveUpsertDMTwoPhase 达梦无 ON CONFLICT/DUPLICATE——两段式(update 未中再 insert):
// 以 sqlite 库模拟 dm 方言位,验证首存插入、重存覆盖且总数不变
func TestSaveUpsertDMTwoPhase(t *testing.T) {
	s := NewSQLStorage()
	if err := s.Init(map[string]interface{}{"driver": "sqlite", "dsn": t.TempDir() + "/dm-sim.db"}); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.driver = "dm" // 模拟达梦方言位(saveUpsert 据此走两段式)
	s.dialect = dialectFor("dm")

	srv := sampleMCPServer("dm-1", "upstream")
	srv.ID = "dm-1"
	if err := s.SaveMCPServer(srv); err != nil {
		t.Fatalf("首存应成功: %v", err)
	}
	srv.Name = "renamed"
	if err := s.SaveMCPServer(srv); err != nil {
		t.Fatalf("重存(两段式覆盖)应成功: %v", err)
	}
	list, total, _ := s.ListMCPServers(1, 10)
	if total != 1 || list[0].Name != "renamed" {
		t.Errorf("两段式 UPSERT 不符: total=%d name=%s", total, list[0].Name)
	}
}
