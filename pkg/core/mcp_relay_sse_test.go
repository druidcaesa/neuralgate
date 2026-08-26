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

package core

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
)

func sseFrame(data string) string {
	return "event: message\ndata: " + data + "\n\n"
}

// TestMCPRelayToolsCallSSE 流式响应逐帧透传且旁路累积至最终响应后审计一次
func TestMCPRelayToolsCallSSE(t *testing.T) {
	finalFrame := `{"jsonrpc":"2.0","id":9,"result":{"content":[{"type":"text","text":"done"}],"isError":false}}`
	upSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(
			sseFrame(`{"jsonrpc":"2.0","method":"notifications/progress","params":{"progress":1}}`) +
				sseFrame(`{"jsonrpc":"2.0","method":"notifications/progress","params":{"progress":2}}`) +
				sseFrame(finalFrame)))
	}))
	defer upSrv.Close()
	storage := oss.NewMemStorage()
	_ = storage.SaveMCPServer(&plugin.MCPServer{ID: mcpTestServerID, Name: "u1",
		Endpoint: upSrv.URL, Enabled: true})
	hook := &hookRecorder{}
	auditor := &recordingAuditor{}
	relay := NewMCPRelay(storage, auditor, hook, upSrv.Client())

	sid := initializeAndGetSession(t, relay)
	rec := postRPC(t, relay,
		`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"long_task"}}`, sid)
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("客户端应收到 SSE Content-Type, got %s", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`"progress":1`,
		`"progress":2`,
		`event: message`,
		`data: {"jsonrpc":"2.0","id":9,`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("透传体缺 %s\n实际: %s", want, body)
		}
	}
	entries := hook.all()
	if len(entries) != 1 {
		t.Fatalf("hook 应恰好一次, got %d", len(entries))
	}
	e := entries[0]
	if e.ToolName != "long_task" || e.Status != plugin.MCPStatusSuccess ||
		!strings.Contains(e.ToolResult, `"text":"done"`) || e.DurationMS < 0 {
		t.Errorf("SSE 审计字段不符: %+v", e)
	}
	auditor.mu.Lock()
	defer auditor.mu.Unlock()
	if len(auditor.finalized) != 1 {
		t.Errorf("常规审计应 Finalize 一次, got %d", len(auditor.finalized))
	}
}

// TestMCPRelaySSENoFinalResponse 流被截断无最终响应 → 不落工具调用审计，
// 但须回收常规审计挂起项（MarkDisconnect），避免 pending 泄漏至进程退出
func TestMCPRelaySSENoFinalResponse(t *testing.T) {
	upSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseFrame(`{"jsonrpc":"2.0","method":"notifications/progress","params":{"progress":1}}`)))
		// 直接 EOF,无最终响应
	}))
	defer upSrv.Close()
	storage := oss.NewMemStorage()
	_ = storage.SaveMCPServer(&plugin.MCPServer{ID: mcpTestServerID, Name: "u1",
		Endpoint: upSrv.URL, Enabled: true})
	hook := &hookRecorder{}
	auditor := &recordingAuditor{}
	relay := NewMCPRelay(storage, auditor, hook, upSrv.Client())

	// 注入确定 RequestID 的上下文，便于断言挂起项按该 ID 回收
	rc := &RequestContext{RequestID: "req-sse-no-final", StartTime: time.Now()}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relay.ServeHTTP(w, r.WithContext(WithRequestContext(r.Context(), rc)))
	})

	sid := initializeAndGetSession(t, handler)
	rec := postRPC(t, handler,
		`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"x"}}`, sid)
	if rec.Code != 200 {
		t.Fatalf("透传本身应成功: %d", rec.Code)
	}
	if entries := hook.all(); len(entries) != 0 {
		t.Errorf("无最终响应不应落审计, got %+v", entries)
	}
	auditor.mu.Lock()
	defer auditor.mu.Unlock()
	if len(auditor.finalized) != 0 {
		t.Errorf("无最终响应不应 Finalize, got %v", auditor.finalized)
	}
	if len(auditor.disconnects) != 1 || auditor.disconnects[0] != rc.RequestID {
		t.Errorf("应恰好按 %q 回收一次挂起项, got %v", rc.RequestID, auditor.disconnects)
	}
}

// TestMCPRelayNonToolCallSSEPurePassThrough 非 tools/call 的 SSE 响应纯透传不累积
func TestMCPRelayNonToolCallSSEPurePassThrough(t *testing.T) {
	upSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var probe mcpRequestProbe
		_ = json.NewDecoder(r.Body).Decode(&probe)
		if probe.Method == "tools/list" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(sseFrame(`{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"a"}]}}`)))
			return
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer upSrv.Close()
	storage := oss.NewMemStorage()
	_ = storage.SaveMCPServer(&plugin.MCPServer{ID: mcpTestServerID, Name: "u1",
		Endpoint: upSrv.URL, Enabled: true})
	hook := &hookRecorder{}
	relay := NewMCPRelay(storage, nil, hook, upSrv.Client())

	sid := initializeAndGetSession(t, relay)
	rec := postRPC(t, relay, toolsListBody, sid)
	if !strings.Contains(rec.Body.String(), `"tools"`) {
		t.Errorf("tools/list SSE 应透传: %s", rec.Body.String())
	}
	if entries := hook.all(); len(entries) != 0 {
		t.Errorf("非 tools/call 不应落审计, got %+v", entries)
	}
}
