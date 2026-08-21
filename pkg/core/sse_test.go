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
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/adapter"
	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
)

// newTestProxyEnv 构造测试代理环境(内存存储 + 单模型 + 单Key + OpenAI 适配器 + 审计)
func newTestProxyEnv(t *testing.T, upstreamURL string) (*oss.MemStorage, *ProxyCore) {
	t.Helper()
	storage := oss.NewMemStorage()
	now := time.Now()
	_ = storage.SaveModelConfig(&plugin.ModelConfig{
		ID: "m1", ModelName: "gpt-4", Provider: "openai", ProviderModel: "gpt-4o",
		BaseURL: upstreamURL, APIKey: "sk", Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	})
	_ = storage.SaveAPIKey(&plugin.APIKey{
		ID: "k1", KeyHash: hashKey("ng-test"), KeyPrefix: "ng-test", Name: "t",
		Status: plugin.APIKeyStatusActive, Quota: -1, CreatedAt: now, UpdatedAt: now,
	})
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	limiter := oss.NewRateLimiter(storage, 100, 100000, "token_bucket")
	_ = limiter.Init(map[string]interface{}{"default_rps": 100, "default_tpm": 100000})
	auditor := oss.NewSimpleAuditor(storage)
	return storage, NewProxyCore(NewPipeline(storage, limiter, auditor, registry), registry)
}

// lockedRecorder 线程安全 ResponseWriter:允许测试在流式写期间并发读取已写内容
type lockedRecorder struct {
	mu     sync.Mutex
	status int
	header http.Header
	body   bytes.Buffer
}

func newLockedRecorder() *lockedRecorder {
	return &lockedRecorder{header: make(http.Header)}
}

func (l *lockedRecorder) Header() http.Header { return l.header }

func (l *lockedRecorder) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.body.Write(p)
}

func (l *lockedRecorder) WriteHeader(code int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.status = code
}

func (l *lockedRecorder) Flush() {}

func (l *lockedRecorder) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.body.String()
}

// newMockSSEUpstream 返回 SSE 流式上游(含 usage 结尾分片)
func newMockSSEUpstream(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		chunks := []string{
			`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
			`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`,
			`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`,
			`data: [DONE]`,
		}
		for _, c := range chunks {
			_, _ = w.Write([]byte(c + "\n\n"))
			flusher.Flush()
		}
	}))
}

func TestProxySSEStream(t *testing.T) {
	upstream := newMockSSEUpstream(t)
	defer upstream.Close()

	storage := oss.NewMemStorage()
	now := time.Now()
	_ = storage.SaveModelConfig(&plugin.ModelConfig{
		ID: "m1", ModelName: "gpt-4", Provider: "openai", ProviderModel: "gpt-4o",
		BaseURL: upstream.URL, APIKey: "sk", Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	})
	_ = storage.SaveAPIKey(&plugin.APIKey{
		ID: "k1", KeyHash: hashKey("ng-test"), KeyPrefix: "ng-test", Name: "t",
		Status: plugin.APIKeyStatusActive, Quota: -1, CreatedAt: now, UpdatedAt: now,
	})
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	limiter := oss.NewRateLimiter(storage, 100, 100000, "token_bucket")
	_ = limiter.Init(map[string]interface{}{"default_rps": 100, "default_tpm": 100000})
	auditor := oss.NewSimpleAuditor(storage)
	pc := NewProxyCore(NewPipeline(storage, limiter, auditor, registry), registry)

	body := `{"model":"gpt-4","messages":[{"role":"user","content":"hi"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer ng-test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	pc.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type = %q; want text/event-stream", ct)
	}
	raw := rec.Body.String()
	if !strings.Contains(raw, "data: [DONE]") {
		t.Fatalf("missing [DONE]: %s", raw)
	}
	// 分片内容透传:包含 Hello
	if !strings.Contains(raw, "Hello") {
		t.Fatalf("missing chunk content: %s", raw)
	}

	// 审计:分片已捕获 + usage 解析 + 状态
	logs, total, err := storage.QueryAuditLogs(plugin.AuditLogFilter{}, 1, 10)
	if err != nil || total != 1 {
		t.Fatalf("audit total = %d, err %v", total, err)
	}
	log := logs[0]
	if !log.IsStream {
		t.Fatalf("audit is_stream = false; want true")
	}
	if len(log.SSEChunks) != 4 {
		t.Fatalf("sse chunks = %d; want 4, got %+v", len(log.SSEChunks), log.SSEChunks)
	}
	if log.TotalTokens != 12 || log.PromptTokens != 10 || log.CompletionTokens != 2 {
		t.Fatalf("audit tokens = %+v", log)
	}
	if log.ResponseStatus != 200 {
		t.Fatalf("audit status = %d", log.ResponseStatus)
	}
}

// TestProxySSEDisconnectRaceSingleAudit 断连竞态:客户端流中断连时,MarkDisconnect 先落库删除 pending,
// 主循环随后 Finalize 不得新建第二条空日志(与断连先落库的分片合并且标记 Disconnected)
func TestProxySSEDisconnectRaceSingleAudit(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		_, _ = w.Write([]byte(`data: {"choices":[{"index":0,"delta":{"content":"part1"}}]}` + "\n\n"))
		flusher.Flush()
		<-release // 保持流打开:断连后主循环仍阻塞在读上游,确保 MarkDisconnect 先行
	}))
	defer upstream.Close()

	storage, pc := newTestProxyEnv(t, upstream.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body := `{"model":"gpt-4","messages":[{"role":"user","content":"hi"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer ng-test")
	req.Header.Set("Content-Type", "application/json")
	rec := newLockedRecorder()

	done := make(chan struct{})
	go func() {
		pc.Handler().ServeHTTP(rec, req)
		close(done)
	}()

	// 等待首分片捕获(流已建立,此时断连分片已被审计捕获)
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(rec.String(), "part1") {
		if time.Now().After(deadline) {
			t.Fatal("timeout: first SSE chunk not captured")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// 客户端断连:取消 ctx → Watch 观察到 → MarkDisconnect 先落库(Disconnected=true)并删除 pending
	cancel()
	deadline = time.Now().Add(3 * time.Second)
	for {
		logs, _, _ := storage.QueryAuditLogs(plugin.AuditLogFilter{}, 1, 10)
		if len(logs) == 1 && logs[0].Disconnected {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timeout: MarkDisconnect audit log not saved")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// 释放上游:主循环 EOF 后 Finalize(pending 已删 → 返回 nil,不得新建空日志)
	close(release)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout: handler did not finish")
	}

	logs, total, err := storage.QueryAuditLogs(plugin.AuditLogFilter{}, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("audit total = %d, want 1 (断连竞态不得产生双记录)", total)
	}
	if !logs[0].Disconnected || logs[0].DisconnectReason != "client_disconnected" {
		t.Errorf("log = %+v, want Disconnected=true reason=client_disconnected", logs[0])
	}
	if len(logs[0].SSEChunks) != 1 {
		t.Errorf("SSEChunks = %d, want 1 (断连前已捕获分片应保留)", len(logs[0].SSEChunks))
	}
}

// TestProxySSEUpstreamReadErrorMarksDisconnect 上游单行超过 1MB(scanner ErrTooLong)或读错误:
// 循环静默退出后须标记断连落库,而非无 [DONE] 的无标记截断
func TestProxySSEUpstreamReadErrorMarksDisconnect(t *testing.T) {
	big := strings.Repeat("a", 1024*1024+16) // 单行超过 scanner 上限(1MB)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: " + big + "\n\n"))
	}))
	defer upstream.Close()

	storage, pc := newTestProxyEnv(t, upstream.URL)

	body := `{"model":"gpt-4","messages":[{"role":"user","content":"hi"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer ng-test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	pc.Handler().ServeHTTP(rec, req)

	logs, total, err := storage.QueryAuditLogs(plugin.AuditLogFilter{}, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("audit total = %d, want 1", total)
	}
	if !logs[0].Disconnected {
		t.Errorf("log Disconnected = false; want true (上游读错误须标记断连)")
	}
	if !strings.Contains(logs[0].DisconnectReason, "token too long") {
		t.Errorf("DisconnectReason = %q, want contains 'token too long'", logs[0].DisconnectReason)
	}
}

func TestStreamReassembler(t *testing.T) {
	chunks := []plugin.SSEChunk{
		{Index: 0, Data: `{"choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`},
		{Index: 1, Data: `{"choices":[{"index":0,"delta":{"content":"Hello"}}]}`},
		{Index: 2, Data: `{"choices":[{"index":0,"delta":{"content":" world"}}]}`},
		{Index: 3, Data: `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`},
	}
	r := NewStreamReassembler()
	out, err := r.Reassemble(chunks)
	if err != nil {
		t.Fatalf("Reassemble: %v", err)
	}
	if !strings.Contains(out, "Hello world") {
		t.Fatalf("reassembled = %q; want contains 'Hello world'", out)
	}
	if out != "Hello world" {
		t.Fatalf("reassembled = %q; want 'Hello world'", out)
	}
}
