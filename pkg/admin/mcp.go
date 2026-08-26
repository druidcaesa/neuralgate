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
	"net/url"
	"strconv"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/gin-gonic/gin"
)

// mcpServerRequest MCP 上游配置创建/更新请求体
type mcpServerRequest struct {
	Name     string            `json:"name" binding:"required,max=128"`
	Endpoint string            `json:"endpoint" binding:"required"`
	Headers  map[string]string `json:"headers"`
	Enabled  *bool             `json:"enabled"`
}

// validateMCPServerRequest 端点须为 http(s) URL；返回规范化后的错误消息（空串=通过）
func validateMCPServerRequest(req mcpServerRequest) string {
	u, err := url.Parse(req.Endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "endpoint 必须为合法的 http(s) 地址"
	}
	return ""
}

// listMCPServers GET /api/mcp-servers：分页 name 升序
func (s *AdminServer) listMCPServers(c *gin.Context) {
	if s.globalOnlyGuard(c) {
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	items, total, err := s.storage.ListMCPServers(page, size)
	if err != nil {
		Error(c, http.StatusInternalServerError, 500, err.Error())
		return
	}
	OK(c, gin.H{"items": items, "total": total, "page": page, "size": size})
}

// createMCPServer POST /api/mcp-servers：name 唯一 + 端点校验
func (s *AdminServer) createMCPServer(c *gin.Context) {
	if s.globalOnlyGuard(c) {
		return
	}
	var req mcpServerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	if msg := validateMCPServerRequest(req); msg != "" {
		Error(c, http.StatusBadRequest, 400, msg)
		return
	}
	existing, _, _ := s.storage.ListMCPServers(1, 1<<20)
	for _, srv := range existing {
		if srv.Name == req.Name {
			Error(c, http.StatusConflict, 409, "MCP 上游名称已存在")
			return
		}
	}
	srv := &plugin.MCPServer{
		Name: req.Name, Endpoint: req.Endpoint, Headers: req.Headers,
		Enabled: req.Enabled == nil || *req.Enabled,
	}
	if err := s.storage.SaveMCPServer(srv); err != nil {
		Error(c, http.StatusInternalServerError, 500, err.Error())
		return
	}
	OK(c, gin.H{"id": srv.ID})
}

// updateMCPServer PUT /api/mcp-servers/:id：存在性校验 + 名称冲突排除自身
func (s *AdminServer) updateMCPServer(c *gin.Context) {
	if s.globalOnlyGuard(c) {
		return
	}
	id := c.Param("id")
	srv, err := s.storage.GetMCPServer(id)
	if err != nil {
		Error(c, http.StatusNotFound, 404, "mcp server not found")
		return
	}
	var req mcpServerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	if msg := validateMCPServerRequest(req); msg != "" {
		Error(c, http.StatusBadRequest, 400, msg)
		return
	}
	others, _, _ := s.storage.ListMCPServers(1, 1<<20)
	for _, other := range others {
		if other.ID != id && other.Name == req.Name {
			Error(c, http.StatusConflict, 409, "MCP 上游名称已存在")
			return
		}
	}
	srv.Name = req.Name
	srv.Endpoint = req.Endpoint
	srv.Headers = req.Headers
	if req.Enabled != nil {
		srv.Enabled = *req.Enabled
	}
	if err := s.storage.SaveMCPServer(srv); err != nil {
		Error(c, http.StatusInternalServerError, 500, err.Error())
		return
	}
	OK(c, gin.H{"id": id})
}

// deleteMCPServer DELETE /api/mcp-servers/:id
func (s *AdminServer) deleteMCPServer(c *gin.Context) {
	if s.globalOnlyGuard(c) {
		return
	}
	if err := s.storage.DeleteMCPServer(c.Param("id")); err != nil {
		Error(c, http.StatusNotFound, 404, "mcp server not found")
		return
	}
	OK(c, gin.H{"id": c.Param("id"), "deleted": true})
}

// listMCPAuditLogs GET /api/mcp-audit-logs：多维筛选+分页倒序。
// 时间参数兼容 RFC3339 与纯日期两种写法，解析失败静默忽略（内部既有风格）
func (s *AdminServer) listMCPAuditLogs(c *gin.Context) {
	if s.globalOnlyGuard(c) {
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	filter := plugin.MCPAuditLogFilter{
		TenantID:  c.Query("tenant_id"),
		RequestID: c.Query("request_id"),
		ToolName:  c.Query("tool"),
		Status:    c.Query("status"),
	}
	parseTimeParam := func(raw string) *time.Time {
		for _, layout := range []string{time.RFC3339, "2006-01-02"} {
			if ts, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
				return &ts
			}
		}
		return nil
	}
	if v := c.Query("start"); v != "" {
		filter.StartTime = parseTimeParam(v)
	}
	if v := c.Query("end"); v != "" {
		filter.EndTime = parseTimeParam(v)
	}
	items, total, err := s.storage.ListMCPAuditLogs(filter, page, size)
	if err != nil {
		Error(c, http.StatusInternalServerError, 500, err.Error())
		return
	}
	OK(c, gin.H{"items": items, "total": total, "page": page, "size": size})
}

// getMCPAuditLog GET /api/mcp-audit-logs/:id：单条详情
func (s *AdminServer) getMCPAuditLog(c *gin.Context) {
	if s.globalOnlyGuard(c) {
		return
	}
	entry, err := s.storage.GetMCPAuditLog(c.Param("id"))
	if err != nil {
		Error(c, http.StatusNotFound, 404, "mcp audit log not found")
		return
	}
	OK(c, entry)
}
