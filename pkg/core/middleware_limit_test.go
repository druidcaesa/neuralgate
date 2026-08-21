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

package core

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
)

func TestRateLimitAllow(t *testing.T) {
	limiter := oss.NewRateLimiter(oss.NewMemStorage(), 100, 100000, "token_bucket")
	_ = limiter.Init(map[string]interface{}{"default_rps": 10, "default_tpm": 100000})
	mw := RateLimitMiddleware(limiter)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req = req.WithContext(WithRequestContext(req.Context(), &RequestContext{TenantID: "test"}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	// 限流 Header 应存在
	if rec.Header().Get("X-RateLimit-Limit-Requests") == "" {
		t.Fatalf("missing X-RateLimit-Limit-Requests header: %v", rec.Header())
	}
}

func TestRateLimitExceeded(t *testing.T) {
	// 构造 rps=1 的限流器
	limiter := oss.NewRateLimiter(oss.NewMemStorage(), 100, 100000, "token_bucket")
	_ = limiter.Init(map[string]interface{}{"default_rps": 1, "default_tpm": 100000})
	mw := RateLimitMiddleware(limiter)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	do := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		req = req.WithContext(WithRequestContext(req.Context(), &RequestContext{TenantID: "test"}))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	// 第一次通过
	if rec := do(); rec.Code != http.StatusOK {
		t.Fatalf("first request status = %d; want 200", rec.Code)
	}
	// 第二次 429
	rec := do()
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d; want 429", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "rate_limit") {
		t.Fatalf("body = %s; want rate_limit", rec.Body.String())
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatalf("missing Retry-After header")
	}
	// X-RateLimit-Reset-Requests 应为 unix 秒时间戳(OpenAI 规范,非硬编码 "1s")
	if v := rec.Header().Get("X-RateLimit-Reset-Requests"); v == "" {
		t.Fatalf("missing X-RateLimit-Reset-Requests header")
	} else if _, err := strconv.Atoi(v); err != nil {
		t.Fatalf("X-RateLimit-Reset-Requests = %q; want unix timestamp", v)
	}
}

func TestRateLimitHealthzExempt(t *testing.T) {
	// /healthz 不计入限流桶(与 AuthMiddleware 的免鉴权对称):Allow 不应被调用
	limiter := &recordingLimiter{}
	mw := RateLimitMiddleware(limiter)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req = req.WithContext(WithRequestContext(req.Context(), &RequestContext{TenantID: "test"}))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("healthz request %d status = %d; want 200", i, rec.Code)
		}
	}
	if limiter.calls != 0 {
		t.Fatalf("Allow calls = %d; want 0 (healthz 不计入限流桶)", limiter.calls)
	}
}

func TestRateLimitTPMExceededTokenLimit(t *testing.T) {
	s := oss.NewMemStorage()
	_ = s.SaveRateLimitConfig(&plugin.RateLimitConfig{
		ID: "g", RequestsPerSec: 1000, TokensPerMin: 5, Strategy: "sliding_window", Enabled: true,
	})
	limiter := oss.NewRateLimiter(s, 1000, 1000000, "sliding_window")
	_ = limiter.ReloadConfig()
	// 先耗尽 TPM
	_ = limiter.RecordTokens("", "", 5)

	mw := RateLimitMiddleware(limiter)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	rc := &RequestContext{TenantID: ""}
	req = req.WithContext(WithRequestContext(req.Context(), rc))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d; want 429", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "token_limit") {
		t.Fatalf("body = %s; want token_limit", rec.Body.String())
	}
}
