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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
