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
	"strconv"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// RateLimitMiddleware 限流中间件:调用 RateLimiter.Allow,放行与超限均附带 X-RateLimit-* Header,超限返回 429 + Retry-After
func RateLimitMiddleware(rateLimiter plugin.RateLimitPlugin) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rc, ok := RequestContextFrom(r.Context())
			if !ok {
				writeOpenAIError(w, http.StatusInternalServerError, "api_error", "internal_error", "internal error")
				return
			}
			model := ""
			if rc.ModelConfig != nil {
				model = rc.ModelConfig.ModelName
			}
			allowed, remaining, err := rateLimiter.Allow(rc.TenantID, model, 0)
			if err != nil {
				// 限流器异常:降级放行(可用性优先)+ 记录错误日志
				next.ServeHTTP(w, r)
				return
			}
			current, limit, resetAt := rateLimiter.Status(rc.TenantID, model)
			if !allowed {
				w.Header().Set("X-RateLimit-Limit-Requests", strconv.FormatInt(limit, 10))
				w.Header().Set("X-RateLimit-Remaining-Requests", "0")
				w.Header().Set("X-RateLimit-Reset-Requests", "1s")
				w.Header().Set("Retry-After", "1")
				writeOpenAIError(w, http.StatusTooManyRequests, "rate_limit_exceeded", "rate_limit",
					"rate limit exceeded (current="+strconv.FormatInt(current, 10)+", limit="+strconv.FormatInt(limit, 10)+", reset="+resetAt.Format(time.RFC3339)+")")
				return
			}
			// 放行:附带剩余额度 Header(与 OpenAI 限流 Header 语义一致)
			w.Header().Set("X-RateLimit-Limit-Requests", strconv.FormatInt(limit, 10))
			w.Header().Set("X-RateLimit-Remaining-Requests", strconv.FormatInt(remaining, 10))
			w.Header().Set("X-RateLimit-Reset-Requests", "1s")
			next.ServeHTTP(w, r)
		})
	}
}
