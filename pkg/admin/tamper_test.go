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

// seedAlerts 写入两条告警（一条已处置）
func seedAlerts(t *testing.T) plugin.StoragePlugin {
	t.Helper()
	s := oss.NewMemStorage()
	if err := s.SaveTamperAlerts([]*plugin.TamperAlert{{AuditLogID: "a1", Reason: "指纹不一致"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveTamperAlerts([]*plugin.TamperAlert{{AuditLogID: "a2", Reason: "指纹不一致"}}); err != nil {
		t.Fatal(err)
	}
	alerts, _, _ := s.ListTamperAlerts(nil, 1, 10)
	for _, a := range alerts {
		if a.AuditLogID == "a2" {
			if err := s.SetTamperAlertResolved(a.ID, true); err != nil {
				t.Fatal(err)
			}
		}
	}
	return s
}

func fetchJSON(t *testing.T, s *AdminServer, method, path string) map[string]interface{} {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("%s %s status = %d; body=%s", method, path, rec.Code, rec.Body.String())
	}
	var resp struct {
		Data map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	return resp.Data
}

func TestListTamperAlertsAPI(t *testing.T) {
	s := NewAdminServer(seedAlerts(t), zap.NewNop(), "enterprise", newTestRateLimiter(), nil)
	s.DisableAuth()

	data := fetchJSON(t, s, http.MethodGet, "/api/tamper-alerts")
	if data["total"].(float64) != 2 {
		t.Errorf("全部应 2 条, got %v", data["total"])
	}

	data = fetchJSON(t, s, http.MethodGet, "/api/tamper-alerts?resolved=false")
	items := data["items"].([]interface{})
	if len(items) != 1 || data["total"].(float64) != 1 {
		t.Errorf("未处置过滤应 1 条: total=%v items=%d", data["total"], len(items))
	}
	first := items[0].(map[string]interface{})
	if first["audit_log_id"] != "a1" {
		t.Errorf("audit_log_id = %v", first["audit_log_id"])
	}
}

func TestResolveTamperAlertAPI(t *testing.T) {
	s := NewAdminServer(seedAlerts(t), zap.NewNop(), "enterprise", newTestRateLimiter(), nil)
	s.DisableAuth()
	data := fetchJSON(t, s, http.MethodGet, "/api/tamper-alerts?resolved=false")
	id := data["items"].([]interface{})[0].(map[string]interface{})["id"].(string)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/tamper-alerts/"+id, strings.NewReader(`{"resolved":true}`))
	req.Header.Set("Content-Type", "application/json")
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve status = %d; body=%s", rec.Code, rec.Body.String())
	}

	data = fetchJSON(t, s, http.MethodGet, "/api/tamper-alerts?resolved=false")
	if data["total"].(float64) != 0 {
		t.Errorf("处置后未处置应归零, got %v", data["total"])
	}
	// 不存在的 ID 应报错
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPatch, "/api/tamper-alerts/nope", strings.NewReader(`{"resolved":true}`))
	req.Header.Set("Content-Type", "application/json")
	s.Router().ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Error("不存在的告警应返回错误")
	}
}

func TestSystemInfoIncludesTamperCount(t *testing.T) {
	s := NewAdminServer(seedAlerts(t), zap.NewNop(), "enterprise", newTestRateLimiter(), nil)
	s.DisableAuth()
	data := fetchSystemData(t, s)
	tamper, ok := data["tamper"].(map[string]interface{})
	if !ok {
		t.Fatal("/api/system 缺少 tamper 概览")
	}
	if tamper["unresolved_count"].(float64) != 1 {
		t.Errorf("unresolved_count = %v, want 1", tamper["unresolved_count"])
	}
}
