package core

import (
	"net/http"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// Middleware 管道中间件（照设计文档 2.2）
type Middleware func(next http.Handler) http.Handler

// Pipeline 管道中间件层：按固定顺序执行预处理链路
type Pipeline struct {
	storage     plugin.StoragePlugin
	rateLimiter plugin.RateLimitPlugin
	auditor     plugin.AuditPipeline
	middlewares []Middleware
}

// NewPipeline 创建管道
func NewPipeline(storage plugin.StoragePlugin, rateLimiter plugin.RateLimitPlugin, auditor plugin.AuditPipeline) *Pipeline {
	return &Pipeline{
		storage:     storage,
		rateLimiter: rateLimiter,
		auditor:     auditor,
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

// fixedChain 固定顺序中间件链（照设计文档 2.2，不可调换）：
// 鉴权 → 限流 → 路由匹配（协议转换与前置钩子 Phase 4 接入）
func (p *Pipeline) fixedChain() []Middleware {
	return []Middleware{
		AuthMiddleware(p.storage),
		RateLimitMiddleware(p.rateLimiter),
		RouteMatchMiddleware(p.storage),
	}
}

// Build 将固定链 + 自定义中间件应用到 handler（路由入口）
func (p *Pipeline) Build(handler http.Handler) http.Handler {
	return p.Apply(handler)
}
