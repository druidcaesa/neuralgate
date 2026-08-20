package core

import (
	"net/http"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// RateLimitMiddleware 限流中间件（骨架期直接放行；
// Phase 4 调用 rateLimiter.Allow 并返回 429 + OpenAI 限流 Header）
func RateLimitMiddleware(rateLimiter plugin.RateLimitPlugin) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}
