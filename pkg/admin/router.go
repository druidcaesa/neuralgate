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

	"github.com/gin-gonic/gin"
)

// registerRoutes 注册路由：/api/auth/login 免认证，其余 /api 全部要求管理会话
func (s *AdminServer) registerRoutes(r *gin.Engine) {
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	api := r.Group("/api")
	{
		api.POST("/auth/login", s.handleLogin)
	}

	authz := api.Group("")
	authz.Use(s.RequireAuth())
	{
		authz.GET("/ping", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"message": "pong"})
		})

		// 修改自身密码（需登录态）
		authz.PUT("/auth/password", s.handleChangePassword)

		// API Key 管理
		authz.POST("/api-keys", s.createAPIKey)
		authz.GET("/api-keys", s.listAPIKeys)
		authz.PATCH("/api-keys/:id", s.updateAPIKey)
		authz.DELETE("/api-keys/:id", s.deleteAPIKey)

		// 模型配置
		authz.POST("/models", s.createModelConfig)
		authz.GET("/models", s.listModelConfigs)
		authz.PUT("/models/:id", s.updateModelConfig)
		authz.DELETE("/models/:id", s.deleteModelConfig)
		authz.POST("/models/:id/test", s.testModelConfig)

		// 上游管理(负载均衡)
		authz.POST("/models/:id/upstreams", s.createUpstream)
		authz.GET("/models/:id/upstreams", s.listUpstreams)
		authz.PUT("/upstreams/:uid", s.updateUpstream)
		authz.DELETE("/upstreams/:uid", s.deleteUpstream)

		// 审计日志
		authz.GET("/audit-logs", s.queryAuditLogs)
		authz.GET("/audit-logs/export", s.exportAuditLogs)
		authz.GET("/audit-logs/:id", s.getAuditLog)

		// 系统信息
		authz.GET("/system", s.systemInfo)

		// 授权信息
		authz.GET("/license", s.licenseInfo)

		// 篡改告警
		authz.GET("/tamper-alerts", s.listTamperAlerts)
		authz.PATCH("/tamper-alerts/:id", s.resolveTamperAlert)

		// 限流配置管理
		authz.POST("/rate-limits", s.createRateLimit)
		authz.GET("/rate-limits", s.listRateLimits)
		authz.PUT("/rate-limits/:id", s.updateRateLimit)
		authz.DELETE("/rate-limits/:id", s.deleteRateLimit)
	}

	// 静态资源 + SPA fallback(go:embed)，页面公开加载，数据由 /api 认证保护
	s.registerWebUI(r)
}
