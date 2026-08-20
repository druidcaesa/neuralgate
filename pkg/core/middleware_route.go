package core

import (
	"net/http"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// RouteMatchMiddleware 路由匹配中间件（骨架期直接放行；
// Phase 4 解析请求体 model 字段 → storage.GetModelConfig → 写入 ctx.ModelConfig，
// 未匹配返回 404）
func RouteMatchMiddleware(storage plugin.StoragePlugin) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}
