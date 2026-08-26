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

//go:build enterprise

package enterprise

import (
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"go.uber.org/zap"
)

// mcpAuditTextCap 单字段审计留存上限：超限截断并标注，防大参数/大结果撑爆存储
const mcpAuditTextCap = 1 << 20

// mcpSnippetCap 安全事件摘要长度（SecurityEvent.Snippet 约定 256 字符）
const mcpSnippetCap = 256

// MCPAuditRecorder 工具调用审计落地器：mcp_audit_logs 全量留存，
// 失败调用追加 mcp_call_failed 安全事件（复用 E4 事件模型与 webui 告警面）。
// 契约：不 panic、不阻塞——存储失败仅告警，转发路径不受影响
type MCPAuditRecorder struct {
	storage plugin.StoragePlugin
	logger  *zap.Logger
}

// NewMCPAuditRecorder 创建落地器
func NewMCPAuditRecorder(storage plugin.StoragePlugin, logger *zap.Logger) *MCPAuditRecorder {
	return &MCPAuditRecorder{storage: storage, logger: logger}
}

// OnToolCall 落库单次工具调用；failed 时追加安全事件
func (r *MCPAuditRecorder) OnToolCall(entry *plugin.MCPAuditLog) {
	entry.ToolArguments = truncateMCPText(entry.ToolArguments)
	entry.ToolResult = truncateMCPText(entry.ToolResult)
	if err := r.storage.SaveMCPAuditLog(entry); err != nil {
		r.logger.Warn("MCP 调用审计落库失败", zap.String("tool", entry.ToolName), zap.Error(err))
		return
	}
	if entry.Status != plugin.MCPStatusFailed {
		return
	}
	ev := &plugin.SecurityEvent{
		RequestID: entry.RequestID,
		RuleName:  "mcp_call_failed",
		Snippet:   truncateMCPSnippet(entry.ErrorMessage),
		ModelName: entry.ToolName,
		ClientIP:  entry.ClientIP,
		CreatedAt: time.Now(),
	}
	if err := r.storage.SaveSecurityEvent(ev); err != nil {
		r.logger.Warn("MCP 失败调用安全事件落库失败", zap.String("tool", entry.ToolName), zap.Error(err))
	}
}

func truncateMCPText(s string) string {
	if len(s) <= mcpAuditTextCap {
		return s
	}
	return s[:mcpAuditTextCap] + "[truncated]"
}

func truncateMCPSnippet(s string) string {
	if len(s) <= mcpSnippetCap {
		return s
	}
	return s[:mcpSnippetCap]
}
