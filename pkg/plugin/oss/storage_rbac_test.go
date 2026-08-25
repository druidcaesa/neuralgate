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
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// TestSQLiteRBACMigrationAndSeed 建表含 RBAC 表；超管种子恰好一条；重启不重复；存量账号回填角色
func TestSQLiteRBACMigrationAndSeed(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "rbac.db")
	cfg := map[string]interface{}{"driver": "sqlite", "dsn": dsn}
	s := NewSQLStorage()
	if err := s.Init(cfg); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	// 模拟存量账号（无角色）
	if _, err := s.db.Exec(
		"INSERT INTO admin_users (id, username, password_hash, tenant_id, role_id, status, created_at, updated_at) VALUES ('u1','legacy','h','','','active',?,?)",
		timeToMS(time.Now()), timeToMS(time.Now()),
	); err != nil {
		t.Fatalf("insert legacy user: %v", err)
	}
	var roleCount int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM roles").Scan(&roleCount); err != nil {
		t.Fatalf("count roles: %v", err)
	}
	if roleCount != 1 {
		t.Fatalf("超管种子数 = %d, want 1", roleCount)
	}

	// 重启：种子不重复且回填 legacy 账号角色
	if err := s.Init(cfg); err != nil {
		t.Fatalf("second Init: %v", err)
	}
	defer s.Close()
	if err := s.db.QueryRow("SELECT COUNT(*) FROM roles").Scan(&roleCount); err != nil {
		t.Fatalf("recount roles: %v", err)
	}
	if roleCount != 1 {
		t.Errorf("重启后超管数 = %d, want 1(不重复)", roleCount)
	}
	var roleID string
	if err := s.db.QueryRow("SELECT role_id FROM admin_users WHERE id = 'u1'").Scan(&roleID); err != nil || roleID == "" {
		t.Errorf("存量账号未回填超管角色: %q err=%v", roleID, err)
	}
	for _, table := range []string{"tenants", "roles", "admin_operation_logs"} {
		var name string
		if err := s.db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name); err != nil {
			t.Errorf("表 %s 不存在: %v", table, err)
		}
	}
}

func TestMemTenantCRUD(t *testing.T) {
	s := NewMemStorage()
	tenant := &plugin.Tenant{Name: "租户A", Code: "tenant-a", Status: "active"}
	if err := s.SaveTenant(tenant); err != nil {
		t.Fatalf("SaveTenant: %v", err)
	}
	got, err := s.GetTenantByCode("tenant-a")
	if err != nil || got.Name != "租户A" {
		t.Fatalf("GetTenantByCode = %+v err=%v", got, err)
	}
	tenant.Name = "改名"
	_ = s.SaveTenant(tenant)
	list, total, _ := s.ListTenants(1, 10)
	if total != 1 || len(list) != 1 || list[0].Name != "改名" {
		t.Errorf("列表往返失败: total=%d %+v", total, list)
	}
	if n, _ := s.CountTenants(); n != 1 {
		t.Errorf("CountTenants = %d", n)
	}
	if err := s.DeleteTenant(tenant.ID); err != nil {
		t.Fatalf("DeleteTenant: %v", err)
	}
	if err := s.DeleteTenant(tenant.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("重复删除应 ErrNotFound, got %v", err)
	}
}

func TestMemRoleCRUD(t *testing.T) {
	s := NewMemStorage()
	role := &plugin.Role{Name: "只读", Permissions: []string{plugin.PermModelRead}}
	if err := s.SaveRole(role); err != nil {
		t.Fatalf("SaveRole: %v", err)
	}
	got, err := s.GetRoleByID(role.ID)
	if err != nil || len(got.Permissions) != 1 {
		t.Fatalf("GetRoleByID = %+v err=%v", got, err)
	}
	roles, _ := s.ListRoles()
	if len(roles) != 1 {
		t.Fatalf("ListRoles = %d", len(roles))
	}
	if !plugin.IsSuperRole(&plugin.Role{Name: plugin.SuperRoleName, Permissions: plugin.AllPermissions}) {
		t.Error("全权限全局角色应判定为超管")
	}
	if plugin.IsSuperRole(&plugin.Role{Name: plugin.SuperRoleName, TenantID: "t1", Permissions: plugin.AllPermissions}) {
		t.Error("租户内角色不应判定为超管")
	}
	if plugin.IsSuperRole(&plugin.Role{Name: plugin.SuperRoleName, Permissions: []string{plugin.PermModelRead}}) {
		t.Error("权限不全不应判定为超管")
	}
}

func TestMemAdminOperationLogs(t *testing.T) {
	s := NewMemStorage()
	for i := 0; i < 3; i++ {
		_ = s.SaveAdminOperationLog(&plugin.AdminOperationLog{
			UserID: "u1", Username: "admin", Method: "POST",
			Path: "/api/api-keys", StatusCode: 200,
			CreatedAt: time.Now().Add(time.Duration(i) * time.Second),
		})
	}
	items, total, err := s.ListAdminOperationLogs(plugin.AdminOpLogFilter{}, 1, 10)
	if err != nil || total != 3 || len(items) != 3 {
		t.Fatalf("total=%d len=%d err=%v", total, len(items), err)
	}
	filtered, ftotal, _ := s.ListAdminOperationLogs(plugin.AdminOpLogFilter{UserID: "u1"}, 1, 10)
	if ftotal != 3 || len(filtered) != 3 {
		t.Errorf("user 过滤不符: %d/%d", ftotal, len(filtered))
	}
	if _, ototal, _ := s.ListAdminOperationLogs(plugin.AdminOpLogFilter{UserID: "other"}, 1, 10); ototal != 0 {
		t.Errorf("他人过滤应为 0")
	}
}

func TestMemCounters(t *testing.T) {
	s := NewMemStorage()
	_ = s.SaveAPIKey(&plugin.APIKey{ID: "k1", KeyHash: "h1", TenantID: "t1"})
	_ = s.SaveAPIKey(&plugin.APIKey{ID: "k2", KeyHash: "h2", TenantID: "t2"})
	if n, _ := s.CountAPIKeysByTenantID("t1"); n != 1 {
		t.Errorf("CountAPIKeysByTenantID = %d, want 1", n)
	}
	_ = s.SaveAdminUser(&plugin.AdminUser{ID: "u1", Username: "a", RoleID: "r1"})
	if n, _ := s.CountAdminUsersByRoleID("r1"); n != 1 {
		t.Errorf("CountAdminUsersByRoleID = %d, want 1", n)
	}
}
