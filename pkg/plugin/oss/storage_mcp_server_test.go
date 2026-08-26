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
	"path/filepath"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

func sampleMCPServer(id, name string) *plugin.MCPServer {
	return &plugin.MCPServer{
		ID:       id,
		Name:     name,
		Endpoint: "http://127.0.0.1:9000/mcp",
		Headers:  map[string]string{"Authorization": "Bearer upstream-token"},
		Enabled:  true,
	}
}

// assertMCPServerEqual 逐字段断言(Headers 深度相等)
func assertMCPServerEqual(t *testing.T, got, want *plugin.MCPServer) {
	t.Helper()
	if got.ID != want.ID || got.Name != want.Name || got.Endpoint != want.Endpoint || got.Enabled != want.Enabled {
		t.Fatalf("标量字段不符: %+v", got)
	}
	if len(got.Headers) != len(want.Headers) {
		t.Fatalf("Headers 数量不符: %+v", got.Headers)
	}
	for k, v := range want.Headers {
		if got.Headers[k] != v {
			t.Errorf("Headers[%s] = %s, want %s", k, got.Headers[k], v)
		}
	}
}

// TestMemStorageMCPServerRoundTrip 空ID自动生成;Save→Get 往返含 Headers
func TestMemStorageMCPServerRoundTrip(t *testing.T) {
	s := NewMemStorage()
	srv := sampleMCPServer("", "tools-a")
	if err := s.SaveMCPServer(srv); err != nil {
		t.Fatal(err)
	}
	if srv.ID == "" {
		t.Fatal("空 ID 应自动生成")
	}
	got, err := s.GetMCPServer(srv.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertMCPServerEqual(t, got, srv)
}

// TestMemStorageMCPServerUpsert 同 ID 重存覆盖且总数不变
func TestMemStorageMCPServerUpsert(t *testing.T) {
	s := NewMemStorage()
	srv := sampleMCPServer("m1", "old-name")
	_ = s.SaveMCPServer(srv)
	srv.Name = "new-name"
	srv.Enabled = false
	if err := s.SaveMCPServer(srv); err != nil {
		t.Fatal(err)
	}
	list, total, _ := s.ListMCPServers(1, 10)
	if total != 1 {
		t.Fatalf("总数应仍为 1, got %d", total)
	}
	if list[0].Name != "new-name" || list[0].Enabled {
		t.Errorf("应覆盖为新值: %+v", list[0])
	}
}

// TestMemStorageMCPServerNotFound 未命中 ErrNotFound
func TestMemStorageMCPServerNotFound(t *testing.T) {
	s := NewMemStorage()
	if _, err := s.GetMCPServer("nope"); err != ErrNotFound {
		t.Errorf("应 ErrNotFound, got %v", err)
	}
}

// TestMemStorageMCPServerListOrderAndPaging name 升序+分页
func TestMemStorageMCPServerListOrderAndPaging(t *testing.T) {
	s := NewMemStorage()
	for _, n := range []string{"beta", "alpha", "gamma"} {
		_ = s.SaveMCPServer(sampleMCPServer("id-"+n, n))
	}
	page1, total, _ := s.ListMCPServers(1, 2)
	page2, _, _ := s.ListMCPServers(2, 2)
	if total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}
	if page1[0].Name != "alpha" || page1[1].Name != "beta" || page2[0].Name != "gamma" {
		t.Errorf("应按 name 升序分页: %s %s | %s", page1[0].Name, page1[1].Name, page2[0].Name)
	}
}

// TestMemStorageMCPServerDelete 删除后未命中
func TestMemStorageMCPServerDelete(t *testing.T) {
	s := NewMemStorage()
	srv := sampleMCPServer("del-1", "x")
	_ = s.SaveMCPServer(srv)
	if err := s.DeleteMCPServer("del-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetMCPServer("del-1"); err != ErrNotFound {
		t.Errorf("删除后应 ErrNotFound, got %v", err)
	}
}

// TestSQLiteMCPServers 建表存在、UPSERT 幂等、headers JSON 往返
func TestSQLiteMCPServers(t *testing.T) {
	s := NewSQLStorage()
	if err := s.Init(map[string]interface{}{"driver": "sqlite", "dsn": filepath.Join(t.TempDir(), "mcp.db")}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer s.Close()
	srv := sampleMCPServer("sql-1", "upstream")
	if err := s.SaveMCPServer(srv); err != nil {
		t.Fatalf("Save: %v", err)
	}
	again := sampleMCPServer("sql-1", "renamed")
	_ = s.SaveMCPServer(again)
	list, total, _ := s.ListMCPServers(1, 10)
	if total != 1 || list[0].Name != "renamed" {
		t.Fatalf("UPSERT 幂等不符: total=%d name=%s", total, list[0].Name)
	}
	got, err := s.GetMCPServer("sql-1")
	if err != nil {
		t.Fatal(err)
	}
	assertMCPServerEqual(t, got, again)
	var table string
	if err := s.db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='mcp_servers'").Scan(&table); err != nil {
		t.Fatalf("表不存在: %v", err)
	}
	_ = time.Now
}
