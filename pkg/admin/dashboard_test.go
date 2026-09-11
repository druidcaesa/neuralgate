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
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
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
		t.Fatalf("无 system:read 访问首页应 403 无权限, got %d %s", rec.Code, rec.Body.String())
	}
	var resp Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Code != http.StatusForbidden {
		t.Errorf("业务码应 403, got %d", resp.Code)
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
		// 新增面板同样必须下发骨架而非 null，前端无需判空
		if len(d.Latency.Buckets) != 6 {
			t.Errorf("%s latency.buckets 档数 = %d, want 6", tc.query, len(d.Latency.Buckets))
		}
		if len(d.Status) != 5 {
			t.Errorf("%s status 档数 = %d, want 5", tc.query, len(d.Status))
		}
		if d.TopModels == nil {
			t.Errorf("%s top_models 不得为 null", tc.query)
		}
		if d.Summary.Failed != 0 {
			t.Errorf("%s 空库 failed 应为 0，实得 %d", tc.query, d.Summary.Failed)
		}
	}
}

// jsonKey 一条契约字段断言：path 按层定位 key，want 为 nil 时只查该 key 是否存在，
// 否则同时核对取值。want 里的数字须写成 float64——encoding/json 把 JSON 数字解为 float64
type jsonKey struct {
	path string
	want any
}

// assertJSONKeys 逐条断言下发 JSON 满足契约字段。
// 响应体解进 map[string]any 而非结构体：结构体解码按 Go 字段名匹配、自行反查 json tag，
// 等于绕开 tag 本身，钉不住前端赖以取值的 key；map 保留真实下发的 key，
// 按路径逐层下钻即只查「该 key 是否存在」，既不依赖字段声明顺序，也不受同层新增字段影响
// ——JSON key 顺序对前端没有语义。requests/tokens/failed/count 等同名 key 落在多个结构体上，
// 由完整路径区分（如 data.summary.tokens 与 data.top_models[0].tokens）。
// 数组取首个元素取样（下标写成 [i]），故排行行/告警条目各有种子用例覆盖。
// 失败信息点名断开的路径，便于判断是哪个字段漂了。
func assertJSONKeys(t *testing.T, body string, keys []jsonKey) {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal([]byte(body), &root); err != nil {
		t.Fatalf("响应体解析失败: %v; body=%s", err, body)
	}
	for _, k := range keys {
		segs := strings.Split(strings.NewReplacer("[", ".", "]", "").Replace(k.path), ".")
		got, broken := probeJSONPath(root, segs)
		if broken != "" {
			t.Errorf("下发 JSON 缺失路径 %s（%s，字段名或层级已漂移）; body=%s", k.path, broken, body)
			continue
		}
		if k.want != nil && !reflect.DeepEqual(got, k.want) {
			t.Errorf("路径 %s 取值 = %#v, want %#v; body=%s", k.path, got, k.want, body)
		}
	}
}

// probeJSONPath 从根逐层下钻：map 按 key、切片按下标取值。
// 返回叶子值与首个断点的说明（以父层路径 + 缺失/越界的段描述），断点为空表示整条路径存在
func probeJSONPath(root any, segs []string) (any, string) {
	cur := root
	for i, seg := range segs {
		switch node := cur.(type) {
		case map[string]any:
			v, ok := node[seg]
			if !ok {
				return nil, fmt.Sprintf("%s 下无 key %q", probeParent(segs[:i]), seg)
			}
			cur = v
		case []any:
			idx, err := strconv.Atoi(seg)
			if err != nil || idx < 0 || idx >= len(node) {
				return nil, fmt.Sprintf("%s 的下标 %s 越界（len=%d）", probeParent(segs[:i]), seg, len(node))
			}
			cur = node[idx]
		default:
			return nil, fmt.Sprintf("%s 不是对象或数组（%T）", probeParent(segs[:i]), cur)
		}
	}
	return cur, ""
}

// probeParent 渲染断点父层的路径，用于失败信息里点名断在了哪一层；
// 数组下标还原成 [i] 写法，与断言里的路径写法一致
func probeParent(segs []string) string {
	if len(segs) == 0 {
		return "响应体根"
	}
	var b strings.Builder
	for _, seg := range segs {
		if _, err := strconv.Atoi(seg); err == nil {
			fmt.Fprintf(&b, "[%s]", seg)
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('.')
		}
		b.WriteString(seg)
	}
	return b.String()
}

// TestDashboardAPIResponseFieldNames 下发 JSON 的字段名是前端契约，前端按固定 key
// 取值，字段名一旦漂移会静默取不到数据，故对原始响应体按路径逐层断言 key 的存在性，
// 绕开结构体解码对 tag 的免疫。改任一字段的 json tag 都会让对应路径失配
func TestDashboardAPIResponseFieldNames(t *testing.T) {
	// 空库下各面板仍须下发骨架，指标恒为零值
	t.Run("空库骨架", func(t *testing.T) {
		s := newDashboardServer(t, oss.NewMemStorage(), nil)
		body := getDashboard(t, s, "?window=24h").Body.String()

		assertJSONKeys(t, body, []jsonKey{
			// DashboardData 八个面板 key
			{path: "data.summary"},
			{path: "data.trend"},
			{path: "data.latency"},
			{path: "data.status"},
			{path: "data.top_models", want: []any{}}, // 空库须为空数组而非 null
			{path: "data.tokens"},
			{path: "data.truncated"},
			{path: "data.alerts", want: []any{}}, // 同上
			// DashboardSummary 五个指标
			{path: "data.summary.requests"},
			{path: "data.summary.success_rate"},
			{path: "data.summary.failed"},
			{path: "data.summary.tokens"},
			{path: "data.summary.avg_latency_ms"},
			// TrendPoint 三个字段；24h 窗口桶数恒定，首个数据点必然存在
			{path: "data.trend[0].date"},
			{path: "data.trend[0].requests"},
			{path: "data.trend[0].tokens"},
			// DashboardLatency 三档分位 + buckets 切片
			{path: "data.latency.p50_ms"},
			{path: "data.latency.p95_ms"},
			{path: "data.latency.p99_ms"},
			{path: "data.latency.buckets"},
			// LatencyBucket 两字段；档位文案含 "<"，会被 gin 的 HTML 转义改写，故不钉取值
			{path: "data.latency.buckets[0].label"},
			{path: "data.latency.buckets[0].count"},
			// StatusBucket 两字段；恒定五档
			{path: "data.status[0].class"},
			{path: "data.status[0].count"},
			// DashboardTokens 六个字段
			{path: "data.tokens.prompt_tokens"},
			{path: "data.tokens.completion_tokens"},
			{path: "data.tokens.stream_requests"},
			{path: "data.tokens.non_stream_requests"},
			{path: "data.tokens.stream_tokens"},
			{path: "data.tokens.non_stream_tokens"},
		})
	})

	// top_models 空库时为空数组，排行行字段只在有行时出现，用最小种子数据覆盖；
	// 非 2xx 样本同时钉住 failed 的取值出处
	t.Run("有排行行时含排行行字段", func(t *testing.T) {
		storage := oss.NewMemStorage()
		if err := storage.SaveAuditLog(&plugin.AuditLog{
			ID: "l1", RequestID: "r1", CreatedAt: time.Now(),
			ResponseStatus: 500, ModelName: "m1", TotalTokens: 42,
		}); err != nil {
			t.Fatal(err)
		}
		s := newDashboardServer(t, storage, nil)
		body := getDashboard(t, s, "?window=24h").Body.String()

		// ModelStat 四个字段；单条 500 即 1 请求 1 失败
		assertJSONKeys(t, body, []jsonKey{
			{path: "data.top_models[0].model_name", want: "m1"},
			{path: "data.top_models[0].requests", want: float64(1)},
			{path: "data.top_models[0].tokens", want: float64(42)},
			{path: "data.top_models[0].failed", want: float64(1)},
		})
	})

	// alerts 无异常时为空数组，告警条目字段只在有告警时出现，用篡改告警种子覆盖
	t.Run("有告警时含告警条目字段", func(t *testing.T) {
		storage := oss.NewMemStorage()
		if err := storage.SaveTamperAlerts([]*plugin.TamperAlert{
			{AuditLogID: "a1", Reason: "指纹不一致"},
		}); err != nil {
			t.Fatal(err)
		}
		s := newDashboardServer(t, storage, nil)
		body := getDashboard(t, s, "").Body.String()

		// DashboardAlert 四个字段；文案不在此处钉，link 带 omitempty，
		// 篡改告警是唯一携 link 的条目，故同时核对取值
		assertJSONKeys(t, body, []jsonKey{
			{path: "data.alerts[0].level", want: "error"},
			{path: "data.alerts[0].title"},
			{path: "data.alerts[0].detail"},
			{path: "data.alerts[0].link", want: "/tamper-alerts"},
		})
	})
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
