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
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
	"go.uber.org/zap"
)

// captureReq 记录一次到达 stub 上游的请求(供断言分路形状)
type captureReq struct {
	method, path     string
	auth, apiKey     string
	anthropicVersion string
	body             string
}

// TestModelConfigTestProtocolAware 连通测试按上游协议分路:
// anthropic(内置 provider 或自定义 tags[adapter]=anthropic)→ POST /v1/messages + x-api-key;
// openai → GET /v1/models + Bearer(现状)
func TestModelConfigTestProtocolAware(t *testing.T) {
	var got captureReq
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = captureReq{
			method: r.Method, path: r.URL.Path,
			auth: r.Header.Get("Authorization"),
			// 统一小写便于断言(Go 服务端头键已规范化,但值保留原样)
			apiKey:           r.Header.Get("X-Api-Key"),
			anthropicVersion: r.Header.Get("Anthropic-Version"),
			body:             string(body),
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer stub.Close()

	s := oss.NewMemStorage()
	now := time.Now()
	models := []*plugin.ModelConfig{
		{ID: "m-openai", ModelName: "gpt-4", Provider: "openai", ProviderModel: "gpt-4o", BaseURL: stub.URL, APIKey: "sk-bearer", Enabled: true, CreatedAt: now, UpdatedAt: now},
		{ID: "m-claude", ModelName: "claude", Provider: "anthropic", ProviderModel: "claude-3-5-sonnet", BaseURL: stub.URL, APIKey: "sk-ant", Enabled: true, CreatedAt: now, UpdatedAt: now},
		{ID: "m-custom", ModelName: "custom-claude", Provider: "custom", ProviderModel: "claude-sonnet", BaseURL: stub.URL, APIKey: "sk-ant2", Tags: map[string]string{"adapter": "anthropic"}, Enabled: true, CreatedAt: now, UpdatedAt: now},
	}
	for _, m := range models {
		if err := s.SaveModelConfig(m); err != nil {
			t.Fatal(err)
		}
	}
	svr := NewAdminServer(s, zap.NewNop(), "oss", oss.NewRateLimiter(s, 100, 100000, "token_bucket"), nil)
	svr.DisableAuth()
	router := svr.Router()

	call := func(id string) (ok bool) {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/models/"+id+"/test", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("test %s status = %d; body=%s", id, w.Code, w.Body.String())
		}
		var resp struct {
			Data struct {
				Ok bool `json:"ok"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp.Data.Ok
	}

	// 内置 anthropic:POST /v1/messages + x-api-key + anthropic-version;请求带 model 与 max_tokens:1
	if !call("m-claude") {
		t.Fatal("anthropic built-in test should report ok")
	}
	if got.method != http.MethodPost || got.path != "/v1/messages" {
		t.Fatalf("anthropic req = %s %s; want POST /v1/messages", got.method, got.path)
	}
	if got.auth != "" {
		t.Fatalf("anthropic req leaked Authorization=%q; want empty", got.auth)
	}
	if got.apiKey != "sk-ant" || got.anthropicVersion != "2023-06-01" {
		t.Fatalf("anthropic req headers = x-api-key:%q version:%q; want sk-ant / 2023-06-01", got.apiKey, got.anthropicVersion)
	}
	if !strings.Contains(got.body, `"model":"claude-3-5-sonnet"`) || !strings.Contains(got.body, `"max_tokens":1`) {
		t.Fatalf("anthropic req body = %s; want model + max_tokens:1", got.body)
	}

	// 自定义 provider + tags[adapter]=anthropic:同样走 Messages 分路
	if !call("m-custom") {
		t.Fatal("custom anthropic test should report ok")
	}
	if got.method != http.MethodPost || got.path != "/v1/messages" || got.apiKey != "sk-ant2" {
		t.Fatalf("custom anthropic req = %s %s x-api-key:%q; want POST /v1/messages sk-ant2", got.method, got.path, got.apiKey)
	}

	// openai:GET /v1/models + Bearer(现状路径)
	if !call("m-openai") {
		t.Fatal("openai test should report ok")
	}
	if got.method != http.MethodGet || got.path != "/v1/models" {
		t.Fatalf("openai req = %s %s; want GET /v1/models", got.method, got.path)
	}
	if got.auth != "Bearer sk-bearer" {
		t.Fatalf("openai req Authorization = %q; want Bearer sk-bearer", got.auth)
	}
}
