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
	"encoding/json"
	"io"
	"net/http"
	"slices"

	"github.com/druidcaesa/neuralgate/pkg/adapter"
	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// RouteMatchMiddleware 路由匹配中间件:解析 model 字段 → 查模型配置 → 校验权限 → 写 RequestContext
func RouteMatchMiddleware(storage plugin.StoragePlugin, registry *adapter.AdapterRegistry) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rc, ok := RequestContextFrom(r.Context())
			if !ok {
				writeOpenAIError(w, http.StatusInternalServerError, "api_error", "internal_error", "internal error")
				return
			}

			// GET 请求(如 /v1/models)无 body,直接放行,不做模型解析
			if r.Method == http.MethodGet {
				next.ServeHTTP(w, r)
				return
			}

			// 透传端点(/v1/completions、/v1/moderations、/v1/audio/*、/v1/images/*、/v1/files*)
			// body 不含 model 路由语义(原样转发),跳过解析;端点/方法校验由代理内核负责
			if _, ok := matchPassthrough(r.URL.Path); ok {
				next.ServeHTTP(w, r)
				return
			}

			// 读取请求体(上限 1MB),缓存后恢复;超限显式 413(避免静默截断丢数据)
			body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err != nil {
				writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "bad_request", "failed to read request body")
				return
			}
			if len(body) >= 1<<20 {
				extra, _ := io.ReadAll(io.LimitReader(r.Body, 1))
				if len(extra) > 0 {
					writeOpenAIError(w, http.StatusRequestEntityTooLarge, "invalid_request_error", "request_too_large", "request body exceeds 1MB limit")
					return
				}
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			rc.RequestBody = body

			// 解析 model 字段(chat/completions 与 embeddings 都含 model)
			var reqBody struct {
				Model string `json:"model"`
			}
			if err := json.Unmarshal(body, &reqBody); err != nil {
				writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "bad_request", "invalid JSON body")
				return
			}
			if reqBody.Model == "" {
				writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "bad_request", "model field is required")
				return
			}

			// 查模型配置
			config, err := storage.GetModelConfig(reqBody.Model)
			if err != nil || config == nil || !config.Enabled {
				writeOpenAIError(w, http.StatusNotFound, "invalid_request_error", "model_not_found", "model not found: "+reqBody.Model)
				return
			}
			rc.ModelConfig = config

			// 加载负载均衡上游(失败或为空则运行时回退 ModelConfig 默认上游)
			if ups, err := storage.ListUpstreams(config.ID); err == nil {
				loaded := make([]plugin.Upstream, 0, len(ups))
				for _, u := range ups {
					loaded = append(loaded, *u)
				}
				rc.Upstreams = loaded
			}

			// Key 模型权限校验(allowed_models 非空且不含 → 403)
			if key, err := storage.GetAPIKeyByID(rc.APIKeyID); err == nil && len(key.AllowedModels) > 0 {
				allowed := slices.Contains(key.AllowedModels, config.ModelName)
				if !allowed {
					writeOpenAIError(w, http.StatusForbidden, "invalid_request_error", "model_access_denied", "model not allowed for this API key")
					return
				}
			}

			// 获取适配器
			adpt, err := registry.Get(config.Provider)
			if err != nil {
				writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "unsupported_model", "unsupported provider: "+config.Provider)
				return
			}
			rc.Adapter = adpt

			next.ServeHTTP(w, r.WithContext(WithRequestContext(r.Context(), rc)))
		})
	}
}
