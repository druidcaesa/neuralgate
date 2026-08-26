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
	"strings"

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
	mcpRelay    http.Handler // MCP 中继(nil=未启用,/v1/mcp 路径走原链零变化)
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

// SetMCPRelay 挂载 MCP 中继；main 装配阶段调用一次，nil 恢复直通
func (p *Pipeline) SetMCPRelay(h http.Handler) { p.mcpRelay = h }

// fixedChain 固定顺序中间件链（不可调换）：鉴权 → MCP 分支 → 路由匹配 → 限流
// MCP 前缀在路由匹配前分流：模型路由与限流的模型维度对 MCP 无意义，
// 中继内部自带 api_key 维度限流与独立错误形状
func (p *Pipeline) fixedChain() []Middleware {
	return []Middleware{
		AuthMiddleware(p.storage),
		p.mcpBranch(),
		RouteMatchMiddleware(p.storage, p.registry),
		RateLimitMiddleware(p.rateLimiter),
	}
}

// mcpBranch /v1/mcp/servers/ 前缀请求短路进中继并施加 api_key 维度限流；
// relay 未挂载时恒放行（OSS 未装配/零行为变化）
func (p *Pipeline) mcpBranch() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if p.mcpRelay != nil && strings.HasPrefix(r.URL.Path, MCPPathPrefix) {
				if rc, ok := RequestContextFrom(r.Context()); ok && rc != nil && p.rateLimiter != nil {
					allowed, _, err := p.rateLimiter.Allow(rc.TenantID, "", 0)
					// 限流器内部异常降级放行(可用性优先,与 chat 链口径一致)；仅真实超限回 429
					if err == nil && !allowed {
						writeOpenAIError(w, http.StatusTooManyRequests, "rate_limit_exceeded", "rate_limit_exceeded", "rate limit exceeded")
						return
					}
				}
				p.mcpRelay.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Build 将固定链 + 自定义中间件应用到 handler（路由入口）
func (p *Pipeline) Build(handler http.Handler) http.Handler {
	return p.Apply(handler)
}
