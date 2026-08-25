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

	"github.com/druidcaesa/neuralgate/pkg/plugin"
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
	authz.Use(s.OperationAudit())
	{
		authz.GET("/ping", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"message": "pong"})
		})

		// 修改自身密码（需登录态）
		authz.PUT("/auth/password", s.handleChangePassword)

		// API Key 管理（RBAC 启用后按权限码守卫，未启用恒放行）
		authz.POST("/api-keys", s.RequirePermission(plugin.PermAPIKeyWrite), s.createAPIKey)
		authz.GET("/api-keys", s.RequirePermission(plugin.PermAPIKeyRead), s.listAPIKeys)
		authz.PATCH("/api-keys/:id", s.RequirePermission(plugin.PermAPIKeyWrite), s.updateAPIKey)
		authz.DELETE("/api-keys/:id", s.RequirePermission(plugin.PermAPIKeyWrite), s.deleteAPIKey)

		// 模型配置
		authz.POST("/models", s.RequirePermission(plugin.PermModelWrite), s.createModelConfig)
		authz.GET("/models", s.RequirePermission(plugin.PermModelRead), s.listModelConfigs)
		authz.PUT("/models/:id", s.RequirePermission(plugin.PermModelWrite), s.updateModelConfig)
		authz.DELETE("/models/:id", s.RequirePermission(plugin.PermModelWrite), s.deleteModelConfig)
		authz.POST("/models/:id/test", s.RequirePermission(plugin.PermModelWrite), s.testModelConfig)

		// 上游管理(负载均衡)
		authz.POST("/models/:id/upstreams", s.RequirePermission(plugin.PermModelWrite), s.createUpstream)
		authz.GET("/models/:id/upstreams", s.RequirePermission(plugin.PermModelRead), s.listUpstreams)
		authz.PUT("/upstreams/:uid", s.RequirePermission(plugin.PermModelWrite), s.updateUpstream)
		authz.DELETE("/upstreams/:uid", s.RequirePermission(plugin.PermModelWrite), s.deleteUpstream)

		// 审计日志
		authz.GET("/audit-logs", s.RequirePermission(plugin.PermAuditRead), s.queryAuditLogs)
		authz.GET("/audit-logs/export", s.RequirePermission(plugin.PermAuditExport), s.exportAuditLogs)
		authz.GET("/audit-logs/:id", s.RequirePermission(plugin.PermAuditRead), s.getAuditLog)

		// 系统信息
		authz.GET("/system", s.RequirePermission(plugin.PermSystemRead), s.systemInfo)

		// 授权信息
		authz.GET("/license", s.RequirePermission(plugin.PermSystemRead), s.licenseInfo)

		// 篡改告警（处置属运维动作归 system:write）
		authz.GET("/tamper-alerts", s.RequirePermission(plugin.PermAuditRead), s.listTamperAlerts)
		authz.PATCH("/tamper-alerts/:id", s.RequirePermission(plugin.PermSystemWrite), s.resolveTamperAlert)

		// 限流配置管理
		authz.POST("/rate-limits", s.RequirePermission(plugin.PermRateLimitWrite), s.createRateLimit)
		authz.GET("/rate-limits", s.RequirePermission(plugin.PermRateLimitRead), s.listRateLimits)
		authz.PUT("/rate-limits/:id", s.RequirePermission(plugin.PermRateLimitWrite), s.updateRateLimit)
		authz.DELETE("/rate-limits/:id", s.RequirePermission(plugin.PermRateLimitWrite), s.deleteRateLimit)

		// 隐私合规(E4)：规则库/白名单/安全事件
		authz.POST("/privacy-rules", s.RequirePermission(plugin.PermPrivacyWrite), s.createPrivacyRule)
		authz.GET("/privacy-rules", s.RequirePermission(plugin.PermPrivacyRead), s.listPrivacyRules)
		authz.PUT("/privacy-rules/:id", s.RequirePermission(plugin.PermPrivacyWrite), s.updatePrivacyRule)
		authz.DELETE("/privacy-rules/:id", s.RequirePermission(plugin.PermPrivacyWrite), s.deletePrivacyRule)
		authz.POST("/privacy-whitelist", s.RequirePermission(plugin.PermPrivacyWrite), s.createPrivacyWhitelistEntry)
		authz.GET("/privacy-whitelist", s.RequirePermission(plugin.PermPrivacyRead), s.listPrivacyWhitelistEntries)
		authz.DELETE("/privacy-whitelist/:id", s.RequirePermission(plugin.PermPrivacyWrite), s.deletePrivacyWhitelistEntry)
		authz.GET("/security-events", s.RequirePermission(plugin.PermPrivacyRead), s.listSecurityEvents)

		// RBAC 权限体系(E5)：租户/角色/用户/操作日志（handler 在 Task4 注册）
		s.registerRBACRoutes(authz)
	}

	// 静态资源 + SPA fallback(go:embed)，页面公开加载，数据由 /api 认证保护
	s.registerWebUI(r)
}
