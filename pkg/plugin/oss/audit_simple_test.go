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

package oss

import (
	"testing"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

func TestFinalizeSavesAuditLog(t *testing.T) {
	storage := NewMemStorage()
	auditor := NewSimpleAuditor(storage)
	if err := auditor.Init(plugin.AuditConfig{}); err != nil {
		t.Fatal(err)
	}
	_ = auditor.Submit(&plugin.AuditEvent{RequestID: "r1", EventType: plugin.AuditEventRequestStart})
	_ = auditor.SubmitSSEChunk("r1", &plugin.SSEChunk{Index: 0, Data: "data: {\"choices\":[]}"})
	_ = auditor.Finalize("r1", &plugin.AuditMeta{
		ResponseStatus:   200,
		PromptTokens:     10,
		CompletionTokens: 5,
		TotalTokens:      15,
		Duration:         120,
	})
	logs, total, err := storage.QueryAuditLogs(plugin.AuditLogFilter{}, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("total = %d, want 1", total)
	}
	l := logs[0]
	if l.RequestID != "r1" || l.ResponseStatus != 200 || l.TotalTokens != 15 {
		t.Errorf("audit log = %+v, want r1/200/15", l)
	}
	if len(l.SSEChunks) != 1 || l.SSEChunks[0].Index != 0 {
		t.Errorf("SSEChunks = %+v, want 1 chunk", l.SSEChunks)
	}
}

func TestMarkDisconnectSavesLog(t *testing.T) {
	storage := NewMemStorage()
	auditor := NewSimpleAuditor(storage)
	_ = auditor.SubmitSSEChunk("r2", &plugin.SSEChunk{Index: 0, Data: "data: hello"})
	_ = auditor.MarkDisconnect("r2", "client_closed_connection", &plugin.AuditMeta{
		ResponseStatus:   200,
		PromptTokens:     10,
		CompletionTokens: 5,
		TotalTokens:      15,
		Duration:         100,
	})
	logs, total, _ := storage.QueryAuditLogs(plugin.AuditLogFilter{}, 1, 10)
	if total != 1 {
		t.Fatalf("total = %d, want 1", total)
	}
	if !logs[0].Disconnected || logs[0].DisconnectReason != "client_closed_connection" {
		t.Errorf("log = %+v, want Disconnected=true", logs[0])
	}
	// 断连记录应补齐 tokens/duration(meta 非 nil 时)
	if logs[0].ResponseStatus != 200 || logs[0].PromptTokens != 10 || logs[0].CompletionTokens != 5 ||
		logs[0].TotalTokens != 15 || logs[0].Duration != 100 {
		t.Errorf("log meta = %+v, want status=200 tokens=10/5/15 duration=100", logs[0])
	}
}

func TestFinalizeAfterMarkDisconnectNoDuplicate(t *testing.T) {
	storage := NewMemStorage()
	auditor := NewSimpleAuditor(storage)
	_ = auditor.Submit(&plugin.AuditEvent{RequestID: "r4", EventType: plugin.AuditEventRequestStart})
	_ = auditor.SubmitSSEChunk("r4", &plugin.SSEChunk{Index: 0, Data: "data: hi"})
	// 断连路径先落库并删除 pending
	if err := auditor.MarkDisconnect("r4", "client_disconnected", nil); err != nil {
		t.Fatal(err)
	}
	// 主循环随后 Finalize:pending 缺失时不得新建空日志(断连竞态双记录修复)
	if err := auditor.Finalize("r4", &plugin.AuditMeta{ResponseStatus: 200}); err != nil {
		t.Fatal(err)
	}
	logs, total, _ := storage.QueryAuditLogs(plugin.AuditLogFilter{}, 1, 10)
	if total != 1 {
		t.Fatalf("total = %d, want 1 (断连竞态不得产生双记录)", total)
	}
	if !logs[0].Disconnected || logs[0].DisconnectReason != "client_disconnected" {
		t.Errorf("log = %+v, want Disconnected=true reason=client_disconnected", logs[0])
	}
}

func TestFinalizeUnknownRequestNoCreate(t *testing.T) {
	storage := NewMemStorage()
	auditor := NewSimpleAuditor(storage)
	if err := auditor.Finalize("unknown", &plugin.AuditMeta{ResponseStatus: 200}); err != nil {
		t.Fatal(err)
	}
	_, total, _ := storage.QueryAuditLogs(plugin.AuditLogFilter{}, 1, 10)
	if total != 0 {
		t.Errorf("total = %d, want 0 (未知请求不得新建日志)", total)
	}
}

func TestShutdownFlushesPending(t *testing.T) {
	storage := NewMemStorage()
	auditor := NewSimpleAuditor(storage)
	_ = auditor.SubmitSSEChunk("r3", &plugin.SSEChunk{Index: 0, Data: "data: hi"})
	if err := auditor.Shutdown(); err != nil {
		t.Fatal(err)
	}
	_, total, _ := storage.QueryAuditLogs(plugin.AuditLogFilter{}, 1, 10)
	if total != 1 {
		t.Errorf("total after Shutdown = %d, want 1", total)
	}
}

// fingerprint 注入后三条落库路径均应填充指纹；未注入则保持空（OSS 行为零变化）
func TestSimpleAuditorFingerprintHook(t *testing.T) {
	newMeta := func() *plugin.AuditMeta {
		return &plugin.AuditMeta{ResponseStatus: 200, TotalTokens: 7}
	}

	t.Run("无钩子指纹为空", func(t *testing.T) {
		s := NewMemStorage()
		a := NewSimpleAuditor(s)
		_ = a.Submit(&plugin.AuditEvent{RequestID: "r1", Data: &plugin.AuditLog{ID: "r1", RequestID: "r1", ModelName: "m"}})
		_ = a.Finalize("r1", newMeta())
		logs, _, _ := s.QueryAuditLogs(plugin.AuditLogFilter{RequestID: "r1"}, 1, 10)
		if logs[0].SHA256Fingerprint != "" {
			t.Fatalf("未注入钩子不应有指纹: %q", logs[0].SHA256Fingerprint)
		}
	})

	t.Run("注入后三路径均带指纹", func(t *testing.T) {
		s := NewMemStorage()
		a := NewSimpleAuditor(s)
		var pipe plugin.AuditPipeline = a
		hook, ok := pipe.(plugin.FingerprintHook)
		if !ok {
			t.Fatal("SimpleAuditor 应实现 FingerprintHook")
		}
		called := false
		hook.SetFingerprintFunc(func(log *plugin.AuditLog) string {
			called = true
			return "fp-" + log.ID
		})

		// 路径1: Finalize
		_ = a.Submit(&plugin.AuditEvent{RequestID: "f1", Data: &plugin.AuditLog{ID: "f1", RequestID: "f1"}})
		_ = a.Finalize("f1", newMeta())
		// 路径2: MarkDisconnect
		_ = a.Submit(&plugin.AuditEvent{RequestID: "d1", Data: &plugin.AuditLog{ID: "d1", RequestID: "d1"}})
		_ = a.MarkDisconnect("d1", "client gone", nil)
		// 路径3: Shutdown 兜底
		_ = a.Submit(&plugin.AuditEvent{RequestID: "s1", Data: &plugin.AuditLog{ID: "s1", RequestID: "s1"}})
		_ = a.Shutdown()

		if !called {
			t.Fatal("指纹函数未被调用")
		}
		for _, id := range []string{"f1", "d1", "s1"} {
			logs, _, _ := s.QueryAuditLogs(plugin.AuditLogFilter{RequestID: id}, 1, 10)
			if len(logs) != 1 || logs[0].SHA256Fingerprint != "fp-"+id {
				t.Errorf("路径 %s 指纹不符: %+v", id, logs)
			}
		}
	})
}
