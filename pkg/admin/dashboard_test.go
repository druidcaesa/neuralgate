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
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// newDashboardServer 构造已关鉴权的管理服务；license 为 nil 时按 OSS 处理
func newDashboardServer(t *testing.T, storage plugin.StoragePlugin, license *LicenseOverview) *AdminServer {
	t.Helper()
	s := NewAdminServer(storage, zap.NewNop(), "enterprise", newTestRateLimiter(), license)
	s.DisableAuth()
	return s
}

func getDashboard(t *testing.T, s *AdminServer, query string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/dashboard"+query, nil))
	return rec
}

func decodeDashboard(t *testing.T, rec *httptest.ResponseRecorder) plugin.DashboardData {
	t.Helper()
	var resp struct {
		Data plugin.DashboardData `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v; body=%s", err, rec.Body.String())
	}
	return resp.Data
}

// TestDashboardForbiddenWithoutPerm 启用 RBAC 时，无 system:read 的会话访问首页 → 403 无权限
func TestDashboardForbiddenWithoutPerm(t *testing.T) {
	f := newRBACFixture(t, true)
	rec := f.do(f.scopedTok, http.MethodGet, "/api/dashboard?window=24h", "")
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "无权限") {
		t.Errorf("无 system:read 访问首页应 403 无权限, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestDashboardAPIInvalidWindow(t *testing.T) {
	s := newDashboardServer(t, oss.NewMemStorage(), nil)

	for _, q := range []string{"?window=1h", "?window=" + "yesterday"} {
		rec := getDashboard(t, s, q)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET /api/dashboard%s status = %d, want 400", q, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "24h") {
			t.Errorf("400 文案应列出合法值，实得: %s", rec.Body.String())
		}
	}
}

func TestDashboardAPIEmptyStorage(t *testing.T) {
	s := newDashboardServer(t, oss.NewMemStorage(), nil)

	for _, tc := range []struct {
		query string
		want  int
	}{
		{"", 7}, // 缺省 7d
		{"?window=24h", 24},
		{"?window=7d", 7},
		{"?window=30d", 30},
	} {
		rec := getDashboard(t, s, tc.query)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/dashboard%s status = %d, want 200; body=%s",
				tc.query, rec.Code, rec.Body.String())
		}
		d := decodeDashboard(t, rec)
		if len(d.Trend) != tc.want {
			t.Errorf("%s 桶数 = %d, want %d", tc.query, len(d.Trend), tc.want)
		}
		if d.Summary.Requests != 0 || d.Summary.Tokens != 0 || d.Summary.SuccessRate != 0 {
			t.Errorf("%s 空库应得零值指标，实得 %+v", tc.query, d.Summary)
		}
		if d.Trend == nil || d.Alerts == nil {
			t.Errorf("%s Trend/Alerts 不得为 null", tc.query)
		}
	}
}

func TestDashboardAPIAggregates(t *testing.T) {
	storage := oss.NewMemStorage()
	now := time.Now()
	for _, l := range []*plugin.AuditLog{
		{ID: "l1", RequestID: "r1", CreatedAt: now.Add(-time.Minute), ResponseStatus: 200, TotalTokens: 100, Duration: 200},
		{ID: "l2", RequestID: "r2", CreatedAt: now.Add(-2 * time.Minute), ResponseStatus: 500, TotalTokens: 50, Duration: 400},
	} {
		if err := storage.SaveAuditLog(l); err != nil {
			t.Fatal(err)
		}
	}
	s := newDashboardServer(t, storage, nil)

	d := decodeDashboard(t, getDashboard(t, s, "?window=24h"))
	if d.Summary.Requests != 2 {
		t.Errorf("requests = %d, want 2", d.Summary.Requests)
	}
	if d.Summary.Tokens != 150 {
		t.Errorf("tokens = %d, want 150", d.Summary.Tokens)
	}
	if d.Summary.SuccessRate != 50 {
		t.Errorf("success_rate = %v, want 50", d.Summary.SuccessRate)
	}
	if d.Summary.AvgLatencyMS != 300 {
		t.Errorf("avg_latency_ms = %d, want 300", d.Summary.AvgLatencyMS)
	}
}

// TestDashboardAPIAlertsLicenseStatus 穷举授权状态：正常态无告警，异常态 warning，
// 未知状态 fail-loud 兜底
func TestDashboardAPIAlertsLicenseStatus(t *testing.T) {
	for _, tc := range []struct {
		name       string
		license    *LicenseOverview
		wantAlerts int
		wantLevel  string
	}{
		{"oss 版无告警", nil, 0, ""},
		{"有效授权无告警", &LicenseOverview{Status: "valid"}, 0, ""},
		{"已过期告警", &LicenseOverview{Status: "expired", Message: "授权已过期"}, 1, "warning"},
		{"无效告警", &LicenseOverview{Status: "invalid", Message: "验签失败"}, 1, "warning"},
		{"缺失告警", &LicenseOverview{Status: "missing", Message: "未检测到授权文件"}, 1, "warning"},
		{"未知状态兜底告警", &LicenseOverview{Status: "revoked"}, 1, "warning"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newDashboardServer(t, oss.NewMemStorage(), tc.license)
			d := decodeDashboard(t, getDashboard(t, s, ""))
			if len(d.Alerts) != tc.wantAlerts {
				t.Fatalf("alerts = %+v, want %d 条", d.Alerts, tc.wantAlerts)
			}
			if tc.wantAlerts > 0 && d.Alerts[0].Level != tc.wantLevel {
				t.Errorf("level = %s, want %s", d.Alerts[0].Level, tc.wantLevel)
			}
		})
	}
}

// TestDashboardAPIAlertsNoLicenseInfoLeak 告警不得带出授权业务字段
func TestDashboardAPIAlertsNoLicenseInfoLeak(t *testing.T) {
	s := newDashboardServer(t, oss.NewMemStorage(), &LicenseOverview{
		Status:  "expired",
		Message: "授权已过期",
		Info: &plugin.LicenseInfo{
			LicenseKey:   "SECRET-KEY-1234",
			CustomerName: "SECRET-CUSTOMER",
			MaxNodes:     99,
		},
	})

	body := getDashboard(t, s, "").Body.String()
	for _, banned := range []string{"SECRET-KEY-1234", "SECRET-CUSTOMER", "license_key", "customer_name", "max_nodes"} {
		if strings.Contains(body, banned) {
			t.Errorf("首页响应泄漏授权业务字段 %q: %s", banned, body)
		}
	}
}

// assertDashboardFailLoud 断言首页存储失败路径:500 + 通用文案,底层原因只进日志不下发浏览器
func assertDashboardFailLoud(t *testing.T, rec *httptest.ResponseRecorder, logs *observer.ObservedLogs, message string, internal error) {
	t.Helper()
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("应 500, got %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), internal.Error()) {
		t.Errorf("底层细节不得下发浏览器: %s", rec.Body.String())
	}
	var resp Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Code != http.StatusInternalServerError {
		t.Errorf("业务码应 500, got %d", resp.Code)
	}
	if resp.Message != message {
		t.Errorf("响应文案应 %q, got %q", message, resp.Message)
	}
	if logs.Len() != 1 {
		t.Fatalf("应恰好 1 条日志, got %d: %v", logs.Len(), logs.All())
	}
	if got := logs.All()[0].ContextMap()["err"]; got != internal.Error() {
		t.Errorf("日志 err 应为底层原因, got %v", got)
	}
}

// TestDashboardAPIHidesAuditSamplesError 采样查询失败必须整页报错:
// 宁可无数据,不可有错数据;底层细节只进日志
func TestDashboardAPIHidesAuditSamplesError(t *testing.T) {
	internal := errors.New("sql: database is locked")
	storage := &failingListStorage{MemStorage: oss.NewMemStorage(), auditSamplesErr: internal}
	s, logs := newObservedServer(t, storage, nil)
	s.DisableAuth()

	assertDashboardFailLoud(t, getDashboard(t, s, "?window=24h"), logs,
		"failed to load dashboard samples", internal)
}

// TestDashboardAPIHidesTamperAlertsError 篡改告警计数失败必须整页报错:
// 存储里躺着未处置告警时首页不得显示「一切正常」,且失败须留日志
func TestDashboardAPIHidesTamperAlertsError(t *testing.T) {
	internal := errors.New("sql: database is locked")
	storage := &failingListStorage{MemStorage: oss.NewMemStorage(), tamperErr: internal}
	s, logs := newObservedServer(t, storage, nil)
	s.DisableAuth()

	assertDashboardFailLoud(t, getDashboard(t, s, "?window=24h"), logs,
		"failed to load tamper alerts", internal)
}

func TestDashboardAPIAlertsTamper(t *testing.T) {
	storage := oss.NewMemStorage()
	if err := storage.SaveTamperAlerts([]*plugin.TamperAlert{
		{AuditLogID: "a1", Reason: "指纹不一致"},
		{AuditLogID: "a2", Reason: "指纹不一致"},
	}); err != nil {
		t.Fatal(err)
	}
	s := newDashboardServer(t, storage, nil)

	d := decodeDashboard(t, getDashboard(t, s, ""))
	if len(d.Alerts) != 1 {
		t.Fatalf("alerts = %+v, want 1 条", d.Alerts)
	}
	if d.Alerts[0].Level != "error" {
		t.Errorf("level = %s, want error", d.Alerts[0].Level)
	}
	if d.Alerts[0].Link != "/tamper-alerts" {
		t.Errorf("link = %s, want /tamper-alerts", d.Alerts[0].Link)
	}
	if !strings.Contains(d.Alerts[0].Detail, "2") {
		t.Errorf("detail 应含条数 2，实得: %s", d.Alerts[0].Detail)
	}
}
