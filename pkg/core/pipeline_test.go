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
	"strings"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/adapter"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
)

// recordingLimiter 记录 Allow 收到的 model 参数(验证模型级限流是否拿到路由注入的模型名)
type recordingLimiter struct {
	models []string
	calls  int
}

func (l *recordingLimiter) Init(config map[string]interface{}) error { return nil }
func (l *recordingLimiter) Allow(tenantID string, model string, tokens int) (bool, int64, error) {
	l.calls++
	l.models = append(l.models, model)
	return true, 10, nil
}
func (l *recordingLimiter) Status(tenantID string, model string) (int64, int64, time.Time) {
	return 0, 10, time.Now().Add(time.Second)
}
func (l *recordingLimiter) Reset(tenantID string, model string) error { return nil }

func TestPipelineOrder(t *testing.T) {
	var calls []string
	record := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, name+"-in")
				next.ServeHTTP(w, r)
				calls = append(calls, name+"-out")
			})
		}
	}
	p := NewPipeline(newTestStorage(), oss.NewMemRateLimiter(), nil, adapter.NewAdapterRegistry())
	p.Use(record("m1"))
	p.Use(record("m2"))
	handler := p.Apply(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "handler")
	}))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer ng-goodkey")
	handler.ServeHTTP(httptest.NewRecorder(), req)
	want := []string{"m1-in", "m2-in", "handler", "m2-out", "m1-out"}
	if len(calls) != len(want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("calls = %v, want %v", calls, want)
		}
	}
}

func TestCustomMiddlewareRunsAfterAuth(t *testing.T) {
	p := NewPipeline(newTestStorage(), oss.NewMemRateLimiter(), nil, adapter.NewAdapterRegistry())
	var sawRC *RequestContext
	p.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rc, ok := RequestContextFrom(r.Context())
			if !ok {
				t.Error("custom middleware must run after AuthMiddleware: RequestContext missing")
				return
			}
			sawRC = rc
			next.ServeHTTP(w, r)
		})
	})
	handler := p.Apply(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer ng-goodkey")
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if sawRC == nil {
		t.Fatal("RequestContext not visible in custom middleware")
	}
	if sawRC.APIKeyID != "k1" {
		t.Errorf("APIKeyID = %q, want k1", sawRC.APIKeyID)
	}
}

func TestAuthMiddlewareCreatesRequestContext(t *testing.T) {
	p := NewPipeline(newTestStorage(), oss.NewMemRateLimiter(), nil, adapter.NewAdapterRegistry())
	var gotRC *RequestContext
	p.Use(AuthMiddleware(p.storage))
	handler := p.Apply(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc, ok := RequestContextFrom(r.Context())
		if !ok {
			t.Error("RequestContext not in context")
			return
		}
		gotRC = rc
	}))
	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer ng-goodkey")
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if gotRC == nil {
		t.Fatal("RequestContext was not created")
	}
	if gotRC.RequestID == "" {
		t.Error("RequestID should not be empty")
	}
	if gotRC.APIKeyID != "k1" {
		t.Errorf("APIKeyID = %q, want k1", gotRC.APIKeyID)
	}
	if gotRC.RequestMethod != "GET" || gotRC.RequestPath != "/v1/models" {
		t.Errorf("RequestMethod/Path = %s %s", gotRC.RequestMethod, gotRC.RequestPath)
	}
	if _, ok := gotRC.RequestHeaders["Authorization"]; ok {
		t.Error("RequestHeaders must not contain Authorization (PRD 5.4 sanitization)")
	}
}

func TestRateLimitAndRouteMiddlewaresPassThrough(t *testing.T) {
	p := NewPipeline(newTestStorage(), oss.NewMemRateLimiter(), nil, adapter.NewAdapterRegistry())
	hit := false
	handler := p.Build(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
	}))
	// GET 请求经路由中间件直接放行(无 body 解析),抵达末端 handler
	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer ng-goodkey")
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if !hit {
		t.Error("handler was not reached through Build() chain")
	}
}

func TestRateLimitReceivesRoutedModel(t *testing.T) {
	// 路由前移(Auth → RouteMatch → RateLimit):POST 经完整链时,限流器必须拿到真实模型名(模型级限流)
	limiter := &recordingLimiter{}
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	p := NewPipeline(routeTestStorage(), limiter, nil, registry)
	hit := false
	handler := p.Build(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4","messages":[]}`))
	req.Header.Set("Authorization", "Bearer ng-open")
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if !hit {
		t.Fatal("handler was not reached through Build() chain")
	}
	if limiter.calls != 1 {
		t.Fatalf("Allow calls = %d, want 1", limiter.calls)
	}
	if len(limiter.models) != 1 || limiter.models[0] != "gpt-4" {
		t.Fatalf("Allow models = %v, want [gpt-4] (模型级限流必须按真实模型名计数)", limiter.models)
	}
}

func TestRateLimitNotConsumedOnRouteReject(t *testing.T) {
	// 路由在前:模型不存在(404)时不得消耗限流配额(Allow 不应被调用)
	limiter := &recordingLimiter{}
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	p := NewPipeline(routeTestStorage(), limiter, nil, registry)
	handler := p.Build(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"nope","messages":[]}`))
	req.Header.Set("Authorization", "Bearer ng-open")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", rec.Code)
	}
	if limiter.calls != 0 {
		t.Fatalf("Allow calls = %d; want 0 (404/403 不消耗限流配额)", limiter.calls)
	}
}
