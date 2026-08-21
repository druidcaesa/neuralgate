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

// Package core_test 端到端集成测试。
// 使用外部测试包(而非 package core):admin 依赖 core(审计分片重组/版本号),
// 内部测试包导入 admin 会形成 import cycle。
package core_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/druidcaesa/neuralgate/pkg/adapter"
	"github.com/druidcaesa/neuralgate/pkg/admin"
	"github.com/druidcaesa/neuralgate/pkg/core"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
)

// newMockUpstream 构造 OpenAI 兼容 mock 上游(契约同 pkg/core 内部测试的 newMockUpstream:
// 校验 model 已替换为 gpt-4o,返回 hello from upstream)
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

// TestEndToEnd 全链路:SQLite 存储 + mock 上游 + Admin 配置模型/Key + 代理调用 + 审计查询
func TestEndToEnd(t *testing.T) {
	// 1. mock 上游
	upstream := newMockUpstream(t)
	defer upstream.Close()

	// 2. SQLite 存储(临时文件)
	storage := oss.NewSQLStorage()
	if err := storage.Init(map[string]interface{}{
		"driver": "sqlite", "dsn": t.TempDir() + "/e2e.db", "encrypt_key": "e2e-key",
	}); err != nil {
		t.Fatalf("storage init: %v", err)
	}
	defer storage.Close()

	// 3. 组件装配(与 cmd/gateway/main.go 一致)
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	registry.Register(adapter.NewQwenAdapter())
	registry.Register(adapter.NewZhipuAdapter())
	registry.Register(adapter.NewDeepSeekAdapter())
	limiter := oss.NewRateLimiter(oss.NewMemStorage(), 100, 100000, "token_bucket")
	_ = limiter.Init(map[string]interface{}{"default_rps": 100, "default_tpm": 100000})
	auditor := oss.NewSimpleAuditor(storage)
	pc := core.NewProxyCore(core.NewPipeline(storage, limiter, auditor, registry), registry)
	proxyHandler := pc.Handler()
	adminRouter := admin.NewAdminServer(storage, nil, "oss", limiter).Router()

	// 4. 通过管理后台创建模型配置(指向 mock 上游)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/models",
		strings.NewReader(`{"name":"gpt-4","provider":"openai","provider_model":"gpt-4o","base_url":"`+upstream.URL+`","api_key":"sk-e2e"}`))
	req.Header.Set("Content-Type", "application/json")
	adminRouter.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("admin create model: %d %s", w.Code, w.Body.String())
	}

	// 5. 创建 API Key
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/api-keys",
		strings.NewReader(`{"name":"e2e-key","quota":-1}`))
	req.Header.Set("Content-Type", "application/json")
	adminRouter.ServeHTTP(w, req)
	var created struct {
		Data struct {
			Key string `json:"key"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || !strings.HasPrefix(created.Data.Key, "ng-") {
		t.Fatalf("admin create key: %s", w.Body.String())
	}

	// 6. 通过代理服务调用(非流式)
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer "+created.Data.Key)
	req.Header.Set("Content-Type", "application/json")
	proxyHandler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("proxy chat: %d %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["object"] != "chat.completion" || !strings.Contains(w.Body.String(), "hello from upstream") {
		t.Fatalf("proxy response: %s", w.Body.String())
	}

	// 7. 审计查询:管理后台可查到该请求
	w = httptest.NewRecorder()
	adminRouter.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/audit-logs?model_name=gpt-4", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("audit query: %d", w.Code)
	}
	var audit struct {
		Data struct {
			Total int `json:"total"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &audit)
	if audit.Data.Total < 1 {
		t.Fatalf("audit total = %d; want >=1, body=%s", audit.Data.Total, w.Body.String())
	}

	// 8. /v1/models 列表可见模型
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+created.Data.Key)
	proxyHandler.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "gpt-4") {
		t.Fatalf("models list: %d %s", w.Code, w.Body.String())
	}
}

// TestEndToEndLoadBalanceAndRateLimit 全链路:SQLite + Admin 建模型/上游/限流配置/Key + 代理调用 + rps 限流生效
func TestEndToEndLoadBalanceAndRateLimit(t *testing.T) {
	upstream := newMockUpstream(t)
	defer upstream.Close()

	storage := oss.NewSQLStorage()
	if err := storage.Init(map[string]interface{}{
		"driver": "sqlite", "dsn": t.TempDir() + "/e2e2.db", "encrypt_key": "e2e-key",
	}); err != nil {
		t.Fatalf("storage init: %v", err)
	}
	defer storage.Close()

	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	limiter := oss.NewRateLimiter(storage, 1000, 1000000, "token_bucket")
	_ = limiter.ReloadConfig()
	auditor := oss.NewSimpleAuditor(storage)
	pc := core.NewProxyCore(core.NewPipeline(storage, limiter, auditor, registry), registry)
	proxyHandler := pc.Handler()
	adminRouter := admin.NewAdminServer(storage, nil, "oss", limiter).Router()

	// 建模型
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/models",
		strings.NewReader(`{"name":"gpt-4","provider":"openai","provider_model":"gpt-4o","base_url":"`+upstream.URL+`","api_key":"sk-e2e"}`))
	req.Header.Set("Content-Type", "application/json")
	adminRouter.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create model: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	// 加上游(指向同 mock,验证多上游路径可用)
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/models/"+created.Data.ID+"/upstreams",
		strings.NewReader(`{"base_url":"`+upstream.URL+`","api_key":"sk-up","weight":1}`))
	req.Header.Set("Content-Type", "application/json")
	adminRouter.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create upstream: %d %s", w.Code, w.Body.String())
	}

	// 建限流配置:gpt-4 rps=2
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/rate-limits",
		strings.NewReader(`{"tenant_id":"","model_name":"gpt-4","requests_per_sec":2,"tokens_per_min":100000,"strategy":"token_bucket"}`))
	req.Header.Set("Content-Type", "application/json")
	adminRouter.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create rate-limit: %d %s", w.Code, w.Body.String())
	}

	// 建 Key
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/api-keys", strings.NewReader(`{"name":"e2e","quota":-1}`))
	req.Header.Set("Content-Type", "application/json")
	adminRouter.ServeHTTP(w, req)
	var keyResp struct {
		Data struct {
			Key string `json:"key"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &keyResp)

	// 调用 3 次:rps=2 → 前 2 次 200,第 3 次 429
	codes := []int{}
	for i := 0; i < 3; i++ {
		w = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
			strings.NewReader(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`))
		req.Header.Set("Authorization", "Bearer "+keyResp.Data.Key)
		proxyHandler.ServeHTTP(w, req)
		codes = append(codes, w.Code)
	}
	if codes[0] != 200 || codes[1] != 200 || codes[2] != 429 {
		t.Fatalf("rps=2 codes = %v; want [200 200 429]", codes)
	}
}
