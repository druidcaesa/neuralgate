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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/adapter"
	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
)

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
	limiter := oss.NewMemRateLimiter()
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
