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
	_ = auditor.MarkDisconnect("r2", "client_closed_connection")
	logs, total, _ := storage.QueryAuditLogs(plugin.AuditLogFilter{}, 1, 10)
	if total != 1 {
		t.Fatalf("total = %d, want 1", total)
	}
	if !logs[0].Disconnected || logs[0].DisconnectReason != "client_closed_connection" {
		t.Errorf("log = %+v, want Disconnected=true", logs[0])
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
