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
	pipeline := NewPipeline(newTestStorage(), oss.NewMemRateLimiter(), nil, adapter.NewAdapterRegistry())
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
	limiter := oss.NewMemRateLimiter()
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
	limiter := oss.NewMemRateLimiter()
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
	limiter := oss.NewMemRateLimiter()
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
	limiter := oss.NewMemRateLimiter()
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
	limiter := oss.NewMemRateLimiter()
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
	limiter := oss.NewMemRateLimiter()
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
	limiter := oss.NewMemRateLimiter()
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
