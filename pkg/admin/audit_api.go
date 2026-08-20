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
	"encoding/csv"
	"net/http"
	"strconv"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/core"
	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/gin-gonic/gin"
)

// queryAuditLogs GET /api/audit-logs:分页查询(过滤参数见 AuditLogFilter)
func (s *AdminServer) queryAuditLogs(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "10"))
	filter := plugin.AuditLogFilter{
		TenantID:  c.Query("tenant_id"),
		APIKeyID:  c.Query("api_key_id"),
		ModelName: c.Query("model_name"),
		RequestID: c.Query("request_id"),
		Status:    parseInt(c.Query("response_status")),
		Keyword:   c.Query("keyword"),
	}
	if v := c.Query("start_time"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			filter.StartTime = &t
		}
	}
	if v := c.Query("end_time"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			filter.EndTime = &t
		}
	}
	if v := c.Query("is_stream"); v == "true" {
		t := true
		filter.IsStream = &t
	} else if v == "false" {
		f := false
		filter.IsStream = &f
	}
	logs, total, err := s.storage.QueryAuditLogs(filter, page, size)
	if err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to query audit logs")
		return
	}
	OK(c, gin.H{"items": logs, "total": total, "page": page, "size": size})
}

// getAuditLog GET /api/audit-logs/:id:详情(按 RequestID 查询,含分片重组)
func (s *AdminServer) getAuditLog(c *gin.Context) {
	id := c.Param("id")
	logs, total, err := s.storage.QueryAuditLogs(plugin.AuditLogFilter{RequestID: id}, 1, 1)
	if err != nil || total == 0 {
		Error(c, http.StatusNotFound, 404, "audit log not found")
		return
	}
	log := logs[0]
	resp := gin.H{
		"id": log.ID, "request_id": log.RequestID, "tenant_id": log.TenantID,
		"model_name": log.ModelName, "provider": log.Provider,
		"request_body": log.RequestBody, "response_body": log.ResponseBody,
		"response_status": log.ResponseStatus, "sse_chunks": log.SSEChunks,
		"prompt_tokens": log.PromptTokens, "completion_tokens": log.CompletionTokens,
		"total_tokens": log.TotalTokens, "duration_ms": log.Duration,
		"is_stream": log.IsStream, "disconnected": log.Disconnected,
		"disconnect_reason": log.DisconnectReason, "created_at": log.CreatedAt,
	}
	if log.IsStream {
		reassembled, _ := core.NewStreamReassembler().Reassemble(log.SSEChunks)
		resp["reassembled"] = reassembled
	}
	OK(c, resp)
}

// exportAuditLogs GET /api/audit-logs/export?format=csv|json:全量导出(翻页拉取,不受单页 100 条上限截断)
func (s *AdminServer) exportAuditLogs(c *gin.Context) {
	format := c.DefaultQuery("format", "json")
	filter := plugin.AuditLogFilter{Keyword: c.Query("keyword")}
	var all []*plugin.AuditLog
	page := 1
	const pageSize = 100
	for {
		logs, total, err := s.storage.QueryAuditLogs(filter, page, pageSize)
		if err != nil {
			Error(c, http.StatusInternalServerError, 500, "failed to export audit logs")
			return
		}
		all = append(all, logs...)
		if int64(page*pageSize) >= total {
			break
		}
		page++
	}
	if format == "csv" {
		c.Header("Content-Type", "text/csv")
		c.Header("Content-Disposition", "attachment; filename=audit-logs.csv")
		cw := csv.NewWriter(c.Writer)
		_ = cw.Write([]string{"id", "request_id", "tenant_id", "model_name", "response_status", "total_tokens", "duration_ms", "is_stream", "created_at"})
		for _, l := range all {
			_ = cw.Write([]string{
				l.ID, l.RequestID, l.TenantID, l.ModelName,
				strconv.Itoa(l.ResponseStatus), strconv.Itoa(l.TotalTokens),
				strconv.FormatInt(l.Duration, 10), strconv.FormatBool(l.IsStream),
				l.CreatedAt.Format(time.RFC3339),
			})
		}
		cw.Flush()
		return
	}
	OK(c, gin.H{"items": all})
}

func parseInt(v string) int {
	n, _ := strconv.Atoi(v)
	return n
}
