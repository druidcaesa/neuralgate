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

func sampleMCPAudit(id string, at time.Time) *plugin.MCPAuditLog {
	return &plugin.MCPAuditLog{
		ID:            id,
		RequestID:     "req-" + id,
		TenantID:      "t1",
		APIKeyID:      "k1",
		ToolName:      "search",
		ToolArguments: `{"q":"go"}`,
		ToolResult:    `{"hits":3}`,
		CallerAgent:   "claude-desktop",
		DurationMS:    120,
		Status:        plugin.MCPStatusSuccess,
		ClientIP:      "10.0.0.1",
		CreatedAt:     at,
	}
}

// TestMemStorageMCPAuditFilters 各筛选维度精确命中与组合、闭区间时间窗
func TestMemStorageMCPAuditFilters(t *testing.T) {
	s := NewMemStorage()
	base := time.Date(2026, 8, 26, 10, 0, 0, 0, time.Local)
	e1 := sampleMCPAudit("a", base)                  // search/success/t1
	e2 := sampleMCPAudit("b", base.Add(time.Minute)) // search/success/t1
	e2.ToolName = "fetch"
	e3 := sampleMCPAudit("c", base.Add(2*time.Minute)) // failed/t2
	e3.Status = plugin.MCPStatusFailed
	e3.ErrorMessage = "tool exploded"
	e3.TenantID = "t2"
	for _, e := range []*plugin.MCPAuditLog{e1, e2, e3} {
		if err := s.SaveMCPAuditLog(e); err != nil {
			t.Fatal(err)
		}
	}
	// 按 status
	got, total, _ := s.ListMCPAuditLogs(plugin.MCPAuditLogFilter{Status: plugin.MCPStatusFailed}, 1, 10)
	if total != 1 || got[0].ID != "c" || got[0].ErrorMessage != "tool exploded" {
		t.Errorf("status 筛选不符: total=%d %+v", total, got)
	}
	// 按工具名 + 租户组合(e2 是 fetch,只有 e1 命中)
	got, total, _ = s.ListMCPAuditLogs(plugin.MCPAuditLogFilter{TenantID: "t1", ToolName: "search"}, 1, 10)
	if total != 1 || got[0].ID != "a" {
		t.Errorf("组合筛选应只命中 a, got %d %+v", total, got)
	}
	// 按 request_id
	got, _, _ = s.ListMCPAuditLogs(plugin.MCPAuditLogFilter{RequestID: "req-b"}, 1, 10)
	if len(got) != 1 || got[0].ID != "b" {
		t.Errorf("request_id 筛选不符: %+v", got)
	}
	// 时间窗闭区间：端点值也计入
	winStart := base.Add(time.Minute)
	winEnd := base.Add(2 * time.Minute)
	got, total, _ = s.ListMCPAuditLogs(plugin.MCPAuditLogFilter{StartTime: &winStart, EndTime: &winEnd}, 1, 10)
	if total != 2 || got[0].ID != "c" || got[1].ID != "b" {
		t.Errorf("时间窗(闭区间)应含 b,c 且倒序: total=%d %s,%s", total, got[0].ID, got[1].ID)
	}
	// 分页
	got, total, _ = s.ListMCPAuditLogs(plugin.MCPAuditLogFilter{}, 2, 2)
	if total != 3 || len(got) != 1 || got[0].ID != "a" {
		t.Errorf("第二页应为最旧一条 a: total=%d %+v", total, got)
	}
}

// TestMemStorageMCPAuditDefaults ID/CreatedAt 零值自动补齐
func TestMemStorageMCPAuditDefaults(t *testing.T) {
	s := NewMemStorage()
	e := &plugin.MCPAuditLog{ToolName: "x", Status: plugin.MCPStatusSuccess}
	if err := s.SaveMCPAuditLog(e); err != nil {
		t.Fatal(err)
	}
	if e.ID == "" || e.CreatedAt.IsZero() {
		t.Errorf("空 ID/CreatedAt 应自动补齐: %+v", e)
	}
}

// TestSQLiteMCPAuditLogs 落库往返(created_at ms 还原)与筛选
func TestSQLiteMCPAuditLogs(t *testing.T) {
	s := NewSQLStorage()
	if err := s.Init(map[string]interface{}{"driver": "sqlite", "dsn": filepath.Join(t.TempDir(), "audit.db")}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer s.Close()
	at := time.Date(2026, 8, 26, 9, 30, 0, 0, time.Local)
	e := sampleMCPAudit("sql-1", at)
	e.Status = plugin.MCPStatusFailed
	e.ErrorMessage = "boom"
	if err := s.SaveMCPAuditLog(e); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, total, _ := s.ListMCPAuditLogs(plugin.MCPAuditLogFilter{Status: plugin.MCPStatusFailed}, 1, 10)
	if total != 1 || len(got) != 1 {
		t.Fatalf("筛选总数不符: %d", total)
	}
	g := got[0]
	if g.RequestID != e.RequestID || g.CallerAgent != e.CallerAgent || !g.CreatedAt.Equal(at) ||
		g.DurationMS != e.DurationMS || g.ErrorMessage != "boom" {
		t.Errorf("往返字段不符: %+v", g)
	}
}
