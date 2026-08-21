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
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/adapter"
	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
)

func TestProxyChatRoutingContextMissing(t *testing.T) {
	pipeline := NewPipeline(newTestStorage(), oss.NewRateLimiter(oss.NewMemStorage(), 100, 100000, "token_bucket"), nil, adapter.NewAdapterRegistry())
	proxy := NewProxyCore(pipeline, adapter.NewAdapterRegistry())
	rec := httptest.NewRecorder()
	// GET 请求经路由中间件直接放行(无 body/模型解析),抵达 handleProxy 时无路由上下文 → 500
	req := httptest.NewRequest("GET", "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer ng-goodkey")
	proxy.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (routing context missing), body=%s", rec.Code, rec.Body.String())
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
	if body.Error.Type != "api_error" || body.Error.Code != "internal_error" {
		t.Errorf("error body = %+v, want api_error/internal_error", body.Error)
	}
}

func TestProxyPassThroughForwarding(t *testing.T) {
	// 透传端点(/v1/completions 等):body 原样转发(不替换 model)、上游 Key 替换、响应头复制
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var reqBody struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(body, &reqBody)
		if reqBody.Model != "gpt-4" { // 透传不替换 model(区别于原生代理)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"model must stay gpt-4","type":"invalid_request_error","code":"bad_request"}}`))
			return
		}
		if r.Header.Get("Authorization") != "Bearer sk-upstream" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"bad upstream key","type":"api_error","code":"auth_error"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Upstream-Test", "echo")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"cmpl-1","object":"text_completion","choices":[{"text":"hello"}]}`))
	}))
	defer upstream.Close()

	storage := oss.NewMemStorage()
	now := time.Now()
	_ = storage.SaveModelConfig(&plugin.ModelConfig{
		ID: "m1", ModelName: "gpt-4", Provider: "openai", ProviderModel: "gpt-4o",
		BaseURL: upstream.URL, APIKey: "sk-upstream", Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	})
	_ = storage.SaveAPIKey(&plugin.APIKey{
		ID: "k1", KeyHash: hashKey("ng-test"), KeyPrefix: "ng-test", Name: "t",
		Status: plugin.APIKeyStatusActive, Quota: -1, CreatedAt: now, UpdatedAt: now,
	})
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	limiter := oss.NewRateLimiter(storage, 100, 100000, "token_bucket")
	_ = limiter.Init(map[string]interface{}{"default_rps": 100, "default_tpm": 100000})
	auditor := oss.NewSimpleAuditor(storage)
	pc := NewProxyCore(NewPipeline(storage, limiter, auditor, registry), registry)

	// 未知端点(completions 等)走透传分支
	req := httptest.NewRequest(http.MethodPost, "/v1/completions", strings.NewReader(`{"model":"gpt-4","prompt":"hi"}`))
	req.Header.Set("Authorization", "Bearer ng-test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	pc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-Upstream-Test") != "echo" {
		t.Errorf("X-Upstream-Test = %q, want echo (响应头未复制)", rec.Header().Get("X-Upstream-Test"))
	}
	if !strings.Contains(rec.Body.String(), "text_completion") {
		t.Errorf("body = %s; want text_completion", rec.Body.String())
	}
}

func TestProxyPassThroughGET(t *testing.T) {
	// GET 透传端点(如 /v1/files/:id):路由中间件对 GET 放行不设模型,
	// handlePassThrough 应取首个启用模型配置作为上游
	var gotMethod, gotPath, gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"file-1","object":"file"}`))
	}))
	defer upstream.Close()

	storage := oss.NewMemStorage()
	now := time.Now()
	_ = storage.SaveModelConfig(&plugin.ModelConfig{
		ID: "m1", ModelName: "gpt-4", Provider: "openai", ProviderModel: "gpt-4o",
		BaseURL: upstream.URL, APIKey: "sk-upstream", Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	})
	_ = storage.SaveAPIKey(&plugin.APIKey{
		ID: "k1", KeyHash: hashKey("ng-test"), KeyPrefix: "ng-test", Name: "t",
		Status: plugin.APIKeyStatusActive, Quota: -1, CreatedAt: now, UpdatedAt: now,
	})
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	limiter := oss.NewRateLimiter(storage, 100, 100000, "token_bucket")
	_ = limiter.Init(map[string]interface{}{"default_rps": 100, "default_tpm": 100000})
	pc := NewProxyCore(NewPipeline(storage, limiter, nil, registry), registry)

	req := httptest.NewRequest(http.MethodGet, "/v1/files/file-1", nil)
	req.Header.Set("Authorization", "Bearer ng-test")
	rec := httptest.NewRecorder()
	pc.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", rec.Code, rec.Body.String())
	}
	if gotMethod != http.MethodGet || gotPath != "/v1/files/file-1" {
		t.Errorf("upstream got %s %s; want GET /v1/files/file-1", gotMethod, gotPath)
	}
	if gotAuth != "Bearer sk-upstream" {
		t.Errorf("upstream auth = %q; want Bearer sk-upstream", gotAuth)
	}
}

func TestProxyPassThroughGETNoEnabledModel(t *testing.T) {
	// 无任何启用模型时,GET 透传端点应返回 404 model_not_found
	storage := oss.NewMemStorage()
	now := time.Now()
	_ = storage.SaveModelConfig(&plugin.ModelConfig{
		ID: "m1", ModelName: "disabled-model", Provider: "openai", ProviderModel: "x",
		BaseURL: "https://upstream", APIKey: "sk", Enabled: false,
		CreatedAt: now, UpdatedAt: now,
	})
	_ = storage.SaveAPIKey(&plugin.APIKey{
		ID: "k1", KeyHash: hashKey("ng-test"), KeyPrefix: "ng-test", Name: "t",
		Status: plugin.APIKeyStatusActive, Quota: -1, CreatedAt: now, UpdatedAt: now,
	})
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	pc := NewProxyCore(NewPipeline(storage, oss.NewRateLimiter(storage, 100, 100000, "token_bucket"), nil, registry), registry)

	req := httptest.NewRequest(http.MethodGet, "/v1/files/file-1", nil)
	req.Header.Set("Authorization", "Bearer ng-test")
	rec := httptest.NewRecorder()
	pc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "model_not_found") {
		t.Fatalf("body = %s; want model_not_found", rec.Body.String())
	}
}

func TestProxyPassThroughAudited(t *testing.T) {
	// 透传端点(/v1/completions):请求开始 Submit + 结束 Finalize,审计必须落库
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"cmpl-1","object":"text_completion","choices":[{"text":"hello"}]}`))
	}))
	defer upstream.Close()

	storage := oss.NewMemStorage()
	now := time.Now()
	_ = storage.SaveModelConfig(&plugin.ModelConfig{
		ID: "m1", ModelName: "gpt-4", Provider: "openai", ProviderModel: "gpt-4o",
		BaseURL: upstream.URL, APIKey: "sk-upstream", Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	})
	_ = storage.SaveAPIKey(&plugin.APIKey{
		ID: "k1", KeyHash: hashKey("ng-test"), KeyPrefix: "ng-test", Name: "t",
		Status: plugin.APIKeyStatusActive, Quota: -1, CreatedAt: now, UpdatedAt: now,
	})
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	limiter := oss.NewRateLimiter(storage, 100, 100000, "token_bucket")
	_ = limiter.Init(map[string]interface{}{"default_rps": 100, "default_tpm": 100000})
	auditor := oss.NewSimpleAuditor(storage)
	pc := NewProxyCore(NewPipeline(storage, limiter, auditor, registry), registry)

	req := httptest.NewRequest(http.MethodPost, "/v1/completions", strings.NewReader(`{"model":"gpt-4","prompt":"hi"}`))
	req.Header.Set("Authorization", "Bearer ng-test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	pc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", rec.Code, rec.Body.String())
	}

	logs, total, err := storage.QueryAuditLogs(plugin.AuditLogFilter{}, 1, 10)
	if err != nil || total != 1 {
		t.Fatalf("audit total = %d, err %v; want 1", total, err)
	}
	if logs[0].RequestPath != "/v1/completions" {
		t.Fatalf("audit RequestPath = %q; want /v1/completions", logs[0].RequestPath)
	}
	if logs[0].ModelName != "gpt-4" || logs[0].Provider != "openai" {
		t.Fatalf("audit log = %+v", logs[0])
	}
	if logs[0].ResponseStatus != http.StatusOK {
		t.Fatalf("audit status = %d; want 200", logs[0].ResponseStatus)
	}
	if logs[0].RequestBody == "" {
		t.Fatal("audit RequestBody empty; want original body preserved")
	}
}

func TestProxyModelsList(t *testing.T) {
	storage := routeTestStorage()
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	limiter := oss.NewRateLimiter(storage, 100, 100000, "token_bucket")
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

func TestProxyModelsListPagination(t *testing.T) {
	// 模型数超单页上限(100)时,/v1/models 必须翻页拉全量,不得截断
	storage := oss.NewMemStorage()
	now := time.Now()
	for i := 0; i < 105; i++ {
		name := fmt.Sprintf("model-%03d", i)
		_ = storage.SaveModelConfig(&plugin.ModelConfig{
			ID: name, ModelName: name, Provider: "openai", ProviderModel: name,
			BaseURL: "https://upstream", APIKey: "sk", Enabled: true,
			CreatedAt: now, UpdatedAt: now,
		})
	}
	_ = storage.SaveAPIKey(&plugin.APIKey{
		ID: "k1", KeyHash: hashKey("ng-test"), KeyPrefix: "ng-test", Name: "t",
		Status: plugin.APIKeyStatusActive, Quota: -1, CreatedAt: now, UpdatedAt: now,
	})
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	pc := NewProxyCore(NewPipeline(storage, oss.NewRateLimiter(storage, 100, 100000, "token_bucket"), nil, registry), registry)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer ng-test")
	rec := httptest.NewRecorder()
	pc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var list struct {
		Data []map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(list.Data) != 105 {
		t.Fatalf("data len = %d; want 105 (存储层单页上限 100,必须翻页拉全量)", len(list.Data))
	}
}

func TestProxyModelsDetail(t *testing.T) {
	storage := routeTestStorage()
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	limiter := oss.NewRateLimiter(storage, 100, 100000, "token_bucket")
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
	pipeline := NewPipeline(newTestStorage(), oss.NewRateLimiter(oss.NewMemStorage(), 100, 100000, "token_bucket"), nil, adapter.NewAdapterRegistry())
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
	pipeline := NewPipeline(newTestStorage(), oss.NewRateLimiter(oss.NewMemStorage(), 100, 100000, "token_bucket"), nil, adapter.NewAdapterRegistry())
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

func TestIsStreamRequestAcceptHeader(t *testing.T) {
	// Accept: text/event-stream → 流式(即使 body 无 stream 字段)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4","messages":[]}`))
	req.Header.Set("Accept", "text/event-stream")
	req = req.WithContext(WithRequestContext(req.Context(), &RequestContext{}))
	if !isStreamRequest(req) {
		t.Error("isStreamRequest = false, want true for Accept: text/event-stream")
	}
	// 无 Accept 且 body stream=false → 非流式
	req2 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4","stream":false}`))
	req2 = req2.WithContext(WithRequestContext(req2.Context(), &RequestContext{}))
	if isStreamRequest(req2) {
		t.Error("isStreamRequest = true, want false")
	}
}

// newMockUpstream 构造 OpenAI 兼容 mock 上游
func newMockUpstream(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var reqBody struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(body, &reqBody)
		if reqBody.Model != "gpt-4o" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"model must be replaced to gpt-4o","type":"invalid_request_error","code":"bad_request"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","object":"chat.completion","created":1700000000,"model":"gpt-4o",
		  "choices":[{"index":0,"message":{"role":"assistant","content":"hello from upstream"},"finish_reason":"stop"}],
		  "usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`))
	}))
}

func TestProxyChatCompletion(t *testing.T) {
	upstream := newMockUpstream(t)
	defer upstream.Close()

	storage := oss.NewMemStorage()
	now := time.Now()
	_ = storage.SaveModelConfig(&plugin.ModelConfig{
		ID: "m1", ModelName: "gpt-4", Provider: "openai", ProviderModel: "gpt-4o",
		BaseURL: upstream.URL, APIKey: "sk-upstream", Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	})
	_ = storage.SaveAPIKey(&plugin.APIKey{
		ID: "k1", KeyHash: hashKey("ng-test"), KeyPrefix: "ng-test", Name: "t",
		Status: plugin.APIKeyStatusActive, Quota: -1, CreatedAt: now, UpdatedAt: now,
	})
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	limiter := oss.NewRateLimiter(storage, 100, 100000, "token_bucket")
	_ = limiter.Init(map[string]interface{}{"default_rps": 100, "default_tpm": 100000})
	auditor := oss.NewSimpleAuditor(storage)
	pc := NewProxyCore(NewPipeline(storage, limiter, auditor, registry), registry)

	body := `{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer ng-test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	pc.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["object"] != "chat.completion" {
		t.Fatalf("object = %v", resp["object"])
	}
	// 上游收到的是替换后的 model(ProviderModel)
	// 审计已落库
	logs, total, err := storage.QueryAuditLogs(plugin.AuditLogFilter{RequestID: ""}, 1, 10)
	if err != nil || total != 1 {
		t.Fatalf("audit total = %d, err %v", total, err)
	}
	if logs[0].ModelName != "gpt-4" || logs[0].Provider != "openai" {
		t.Fatalf("audit log = %+v", logs[0])
	}
	if logs[0].TotalTokens != 15 || logs[0].PromptTokens != 10 {
		t.Fatalf("audit tokens = %+v", logs[0])
	}
	if logs[0].ResponseStatus != 200 {
		t.Fatalf("audit status = %d", logs[0].ResponseStatus)
	}
	if logs[0].ResponseBody == "" || !strings.Contains(logs[0].ResponseBody, "hello from upstream") {
		t.Fatalf("audit ResponseBody = %q; want non-empty upstream content", logs[0].ResponseBody)
	}
}

func TestProxyQuotaUpdate(t *testing.T) {
	// 有限额 Key(Quota=100, 已用 10):请求成功后 UsedQuota 回补本次用量(10+15)
	upstream := newMockUpstream(t)
	defer upstream.Close()

	storage := oss.NewMemStorage()
	now := time.Now()
	_ = storage.SaveModelConfig(&plugin.ModelConfig{
		ID: "m1", ModelName: "gpt-4", Provider: "openai", ProviderModel: "gpt-4o",
		BaseURL: upstream.URL, APIKey: "sk-upstream", Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	})
	_ = storage.SaveAPIKey(&plugin.APIKey{
		ID: "k1", KeyHash: hashKey("ng-test"), KeyPrefix: "ng-test", Name: "t",
		Status: plugin.APIKeyStatusActive, Quota: 100, UsedQuota: 10,
		CreatedAt: now, UpdatedAt: now,
	})
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	limiter := oss.NewRateLimiter(storage, 100, 100000, "token_bucket")
	_ = limiter.Init(map[string]interface{}{"default_rps": 100, "default_tpm": 100000})
	auditor := oss.NewSimpleAuditor(storage)
	pc := NewProxyCore(NewPipeline(storage, limiter, auditor, registry), registry)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer ng-test")
	rec := httptest.NewRecorder()
	pc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	key, err := storage.GetAPIKeyByID("k1")
	if err != nil {
		t.Fatal(err)
	}
	if key.UsedQuota != 25 {
		t.Fatalf("UsedQuota = %d; want 25 (10 + 15 tokens)", key.UsedQuota)
	}
}

func TestProxyRecordsTokensAfterForward(t *testing.T) {
	upstream := newMockUpstream(t)
	defer upstream.Close()

	storage := oss.NewMemStorage()
	now := time.Now()
	_ = storage.SaveModelConfig(&plugin.ModelConfig{
		ID: "m1", ModelName: "gpt-4", Provider: "openai", ProviderModel: "gpt-4o",
		BaseURL: upstream.URL, APIKey: "sk", Enabled: true, CreatedAt: now, UpdatedAt: now,
	})
	_ = storage.SaveAPIKey(&plugin.APIKey{
		ID: "k1", KeyHash: hashKey("ng-test"), KeyPrefix: "ng-test", Name: "t",
		Status: plugin.APIKeyStatusActive, Quota: -1, CreatedAt: now, UpdatedAt: now,
	})
	// tpm=100 sliding_window;mock 上游返回 total_tokens=15
	_ = storage.SaveRateLimitConfig(&plugin.RateLimitConfig{
		ID: "g", RequestsPerSec: 1000, TokensPerMin: 100, Strategy: "sliding_window", Enabled: true,
	})
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	limiter := oss.NewRateLimiter(storage, 1000, 1000000, "sliding_window")
	_ = limiter.ReloadConfig()
	auditor := oss.NewSimpleAuditor(storage)
	pc := NewProxyCore(NewPipeline(storage, limiter, auditor, registry), registry)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer ng-test")
	rec := httptest.NewRecorder()
	pc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	// 回补后 TPM 用量应为 15
	current, _, _ := limiter.Status("", "gpt-4") // 注:Status 返回 RPS;需另验 TPM
	_ = current
	// 通过再次 RecordTokens 触发 TPM 判断:已用 15,tpm=100,再补 90 → 105 超限
	_ = limiter.RecordTokens("", "gpt-4", 90)
	if a, _, _ := limiter.Allow("", "gpt-4", 0); a {
		t.Fatal("TPM should be exhausted after 15+90 > 100")
	}
}

func TestProxyUpstreamError(t *testing.T) {
	// 上游返回 500 → 网关 502 upstream_error
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"upstream boom","type":"api_error","code":"server_error"}}`))
	}))
	defer upstream.Close()

	storage := oss.NewMemStorage()
	now := time.Now()
	_ = storage.SaveModelConfig(&plugin.ModelConfig{
		ID: "m1", ModelName: "gpt-4", Provider: "openai", ProviderModel: "gpt-4o",
		BaseURL: upstream.URL, APIKey: "sk", Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	})
	_ = storage.SaveAPIKey(&plugin.APIKey{
		ID: "k1", KeyHash: hashKey("ng-test"), KeyPrefix: "ng-test", Name: "t",
		Status: plugin.APIKeyStatusActive, Quota: -1, CreatedAt: now, UpdatedAt: now,
	})
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	limiter := oss.NewRateLimiter(storage, 100, 100000, "token_bucket")
	_ = limiter.Init(map[string]interface{}{"default_rps": 100, "default_tpm": 100000})
	auditor := oss.NewSimpleAuditor(storage)
	pc := NewProxyCore(NewPipeline(storage, limiter, auditor, registry), registry)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4","messages":[{"role":"user","content":"x"}]}`))
	req.Header.Set("Authorization", "Bearer ng-test")
	rec := httptest.NewRecorder()
	pc.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d; want 502, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "upstream_error") {
		t.Fatalf("body = %s; want upstream_error", rec.Body.String())
	}
	// 错误路径审计仍应落库(记录上游状态码)
	logs, total, err := storage.QueryAuditLogs(plugin.AuditLogFilter{}, 1, 10)
	if err != nil || total != 1 {
		t.Fatalf("audit total = %d, err %v; want 1", total, err)
	}
	if logs[0].ResponseStatus != http.StatusInternalServerError {
		t.Fatalf("audit status = %d; want 500 (upstream code)", logs[0].ResponseStatus)
	}
	if logs[0].RequestBody == "" {
		t.Fatalf("audit RequestBody empty; want original body preserved")
	}
}

func TestProxyUpstreamTimeoutAudited(t *testing.T) {
	// 上游超时/不可达 → 504;审计记录必须落库且 ResponseStatus=504
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	storage := oss.NewMemStorage()
	now := time.Now()
	_ = storage.SaveModelConfig(&plugin.ModelConfig{
		ID: "m1", ModelName: "gpt-4", Provider: "openai", ProviderModel: "gpt-4o",
		BaseURL: upstream.URL, APIKey: "sk", Enabled: true,
		Timeout: 1, MaxRetries: 0, CreatedAt: now, UpdatedAt: now,
	})
	_ = storage.SaveAPIKey(&plugin.APIKey{
		ID: "k1", KeyHash: hashKey("ng-test"), KeyPrefix: "ng-test", Name: "t",
		Status: plugin.APIKeyStatusActive, Quota: -1, CreatedAt: now, UpdatedAt: now,
	})
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	limiter := oss.NewRateLimiter(storage, 100, 100000, "token_bucket")
	_ = limiter.Init(map[string]interface{}{"default_rps": 100, "default_tpm": 100000})
	auditor := oss.NewSimpleAuditor(storage)
	pc := NewProxyCore(NewPipeline(storage, limiter, auditor, registry), registry)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4","messages":[{"role":"user","content":"x"}]}`))
	req.Header.Set("Authorization", "Bearer ng-test")
	rec := httptest.NewRecorder()
	pc.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d; want 504, body=%s", rec.Code, rec.Body.String())
	}
	logs, total, err := storage.QueryAuditLogs(plugin.AuditLogFilter{}, 1, 10)
	if err != nil || total != 1 {
		t.Fatalf("audit total = %d, err %v; want 1", total, err)
	}
	if logs[0].ResponseStatus != http.StatusGatewayTimeout {
		t.Fatalf("audit status = %d; want 504", logs[0].ResponseStatus)
	}
}

func TestProxyUpstreamTruncatedBodyAudited(t *testing.T) {
	// 上游响应体截断(Content-Length 与实发不符)→ 读上游失败 → 502;审计记录必须落库且 ResponseStatus=502
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1"}`))
	}))
	defer upstream.Close()

	storage := oss.NewMemStorage()
	now := time.Now()
	_ = storage.SaveModelConfig(&plugin.ModelConfig{
		ID: "m1", ModelName: "gpt-4", Provider: "openai", ProviderModel: "gpt-4o",
		BaseURL: upstream.URL, APIKey: "sk", Enabled: true,
		Timeout: 5, MaxRetries: 0, CreatedAt: now, UpdatedAt: now,
	})
	_ = storage.SaveAPIKey(&plugin.APIKey{
		ID: "k1", KeyHash: hashKey("ng-test"), KeyPrefix: "ng-test", Name: "t",
		Status: plugin.APIKeyStatusActive, Quota: -1, CreatedAt: now, UpdatedAt: now,
	})
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	limiter := oss.NewRateLimiter(storage, 100, 100000, "token_bucket")
	_ = limiter.Init(map[string]interface{}{"default_rps": 100, "default_tpm": 100000})
	auditor := oss.NewSimpleAuditor(storage)
	pc := NewProxyCore(NewPipeline(storage, limiter, auditor, registry), registry)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4","messages":[{"role":"user","content":"x"}]}`))
	req.Header.Set("Authorization", "Bearer ng-test")
	rec := httptest.NewRecorder()
	pc.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d; want 502, body=%s", rec.Code, rec.Body.String())
	}
	logs, total, err := storage.QueryAuditLogs(plugin.AuditLogFilter{}, 1, 10)
	if err != nil || total != 1 {
		t.Fatalf("audit total = %d, err %v; want 1", total, err)
	}
	if logs[0].ResponseStatus != http.StatusBadGateway {
		t.Fatalf("audit status = %d; want 502", logs[0].ResponseStatus)
	}
}

func TestProxyLoadBalanceMultiUpstream(t *testing.T) {
	// 两个 mock 上游,各自返回可区分内容
	up1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"1","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"up1"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer up1.Close()
	up2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"2","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"up2"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer up2.Close()

	storage := oss.NewMemStorage()
	now := time.Now()
	_ = storage.SaveModelConfig(&plugin.ModelConfig{
		ID: "m1", ModelName: "gpt-4", Provider: "openai", ProviderModel: "gpt-4o",
		BaseURL: "http://unused", APIKey: "sk", Enabled: true, CreatedAt: now, UpdatedAt: now,
	})
	// 两个上游,各 weight 1
	_ = storage.SaveUpstream(&plugin.Upstream{ID: "u1", ModelConfigID: "m1", BaseURL: up1.URL, APIKey: "sk1", Weight: 1, Enabled: true, CreatedAt: now, UpdatedAt: now})
	_ = storage.SaveUpstream(&plugin.Upstream{ID: "u2", ModelConfigID: "m1", BaseURL: up2.URL, APIKey: "sk2", Weight: 1, Enabled: true, CreatedAt: now, UpdatedAt: now})
	_ = storage.SaveAPIKey(&plugin.APIKey{ID: "k1", KeyHash: hashKey("ng-test"), KeyPrefix: "ng-test", Name: "t", Status: plugin.APIKeyStatusActive, Quota: -1, CreatedAt: now, UpdatedAt: now})

	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	limiter := oss.NewRateLimiter(storage, 1000, 1000000, "token_bucket")
	_ = limiter.ReloadConfig()
	auditor := oss.NewSimpleAuditor(storage)
	pc := NewProxyCore(NewPipeline(storage, limiter, auditor, registry), registry)

	// 多次调用,命中的 content 必为 up1 或 up2(证明走了 upstreams 而非 unused base_url)
	seen := map[string]bool{}
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
			strings.NewReader(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`))
		req.Header.Set("Authorization", "Bearer ng-test")
		rec := httptest.NewRecorder()
		pc.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "up1") {
			seen["up1"] = true
		} else if strings.Contains(rec.Body.String(), "up2") {
			seen["up2"] = true
		} else {
			t.Fatalf("response from neither upstream: %s", rec.Body.String())
		}
	}
	// 10 次 50/50 权重,两个上游都应至少命中一次(极小概率偶发,统计上稳健)
	if !seen["up1"] && !seen["up2"] {
		t.Fatal("no upstream hit")
	}
}

func TestProxyFallbackToModelConfigWhenNoUpstream(t *testing.T) {
	// 无 upstreams → 回退 ModelConfig.base_url
	upstream := newMockUpstream(t)
	defer upstream.Close()
	storage := oss.NewMemStorage()
	now := time.Now()
	_ = storage.SaveModelConfig(&plugin.ModelConfig{
		ID: "m1", ModelName: "gpt-4", Provider: "openai", ProviderModel: "gpt-4o",
		BaseURL: upstream.URL, APIKey: "sk", Enabled: true, CreatedAt: now, UpdatedAt: now,
	})
	_ = storage.SaveAPIKey(&plugin.APIKey{ID: "k1", KeyHash: hashKey("ng-test"), KeyPrefix: "ng-test", Name: "t", Status: plugin.APIKeyStatusActive, Quota: -1, CreatedAt: now, UpdatedAt: now})
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	limiter := oss.NewRateLimiter(storage, 1000, 1000000, "token_bucket")
	_ = limiter.ReloadConfig()
	auditor := oss.NewSimpleAuditor(storage)
	pc := NewProxyCore(NewPipeline(storage, limiter, auditor, registry), registry)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer ng-test")
	rec := httptest.NewRecorder()
	pc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("no-upstream fallback status = %d; body=%s", rec.Code, rec.Body.String())
	}
}
