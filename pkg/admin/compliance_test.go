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
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// seedComplianceReports 预置三份日报表（period_start 依次递增），返回按写入序的报表切片
func seedComplianceReports(t *testing.T, f *rbacFixture) []*plugin.ComplianceReport {
	t.Helper()
	reports := make([]*plugin.ComplianceReport, 0, 3)
	for i := 0; i < 3; i++ {
		start := time.Date(2026, 8, 1+i, 0, 0, 0, 0, time.Local)
		r := &plugin.ComplianceReport{
			ID:          fmt.Sprintf("report-%d", i+1),
			PeriodType:  plugin.PeriodDay,
			PeriodStart: start,
			PeriodEnd:   start.AddDate(0, 0, 1),
			GeneratedAt: start.Add(time.Hour),
			Content: &plugin.ReportContent{
				TotalRequests: int64(100 + i), TotalTokens: int64(2000 + i*10),
				Error4xx: int64(i + 1), Error5xx: int64(i), StreamCount: int64(10 + i),
				ByModel: []plugin.DimensionStat{
					{Key: "gpt-4", Requests: 60, Tokens: 1200},
					{Key: "claude", Requests: 40 + int64(i), Tokens: 800},
				},
				ByTenant: []plugin.DimensionStat{
					{Key: rbacTestTenantA, Requests: 70, Tokens: 1400},
					{Key: "(global)", Requests: 30 + int64(i), Tokens: 600},
				},
			},
		}
		if err := f.storage.SaveComplianceReport(r); err != nil {
			t.Fatal(err)
		}
		reports = append(reports, r)
	}
	return reports
}

// TestComplianceReportListPagingDesc 列表分页且 period_start 倒序（最近优先）
func TestComplianceReportListPagingDesc(t *testing.T) {
	f := newRBACFixture(t, true)
	seedComplianceReports(t, f)

	var resp struct {
		Data struct {
			Items []struct {
				ID          string    `json:"id"`
				PeriodType  string    `json:"period_type"`
				PeriodStart time.Time `json:"period_start"`
			} `json:"items"`
			Total int64 `json:"total"`
			Page  int   `json:"page"`
			Size  int   `json:"size"`
		} `json:"data"`
	}

	rec := f.do(f.superTok, http.MethodGet, "/api/compliance-reports?page=1&size=2", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("列表应 200, got %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.Total != 3 || resp.Data.Page != 1 || resp.Data.Size != 2 {
		t.Errorf("分页字段不符: total=%d page=%d size=%d", resp.Data.Total, resp.Data.Page, resp.Data.Size)
	}
	if len(resp.Data.Items) != 2 {
		t.Fatalf("第一页应 2 条, got %d", len(resp.Data.Items))
	}
	wantFirst := time.Date(2026, 8, 3, 0, 0, 0, 0, time.Local)
	wantSecond := time.Date(2026, 8, 2, 0, 0, 0, 0, time.Local)
	if !resp.Data.Items[0].PeriodStart.Equal(wantFirst) || !resp.Data.Items[1].PeriodStart.Equal(wantSecond) {
		t.Errorf("应按 period_start 倒序: got %v, %v", resp.Data.Items[0].PeriodStart, resp.Data.Items[1].PeriodStart)
	}

	rec = f.do(f.superTok, http.MethodGet, "/api/compliance-reports?page=2&size=2", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data.Items) != 1 {
		t.Fatalf("第二页应剩 1 条, got %d", len(resp.Data.Items))
	}
	if want := time.Date(2026, 8, 1, 0, 0, 0, 0, time.Local); !resp.Data.Items[0].PeriodStart.Equal(want) {
		t.Errorf("第二页应为最早一条, got %v", resp.Data.Items[0].PeriodStart)
	}
}

// TestComplianceReportGetJSON JSON 下载字段完整
func TestComplianceReportGetJSON(t *testing.T) {
	f := newRBACFixture(t, true)
	seeded := seedComplianceReports(t, f)

	rec := f.do(f.superTok, http.MethodGet, "/api/compliance-reports/"+seeded[0].ID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("详情应 200, got %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			ID          string    `json:"id"`
			PeriodType  string    `json:"period_type"`
			PeriodStart time.Time `json:"period_start"`
			PeriodEnd   time.Time `json:"period_end"`
			GeneratedAt time.Time `json:"generated_at"`
			Content     struct {
				TotalRequests int64 `json:"total_requests"`
				TotalTokens   int64 `json:"total_tokens"`
				Error4xx      int64 `json:"error_4xx"`
				Error5xx      int64 `json:"error_5xx"`
				StreamCount   int64 `json:"stream_count"`
				ByModel       []struct {
					Key      string `json:"key"`
					Requests int64  `json:"requests"`
					Tokens   int64  `json:"tokens"`
				} `json:"by_model"`
				ByTenant []struct {
					Key string `json:"key"`
				} `json:"by_tenant"`
			} `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	d := resp.Data
	if d.ID != seeded[0].ID || d.PeriodType != plugin.PeriodDay {
		t.Errorf("标识字段不符: id=%s period=%s", d.ID, d.PeriodType)
	}
	if d.GeneratedAt.IsZero() || d.PeriodEnd.IsZero() {
		t.Error("generated_at / period_end 不应缺省")
	}
	c := d.Content
	if c.TotalRequests != 100 || c.TotalTokens != 2000 || c.Error4xx != 1 ||
		c.Error5xx != 0 || c.StreamCount != 10 {
		t.Errorf("summary 数值不符: %+v", c)
	}
	if len(c.ByModel) != 2 || len(c.ByTenant) != 2 || c.ByModel[0].Key != "gpt-4" || c.ByModel[0].Tokens != 1200 {
		t.Errorf("维度分布不符: models=%+v tenants=%+v", c.ByModel, c.ByTenant)
	}

	// 不存在的 id → 404
	if rec = f.do(f.superTok, http.MethodGet, "/api/compliance-reports/no-such-id", ""); rec.Code != http.StatusNotFound {
		t.Errorf("未知 id 应 404, got %d", rec.Code)
	}
}

// TestComplianceReportGetCSV CSV 导出含表头、summary 五行与 model/tenant 维度行
func TestComplianceReportGetCSV(t *testing.T) {
	f := newRBACFixture(t, true)
	seeded := seedComplianceReports(t, f)

	req := httptest.NewRequest(http.MethodGet, "/api/compliance-reports/"+seeded[0].ID+"?format=csv", nil)
	req.Header.Set(tokenHeader, f.superTok)
	rec := httptest.NewRecorder()
	f.s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("CSV 导出应 200, got %d %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("Content-Type 应为 text/csv, got %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition 应为 attachment, got %q", cd)
	}

	lines := strings.Split(strings.TrimRight(rec.Body.String(), "\n"), "\n")
	if lines[0] != "section,key,requests,tokens" {
		t.Errorf("CSV 表头不符: %q", lines[0])
	}
	summary, model, tenant := 0, 0, 0
	for _, line := range lines[1:] {
		switch strings.SplitN(line, ",", 2)[0] {
		case "summary":
			summary++
		case "model":
			model++
		case "tenant":
			tenant++
		default:
			t.Errorf("未知 section 行: %q", line)
		}
	}
	if summary != 5 || model != 2 || tenant != 2 {
		t.Errorf("行数不符: summary=%d(期5) model=%d(期2) tenant=%d(期2)", summary, model, tenant)
	}
	// summary 行 requests 放数值 tokens 留空
	for _, key := range []string{"total_requests", "total_tokens", "error_4xx", "error_5xx", "stream_count"} {
		found := false
		for _, line := range lines[1:] {
			parts := strings.Split(line, ",")
			if parts[0] == "summary" && parts[1] == key {
				found = true
				if len(parts) != 4 || parts[3] != "" || parts[2] == "" {
					t.Errorf("%s 行应为 requests=数值 tokens=空: %q", key, line)
				}
			}
		}
		if !found {
			t.Errorf("缺少 summary.%s 行", key)
		}
	}
	if !strings.Contains(rec.Body.String(), "summary,total_requests,100,\n") {
		t.Errorf("total_requests 行数值不符:\n%s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "model,model_gpt-4,60,1200") {
		t.Errorf("维度行应带 model_ 前缀键:\n%s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "tenant,tenant_"+rbacTestTenantA+",70,1400") {
		t.Errorf("维度行应带 tenant_ 前缀键:\n%s", rec.Body.String())
	}

	// 未知格式 → 400
	req = httptest.NewRequest(http.MethodGet, "/api/compliance-reports/"+seeded[0].ID+"?format=xml", nil)
	req.Header.Set(tokenHeader, f.superTok)
	rec = httptest.NewRecorder()
	f.s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("format=xml 应 400, got %d", rec.Code)
	}
}

// stubReportGenerator 注入桩生成器：聚合后写库（模拟 enterprise.GenerateComplianceReport），
// 同期 UPSERT 覆盖；记录每次调用的入参
func stubReportGenerator(f *rbacFixture, calls *[]string) {
	f.s.SetReportGenerator(func(periodType string, start time.Time) (*plugin.ComplianceReport, error) {
		*calls = append(*calls, periodType+"|"+start.Format(time.RFC3339))
		report := &plugin.ComplianceReport{
			PeriodType:  periodType,
			PeriodStart: start,
			PeriodEnd:   start.AddDate(0, 0, 1),
			GeneratedAt: time.Now(),
			Content:     &plugin.ReportContent{TotalRequests: int64(len(*calls))},
		}
		if err := f.storage.SaveComplianceReport(report); err != nil {
			return nil, err
		}
		return report, nil
	})
}

// TestGenerateComplianceReportIdempotent 指定 start 补生成：同 start 再生成覆盖（Count==1）
func TestGenerateComplianceReportIdempotent(t *testing.T) {
	f := newRBACFixture(t, true)
	var calls []string
	stubReportGenerator(f, &calls)

	body := `{"period_type":"day","start":"2026-08-20"}`
	rec := f.do(f.superTok, http.MethodPost, "/api/compliance-reports/generate", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("生成应 200, got %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			ID         string    `json:"id"`
			PeriodType string    `json:"period_type"`
			PeriodEnd  time.Time `json:"period_end"`
			Content    struct {
				TotalRequests int64 `json:"total_requests"`
			} `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	firstID := resp.Data.ID
	if firstID == "" || resp.Data.Content.TotalRequests != 1 {
		t.Fatalf("首次生成应返回完整报表: %+v", resp.Data)
	}
	if n, _ := f.storage.CountComplianceReports(); n != 1 {
		t.Fatalf("生成一次后应有 1 条报表, got %d", n)
	}

	// 同 start 再生成 → 覆盖原记录
	if rec = f.do(f.superTok, http.MethodPost, "/api/compliance-reports/generate", body); rec.Code != http.StatusOK {
		t.Fatalf("再生成应 200, got %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if n, _ := f.storage.CountComplianceReports(); n != 1 {
		t.Errorf("同期重复生成应覆盖而非新增, got %d", n)
	}
	if resp.Data.ID != firstID {
		t.Errorf("覆盖后应保留业务键对应记录 ID: first=%s second=%s", firstID, resp.Data.ID)
	}
	if resp.Data.Content.TotalRequests != 2 {
		t.Errorf("覆盖后内容应刷新, got %+v", resp.Data.Content)
	}

	// 非法 period_type 与非法日期 → 400
	if rec = f.do(f.superTok, http.MethodPost, "/api/compliance-reports/generate",
		`{"period_type":"quarter","start":"2026-08-20"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("非法周期类型应 400, got %d", rec.Code)
	}
	if rec = f.do(f.superTok, http.MethodPost, "/api/compliance-reports/generate",
		`{"period_type":"day","start":"08/20/2026"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("非法日期格式应 400, got %d", rec.Code)
	}
}

// TestGenerateUsesCurrentPeriodOnEmptyStart 省略 start 时取当前周期起点（day=今日零点）
func TestGenerateUsesCurrentPeriodOnEmptyStart(t *testing.T) {
	f := newRBACFixture(t, true)
	var calls []string
	stubReportGenerator(f, &calls)

	if rec := f.do(f.superTok, http.MethodPost, "/api/compliance-reports/generate",
		`{"period_type":"day"}`); rec.Code != http.StatusOK {
		t.Fatalf("省略 start 应可生成, got %d %s", rec.Code, rec.Body.String())
	}
	y, m, d := time.Now().Date()
	want := time.Date(y, m, d, 0, 0, 0, 0, time.Local)
	if len(calls) != 1 || !strings.HasSuffix(calls[0], "|"+want.Format(time.RFC3339)) {
		t.Errorf("空 start 应取今日零点, calls=%v want=%v", calls, want)
	}
}

// TestGenerateUnavailableWithoutInjector 未注入生成器（OSS 构建）→ 503
func TestGenerateUnavailableWithoutInjector(t *testing.T) {
	f := newRBACFixture(t, true)
	rec := f.do(f.superTok, http.MethodPost, "/api/compliance-reports/generate",
		`{"period_type":"day","start":"2026-08-20"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("未注入生成器应 503, got %d %s", rec.Code, rec.Body.String())
	}
}

// TestComplianceEndpointsGlobalOnly 租户内用户即使持全量权限码也被 globalOnlyGuard 拒绝（403）
func TestComplianceEndpointsGlobalOnly(t *testing.T) {
	f := newRBACFixture(t, true)
	seeded := seedComplianceReports(t, f)

	// 给租户 A 用户挂全权限角色并重新签发会话（排除权限码拦截，专测全局域守卫）
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
		{"list", func() *httptest.ResponseRecorder {
			return f.do(tok, http.MethodGet, "/api/compliance-reports?page=1&size=10", "")
		}},
		{"get", func() *httptest.ResponseRecorder {
			return f.do(tok, http.MethodGet, "/api/compliance-reports/"+seeded[0].ID, "")
		}},
		{"csv", func() *httptest.ResponseRecorder {
			return f.do(tok, http.MethodGet, "/api/compliance-reports/"+seeded[0].ID+"?format=csv", "")
		}},
		{"generate", func() *httptest.ResponseRecorder {
			return f.do(tok, http.MethodPost, "/api/compliance-reports/generate", `{"period_type":"day","start":"2026-08-20"}`)
		}},
	}
	for _, tc := range cases {
		if rec := tc.do(); rec.Code != http.StatusForbidden {
			t.Errorf("租户内用户 %s 应 403, got %d %s", tc.name, rec.Code, rec.Body.String())
		}
	}
}
