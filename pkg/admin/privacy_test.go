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

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
	"go.uber.org/zap"
)

func newPrivacyServer() *AdminServer {
	s := NewAdminServer(oss.NewMemStorage(), zap.NewNop(), "enterprise", oss.NewRateLimiter(oss.NewMemStorage(), 100, 100000, "token_bucket"), nil)
	s.DisableAuth()
	return s
}

func doJSON(t *testing.T, s *AdminServer, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	return rec
}

func decodeData(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var resp struct {
		Code int            `json:"code"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应非 JSON: %v body=%s", err, rec.Body.String())
	}
	return resp.Data
}

func TestCreatePrivacyRule(t *testing.T) {
	s := newPrivacyServer()
	rec := doJSON(t, s, http.MethodPost, "/api/privacy-rules",
		`{"rule_type":"pii","name":"员工编号","pattern":"EMP\\d{6}","replacement":"[已隐藏]","scope":"both"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	data := decodeData(t, rec)
	id, _ := data["id"].(string)
	if id == "" {
		t.Fatal("应返回新规则 id")
	}
	rules, _ := s.storage.ListPrivacyRules(nil)
	if len(rules) != 1 || rules[0].Name != "员工编号" || !rules[0].Enabled {
		t.Errorf("规则未入库: %+v", rules)
	}
}

func TestCreatePrivacyRuleInvalidPattern(t *testing.T) {
	s := newPrivacyServer()
	rec := doJSON(t, s, http.MethodPost, "/api/privacy-rules",
		`{"rule_type":"pii","name":"坏正则","pattern":"([unclosed","replacement":"*","scope":"both"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("非法正则应 400, got %d", rec.Code)
	}
}

func TestCreatePrivacyRuleBadScope(t *testing.T) {
	s := newPrivacyServer()
	rec := doJSON(t, s, http.MethodPost, "/api/privacy-rules",
		`{"rule_type":"pii","name":"x","pattern":"a","scope":"everywhere"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("非法 scope 应 400, got %d", rec.Code)
	}
}

func TestCreateInjectionRuleForcesRequestScope(t *testing.T) {
	s := newPrivacyServer()
	rec := doJSON(t, s, http.MethodPost, "/api/privacy-rules",
		`{"rule_type":"injection","name":"注入","pattern":"ignore.*instructions","scope":"response"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	rules, _ := s.storage.ListPrivacyRules(nil)
	if len(rules) != 1 || rules[0].Scope != plugin.PrivacyScopeRequest {
		t.Errorf("injection 规则 scope 应强制 request: %+v", rules)
	}
}

func TestListPrivacyRulesFilter(t *testing.T) {
	s := newPrivacyServer()
	doJSON(t, s, http.MethodPost, "/api/privacy-rules", `{"rule_type":"pii","name":"a","pattern":"a","scope":"both"}`)
	doJSON(t, s, http.MethodPost, "/api/privacy-rules", `{"rule_type":"injection","name":"b","pattern":"b","scope":"request"}`)

	rec := doJSON(t, s, http.MethodGet, "/api/privacy-rules?rule_type=pii", "")
	data := decodeData(t, rec)
	items, _ := data["items"].([]any)
	if len(items) != 1 {
		t.Errorf("pii 过滤应返回 1 条, got %d", len(items))
	}
	rec = doJSON(t, s, http.MethodGet, "/api/privacy-rules", "")
	data = decodeData(t, rec)
	items, _ = data["items"].([]any)
	if len(items) != 2 {
		t.Errorf("无过滤应返回全部 2 条, got %d", len(items))
	}
}

func TestUpdateAndDeletePrivacyRule(t *testing.T) {
	s := newPrivacyServer()
	rec := doJSON(t, s, http.MethodPost, "/api/privacy-rules", `{"rule_type":"pii","name":"旧名","pattern":"p","replacement":"r","scope":"both"}`)
	id, _ := decodeData(t, rec)["id"].(string)

	rec = doJSON(t, s, http.MethodPut, "/api/privacy-rules/"+id,
		`{"rule_type":"pii","name":"新名","pattern":"p2","replacement":"r2","scope":"response"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d", rec.Code)
	}
	rules, _ := s.storage.ListPrivacyRules(nil)
	if len(rules) != 1 || rules[0].Name != "新名" || rules[0].Replacement != "r2" || !rules[0].Enabled {
		t.Errorf("更新未生效(默认启用): %+v", rules)
	}

	rec = doJSON(t, s, http.MethodDelete, "/api/privacy-rules/"+id, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d", rec.Code)
	}
	rec = doJSON(t, s, http.MethodDelete, "/api/privacy-rules/"+id, "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("重复删除应 404, got %d", rec.Code)
	}
}

func TestPrivacyWhitelistCRUD(t *testing.T) {
	s := newPrivacyServer()
	rec := doJSON(t, s, http.MethodPost, "/api/privacy-whitelist",
		`{"pattern":"^压测样本","note":"压测专用"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body.String())
	}
	id, _ := decodeData(t, rec)["id"].(string)

	list := decodeData(t, doJSON(t, s, http.MethodGet, "/api/privacy-whitelist", ""))
	items, _ := list["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("白名单应有 1 条, got %d", len(items))
	}

	if rec = doJSON(t, s, http.MethodDelete, "/api/privacy-whitelist/"+id, ""); rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d", rec.Code)
	}
	if rec = doJSON(t, s, http.MethodDelete, "/api/privacy-whitelist/"+id, ""); rec.Code != http.StatusNotFound {
		t.Errorf("重复删除白名单应 404, got %d", rec.Code)
	}
}

func TestCreateWhitelistInvalidPattern(t *testing.T) {
	s := newPrivacyServer()
	rec := doJSON(t, s, http.MethodPost, "/api/privacy-whitelist", `{"pattern":"([bad","note":""}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("非法正则应 400, got %d", rec.Code)
	}
}

func TestListSecurityEvents(t *testing.T) {
	s := newPrivacyServer()
	for i := 0; i < 3; i++ {
		_ = s.storage.SaveSecurityEvent(&plugin.SecurityEvent{RequestID: "req", RuleName: "r"})
	}
	data := decodeData(t, doJSON(t, s, http.MethodGet, "/api/security-events?page=1&size=2", ""))
	items, _ := data["items"].([]any)
	total, _ := data["total"].(float64)
	if total != 3 || len(items) != 2 {
		t.Errorf("分页不符: total=%v items=%d", total, len(items))
	}
}
