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

	"github.com/druidcaesa/neuralgate/pkg/core"
	"github.com/druidcaesa/neuralgate/pkg/license"
	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/gin-gonic/gin"
)

// registerRoutes 注册路由：/api/auth/login 免认证，其余 /api 全部要求管理会话
func (s *AdminServer) registerRoutes(r *gin.Engine) {
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	// /readyz 就绪探针(依赖感知,免鉴权供 LB 探测):storage 存活 + 非排空 → 200,否则 503
	r.GET("/readyz", func(c *gin.Context) {
		core.HandleReady(c.Writer, c.Request, s.storage)
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
		authz.POST("/api-keys/batch-create", s.RequirePermission(plugin.PermAPIKeyWrite), s.batchCreateAPIKeys)
		authz.POST("/api-keys/batch-delete", s.RequirePermission(plugin.PermAPIKeyWrite), s.batchDeleteAPIKeys)
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

		// 接口说明入口元信息:仅需登录态,无权限码(顶栏按钮对所有登录用户可见)
		authz.GET("/gateway-meta", s.gatewayMeta)

		// 系统信息
		authz.GET("/system", s.RequirePermission(plugin.PermSystemRead), s.systemInfo)

		// 首页概览：系统级只读总览，复用 system:read。此处有意以 system:read 作为
		// 「全局域」口径（与 /system、/license、/operation-logs 一致）：接口一次返回
		// 全平台的请求量/成功率/Token 总量，不做租户过滤，故不套 scopeTenant，
		// 也不套 globalOnlyGuard——租户账号因缺 system:read 本就进不来。
		// 不新增权限码，以免既有部署的超管角色因权限全集变化而失效
		authz.GET("/dashboard", s.RequirePermission(plugin.PermSystemRead), s.dashboard)

		// 授权信息
		authz.GET("/license", s.RequirePermission(plugin.PermSystemRead), s.licenseInfo)
		authz.POST("/license", s.RequirePermission(plugin.PermSystemWrite), s.uploadLicense)

		// 篡改告警（处置属运维动作归 system:write）
		authz.GET("/tamper-alerts", s.RequireFeature(license.FeatureTamperProof), s.RequirePermission(plugin.PermAuditRead), s.listTamperAlerts)
		authz.PATCH("/tamper-alerts/:id", s.RequireFeature(license.FeatureTamperProof), s.RequirePermission(plugin.PermSystemWrite), s.resolveTamperAlert)

		// 合规报表(E6)：查询/下载/手动补生成（生成器由 enterprise 装配注入，未注入时生成 503）
		authz.GET("/compliance-reports", s.RequireFeature(license.FeatureCompliance), s.RequirePermission(plugin.PermSystemRead), s.listComplianceReports)
		authz.GET("/compliance-reports/:id", s.RequireFeature(license.FeatureCompliance), s.RequirePermission(plugin.PermSystemRead), s.getComplianceReport)
		authz.POST("/compliance-reports/generate", s.RequireFeature(license.FeatureCompliance), s.RequirePermission(plugin.PermSystemWrite), s.generateComplianceReport)

		// MCP 上游管理与工具调用审计(E7)：全局域数据，租户内用户一律 403
		authz.GET("/mcp-servers", s.RequirePermission(plugin.PermSystemRead), s.listMCPServers)
		authz.POST("/mcp-servers", s.RequirePermission(plugin.PermSystemWrite), s.createMCPServer)
		authz.PUT("/mcp-servers/:id", s.RequirePermission(plugin.PermSystemWrite), s.updateMCPServer)
		authz.DELETE("/mcp-servers/:id", s.RequirePermission(plugin.PermSystemWrite), s.deleteMCPServer)
		authz.GET("/mcp-audit-logs", s.RequireFeature(license.FeatureMCPAudit), s.RequirePermission(plugin.PermSystemRead), s.listMCPAuditLogs)
		authz.GET("/mcp-audit-logs/:id", s.RequireFeature(license.FeatureMCPAudit), s.RequirePermission(plugin.PermSystemRead), s.getMCPAuditLog)

		// 限流配置管理
		authz.POST("/rate-limits", s.RequirePermission(plugin.PermRateLimitWrite), s.createRateLimit)
		authz.GET("/rate-limits", s.RequirePermission(plugin.PermRateLimitRead), s.listRateLimits)
		authz.PUT("/rate-limits/:id", s.RequirePermission(plugin.PermRateLimitWrite), s.updateRateLimit)
		authz.DELETE("/rate-limits/:id", s.RequirePermission(plugin.PermRateLimitWrite), s.deleteRateLimit)

		// 隐私合规(E4)：规则库/白名单/安全事件
		authz.POST("/privacy-rules", s.RequireFeature(license.FeaturePrivacy), s.RequirePermission(plugin.PermPrivacyWrite), s.createPrivacyRule)
		authz.GET("/privacy-rules", s.RequireFeature(license.FeaturePrivacy), s.RequirePermission(plugin.PermPrivacyRead), s.listPrivacyRules)
		authz.PUT("/privacy-rules/:id", s.RequireFeature(license.FeaturePrivacy), s.RequirePermission(plugin.PermPrivacyWrite), s.updatePrivacyRule)
		authz.DELETE("/privacy-rules/:id", s.RequireFeature(license.FeaturePrivacy), s.RequirePermission(plugin.PermPrivacyWrite), s.deletePrivacyRule)
		authz.POST("/privacy-whitelist", s.RequireFeature(license.FeaturePrivacy), s.RequirePermission(plugin.PermPrivacyWrite), s.createPrivacyWhitelistEntry)
		authz.GET("/privacy-whitelist", s.RequireFeature(license.FeaturePrivacy), s.RequirePermission(plugin.PermPrivacyRead), s.listPrivacyWhitelistEntries)
		authz.DELETE("/privacy-whitelist/:id", s.RequireFeature(license.FeaturePrivacy), s.RequirePermission(plugin.PermPrivacyWrite), s.deletePrivacyWhitelistEntry)
		authz.GET("/security-events", s.RequireFeature(license.FeaturePrivacy), s.RequirePermission(plugin.PermPrivacyRead), s.listSecurityEvents)

		// RBAC 权限体系(E5)：租户/角色/用户/操作日志（handler 在 Task4 注册）
		s.registerRBACRoutes(authz)
	}

	// 静态资源 + SPA fallback(go:embed)，页面公开加载，数据由 /api 认证保护
	s.registerWebUI(r)
}
