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

package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"golang.org/x/crypto/bcrypt"
)

// rbacAPIData 解析统一响应 data 字段
func rbacAPIData(t *testing.T, rec *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var resp struct {
		Code int                    `json:"code"`
		Data map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应非 JSON: %v body=%s", err, rec.Body.String())
	}
	return resp.Data
}

func dataID(t *testing.T, data map[string]interface{}) string {
	t.Helper()
	id, _ := data["id"].(string)
	if id == "" {
		t.Fatalf("响应缺少 id: %v", data)
	}
	return id
}

func TestTenantAPI(t *testing.T) {
	f := newRBACFixture(t, true)

	rec := f.do(f.superTok, http.MethodPost, "/api/tenants",
		`{"name":"租户A","code":"tenanta","config":{"env":"prod"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("创建租户失败: %d %s", rec.Code, rec.Body.String())
	}
	tenantID := dataID(t, rbacAPIData(t, rec))

	// code 重复 → 409
	if rec = f.do(f.superTok, http.MethodPost, "/api/tenants", `{"name":"重复","code":"tenanta"}`); rec.Code != http.StatusConflict {
		t.Errorf("重名 code 应 409, got %d", rec.Code)
	}

	// 列表
	rec = f.do(f.superTok, http.MethodGet, "/api/tenants?page=1&size=10", "")
	data := rbacAPIData(t, rec)
	if total, _ := data["total"].(float64); total != 1 {
		t.Errorf("租户总数应为 1, got %v", data["total"])
	}

	// 更新状态
	if rec = f.do(f.superTok, http.MethodPut, "/api/tenants/"+tenantID,
		`{"name":"租户A","code":"tenanta","status":"disabled"}`); rec.Code != http.StatusOK {
		t.Errorf("更新租户失败: %d %s", rec.Code, rec.Body.String())
	}
	got, _ := f.storage.GetTenantByID(tenantID)
	if got.Status != plugin.TenantStatusDisabled {
		t.Errorf("禁用未生效: %+v", got)
	}

	// 删除无 Key 租户成功；再删 404
	if rec = f.do(f.superTok, http.MethodDelete, "/api/tenants/"+tenantID, ""); rec.Code != http.StatusOK {
		t.Errorf("删除租户失败: %d", rec.Code)
	}
	if rec = f.do(f.superTok, http.MethodDelete, "/api/tenants/"+tenantID, ""); rec.Code != http.StatusNotFound {
		t.Errorf("重复删除应 404, got %d", rec.Code)
	}
}

func TestTenantDeleteBlockedWithKeys(t *testing.T) {
	f := newRBACFixture(t, true)
	rec := f.do(f.superTok, http.MethodPost, "/api/tenants", `{"name":"占用","code":"occupied1"}`)
	tenantID := dataID(t, rbacAPIData(t, rec))
	now := time.Now()
	_ = f.storage.SaveAPIKey(&plugin.APIKey{ID: "k1", KeyHash: "h1", TenantID: tenantID,
		Status: plugin.APIKeyStatusActive, CreatedAt: now, UpdatedAt: now})

	if rec = f.do(f.superTok, http.MethodDelete, "/api/tenants/"+tenantID, ""); rec.Code != http.StatusConflict {
		t.Errorf("有 Key 的租户删除应 409, got %d", rec.Code)
	}
}

func TestRoleAPI(t *testing.T) {
	f := newRBACFixture(t, true)

	// 非法权限码 → 400
	rec := f.do(f.superTok, http.MethodPost, "/api/roles",
		`{"name":"坏角色","permissions":["model:read","not:a:perm"]}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("非法权限码应 400, got %d", rec.Code)
	}

	rec = f.do(f.superTok, http.MethodPost, "/api/roles",
		`{"name":"运维","permissions":["model:read","audit:read"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("创建角色失败: %d %s", rec.Code, rec.Body.String())
	}
	roleID := dataID(t, rbacAPIData(t, rec))

	// 超管角色不可改删（fixture 已建超管角色）
	var roles struct {
		Data struct {
			Items []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"items"`
		} `json:"data"`
	}
	listRec := f.do(f.superTok, http.MethodGet, "/api/roles", "")
	if err := json.Unmarshal(listRec.Body.Bytes(), &roles); err != nil {
		t.Fatal(err)
	}
	superRoleID := ""
	for _, r := range roles.Data.Items {
		if r.Name == plugin.SuperRoleName {
			superRoleID = r.ID
		}
	}
	if superRoleID == "" {
		t.Fatal("超管角色应在列表中")
	}
	if rec = f.do(f.superTok, http.MethodDelete, "/api/roles/"+superRoleID, ""); rec.Code != http.StatusConflict {
		t.Errorf("删除超管角色应 409, got %d", rec.Code)
	}
	if rec = f.do(f.superTok, http.MethodPut, "/api/roles/"+superRoleID,
		`{"name":"x","permissions":["model:read"]}`); rec.Code != http.StatusConflict {
		t.Errorf("修改超管角色应 409, got %d", rec.Code)
	}

	// 有账号挂载的角色不可删
	scopedUser := &plugin.AdminUser{ID: "u-holder", Username: "holder",
		TenantID: "t-x", RoleID: roleID, Status: plugin.AdminUserStatusActive}
	if err := f.storage.SaveAdminUser(scopedUser); err != nil {
		t.Fatal(err)
	}
	if rec = f.do(f.superTok, http.MethodDelete, "/api/roles/"+roleID, ""); rec.Code != http.StatusConflict {
		t.Errorf("有账号挂载删角色应 409, got %d", rec.Code)
	}
	_ = f.storage.DeleteAdminUser(scopedUser.ID)
	if rec = f.do(f.superTok, http.MethodDelete, "/api/roles/"+roleID, ""); rec.Code != http.StatusOK {
		t.Errorf("无挂载删角色应成功, got %d", rec.Code)
	}
}

func TestAdminUserAPI(t *testing.T) {
	f := newRBACFixture(t, true)
	f.do(f.superTok, http.MethodPost, "/api/tenants", `{"name":"租户B","code":"tb"}`)

	// 创建：role_id 缺失应被校验拒绝
	rec := f.do(f.superTok, http.MethodPost, "/api/admin-users",
		`{"username":"ops01","password":"secret-pass-1","tenant_id":"","role_id":""}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("空 role_id 应被校验拒绝(400), got %d %s", rec.Code, rec.Body.String())
	}

	roleRec := f.do(f.superTok, http.MethodPost, "/api/roles", `{"name":"B只读","permissions":["model:read"]}`)
	roleID := dataID(t, rbacAPIData(t, roleRec))

	rec = f.do(f.superTok, http.MethodPost, "/api/admin-users",
		`{"username":"ops01","password":"secret-pass-1","role_id":"`+roleID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("创建用户失败: %d %s", rec.Code, rec.Body.String())
	}
	if dataID(t, rbacAPIData(t, rec)) == "" {
		t.Fatal("创建用户应返回 id")
	}
	if strings.Contains(rec.Body.String(), "$2") || strings.Contains(rec.Body.String(), "password_hash") {
		t.Error("响应不应回显密码哈希")
	}

	// 重名 → 409
	if rec = f.do(f.superTok, http.MethodPost, "/api/admin-users",
		`{"username":"ops01","password":"secret-pass-1","role_id":"`+roleID+`"}`); rec.Code != http.StatusConflict {
		t.Errorf("重名用户应 409, got %d", rec.Code)
	}

	// 删除自己 → 409
	superUser, _ := f.storage.GetAdminUserByUsername("root")
	if rec = f.do(f.superTok, http.MethodDelete, "/api/admin-users/"+superUser.ID, ""); rec.Code != http.StatusConflict {
		t.Errorf("删除自己应 409, got %d", rec.Code)
	}

	// 删除最后一个超管账号(另一个超管) → 409
	hash, _ := bcrypt.GenerateFromPassword([]byte("another-pass-1"), bcrypt.MinCost)
	secondSuper := &plugin.AdminUser{ID: "u-super2", Username: "super2",
		PasswordHash: string(hash), RoleID: superUserID(t, f), Status: plugin.AdminUserStatusActive}
	_ = f.storage.SaveAdminUser(secondSuper)
	if rec = f.do(f.superTok, http.MethodDelete, "/api/admin-users/"+secondSuper.ID, ""); rec.Code != http.StatusOK {
		t.Errorf("非最后超管可删, got %d %s", rec.Code, rec.Body.String())
	}
	lastSuper := &plugin.AdminUser{ID: "u-super3", Username: "super3",
		PasswordHash: string(hash), RoleID: superUserID(t, f), Status: plugin.AdminUserStatusActive}
	_ = f.storage.SaveAdminUser(lastSuper)
	// 删掉 root 自己之外的两个超管之一后，仅剩 root 与 lastSuper；root 删 lastSuper 时只剩自己 → 允许
	// 构造真正“最后一个超管”场景：临时把 root 角色改为只读
	rootUser, _ := f.storage.GetAdminUserByUsername("root")
	readOnly := &plugin.Role{Name: "降权", Permissions: []string{plugin.PermModelRead}}
	_ = f.storage.SaveRole(readOnly)
	origRole := rootUser.RoleID
	rootUser.RoleID = readOnly.ID
	_ = f.storage.SaveAdminUser(rootUser)
	if rec = f.do(f.superTok, http.MethodDelete, "/api/admin-users/"+lastSuper.ID, ""); rec.Code != http.StatusConflict {
		t.Errorf("删除最后一个超管应 409, got %d", rec.Code)
	}
	rootUser.RoleID = origRole
	_ = f.storage.SaveAdminUser(rootUser)
}

// superUserID 取 fixture 中超管角色 ID
func superUserID(t *testing.T, f *rbacFixture) string {
	t.Helper()
	roles, _ := f.storage.ListRoles()
	for _, r := range roles {
		if plugin.IsSuperRole(r) {
			return r.ID
		}
	}
	t.Fatal("超管角色不存在")
	return ""
}

func TestOperationLogsWrittenOnMutationsOnly(t *testing.T) {
	f := newRBACFixture(t, true)

	f.do(f.superTok, http.MethodGet, "/api/tenants?page=1&size=10", "") // 读不落库
	createRec := f.do(f.superTok, http.MethodPost, "/api/tenants", `{"name":"审计租户","code":"auditt"}`)
	tenantID := dataID(t, rbacAPIData(t, createRec))
	f.do(f.superTok, http.MethodDelete, "/api/tenants/"+tenantID, "") // 带 :id 的写操作

	logs, total, err := f.storage.ListAdminOperationLogs(plugin.AdminOpLogFilter{}, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(logs) != 2 {
		t.Fatalf("仅两条写操作应落库: total=%d len=%d", total, len(logs))
	}
	del := logs[0] // 倒序，最新在前
	if del.Method != http.MethodDelete || del.Path != "/api/tenants/"+tenantID ||
		del.TargetID != tenantID || del.StatusCode != http.StatusOK || del.Username != "root" {
		t.Errorf("删除日志字段不符: %+v", del)
	}
}

// TestScopedUserCannotManageOtherTenants 租户内用户即使持 tenant:write 也仅能管本租户（规格 §4.3 边界）
func TestScopedUserCannotManageOtherTenants(t *testing.T) {
	f := newRBACFixture(t, true)
	// 给租户A用户追加 tenant/rbac 写权限(模拟误配)
	scoped, _ := f.storage.GetAdminUserByID("u-scoped")
	wide := &plugin.Role{Name: "越权角色", TenantID: rbacTestTenantA,
		Permissions: plugin.AllPermissions}
	_ = f.storage.SaveRole(wide)
	scoped.RoleID = wide.ID
	_ = f.storage.SaveAdminUser(scoped)
	// 重新签发带全权限的会话(仍非超管: 角色挂租户A且…注意全权限+租户内≠超管)
	sm := NewSessionManager([]byte("rbac-test-secret"), time.Hour)
	tok, _, _ := sm.Mint(scoped, plugin.AllPermissions, false, time.Now())

	// 建/改/删其他租户 → 应被拒
	if rec := f.do(tok, http.MethodPost, "/api/tenants", `{"name":"越权","code":"yuequan1"}`); rec.Code != http.StatusForbidden {
		t.Errorf("租户内用户建租户应 403, got %d", rec.Code)
	}

	// 改删他租户的角色 → 404 不暴露存在性
	foreignRole := &plugin.Role{Name: "B租户角色", TenantID: rbacTestTenantB, Permissions: []string{plugin.PermModelRead}}
	_ = f.storage.SaveRole(foreignRole)
	if rec := f.do(tok, http.MethodDelete, "/api/roles/"+foreignRole.ID, ""); rec.Code != http.StatusNotFound {
		t.Errorf("跨租户删角色应 404, got %d", rec.Code)
	}

	// 改删他租户的用户 → 404
	foreignUser := &plugin.AdminUser{ID: "u-foreign", Username: "foreign",
		TenantID: rbacTestTenantB, RoleID: foreignRole.ID, Status: plugin.AdminUserStatusActive}
	_ = f.storage.SaveAdminUser(foreignUser)
	if rec := f.do(tok, http.MethodDelete, "/api/admin-users/"+foreignUser.ID, ""); rec.Code != http.StatusNotFound {
		t.Errorf("跨租户删用户应 404, got %d", rec.Code)
	}

	// 列表只见本租户
	var rolesResp struct {
		Data struct {
			Items []struct {
				TenantID string `json:"tenant_id"`
			} `json:"items"`
		} `json:"data"`
	}
	rec := f.do(tok, http.MethodGet, "/api/roles", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &rolesResp); err != nil {
		t.Fatal(err)
	}
	for _, r := range rolesResp.Data.Items {
		if r.TenantID != "" && r.TenantID != rbacTestTenantA {
			t.Errorf("租户内用户不应看到他租户角色: %s", r.TenantID)
		}
	}
}
