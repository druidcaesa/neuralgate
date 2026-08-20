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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/druidcaesa/neuralgate/pkg/adapter"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
)

func TestProxyCoreSkeletonResponse(t *testing.T) {
	pipeline := NewPipeline(newTestStorage(), oss.NewMemRateLimiter(), nil, adapter.NewAdapterRegistry())
	registry := adapter.NewAdapterRegistry()
	proxy := NewProxyCore(pipeline, registry)
	rec := httptest.NewRecorder()
	// GET 请求经路由中间件直接放行(无 body 解析),抵达代理内核
	// 端点分类:/v1/chat/completions → handleProxy(占位 503)
	req := httptest.NewRequest("GET", "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer ng-goodkey")
	proxy.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
	var body struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body.Error.Type != "api_error" || body.Error.Code != "service_unavailable" {
		t.Errorf("error body = %+v, want api_error/service_unavailable", body.Error)
	}
	if body.Error.Message != "proxy not implemented yet" {
		t.Errorf("message = %q, want proxy not implemented yet (handleProxy 占位)", body.Error.Message)
	}
}

func TestProxyCorePassThroughPlaceholder(t *testing.T) {
	storage := routeTestStorage()
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	limiter := oss.NewMemRateLimiter()
	auditor := oss.NewSimpleAuditor(storage)
	pc := NewProxyCore(NewPipeline(storage, limiter, auditor, registry), registry)

	// 未知端点(completions 等)走透传分支,同样返回占位 503
	req := httptest.NewRequest(http.MethodPost, "/v1/completions", strings.NewReader(`{"model":"gpt-4","prompt":"hi"}`))
	req.Header.Set("Authorization", "Bearer ng-open")
	rec := httptest.NewRecorder()
	pc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; want 503 (handlePassThrough 占位), body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body.Error.Message != "proxy not implemented yet" {
		t.Errorf("message = %q, want proxy not implemented yet", body.Error.Message)
	}
}

func TestProxyModelsList(t *testing.T) {
	storage := routeTestStorage()
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	limiter := oss.NewMemRateLimiter()
	auditor := oss.NewSimpleAuditor(storage)
	pc := NewProxyCore(NewPipeline(storage, limiter, auditor, registry), registry)

	// /v1/models 也走鉴权中间件(路由中间件对 GET 请求跳过 body 解析)
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer ng-open")
	rec := httptest.NewRecorder()
	pc.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var list struct {
		Object string `json:"object"`
		Data   []struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if list.Object != "list" {
		t.Fatalf("object = %q; want list", list.Object)
	}
	// routeTestStorage 中有 3 个模型配置(1 个 disabled)
	if len(list.Data) != 2 {
		t.Fatalf("data len = %d; want 2 (enabled only)", len(list.Data))
	}
	found := false
	for _, m := range list.Data {
		if m.ID == "gpt-4" && m.OwnedBy == "neuralgate" {
			found = true
		}
	}
	if !found {
		t.Fatalf("gpt-4 missing: %+v", list.Data)
	}
}

func TestProxyModelsDetail(t *testing.T) {
	storage := routeTestStorage()
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	limiter := oss.NewMemRateLimiter()
	auditor := oss.NewSimpleAuditor(storage)
	pc := NewProxyCore(NewPipeline(storage, limiter, auditor, registry), registry)

	req := httptest.NewRequest(http.MethodGet, "/v1/models/gpt-4", nil)
	req.Header.Set("Authorization", "Bearer ng-open")
	rec := httptest.NewRecorder()
	pc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/models/nope", nil)
	req.Header.Set("Authorization", "Bearer ng-open")
	rec = httptest.NewRecorder()
	pc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("detail missing model status = %d; want 404", rec.Code)
	}
}

func TestProxyCoreHealthzNoAuth(t *testing.T) {
	// /healthz 免鉴权:无 Bearer 也应直接放行(运维探活)
	pipeline := NewPipeline(newTestStorage(), oss.NewMemRateLimiter(), nil, adapter.NewAdapterRegistry())
	proxy := NewProxyCore(pipeline, adapter.NewAdapterRegistry())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/healthz", nil)
	proxy.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != `{"status":"ok"}` {
		t.Errorf("body = %q, want {\"status\":\"ok\"}", body)
	}
}

func TestProxyCoreHealthz(t *testing.T) {
	pipeline := NewPipeline(newTestStorage(), oss.NewMemRateLimiter(), nil, adapter.NewAdapterRegistry())
	registry := adapter.NewAdapterRegistry()
	proxy := NewProxyCore(pipeline, registry)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/healthz", nil)
	req.Header.Set("Authorization", "Bearer ng-goodkey")
	proxy.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != `{"status":"ok"}` {
		t.Errorf("body = %q, want {\"status\":\"ok\"}", body)
	}
}

func TestIPFilterDefaultsAllow(t *testing.T) {
	f := NewIPFilter()
	if !f.Allow("192.168.1.1") {
		t.Error("Allow() should default to true")
	}
}

func TestProtocolParserIsSSE(t *testing.T) {
	p := NewProtocolParser()
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("Accept", "text/event-stream")
	if !p.IsSSE(req) {
		t.Error("IsSSE() = false, want true for text/event-stream Accept")
	}
	req2 := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	if p.IsSSE(req2) {
		t.Error("IsSSE() = true, want false without Accept header")
	}
}
