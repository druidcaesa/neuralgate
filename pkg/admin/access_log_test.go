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
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/license"
	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// newObservedServer 构造带日志观察器的管理后台(enterprise + 指定授权)。
// storage 传 nil 时使用默认内存存储;认证保持 NewAdminServer 的默认状态(开启),
// 需要免认证的用例自行调用 s.DisableAuth()
func newObservedServer(t *testing.T, storage plugin.StoragePlugin, lic *LicenseOverview) (*AdminServer, *observer.ObservedLogs) {
	t.Helper()
	if storage == nil {
		storage = oss.NewMemStorage()
	}
	core, logs := observer.New(zapcore.DebugLevel)
	s := NewAdminServer(storage, zap.New(core), "enterprise",
		oss.NewRateLimiter(oss.NewMemStorage(), 100, 100000, "token_bucket"), lic)
	return s, logs
}

// doGet 向管理后台发一次 GET;token 为空表示不带认证头
func doGet(s *AdminServer, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set(tokenHeader, token)
	}
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	return rec
}

// TestAccessLogSkips2xx 2xx 不产生任何日志(后台轮询不得刷屏)
func TestAccessLogSkips2xx(t *testing.T) {
	s, logs := newObservedServer(t, nil, enterpriseLicenseAll())
	s.DisableAuth()

	if rec := doGet(s, "/healthz", ""); rec.Code != http.StatusOK {
		t.Fatalf("/healthz 应 200, got %d", rec.Code)
	}
	if rec := doGet(s, "/api/ping", ""); rec.Code != http.StatusOK {
		t.Fatalf("/api/ping 应 200, got %d", rec.Code)
	}
	if logs.Len() != 0 {
		t.Errorf("2xx 不应产生日志, got %d 条: %v", logs.Len(), logs.All())
	}
}

// TestAccessLogWarns4xx 未认证访问受保护接口 → 一条 warn,含 method/path/status/request_id/biz_code
func TestAccessLogWarns4xx(t *testing.T) {
	s, logs := newObservedServer(t, nil, enterpriseLicenseAll())
	// 认证保持默认开启:不带 token 请求受保护接口 → 401

	rec := doGet(s, "/api/api-keys", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("未认证应 401, got %d %s", rec.Code, rec.Body.String())
	}
	if logs.Len() != 1 {
		t.Fatalf("应恰好 1 条日志, got %d: %v", logs.Len(), logs.All())
	}
	entry := logs.All()[0]
	if entry.Level != zapcore.WarnLevel {
		t.Errorf("4xx 应为 warn 级, got %v", entry.Level)
	}
	ctx := entry.ContextMap()
	if ctx["status"] != int64(http.StatusUnauthorized) {
		t.Errorf("status 应 401, got %v", ctx["status"])
	}
	if ctx["method"] != http.MethodGet {
		t.Errorf("method 应 GET, got %v", ctx["method"])
	}
	if ctx["path"] != "/api/api-keys" {
		t.Errorf("path 不符: %v", ctx["path"])
	}
	if ctx["biz_code"] != int64(http.StatusUnauthorized) {
		t.Errorf("biz_code 应 401, got %v", ctx["biz_code"])
	}
	if rid, ok := ctx["request_id"].(string); !ok || rid == "" {
		t.Errorf("request_id 不应为空: %v", ctx["request_id"])
	}
}

// TestAccessLogRecordsFeatureLockedBizCode 同为 403,业务码 4030(需企业版授权)可与普通无权限区分
func TestAccessLogRecordsFeatureLockedBizCode(t *testing.T) {
	// 授权不含 compliance → RequireFeature 拦下并返回 CodeFeatureLocked
	lic := &LicenseOverview{Status: "valid", Info: &plugin.LicenseInfo{Features: []string{license.FeatureRBAC}}}
	s, logs := newObservedServer(t, nil, lic)
	s.DisableAuth()

	rec := doGet(s, "/api/compliance-reports", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("应 403, got %d %s", rec.Code, rec.Body.String())
	}
	if logs.Len() != 1 {
		t.Fatalf("应恰好 1 条日志, got %d", logs.Len())
	}
	ctx := logs.All()[0].ContextMap()
	if ctx["status"] != int64(http.StatusForbidden) {
		t.Errorf("status 应 403, got %v", ctx["status"])
	}
	if ctx["biz_code"] != int64(CodeFeatureLocked) {
		t.Errorf("biz_code 应 %d(需企业版授权), got %v", CodeFeatureLocked, ctx["biz_code"])
	}
}

// TestAccessLogPathExcludesQuery 日志 path 只含路径,避免 query 中的密钥进日志
func TestAccessLogPathExcludesQuery(t *testing.T) {
	s, logs := newObservedServer(t, nil, enterpriseLicenseAll())

	if rec := doGet(s, "/api/api-keys?token=super-secret-value&page=1", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("应 401, got %d", rec.Code)
	}
	if logs.Len() != 1 {
		t.Fatalf("应恰好 1 条日志, got %d", logs.Len())
	}
	entry := logs.All()[0]
	if got := entry.ContextMap()["path"]; got != "/api/api-keys" {
		t.Errorf("path 应不含 query, got %v", got)
	}
	// LoggedEntry.Context 是 []zapcore.Field,故遍历 ContextMap 检查有无字段泄漏 query 值
	for k, v := range entry.ContextMap() {
		if s, ok := v.(string); ok && strings.Contains(s, "super-secret-value") {
			t.Errorf("字段 %s 泄漏了 query 值: %s", k, s)
		}
	}
}

// TestAccessLogSetsRequestIDHeader 响应头下发 X-Request-Id,供 DevTools 里的失败请求对应终端日志行
func TestAccessLogSetsRequestIDHeader(t *testing.T) {
	s, logs := newObservedServer(t, nil, enterpriseLicenseAll())
	s.DisableAuth()

	rec := doGet(s, "/healthz", "")
	rid := rec.Header().Get(requestIDHeader)
	if rid == "" {
		t.Fatal("响应头应带 X-Request-Id")
	}
	// 2xx 不打日志,故观察器为空,但请求头仍须下发
	if logs.Len() != 0 {
		t.Errorf("2xx 不应产生日志, got %d", logs.Len())
	}
}

// TestAccessLogNilLoggerDoesNotPanic 现有测试大量以 nil 作 logger,中间件必须短路而非崩溃
func TestAccessLogNilLoggerDoesNotPanic(t *testing.T) {
	s := NewAdminServer(oss.NewMemStorage(), nil, "oss",
		oss.NewRateLimiter(oss.NewMemStorage(), 100, 100000, "token_bucket"), nil)
	s.DisableAuth()
	s.Router().GET("/api/_test/boom", func(c *gin.Context) {
		Error(c, http.StatusInternalServerError, 500, "boom")
	})

	rec := doGet(s, "/api/_test/boom", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("应 500, got %d", rec.Code)
	}
	if rec.Header().Get(requestIDHeader) == "" {
		t.Error("nil logger 时仍应下发 X-Request-Id")
	}
}

// TestAccessLogErrors5xxCarriesCause 5xx 记为 error,并带出 ErrorCause 传入的底层原因,
// 同时该原因不得出现在响应体
func TestAccessLogErrors5xxCarriesCause(t *testing.T) {
	s, logs := newObservedServer(t, nil, enterpriseLicenseAll())
	s.DisableAuth()
	s.Router().GET("/api/_test/boom", func(c *gin.Context) {
		ErrorCause(c, http.StatusInternalServerError, 500, "failed to do thing", errors.New("disk on fire"))
	})

	rec := doGet(s, "/api/_test/boom", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("应 500, got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "disk on fire") {
		t.Errorf("底层原因不得下发浏览器: %s", rec.Body.String())
	}

	if logs.Len() != 1 {
		t.Fatalf("应恰好 1 条日志, got %d: %v", logs.Len(), logs.All())
	}
	entry := logs.All()[0]
	if entry.Level != zapcore.ErrorLevel {
		t.Errorf("5xx 应为 error 级, got %v", entry.Level)
	}
	ctx := entry.ContextMap()
	if ctx["status"] != int64(http.StatusInternalServerError) {
		t.Errorf("status 应 500, got %v", ctx["status"])
	}
	if ctx["err"] != "disk on fire" {
		t.Errorf("err 应为底层原因, got %v", ctx["err"])
	}
}

// TestAccessLog5xxWithoutCauseOmitsErrField 无私有错误来源的 5xx 不写 err 字段,
// 但仍须记录(如 webui.go 的 API 404、Recovery 直接中止的请求)
func TestAccessLog5xxWithoutCauseOmitsErrField(t *testing.T) {
	s, logs := newObservedServer(t, nil, enterpriseLicenseAll())
	s.DisableAuth()
	s.Router().GET("/api/_test/bare", func(c *gin.Context) {
		c.JSON(http.StatusInternalServerError, Response{Code: 500, Message: "bare"})
	})

	if rec := doGet(s, "/api/_test/bare", ""); rec.Code != http.StatusInternalServerError {
		t.Fatalf("应 500, got %d", rec.Code)
	}
	if logs.Len() != 1 {
		t.Fatalf("应恰好 1 条日志, got %d", logs.Len())
	}
	if _, ok := logs.All()[0].ContextMap()["err"]; ok {
		t.Error("无私有错误时不应写 err 字段")
	}
}

// TestAccessLog5xxOnPanic 锁住中间件顺序:handler panic 被 Recovery 转为 500 后,
// AccessLog 仍须拿到终态 500 并记录。若 AccessLog 被移到 Recovery 内层,本用例会失败
func TestAccessLog5xxOnPanic(t *testing.T) {
	// Recovery 会把 panic 堆栈写往 DefaultErrorWriter,测试中静默以免污染输出。
	// 须在构造 AdminServer 前设置:gin.Recovery() 在构造时即读取该值
	prev := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = io.Discard
	defer func() { gin.DefaultErrorWriter = prev }()

	s, logs := newObservedServer(t, nil, enterpriseLicenseAll())
	s.DisableAuth()
	s.Router().GET("/api/_test/panic", func(c *gin.Context) {
		panic("boom")
	})

	rec := doGet(s, "/api/_test/panic", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("panic 应被转为 500, got %d", rec.Code)
	}
	if logs.Len() != 1 {
		t.Fatalf("panic 请求也须记录日志, got %d 条", logs.Len())
	}
	entry := logs.All()[0]
	if entry.Level != zapcore.ErrorLevel {
		t.Errorf("应 error 级, got %v", entry.Level)
	}
	if got := entry.ContextMap()["status"]; got != int64(http.StatusInternalServerError) {
		t.Errorf("status 应 500(AccessLog 必须在 Recovery 之外), got %v", got)
	}
}

// failingListStorage 按方法粒度注入失败,其余方法委托内存存储。
// 用于验证存储层报错时不再把底层细节下发给浏览器
type failingListStorage struct {
	*oss.MemStorage
	mcpErr           error
	complianceErr    error
	tamperErr        error
	saveMCPServerErr error
	mcpAuditErr      error
}

func (f *failingListStorage) ListMCPServers(page, size int) ([]*plugin.MCPServer, int64, error) {
	return nil, 0, f.mcpErr
}

func (f *failingListStorage) ListComplianceReports(page, size int) ([]*plugin.ComplianceReport, int64, error) {
	return nil, 0, f.complianceErr
}

func (f *failingListStorage) ListTamperAlerts(resolved *bool, page, size int) ([]*plugin.TamperAlert, int64, error) {
	return nil, 0, f.tamperErr
}

func (f *failingListStorage) SaveMCPServer(server *plugin.MCPServer) error {
	return f.saveMCPServerErr
}

func (f *failingListStorage) ListMCPAuditLogs(filter plugin.MCPAuditLogFilter, page, size int) ([]*plugin.MCPAuditLog, int64, error) {
	return nil, 0, f.mcpAuditErr
}

// TestAccessLogHidesInternalError 存储层报错时:响应体为通用文案(不含底层细节),
// 底层原因只出现在日志的 err 字段
func TestAccessLogHidesInternalError(t *testing.T) {
	internal := errors.New("sql: database is locked")
	mem := oss.NewMemStorage()
	// 更新侧要求目标记录已存在;SaveMCPServer 的失败注入会拦住桩上写入,
	// 故播种直接落到底层内存存储
	seeded := &plugin.MCPServer{Name: "seeded", Endpoint: "http://example.com"}
	if err := mem.SaveMCPServer(seeded); err != nil {
		t.Fatal(err)
	}
	storage := &failingListStorage{
		MemStorage:       mem,
		mcpErr:           internal,
		complianceErr:    internal,
		tamperErr:        internal,
		saveMCPServerErr: internal,
		mcpAuditErr:      internal,
	}
	s, logs := newObservedServer(t, storage, enterpriseLicenseAll())
	s.DisableAuth()

	cases := []struct {
		name    string
		method  string
		path    string
		body    string
		message string
	}{
		{"mcp 列表", http.MethodGet, "/api/mcp-servers", "", "failed to list mcp servers"},
		{"合规报表列表", http.MethodGet, "/api/compliance-reports", "", "failed to list compliance reports"},
		{"篡改告警列表", http.MethodGet, "/api/tamper-alerts", "", "failed to list tamper alerts"},
		{"mcp 创建", http.MethodPost, "/api/mcp-servers", `{"name":"srv-a","endpoint":"http://example.com"}`, "failed to save mcp server"},
		{"mcp 更新", http.MethodPut, "/api/mcp-servers/" + seeded.ID, `{"name":"srv-a","endpoint":"http://example.com"}`, "failed to save mcp server"},
		{"mcp 审计日志列表", http.MethodGet, "/api/mcp-audit-logs", "", "failed to list mcp audit logs"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := logs.Len()
			var rec *httptest.ResponseRecorder
			if tc.body == "" {
				rec = doGet(s, tc.path, "")
			} else {
				req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
				req.Header.Set("Content-Type", "application/json")
				rec = httptest.NewRecorder()
				s.Router().ServeHTTP(rec, req)
			}
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("应 500, got %d %s", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "database is locked") {
				t.Errorf("底层细节不得下发: %s", rec.Body.String())
			}
			var resp Response
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatal(err)
			}
			if resp.Message != tc.message {
				t.Errorf("响应文案应 %q, got %q", tc.message, resp.Message)
			}
			if resp.Code != http.StatusInternalServerError {
				t.Errorf("业务码应 500, got %d", resp.Code)
			}

			entries := logs.All()[before:]
			if len(entries) != 1 {
				t.Fatalf("应恰好 1 条日志, got %d", len(entries))
			}
			if got := entries[0].ContextMap()["err"]; got != "sql: database is locked" {
				t.Errorf("日志 err 应为底层原因, got %v", got)
			}
		})
	}
}

// TestAccessLogHidesGeneratorError 合规补生成器报错同样只进日志,不下发浏览器
func TestAccessLogHidesGeneratorError(t *testing.T) {
	s, logs := newObservedServer(t, nil, enterpriseLicenseAll())
	s.DisableAuth()
	s.SetReportGenerator(func(periodType string, start time.Time) (*plugin.ComplianceReport, error) {
		return nil, errors.New("internal generator failure")
	})

	req := httptest.NewRequest(http.MethodPost, "/api/compliance-reports/generate",
		strings.NewReader(`{"period_type":"day"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("应 500, got %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "internal generator failure") {
		t.Errorf("底层细节不得下发: %s", rec.Body.String())
	}
	var resp Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Message != "failed to generate compliance report" {
		t.Errorf("响应文案不符: %q", resp.Message)
	}
	if logs.Len() != 1 {
		t.Fatalf("应恰好 1 条日志, got %d", logs.Len())
	}
	if got := logs.All()[0].ContextMap()["err"]; got != "internal generator failure" {
		t.Errorf("日志 err 应为底层原因, got %v", got)
	}
}
