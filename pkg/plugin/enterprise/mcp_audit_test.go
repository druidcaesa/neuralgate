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
	"strings"
	"testing"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
	"go.uber.org/zap"
)

// TestMCPAuditRecorderSuccess 成功调用：落审计、不产生安全事件
func TestMCPAuditRecorderSuccess(t *testing.T) {
	storage := oss.NewMemStorage()
	r := NewMCPAuditRecorder(storage, zap.NewNop())
	entry := &plugin.MCPAuditLog{
		RequestID: "req-1", ToolName: "search", ToolArguments: `{"q":"go"}`,
		ToolResult: `{"hits":1}`, Status: plugin.MCPStatusSuccess,
		CallerAgent: "agent-x",
	}
	r.OnToolCall(entry)
	got, total, _ := storage.ListMCPAuditLogs(plugin.MCPAuditLogFilter{}, 1, 10)
	if total != 1 || got[0].ToolName != "search" {
		t.Fatalf("审计应落库: total=%d", total)
	}
	events, etotal, _ := storage.ListSecurityEvents(1, 10)
	if etotal != 0 || len(events) != 0 {
		t.Errorf("成功调用不应产生安全事件: %+v", events)
	}
}

// TestMCPAuditRecorderFailed 失败调用：落审计并产生 mcp_call_failed 安全事件
func TestMCPAuditRecorderFailed(t *testing.T) {
	storage := oss.NewMemStorage()
	r := NewMCPAuditRecorder(storage, zap.NewNop())
	longErr := strings.Repeat("x", 500)
	r.OnToolCall(&plugin.MCPAuditLog{
		RequestID: "req-2", ToolName: "boom", Status: plugin.MCPStatusFailed,
		ErrorMessage: longErr, ClientIP: "10.1.1.1",
	})
	if _, total, _ := storage.ListMCPAuditLogs(plugin.MCPAuditLogFilter{}, 1, 10); total != 1 {
		t.Fatal("失败调用也应落审计")
	}
	events, _, _ := storage.ListSecurityEvents(1, 10)
	if len(events) != 1 {
		t.Fatalf("应产生一条安全事件, got %d", len(events))
	}
	ev := events[0]
	if ev.RuleName != "mcp_call_failed" || ev.ModelName != "boom" ||
		ev.ClientIP != "10.1.1.1" || ev.RequestID != "req-2" {
		t.Errorf("安全事件字段不符: %+v", ev)
	}
	if len(ev.Snippet) != 256 {
		t.Errorf("Snippet 应截断为 256, got %d", len(ev.Snippet))
	}
}

// TestMCPAuditRecorderTruncates 超限参数/结果截断并标注
func TestMCPAuditRecorderTruncates(t *testing.T) {
	storage := oss.NewMemStorage()
	r := NewMCPAuditRecorder(storage, zap.NewNop())
	big := strings.Repeat("a", mcpAuditTextCap+100)
	r.OnToolCall(&plugin.MCPAuditLog{
		ToolName: "t", ToolArguments: big, ToolResult: big,
		Status: plugin.MCPStatusSuccess,
	})
	got, _, _ := storage.ListMCPAuditLogs(plugin.MCPAuditLogFilter{}, 1, 10)
	wantLen := mcpAuditTextCap + len("[truncated]")
	if len(got[0].ToolArguments) != wantLen || !strings.HasSuffix(got[0].ToolResult, "[truncated]") {
		t.Errorf("超限文本应截断标注: args=%d result=%d", len(got[0].ToolArguments), len(got[0].ToolResult))
	}
}
