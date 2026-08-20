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
	"strings"

	"github.com/druidcaesa/neuralgate/pkg/adapter"
)

// ProxyCore 代理内核层：端点分类 → 本地响应或核心代理转发
type ProxyCore struct {
	pipeline *Pipeline
	registry *adapter.AdapterRegistry
}

// NewProxyCore 创建代理内核
func NewProxyCore(pipeline *Pipeline, registry *adapter.AdapterRegistry) *ProxyCore {
	return &ProxyCore{pipeline: pipeline, registry: registry}
}

// Handler 返回经管道包装的代理入口
func (p *ProxyCore) Handler() http.Handler {
	return p.pipeline.Build(http.HandlerFunc(p.proxyHandler))
}

// proxyHandler 代理处理入口：端点分类
func (p *ProxyCore) proxyHandler(w http.ResponseWriter, r *http.Request) {
	// 健康检查
	if r.URL.Path == "/healthz" {
		writeHealthz(w)
		return
	}
	rc, ok := RequestContextFrom(r.Context())
	if !ok {
		writeOpenAIError(w, http.StatusInternalServerError, "api_error", "internal_error", "internal error")
		return
	}

	switch {
	case r.URL.Path == "/v1/models":
		p.handleModelsList(w, rc)
	case strings.HasPrefix(r.URL.Path, "/v1/models/"):
		p.handleModelDetail(w, r, rc)
	case r.URL.Path == "/v1/chat/completions" || r.URL.Path == "/v1/embeddings":
		p.handleProxy(w, r, rc)
	default:
		// 透传端点（completions/moderations/images/audio/files 等）
		p.handlePassThrough(w, r, rc)
	}
}

// handleModelsList GET /v1/models：返回启用模型列表（本地响应）
func (p *ProxyCore) handleModelsList(w http.ResponseWriter, rc *RequestContext) {
	models, _, err := p.pipeline.storage.ListModelConfigs(1, 1000)
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "api_error", "internal_error", "failed to list models")
		return
	}
	type modelItem struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		OwnedBy string `json:"owned_by"`
	}
	data := make([]modelItem, 0, len(models))
	for _, m := range models {
		if !m.Enabled {
			continue
		}
		data = append(data, modelItem{
			ID:      m.ModelName,
			Object:  "model",
			Created: m.CreatedAt.Unix(),
			OwnedBy: "neuralgate",
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"object": "list", "data": data})
}

// handleModelDetail GET /v1/models/{model}
func (p *ProxyCore) handleModelDetail(w http.ResponseWriter, r *http.Request, rc *RequestContext) {
	name := strings.TrimPrefix(r.URL.Path, "/v1/models/")
	config, err := p.pipeline.storage.GetModelConfig(name)
	if err != nil || !config.Enabled {
		writeOpenAIError(w, http.StatusNotFound, "invalid_request_error", "model_not_found", "model not found: "+name)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"id": config.ModelName, "object": "model",
		"created": config.CreatedAt.Unix(), "owned_by": "neuralgate",
	})
}

// handleProxy 核心代理（chat/completions、embeddings）：当前为占位实现，转发逻辑后续补齐
func (p *ProxyCore) handleProxy(w http.ResponseWriter, r *http.Request, rc *RequestContext) {
	writeOpenAIError(w, http.StatusServiceUnavailable, "api_error", "service_unavailable", "proxy not implemented yet")
}

// handlePassThrough 透传端点：当前为占位实现，转发逻辑后续补齐
func (p *ProxyCore) handlePassThrough(w http.ResponseWriter, r *http.Request, rc *RequestContext) {
	writeOpenAIError(w, http.StatusServiceUnavailable, "api_error", "service_unavailable", "proxy not implemented yet")
}

// writeHealthz 健康检查响应体
func writeHealthz(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// openAIErrorBody OpenAI 错误响应体
type openAIErrorBody struct {
	Error openAIError `json:"error"`
}

type openAIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Param   any    `json:"param"`
	Code    string `json:"code"`
}

// writeOpenAIError 按 OpenAI 错误格式写响应
func writeOpenAIError(w http.ResponseWriter, status int, etype, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(openAIErrorBody{
		Error: openAIError{Message: message, Type: etype, Param: nil, Code: code},
	})
}
