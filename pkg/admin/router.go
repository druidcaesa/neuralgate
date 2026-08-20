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

// registerRoutes 注册路由
func (s *AdminServer) registerRoutes(r *gin.Engine) {
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	api := r.Group("/api")
	{
		api.GET("/ping", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"message": "pong"})
		})

		// API Key 管理
		api.POST("/api-keys", s.createAPIKey)
		api.GET("/api-keys", s.listAPIKeys)
		api.PATCH("/api-keys/:id", s.updateAPIKey)
		api.DELETE("/api-keys/:id", s.deleteAPIKey)

		// 模型配置
		api.POST("/models", s.createModelConfig)
		api.GET("/models", s.listModelConfigs)
		api.PUT("/models/:id", s.updateModelConfig)
		api.DELETE("/models/:id", s.deleteModelConfig)
		api.POST("/models/:id/test", s.testModelConfig)

		// 审计日志
		api.GET("/audit-logs", s.queryAuditLogs)
		api.GET("/audit-logs/export", s.exportAuditLogs)
		api.GET("/audit-logs/:id", s.getAuditLog)

		// 系统信息
		api.GET("/system", s.systemInfo)
	}
}
