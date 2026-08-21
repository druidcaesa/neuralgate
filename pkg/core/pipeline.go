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
	"net/http"

	"github.com/druidcaesa/neuralgate/pkg/adapter"
	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// Middleware 管道中间件
type Middleware func(next http.Handler) http.Handler

// Pipeline 管道中间件层：按固定顺序执行预处理链路
type Pipeline struct {
	storage     plugin.StoragePlugin
	rateLimiter plugin.RateLimitPlugin
	auditor     plugin.AuditPipeline
	registry    *adapter.AdapterRegistry
	middlewares []Middleware
}

// NewPipeline 创建管道
func NewPipeline(storage plugin.StoragePlugin, rateLimiter plugin.RateLimitPlugin, auditor plugin.AuditPipeline, registry *adapter.AdapterRegistry) *Pipeline {
	return &Pipeline{
		storage:     storage,
		rateLimiter: rateLimiter,
		auditor:     auditor,
		registry:    registry,
	}
}

// Use 追加自定义中间件（在固定链之后执行）
func (p *Pipeline) Use(mw Middleware) {
	p.middlewares = append(p.middlewares, mw)
}

// Apply 将中间件链包装到 handler 上
func (p *Pipeline) Apply(handler http.Handler) http.Handler {
	h := handler
	all := append(append([]Middleware{}, p.fixedChain()...), p.middlewares...)
	// 逆序包装：第一个中间件最先执行
	for i := len(all) - 1; i >= 0; i-- {
		h = all[i](h)
	}
	return h
}

// fixedChain 固定顺序中间件链（不可调换）：鉴权 → 路由匹配 → 限流
// 路由在前使限流可获取模型名（模型级限流），且 404/403 不消耗限流配额
func (p *Pipeline) fixedChain() []Middleware {
	return []Middleware{
		AuthMiddleware(p.storage),
		RouteMatchMiddleware(p.storage, p.registry),
		RateLimitMiddleware(p.rateLimiter),
	}
}

// Build 将固定链 + 自定义中间件应用到 handler（路由入口）
func (p *Pipeline) Build(handler http.Handler) http.Handler {
	return p.Apply(handler)
}
