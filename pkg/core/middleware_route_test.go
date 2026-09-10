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
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/druidcaesa/neuralgate/pkg/adapter"
	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
)

func routeTestStorage() *oss.MemStorage {
	s := oss.NewMemStorage()
	now := time.Now()
	_ = s.SaveModelConfig(&plugin.ModelConfig{
		ID: "m1", ModelName: "gpt-4", Provider: "openai", ProviderModel: "gpt-4o",
		BaseURL: "https://upstream", APIKey: "sk", Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	})
	_ = s.SaveModelConfig(&plugin.ModelConfig{
		ID: "m2", ModelName: "disabled-model", Provider: "openai", ProviderModel: "x",
		BaseURL: "https://upstream", APIKey: "sk", Enabled: false,
		CreatedAt: now, UpdatedAt: now,
	})
	_ = s.SaveModelConfig(&plugin.ModelConfig{
		ID: "m3", ModelName: "deepseek-chat", Provider: "deepseek", ProviderModel: "deepseek-chat",
		BaseURL: "https://upstream", APIKey: "sk", Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	})
	// Key 限制模型:仅允许 gpt-4
	_ = s.SaveAPIKey(&plugin.APIKey{
		ID: "k1", KeyHash: hashKey("ng-restricted"), KeyPrefix: "ng-restricted",
		Name: "restricted", Status: plugin.APIKeyStatusActive,
		AllowedModels: []string{"gpt-4"}, CreatedAt: now, UpdatedAt: now,
	})
	_ = s.SaveAPIKey(&plugin.APIKey{
		ID: "k2", KeyHash: hashKey("ng-open"), KeyPrefix: "ng-open",
		Name: "open", Status: plugin.APIKeyStatusActive, AllowedModels: nil,
		Quota:     -1, // 无额度限制,否则 Quota 默认 0 会被鉴权中间件判为 quota_exceeded(429)
		CreatedAt: now, UpdatedAt: now,
	})
	return s
}

func doRouteRequest(storage plugin.StoragePlugin, registry *adapter.AdapterRegistry, keyID string, body string) *httptest.ResponseRecorder {
	rc := &RequestContext{APIKeyID: keyID, TenantID: "t1"}
	ctx := WithRequestContext(context.Background(), rc)
	mw := RouteMatchMiddleware(storage, registry, zap.NewNop())
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc, _ := RequestContextFrom(r.Context())
		// 恢复的 body 可再次读取
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Model", rc.ModelConfig.ModelName)
		w.Header().Set("X-Provider", rc.Adapter.Name())
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader([]byte(body)))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestRouteValidModel(t *testing.T) {
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	rec := doRouteRequest(routeTestStorage(), registry, "k2", `{"model":"gpt-4","messages":[]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-Model") != "gpt-4" || rec.Header().Get("X-Provider") != "openai" {
		t.Fatalf("headers = %s,%s", rec.Header().Get("X-Model"), rec.Header().Get("X-Provider"))
	}
	// body 恢复后可读
	if !strings.Contains(rec.Body.String(), "gpt-4") {
		t.Fatalf("restored body = %s", rec.Body.String())
	}
}

func TestRouteModelNotFound(t *testing.T) {
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	rec := doRouteRequest(routeTestStorage(), registry, "k2", `{"model":"nope","messages":[]}`)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "model_not_found") {
		t.Fatalf("status=%d body=%s; want 404 model_not_found", rec.Code, rec.Body.String())
	}
}

func TestRouteDisabledModel(t *testing.T) {
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	rec := doRouteRequest(routeTestStorage(), registry, "k2", `{"model":"disabled-model","messages":[]}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", rec.Code)
	}
}

func TestRouteModelAccessDenied(t *testing.T) {
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	rec := doRouteRequest(routeTestStorage(), registry, "k1", `{"model":"deepseek-chat","messages":[]}`)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "model_access_denied") {
		t.Fatalf("status=%d body=%s; want 403 model_access_denied", rec.Code, rec.Body.String())
	}
}

func TestRouteBodyTooLarge(t *testing.T) {
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	// >1MB 请求体:显式 413 request_too_large,不得静默截断后继续转发
	big := `{"model":"gpt-4","padding":"` + strings.Repeat("a", 1<<20) + `"}`
	rec := doRouteRequest(routeTestStorage(), registry, "k2", big)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d; want 413, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "request_too_large") {
		t.Fatalf("body = %s; want request_too_large", rec.Body.String())
	}
}

func TestRouteBadJSON(t *testing.T) {
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	rec := doRouteRequest(routeTestStorage(), registry, "k2", `{invalid`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", rec.Code)
	}
}

func TestRouteCustomProviderFallsBackToOpenAI(t *testing.T) {
	// 自定义供应商(未注册适配器) → 回退 OpenAIAdapter(原生透传)
	s := routeTestStorage()
	now := time.Now()
	_ = s.SaveModelConfig(&plugin.ModelConfig{
		ID: "m-custom", ModelName: "custom-model", Provider: "my-custom", ProviderModel: "custom-1",
		BaseURL: "https://custom.example.com", APIKey: "sk", Enabled: true, CreatedAt: now, UpdatedAt: now,
	})
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())

	rc := &RequestContext{APIKeyID: "k2", TenantID: "t1"}
	ctx := WithRequestContext(context.Background(), rc)
	mw := RouteMatchMiddleware(s, registry, zap.NewNop())
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc, _ := RequestContextFrom(r.Context())
		w.Header().Set("X-Provider", rc.Adapter.Name())
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		bytes.NewReader([]byte(`{"model":"custom-model","messages":[]}`)))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", rec.Code, rec.Body.String())
	}
	// 自定义 provider 回退到 openai 适配器(原生透传)
	if rec.Header().Get("X-Provider") != "openai" {
		t.Fatalf("provider = %s; want openai (fallback)", rec.Header().Get("X-Provider"))
	}
}

// stubProtocolAdapter 仅用于验证自定义 provider 按 tags["adapter"] 路由的测试替身
type stubProtocolAdapter struct{ name string }

func (s *stubProtocolAdapter) Name() string              { return s.name }
func (s *stubProtocolAdapter) SupportsNativeProxy() bool { return true }
func (s *stubProtocolAdapter) TransformRequest(*adapter.UnifiedRequest, []byte) (*http.Request, error) {
	return nil, errors.New("native proxy only")
}
func (s *stubProtocolAdapter) TransformResponse(*http.Response) (*adapter.UnifiedResponse, error) {
	return nil, errors.New("native proxy only")
}
func (s *stubProtocolAdapter) TransformStreamChunk([]byte) (*adapter.UnifiedSSEChunk, error) {
	return nil, errors.New("native proxy only")
}
func (s *stubProtocolAdapter) ParseTokenUsage(*http.Response) (int, int, int) { return 0, 0, 0 }
func (s *stubProtocolAdapter) ParseStreamUsage([]byte) (int, int, int)        { return 0, 0, 0 }
func (s *stubProtocolAdapter) ParseError(*http.Response) (int, string)        { return 0, "" }

// TestRouteCustomProviderAdapterTagDrivesSelection:自定义 provider + tags["adapter"]
// → 按 tag 名选已注册适配器(而非一律回退 openai)
func TestRouteCustomProviderAdapterTagDrivesSelection(t *testing.T) {
	s := routeTestStorage()
	now := time.Now()
	_ = s.SaveModelConfig(&plugin.ModelConfig{
		ID: "m-tag", ModelName: "tag-routed", Provider: "my-proxy", ProviderModel: "m-1",
		BaseURL: "https://custom.example.com", APIKey: "sk", Enabled: true,
		Tags: map[string]string{"adapter": "anthropic"}, CreatedAt: now, UpdatedAt: now,
	})
	registry := adapter.NewAdapterRegistry()
	registry.Register(&stubProtocolAdapter{name: "anthropic"})

	rc := &RequestContext{APIKeyID: "k2", TenantID: "t1"}
	ctx := WithRequestContext(context.Background(), rc)
	mw := RouteMatchMiddleware(s, registry, zap.NewNop())
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc, _ := RequestContextFrom(r.Context())
		w.Header().Set("X-Provider", rc.Adapter.Name())
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		bytes.NewReader([]byte(`{"model":"tag-routed","messages":[]}`)))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-Provider") != "anthropic" {
		t.Fatalf("provider = %s; want anthropic (adapter tag)", rec.Header().Get("X-Provider"))
	}
}

// TestRouteCustomProviderOpenaiTag:前端默认 provider=custom + tags["adapter"]=openai
// → 解析到注册的 openai 适配器(与缺失 tag 的回退结果一致)
func TestRouteCustomProviderOpenaiTag(t *testing.T) {
	s := routeTestStorage()
	now := time.Now()
	_ = s.SaveModelConfig(&plugin.ModelConfig{
		ID: "m-custom-oai", ModelName: "custom-oai", Provider: "custom", ProviderModel: "llama-3.1",
		BaseURL: "http://127.0.0.1:8000/v1", APIKey: "sk", Enabled: true,
		Tags: map[string]string{"adapter": "openai"}, CreatedAt: now, UpdatedAt: now,
	})
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())

	rc := &RequestContext{APIKeyID: "k2", TenantID: "t1"}
	ctx := WithRequestContext(context.Background(), rc)
	mw := RouteMatchMiddleware(s, registry, zap.NewNop())
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc, _ := RequestContextFrom(r.Context())
		w.Header().Set("X-Provider", rc.Adapter.Name())
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		bytes.NewReader([]byte(`{"model":"custom-oai","messages":[]}`)))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-Provider") != "openai" {
		t.Fatalf("provider = %s; want openai", rec.Header().Get("X-Provider"))
	}
}

// TestRouteBuiltinIgnoresAdapterTag:内置 provider 残留 tags[adapter] 仍走注册的
// 内置适配器(tag 只对未注册的自定义 provider 生效,保证向后兼容)
func TestRouteBuiltinIgnoresAdapterTag(t *testing.T) {
	s := routeTestStorage()
	now := time.Now()
	_ = s.SaveModelConfig(&plugin.ModelConfig{
		ID: "m-builtin-tag", ModelName: "builtin-tag", Provider: "openai", ProviderModel: "gpt-4o-mini",
		BaseURL: "https://upstream", APIKey: "sk", Enabled: true,
		Tags: map[string]string{"adapter": "anthropic"}, CreatedAt: now, UpdatedAt: now,
	})
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	registry.Register(&stubProtocolAdapter{name: "anthropic"})

	rec := doRouteRequest(s, registry, "k2", `{"model":"builtin-tag","messages":[]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Provider"); got != "openai" {
		t.Fatalf("provider = %s; want openai (builtin ignores tags.adapter)", got)
	}
}
