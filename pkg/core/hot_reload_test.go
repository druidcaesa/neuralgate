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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/adapter"
)

// TestModelConfigHotReload 模型配置热更新集成验证:RouteMatchMiddleware 链只构建一次,
// 两次请求同一 model 名,中间直接把存储中该模型的 Enabled 改为 false —— 不重启、
// 不做任何缓存失效动作。第二次请求即 404 model_not_found,证明路由链每个请求实时读存储,
// 模型配置变更无需停机即生效。
func TestModelConfigHotReload(t *testing.T) {
	storage := routeTestStorage()
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())

	// 中间件链构建一次,两次请求复用同一实例(等价于进程不重启)
	handler := RouteMatchMiddleware(storage, registry)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc, _ := RequestContextFrom(r.Context())
		w.Header().Set("X-Model", rc.ModelConfig.ModelName)
		w.WriteHeader(http.StatusOK)
	}))
	do := func() *httptest.ResponseRecorder {
		rc := &RequestContext{APIKeyID: "k2", TenantID: "t1"}
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
			bytes.NewReader([]byte(`{"model":"gpt-4","messages":[]}`)))
		req = req.WithContext(WithRequestContext(context.Background(), rc))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	// 第一次请求:模型处于启用状态 → 路由通过
	rec1 := do()
	if rec1.Code != http.StatusOK || rec1.Header().Get("X-Model") != "gpt-4" {
		t.Fatalf("第一次请求: status=%d model=%q body=%s; want 200 + X-Model=gpt-4",
			rec1.Code, rec1.Header().Get("X-Model"), rec1.Body.String())
	}

	// 直接改存储:按管理后台 UpdateModelConfig 的取改存路径翻转 Enabled;
	// 存储层本身无缓存,不存在任何失效动作
	cfg, err := storage.GetModelConfigByID("m1")
	if err != nil || cfg == nil {
		t.Fatalf("GetModelConfigByID(m1): cfg=%v err=%v", cfg, err)
	}
	cfg.Enabled = false
	cfg.UpdatedAt = time.Now()
	if err := storage.SaveModelConfig(cfg); err != nil {
		t.Fatalf("SaveModelConfig: %v", err)
	}

	// 第二次请求:同一中间件链、同一 model 名,配置变更已生效 → 404 model_not_found
	rec2 := do()
	if rec2.Code != http.StatusNotFound || !strings.Contains(rec2.Body.String(), "model_not_found") {
		t.Fatalf("第二次请求: status=%d body=%s; want 404 model_not_found", rec2.Code, rec2.Body.String())
	}
}
