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
		CreatedAt: now, UpdatedAt: now,
	})
	return s
}

func doRouteRequest(storage plugin.StoragePlugin, registry *adapter.AdapterRegistry, keyID string, body string) *httptest.ResponseRecorder {
	rc := &RequestContext{APIKeyID: keyID, TenantID: "t1"}
	ctx := WithRequestContext(context.Background(), rc)
	mw := RouteMatchMiddleware(storage, registry)
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

func TestRouteBadJSON(t *testing.T) {
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	rec := doRouteRequest(routeTestStorage(), registry, "k2", `{invalid`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", rec.Code)
	}
}
