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
)

func validMCPServerBody(name string) string {
	return `{"name":"` + name + `","endpoint":"http://127.0.0.1:9100/mcp","headers":{"Authorization":"Bearer x"},"enabled":true}`
}

// TestMCPServerCRUD 创建/重名冲突/非法URL/更新未知ID/删除后未命中
func TestMCPServerCRUD(t *testing.T) {
	f := newRBACFixture(t, true)

	rec := f.do(f.superTok, http.MethodPost, "/api/mcp-servers", validMCPServerBody("tools-a"))
	if rec.Code != http.StatusOK {
		t.Fatalf("创建应成功: %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.Data.ID == "" {
		t.Fatalf("应返回 id: %s", rec.Body.String())
	}

	if rec = f.do(f.superTok, http.MethodPost, "/api/mcp-servers", validMCPServerBody("tools-a")); rec.Code != http.StatusConflict {
		t.Errorf("重名应 409, got %d", rec.Code)
	}
	rec = f.do(f.superTok, http.MethodPost, "/api/mcp-servers",
		`{"name":"bad","endpoint":"ftp://x/mcp"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("非 http(s) 端点应 400, got %d", rec.Code)
	}

	rec = f.do(f.superTok, http.MethodPut, "/api/mcp-servers/"+created.Data.ID,
		validMCPServerBody("tools-a2"))
	if rec.Code != http.StatusOK {
		t.Errorf("更新应成功: %d %s", rec.Code, rec.Body.String())
	}
	rec = f.do(f.superTok, http.MethodPut, "/api/mcp-servers/no-such-id",
		validMCPServerBody("whatever"))
	if rec.Code != http.StatusNotFound {
		t.Errorf("更新未知 id 应 404, got %d", rec.Code)
	}

	rec = f.do(f.superTok, http.MethodGet, "/api/mcp-servers?page=1&size=10", "")
	var list struct {
		Data struct {
			Items []struct {
				Name     string `json:"name"`
				Endpoint string `json:"endpoint"`
				Enabled  bool   `json:"enabled"`
			} `json:"items"`
			Total int64 `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if list.Data.Total != 1 || len(list.Data.Items) != 1 || list.Data.Items[0].Name != "tools-a2" {
		t.Errorf("列表不符: %+v", list.Data)
	}

	rec = f.do(f.superTok, http.MethodDelete, "/api/mcp-servers/"+created.Data.ID, "")
	if rec.Code != http.StatusOK {
		t.Errorf("删除应成功: %d", rec.Code)
	}
	rec = f.do(f.superTok, http.MethodDelete, "/api/mcp-servers/"+created.Data.ID, "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("重复删除应 404, got %d", rec.Code)
	}
}

// seedMCPAuditLogs 预置三条不同工具/状态/时间的审计记录
func seedMCPAuditLogs(t *testing.T, f *rbacFixture) {
	t.Helper()
	base := time.Date(2026, 8, 26, 10, 0, 0, 0, time.Local)
	entries := []*plugin.MCPAuditLog{
		{ID: "ma-a", ToolName: "search", Status: plugin.MCPStatusSuccess, CreatedAt: base},
		{ID: "ma-b", ToolName: "fetch", Status: plugin.MCPStatusSuccess, CreatedAt: base.Add(time.Minute)},
		{ID: "ma-c", ToolName: "search", Status: plugin.MCPStatusFailed, ErrorMessage: "boom", CreatedAt: base.Add(2 * time.Minute)},
	}
	for _, e := range entries {
		if err := f.storage.SaveMCPAuditLog(e); err != nil {
			t.Fatal(err)
		}
	}
}

// TestMCPAuditLogsQuery 组合筛选+倒序分页+详情
func TestMCPAuditLogsQuery(t *testing.T) {
	f := newRBACFixture(t, true)
	seedMCPAuditLogs(t, f)

	rec := f.do(f.superTok, http.MethodGet,
		"/api/mcp-audit-logs?page=1&size=10&tool=search&status=failed", "")
	var list struct {
		Data struct {
			Items []struct {
				ID      string `json:"id"`
				Status  string `json:"status"`
				Details struct {
					ErrorMessage string `json:"error_message"`
				} `json:"-"`
			} `json:"items"`
			Total int64 `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if list.Data.Total != 1 || len(list.Data.Items) != 1 || list.Data.Items[0].ID != "ma-c" {
		t.Errorf("tool+status 组合筛选应只命中 ma-c: %+v", list.Data)
	}

	rec = f.do(f.superTok, http.MethodGet, "/api/mcp-audit-logs?page=1&size=2", "")
	if !strings.Contains(rec.Body.String(), `"total":3`) || strings.Count(rec.Body.String(), `"id"`) < 2 {
		t.Errorf("全量分页应 total=3 且本页 2 条: %s", rec.Body.String())
	}
	first := struct {
		Data struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
		} `json:"data"`
	}{}
	_ = json.Unmarshal(rec.Body.Bytes(), &first)
	if first.Data.Items[0].ID != "ma-c" {
		t.Errorf("应按 created_at 倒序(最新优先): %+v", first.Data.Items)
	}

	rec = f.do(f.superTok, http.MethodGet, "/api/mcp-audit-logs/ma-b", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"fetch"`) {
		t.Errorf("详情应命中: %d %s", rec.Code, rec.Body.String())
	}
	rec = f.do(f.superTok, http.MethodGet, "/api/mcp-audit-logs/no-such-id", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("未知 id 详情应 404, got %d", rec.Code)
	}
}

// TestMCPEndpointsGlobalOnly 租户内用户持全量权限仍被全局域守卫拒绝
func TestMCPEndpointsGlobalOnly(t *testing.T) {
	f := newRBACFixture(t, true)
	seedMCPAuditLogs(t, f)
	scoped, _ := f.storage.GetAdminUserByID("u-scoped")
	wide := &plugin.Role{Name: "越权角色", TenantID: rbacTestTenantA, Permissions: plugin.AllPermissions}
	_ = f.storage.SaveRole(wide)
	scoped.RoleID = wide.ID
	_ = f.storage.SaveAdminUser(scoped)
	sm := NewSessionManager([]byte("rbac-test-secret"), time.Hour)
	tok, _, _ := sm.Mint(scoped, plugin.AllPermissions, false, time.Now())

	cases := []struct {
		name string
		do   func() *httptest.ResponseRecorder
	}{
		{"list-servers", func() *httptest.ResponseRecorder {
			return f.do(tok, http.MethodGet, "/api/mcp-servers", "")
		}},
		{"create-server", func() *httptest.ResponseRecorder {
			return f.do(tok, http.MethodPost, "/api/mcp-servers", validMCPServerBody("x"))
		}},
		{"update-server", func() *httptest.ResponseRecorder {
			return f.do(tok, http.MethodPut, "/api/mcp-servers/some-id", validMCPServerBody("y"))
		}},
		{"delete-server", func() *httptest.ResponseRecorder {
			return f.do(tok, http.MethodDelete, "/api/mcp-servers/some-id", "")
		}},
		{"list-audit", func() *httptest.ResponseRecorder {
			return f.do(tok, http.MethodGet, "/api/mcp-audit-logs", "")
		}},
		{"get-audit", func() *httptest.ResponseRecorder {
			return f.do(tok, http.MethodGet, "/api/mcp-audit-logs/ma-a", "")
		}},
	}
	for _, tc := range cases {
		if rec := tc.do(); rec.Code != http.StatusForbidden {
			t.Errorf("租户内用户 %s 应 403, got %d %s", tc.name, rec.Code, rec.Body.String())
		}
	}
}
