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

var (
	testSecret = []byte("unit-test-session-secret")
	testNow    = time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
)

// newBcryptHash 生成测试口令的 bcrypt 哈希
func newBcryptHash(t *testing.T, password string) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return string(h)
}

// seedAdminUser 向存储写入测试管理员并返回（口令为明文入参）
func seedAdminUser(t *testing.T, s plugin.StoragePlugin, username, password string, status plugin.AdminUserStatus) *plugin.AdminUser {
	t.Helper()
	now := time.Now()
	u := &plugin.AdminUser{
		ID:           "u-" + username,
		Username:     username,
		PasswordHash: newBcryptHash(t, password),
		Status:       status,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.SaveAdminUser(u); err != nil {
		t.Fatal(err)
	}
	return u
}

// newAuthTestServer 构造启用认证的管理后台（mem 存储 + 已知口令的 admin 账号）
func newAuthTestServer(t *testing.T) (*AdminServer, *plugin.AdminUser, string) {
	t.Helper()
	s := NewAdminServer(oss.NewMemStorage(), nil, "oss", nil, nil)
	s.EnableAuth(NewSessionManager(testSecret, 24*time.Hour), nil)
	u := seedAdminUser(t, s.storage, "admin", "correct-pass", plugin.AdminUserStatusActive)
	tok, _, err := s.sessions.Mint(u, nil, false, testNow)
	if err != nil {
		t.Fatal(err)
	}
	return s, u, tok
}

func authedGet(s *AdminServer, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("X-Admin-Token", token)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	return rec
}

func postJSON(s *AdminServer, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	return rec
}

// ===== SessionManager =====

func TestSessionTokenRoundTrip(t *testing.T) {
	m := NewSessionManager(testSecret, 24*time.Hour)
	u := &plugin.AdminUser{ID: "u1", Username: "root", PasswordHash: "$2a$10$abc", Status: plugin.AdminUserStatusActive}
	tok, exp, err := m.Mint(u, nil, false, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if !exp.Equal(testNow.Add(24 * time.Hour)) {
		t.Errorf("exp = %v, want %v", exp, testNow.Add(24*time.Hour))
	}
	lookup := func(string) (*plugin.AdminUser, error) { return u, nil }
	claims, err := m.Verify(tok, lookup, testNow.Add(time.Minute))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Sub != "u1" || claims.Name != "root" {
		t.Errorf("claims = %+v", claims)
	}
	if claims.PwdDigest != pwdDigest(u.PasswordHash) {
		t.Errorf("PwdDigest mismatch")
	}
}

func TestSessionTokenExpired(t *testing.T) {
	m := NewSessionManager(testSecret, time.Hour)
	u := &plugin.AdminUser{ID: "u1", Username: "root", PasswordHash: "h"}
	tok, _, err := m.Mint(u, nil, false, testNow)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Verify(tok, func(string) (*plugin.AdminUser, error) { return u, nil }, testNow.Add(2*time.Hour))
	if err == nil {
		t.Fatal("expected expired error")
	}
}

func TestSessionTokenTampered(t *testing.T) {
	m := NewSessionManager(testSecret, 24*time.Hour)
	u := &plugin.AdminUser{ID: "u1", Username: "root", PasswordHash: "h"}
	tok, _, _ := m.Mint(u, nil, false, testNow)
	bad := tok[:len(tok)-3] + "xxx"
	if _, err := m.Verify(bad, func(string) (*plugin.AdminUser, error) { return u, nil }, testNow); err == nil {
		t.Fatal("expected tampered token rejected")
	}
}

func TestSessionTokenPasswordChangeInvalidates(t *testing.T) {
	m := NewSessionManager(testSecret, 24*time.Hour)
	u := &plugin.AdminUser{ID: "u1", Username: "root", PasswordHash: "old-hash"}
	tok, _, _ := m.Mint(u, nil, false, testNow)
	rotated := &plugin.AdminUser{ID: "u1", Username: "root", PasswordHash: "new-hash"}
	if _, err := m.Verify(tok, func(string) (*plugin.AdminUser, error) { return rotated, nil }, testNow); err == nil {
		t.Fatal("old token should be invalid after password change")
	}
}

func TestSessionTokenDisabledUser(t *testing.T) {
	m := NewSessionManager(testSecret, 24*time.Hour)
	u := &plugin.AdminUser{ID: "u1", Username: "root", PasswordHash: "h", Status: plugin.AdminUserStatusActive}
	tok, _, _ := m.Mint(u, nil, false, testNow)
	disabled := &plugin.AdminUser{ID: "u1", Username: "root", PasswordHash: "h", Status: plugin.AdminUserStatusDisabled}
	if _, err := m.Verify(tok, func(string) (*plugin.AdminUser, error) { return disabled, nil }, testNow); err == nil {
		t.Fatal("disabled account should fail verification")
	}
}

// ===== 中间件 =====

func TestRequireAuthBlocksAnonymous(t *testing.T) {
	s, _, _ := newAuthTestServer(t)
	if rec := authedGet(s, "/api/ping", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token: %d, want 401", rec.Code)
	}
	if rec := authedGet(s, "/api/ping", "garbage-token"); rec.Code != http.StatusUnauthorized {
		t.Errorf("bad token: %d, want 401", rec.Code)
	}
}

func TestRequireAuthAcceptsValidToken(t *testing.T) {
	s, _, tok := newAuthTestServer(t)
	if rec := authedGet(s, "/api/ping", tok); rec.Code != http.StatusOK {
		t.Errorf("valid token: %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestHealthzExemptFromAuth(t *testing.T) {
	s, _, _ := newAuthTestServer(t)
	if rec := authedGet(s, "/healthz", ""); rec.Code != http.StatusOK {
		t.Errorf("/healthz: %d, want 200", rec.Code)
	}
}

// ===== 登录 =====

func TestLoginSuccessAndFailure(t *testing.T) {
	s, _, _ := newAuthTestServer(t)
	ok := postJSON(s, "/api/auth/login", `{"username":"admin","password":"correct-pass"}`)
	if ok.Code != http.StatusOK {
		t.Fatalf("login: %d, body=%s", ok.Code, ok.Body.String())
	}
	bad := postJSON(s, "/api/auth/login", `{"username":"admin","password":"wrong"}`)
	if bad.Code != http.StatusUnauthorized {
		t.Errorf("wrong password: %d, want 401", bad.Code)
	}
	unknown := postJSON(s, "/api/auth/login", `{"username":"nobody","password":"x"}`)
	if unknown.Code != http.StatusUnauthorized {
		t.Errorf("unknown user: %d, want 401", unknown.Code)
	}
}

func TestLoginLockoutAfterFiveFailures(t *testing.T) {
	s, _, _ := newAuthTestServer(t)
	for i := 0; i < 5; i++ {
		if rec := postJSON(s, "/api/auth/login", `{"username":"admin","password":"wrong"}`); rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d, want 401", i+1, rec.Code)
		}
	}
	if rec := postJSON(s, "/api/auth/login", `{"username":"admin","password":"correct-pass"}`); rec.Code != http.StatusTooManyRequests {
		t.Errorf("locked attempt: %d, want 429", rec.Code)
	}
}

func TestLoginDisabledAccountForbidden(t *testing.T) {
	s := NewAdminServer(oss.NewMemStorage(), nil, "oss", nil, nil)
	s.EnableAuth(NewSessionManager(testSecret, 24*time.Hour), nil)
	seedAdminUser(t, s.storage, "admin", "correct-pass", plugin.AdminUserStatusDisabled)
	if rec := postJSON(s, "/api/auth/login", `{"username":"admin","password":"correct-pass"}`); rec.Code != http.StatusForbidden {
		t.Errorf("disabled login: %d, want 403", rec.Code)
	}
}

// ===== 改密 =====

func postChangePassword(s *AdminServer, token, oldPass, newPass string) *httptest.ResponseRecorder {
	body := `{"old_password":"` + oldPass + `","new_password":"` + newPass + `"}`
	req := httptest.NewRequest(http.MethodPut, "/api/auth/password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Admin-Token", token)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	return rec
}

func TestChangePasswordRotatesCredential(t *testing.T) {
	s, u, oldTok := newAuthTestServer(t)
	badOld := postChangePassword(s, oldTok, "wrong-old", "brand-new-pass")
	if badOld.Code != http.StatusBadRequest {
		t.Fatalf("wrong old password: %d, want 400", badOld.Code)
	}
	ok := postChangePassword(s, oldTok, "correct-pass", "brand-new-pass")
	if ok.Code != http.StatusOK {
		t.Fatalf("change password: %d, body=%s", ok.Code, ok.Body.String())
	}
	// 旧口令不能再登录
	if rec := postJSON(s, "/api/auth/login", `{"username":"admin","password":"correct-pass"}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("old password still valid: %d", rec.Code)
	}
	// 新口令可登录
	login := postJSON(s, "/api/auth/login", `{"username":"admin","password":"brand-new-pass"}`)
	if login.Code != http.StatusOK {
		t.Fatalf("new password login failed: %d", login.Code)
	}
	// 旧 token 因口令摘要变化立即失效
	if rec := authedGet(s, "/api/ping", oldTok); rec.Code != http.StatusUnauthorized {
		t.Errorf("old token still accepted: %d", rec.Code)
	}
	_ = u
}

func TestChangePasswordRejectsShortNewPassword(t *testing.T) {
	s, _, tok := newAuthTestServer(t)
	if rec := postChangePassword(s, tok, "correct-pass", "short"); rec.Code != http.StatusBadRequest {
		t.Errorf("short new password: %d, want 400", rec.Code)
	}
}

// ===== CORS =====

func TestCORSNoHeaderWhenOriginsEmpty(t *testing.T) {
	s, _, _ := newAuthTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("ACAO = %q, want empty", got)
	}
}

func TestCORSEchoesWhitelistedOriginOnly(t *testing.T) {
	s := NewAdminServer(oss.NewMemStorage(), nil, "oss", nil, nil)
	s.EnableAuth(nil, []string{"https://ops.example.com"})
	for _, tc := range []struct{ origin, want string }{
		{"https://ops.example.com", "https://ops.example.com"},
		{"https://evil.example.com", ""},
	} {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.Header.Set("Origin", tc.origin)
		rec := httptest.NewRecorder()
		s.Router().ServeHTTP(rec, req)
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != tc.want {
			t.Errorf("origin %q: ACAO = %q, want %q", tc.origin, got, tc.want)
		}
	}
}

// TestBootstrapAdminGetsSuperRole 首启账号自动挂载（或创建）超管角色
func TestBootstrapAdminGetsSuperRole(t *testing.T) {
	storage := oss.NewMemStorage()
	if err := EnsureBootstrapAdmin(storage, zap.NewNop(), "password-123"); err != nil {
		t.Fatalf("EnsureBootstrapAdmin: %v", err)
	}
	user, err := storage.GetAdminUserByUsername("admin")
	if err != nil || user.RoleID == "" {
		t.Fatalf("bootstrap 账号未挂载角色: %+v err=%v", user, err)
	}
	role, err := storage.GetRoleByID(user.RoleID)
	if err != nil || !plugin.IsSuperRole(role) {
		t.Fatalf("挂载的应为超管角色: %+v err=%v", role, err)
	}
}

// TestLoginResponseCarriesPermissions 登录响应固化权限快照与超管标记
func TestLoginResponseCarriesPermissions(t *testing.T) {
	storage := oss.NewMemStorage()
	if err := EnsureBootstrapAdmin(storage, zap.NewNop(), "password-123"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	// 追加一个只读租户用户
	readRole := &plugin.Role{Name: "只读", TenantID: "t1", Permissions: []string{plugin.PermModelRead}}
	if err := storage.SaveRole(readRole); err != nil {
		t.Fatal(err)
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte("password-123"), bcrypt.MinCost)
	if err := storage.SaveAdminUser(&plugin.AdminUser{
		ID: "u2", Username: "viewer", PasswordHash: string(hash),
		TenantID: "t1", RoleID: readRole.ID, Status: plugin.AdminUserStatusActive,
	}); err != nil {
		t.Fatal(err)
	}

	s := NewAdminServer(storage, zap.NewNop(), "enterprise", oss.NewRateLimiter(oss.NewMemStorage(), 100, 100000, "token_bucket"), nil)

	login := func(username string) map[string]interface{} {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"`+username+`","password":"password-123"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.Router().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("login %s status = %d body=%s", username, rec.Code, rec.Body.String())
		}
		var resp struct {
			Data map[string]interface{} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp.Data
	}

	superData := login("admin")
	if isSuper, _ := superData["is_super"].(bool); !isSuper {
		t.Errorf("bootstrap 账号应标记 is_super=true: %v", superData["is_super"])
	}
	if perms, _ := superData["permissions"].([]interface{}); len(perms) == 0 {
		t.Error("超管登录应携带非空 permissions")
	}

	viewerData := login("viewer")
	if isSuper, _ := viewerData["is_super"].(bool); isSuper {
		t.Error("只读用户不应标记 is_super")
	}
	if perms, _ := viewerData["permissions"].([]interface{}); len(perms) != 1 || perms[0] != plugin.PermModelRead {
		t.Errorf("只读用户 permissions 应为 [model:read]: %v", viewerData["permissions"])
	}
	if tid, _ := viewerData["tenant_id"].(string); tid != "t1" {
		t.Errorf("viewer tenant_id = %v, want t1", viewerData["tenant_id"])
	}
}
