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

	"github.com/druidcaesa/neuralgate/pkg/adapter"
)

// ProxyCore 代理内核层：当前所有 /v1/* 请求返回 OpenAI 格式占位错误（503 service_unavailable）
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

// proxyHandler 代理处理入口（当前返回占位错误）
func (p *ProxyCore) proxyHandler(w http.ResponseWriter, r *http.Request) {
	// 健康检查路由：/healthz 不经过模型代理链路，直接返回 200
	if r.URL.Path == "/healthz" {
		writeHealthz(w)
		return
	}
	writeOpenAIError(w, http.StatusServiceUnavailable, "api_error", "service_unavailable", "service not initialized")
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
