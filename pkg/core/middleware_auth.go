package core

import (
	"net/http"
	"strings"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/google/uuid"
)

// AuthMiddleware 鉴权中间件（骨架期：提取 Bearer API Key 写入 RequestContext 后放行；
// Phase 4 调用 storage.GetAPIKey 校验并返回 401）
func AuthMiddleware(storage plugin.StoragePlugin) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rc := &RequestContext{
				RequestID:      uuid.NewString(),
				StartTime:      time.Now(),
				ClientIP:       r.RemoteAddr,
				RequestMethod:  r.Method,
				RequestPath:    r.URL.Path,
				RequestHeaders: headerMap(r.Header),
			}
			if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
				rc.APIKeyID = strings.TrimPrefix(auth, "Bearer ")
			}
			ctx := WithRequestContext(r.Context(), rc)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// headerMap http.Header 转换为 map[string]string（取每个头的第一个值）
func headerMap(h http.Header) map[string]string {
	m := make(map[string]string, len(h))
	for k, v := range h {
		if len(v) > 0 {
			m[k] = v[0]
		}
	}
	return m
}
