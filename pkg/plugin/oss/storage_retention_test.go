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
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// TestMemRetentionAcrossTables mem 版统一留存:四类日志只删超期、保留新记录
func TestMemRetentionAcrossTables(t *testing.T) {
	s := NewMemStorage()
	old := time.Now().Add(-72 * time.Hour)
	fresh := time.Now()

	if err := s.SaveAuditLog(&plugin.AuditLog{ID: "a-old", RequestID: "a-old", CreatedAt: old}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveAuditLog(&plugin.AuditLog{ID: "a-new", RequestID: "a-new", CreatedAt: fresh}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSecurityEvent(&plugin.SecurityEvent{ID: "s-old", CreatedAt: old}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSecurityEvent(&plugin.SecurityEvent{ID: "s-new", CreatedAt: fresh}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveMCPAuditLog(&plugin.MCPAuditLog{ID: "m-old", CreatedAt: old}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveMCPAuditLog(&plugin.MCPAuditLog{ID: "m-new", CreatedAt: fresh}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveAdminOperationLog(&plugin.AdminOperationLog{ID: "o-old", CreatedAt: old}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveAdminOperationLog(&plugin.AdminOperationLog{ID: "o-new", CreatedAt: fresh}); err != nil {
		t.Fatal(err)
	}

	cutoff := time.Now().Add(-24 * time.Hour)
	for name, fn := range map[string]func(time.Time) (int64, error){
		"audit": s.DeleteAuditLogsBefore,
		"sec":   s.DeleteSecurityEventsBefore,
		"mcp":   s.DeleteMCPAuditLogsBefore,
		"oplog": s.DeleteOperationLogsBefore,
	} {
		if n, err := fn(cutoff); err != nil || n != 1 {
			t.Fatalf("%s 应删 1 条: n=%d err=%v", name, n, err)
		}
	}
	if len(s.auditLogs) != 1 || len(s.securityEvents) != 1 || len(s.mcpAuditLogs) != 1 || len(s.adminOpLogs) != 1 {
		t.Errorf("各表应各留 1 条新记录: audit=%d sec=%d mcp=%d oplog=%d",
			len(s.auditLogs), len(s.securityEvents), len(s.mcpAuditLogs), len(s.adminOpLogs))
	}
}

// TestSQLRetentionOtherTables sqlite 版:security_events/mcp_audit_logs/admin_operation_logs 留存删除
func TestSQLRetentionOtherTables(t *testing.T) {
	s := NewSQLStorage()
	if err := s.Init(map[string]interface{}{"driver": "sqlite",
		"dsn":         t.TempDir() + "/ret.db",
		"encrypt_key": "k"}); err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// 各表按需显式给 NOT NULL 无默认列(security_events.request_id、admin_operation_logs.user_id/method)
	oldMS := time.Now().Add(-72 * time.Hour).UnixMilli()
	newMS := time.Now().UnixMilli()
	insert := func(table, cols string, row []any) {
		qs := ""
		for i := 0; i < len(row); i++ {
			qs += "?"
			if i < len(row)-1 {
				qs += ", "
			}
		}
		if _, err := s.exec("INSERT INTO "+table+" ("+cols+") VALUES ("+qs+")", row...); err != nil {
			t.Fatalf("seed %s: %v", table, err)
		}
	}
	insert("security_events", "id, request_id, created_at", []any{"old", "req", oldMS})
	insert("security_events", "id, request_id, created_at", []any{"new", "req", newMS})
	insert("mcp_audit_logs", "id, created_at", []any{"old", oldMS})
	insert("mcp_audit_logs", "id, created_at", []any{"new", newMS})
	insert("admin_operation_logs", "id, user_id, method, created_at", []any{"old", "uid", "GET", oldMS})
	insert("admin_operation_logs", "id, user_id, method, created_at", []any{"new", "uid", "GET", newMS})

	cutoff := time.Now().Add(-24 * time.Hour)
	checks := []struct {
		name string
		fn   func(time.Time) (int64, error)
	}{
		{"security_events", s.DeleteSecurityEventsBefore},
		{"mcp_audit_logs", s.DeleteMCPAuditLogsBefore},
		{"admin_operation_logs", s.DeleteOperationLogsBefore},
	}
	for _, c := range checks {
		if n, err := c.fn(cutoff); err != nil || n != 1 {
			t.Fatalf("%s 应删 1 条: n=%d err=%v", c.name, n, err)
		}
	}
	// 新记录(id=new)应保留
	cut := timeToMS(cutoff)
	for _, table := range []string{"security_events", "mcp_audit_logs", "admin_operation_logs"} {
		var newLeft int64
		if err := s.queryRow("SELECT COUNT(*) FROM " + table + " WHERE id = 'new'").Scan(&newLeft); err != nil || newLeft != 1 {
			t.Errorf("%s 应保留新记录: left=%d err=%v", table, newLeft, err)
		}
		var oldLeft int64
		if err := s.queryRow("SELECT COUNT(*) FROM "+table+" WHERE created_at < ?", cut).Scan(&oldLeft); err != nil || oldLeft != 0 {
			t.Errorf("%s 超期应删净: left=%d err=%v", table, oldLeft, err)
		}
	}
}
