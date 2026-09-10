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

package admin

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	// ctxRequestID 请求 id 在 gin.Context 中的键
	ctxRequestID = "ng_admin_request_id"
	// requestIDHeader 回传请求 id 的响应头,供调用方按 id 检索日志
	requestIDHeader = "X-Request-Id"
)

// AccessLog 管理后台访问日志中间件:仅记录 4xx/5xx(2xx 静默,避免后台轮询刷屏),
// 5xx 记为 error、4xx 记为 warn,级别即过滤开关(配置 log.level 设为 warn 只剩 4xx/5xx,
// 设为 error 只剩 5xx)。
//
// 必须挂在内层中间件(尤其 Recovery)之外:c.Next() 返回时才有终态状态码;
// 若挂在内层,handler panic 会直接穿过本中间件,拿不到 panic 转换后的 500。
//
// logger 为 nil 时只下发 request_id 不记录,保持既有以 nil 构造的调用方可用。
func (s *AdminServer) AccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 请求 id 一律服务端生成:入站 X-Request-Id 可被伪造,且是日志注入面
		rid := uuid.NewString()
		c.Set(ctxRequestID, rid)
		c.Header(requestIDHeader, rid)

		if s.logger == nil {
			c.Next()
			return
		}

		start := time.Now()
		c.Next()

		status := c.Writer.Status()
		if status < http.StatusBadRequest {
			return
		}

		fields := []zap.Field{
			zap.String("method", c.Request.Method),
			// 只取路径:query 可能含 token 等密钥
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", status),
			zap.Int64("duration_ms", time.Since(start).Milliseconds()),
			zap.String("request_id", rid),
			zap.String("client_ip", c.ClientIP()),
		}
		if v, ok := c.Get(ctxBizCode); ok {
			if code, ok := v.(int); ok {
				fields = append(fields, zap.Int("biz_code", code))
			}
		}
		if cause := lastCause(c); cause != "" {
			fields = append(fields, zap.String("err", cause))
		}

		if status >= http.StatusInternalServerError {
			s.logger.Error("管理后台请求失败", fields...)
			return
		}
		s.logger.Warn("管理后台请求失败", fields...)
	}
}

// lastCause 取 c.Errors 中最后一条私有错误(即 ErrorCause 传入的底层原因),
// 无则返回空串。未经 Error()/ErrorCause() 的 4xx(如 API 404)与 Recovery 直接
// 中止的 panic 请求没有私有错误,按无原因记录
func lastCause(c *gin.Context) string {
	for i := len(c.Errors) - 1; i >= 0; i-- {
		if c.Errors[i].IsType(gin.ErrorTypePrivate) {
			return c.Errors[i].Err.Error()
		}
	}
	return ""
}
