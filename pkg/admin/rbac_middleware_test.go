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
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

const rbacTestTenantA = "tenant-a"
const rbacTestTenantB = "tenant-b"

// rbacFixture 双租户环境：超管 root + 租户 A 的 scoped 用户（api_key/model/audit/rate_limit 读写）
type rbacFixture struct {
	s         *AdminServer
	storage   *oss.MemStorage
	superTok  string
	scopedTok string
}

func newRBACFixture(t *testing.T, enableRBAC bool) *rbacFixture {
	t.Helper()
	storage := oss.NewMemStorage()
	f := &rbacFixture{s: NewAdminServer(storage, zap.NewNop(), "enterprise", oss.NewRateLimiter(oss.NewMemStorage(), 100, 100000, "token_bucket"), nil), storage: storage}
	sm := NewSessionManager([]byte("rbac-test-secret"), time.Hour)
	f.s.EnableAuth(sm, nil)
	if enableRBAC {
		f.s.EnableRBAC(true)
	}

	hash, _ := bcrypt.GenerateFromPassword([]byte("password-123"), bcrypt.MinCost)
	now := time.Now()

	superRole := &plugin.Role{Name: plugin.SuperRoleName, Permissions: plugin.AllPermissions}
	_ = storage.SaveRole(superRole)
	super := &plugin.AdminUser{
		ID: "u-root", Username: "root", PasswordHash: string(hash),
		RoleID: superRole.ID, Status: plugin.AdminUserStatusActive, CreatedAt: now, UpdatedAt: now,
	}
	_ = storage.SaveAdminUser(super)

	scopedRole := &plugin.Role{Name: "租户A读写", TenantID: rbacTestTenantA,
		Permissions: []string{plugin.PermAPIKeyRead, plugin.PermAPIKeyWrite,
			plugin.PermModelRead, plugin.PermAuditRead,
			plugin.PermRateLimitRead, plugin.PermRateLimitWrite}}
	_ = storage.SaveRole(scopedRole)
	scoped := &plugin.AdminUser{
		ID: "u-scoped", Username: "scoped", PasswordHash: string(hash),
		TenantID: rbacTestTenantA, RoleID: scopedRole.ID, Status: plugin.AdminUserStatusActive,
		CreatedAt: now, UpdatedAt: now,
	}
	_ = storage.SaveAdminUser(scoped)

	f.superTok, _, _ = sm.Mint(super, superRole.Permissions, true, now)
	f.scopedTok, _, _ = sm.Mint(scoped, scopedRole.Permissions, false, now)
	return f
}

func (f *rbacFixture) do(token, method, path, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.Header.Set(tokenHeader, token)
	rec := httptest.NewRecorder()
	f.s.Router().ServeHTTP(rec, req)
	return rec
}

// TestRequirePermissionDisabledByDefault 未启用 RBAC 时恒放行（现状行为零变化）
func TestRequirePermissionDisabledByDefault(t *testing.T) {
	f := newRBACFixture(t, false)
	rec := f.do(f.scopedTok, http.MethodPost, "/api/api-keys", `{"name":"k"}`)
	if rec.Code == http.StatusForbidden && strings.Contains(rec.Body.String(), "无权限") {
		t.Errorf("未启用 RBAC 不应 403: %d %s", rec.Code, rec.Body.String())
	}
}

// TestRequirePermissionForbidden 无所需权限 → 403 无权限
func TestRequirePermissionForbidden(t *testing.T) {
	f := newRBACFixture(t, true)
	rec := f.do(f.scopedTok, http.MethodPost, "/api/models", `{"name":"m"}`)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "无权限") {
		t.Errorf("scoped 建模型应 403 无权限, got %d %s", rec.Code, rec.Body.String())
	}
}

// TestRequirePermissionAllowedByPerm 持有所需权限放行
func TestRequirePermissionAllowedByPerm(t *testing.T) {
	f := newRBACFixture(t, true)
	rec := f.do(f.scopedTok, http.MethodGet, "/api/api-keys?page=1&size=10", "")
	if rec.Code != http.StatusOK {
		t.Errorf("scoped 列 Key 应放行, got %d %s", rec.Code, rec.Body.String())
	}
}

// TestSuperBypassesPermissions 超管不受权限码限制
func TestSuperBypassesPermissions(t *testing.T) {
	f := newRBACFixture(t, true)
	rec := f.do(f.superTok, http.MethodPost, "/api/rate-limits",
		`{"tenant_id":"","model_name":"m","requests_per_sec":1,"tokens_per_min":1,"strategy":"token_bucket"}`)
	if rec.Code == http.StatusForbidden {
		t.Errorf("超管不应被权限码拦截: %s", rec.Body.String())
	}
}

// seedScopedKeys 两租户各一枚 Key，返回 [本租户KeyID, 他租户KeyID]
func seedScopedKeys(t *testing.T, f *rbacFixture) (string, string) {
	t.Helper()
	now := time.Now()
	keyA := &plugin.APIKey{ID: "key-a", KeyHash: "hash-a", TenantID: rbacTestTenantA, Status: plugin.APIKeyStatusActive, CreatedAt: now, UpdatedAt: now}
	keyB := &plugin.APIKey{ID: "key-b", KeyHash: "hash-b", TenantID: rbacTestTenantB, Status: plugin.APIKeyStatusActive, CreatedAt: now, UpdatedAt: now}
	if err := f.storage.SaveAPIKey(keyA); err != nil {
		t.Fatal(err)
	}
	if err := f.storage.SaveAPIKey(keyB); err != nil {
		t.Fatal(err)
	}
	return keyA.ID, keyB.ID
}

// TestTenantScopeOnListKeys 非超管列 Key 只见本租户
func TestTenantScopeOnListKeys(t *testing.T) {
	f := newRBACFixture(t, true)
	seedScopedKeys(t, f)
	rec := f.do(f.scopedTok, http.MethodGet, "/api/api-keys?page=1&size=10", "")
	var resp struct {
		Data struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
			Total int64 `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.Total != 1 || len(resp.Data.Items) != 1 || resp.Data.Items[0].ID != "key-a" {
		t.Errorf("租户过滤应只见 key-a: total=%d items=%v", resp.Data.Total, resp.Data.Items)
	}
}

// TestTenantScopeCrossDeleteBlocked 跨租户删除返回 404；本租户正常
func TestTenantScopeCrossDeleteBlocked(t *testing.T) {
	f := newRBACFixture(t, true)
	keyA, keyB := seedScopedKeys(t, f)
	if rec := f.do(f.scopedTok, http.MethodDelete, "/api/api-keys/"+keyB, ""); rec.Code != http.StatusNotFound {
		t.Errorf("跨租户删除应 404, got %d", rec.Code)
	}
	if rec := f.do(f.scopedTok, http.MethodDelete, "/api/api-keys/"+keyA, ""); rec.Code != http.StatusOK {
		t.Errorf("本租户删除应成功, got %d %s", rec.Code, rec.Body.String())
	}
}

// TestTenantScopeCreateRateLimitForced 创建限流配置时租户强制取自身
func TestTenantScopeCreateRateLimitForced(t *testing.T) {
	f := newRBACFixture(t, true)
	rec := f.do(f.scopedTok, http.MethodPost, "/api/rate-limits",
		`{"tenant_id":"`+rbacTestTenantB+`","model_name":"m","requests_per_sec":5,"tokens_per_min":100,"strategy":"token_bucket"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("创建失败: %d %s", rec.Code, rec.Body.String())
	}
	cfgs, _, _ := f.storage.ListRateLimitConfigs(nil, 1, 10)
	if len(cfgs) != 1 || cfgs[0].TenantID != rbacTestTenantA {
		t.Errorf("租户应强制为 tenant-a: %+v", cfgs)
	}
}

// TestTenantScopeOnAuditLogs 审计日志查询强制注入租户条件
func TestTenantScopeOnAuditLogs(t *testing.T) {
	f := newRBACFixture(t, true)
	now := time.Now()
	_ = f.storage.SaveAuditLog(&plugin.AuditLog{ID: "log-a", RequestID: "ra", TenantID: rbacTestTenantA, CreatedAt: now})
	_ = f.storage.SaveAuditLog(&plugin.AuditLog{ID: "log-b", RequestID: "rb", TenantID: rbacTestTenantB, CreatedAt: now})

	rec := f.do(f.scopedTok, http.MethodGet, "/api/audit-logs?page=1&size=10", "")
	var resp struct {
		Data struct {
			Total int64 `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.Total != 1 {
		t.Errorf("审计过滤应只见本租户 1 条, got %d", resp.Data.Total)
	}
}
