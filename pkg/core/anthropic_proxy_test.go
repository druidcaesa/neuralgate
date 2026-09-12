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
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/adapter"
	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
)

// ===== 公共测试装配 =====

// newDualProxy 装配带 openai/anthropic/qwen 适配器的代理链(单模型配置)
func newDualProxy(t *testing.T, cfg *plugin.ModelConfig) (*oss.MemStorage, *ProxyCore) {
	t.Helper()
	storage := oss.NewMemStorage()
	now := time.Now()
	if cfg != nil {
		if err := storage.SaveModelConfig(cfg); err != nil {
			t.Fatal(err)
		}
	}
	_ = storage.SaveAPIKey(&plugin.APIKey{
		ID: "k1", KeyHash: hashKey("ng-test"), KeyPrefix: "ng-test", Name: "t",
		Status: plugin.APIKeyStatusActive, Quota: -1, CreatedAt: now, UpdatedAt: now,
	})
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	registry.Register(adapter.NewAnthropicAdapter())
	registry.Register(adapter.NewQwenAdapter())
	limiter := oss.NewRateLimiter(storage, 1000, 1000000, "token_bucket")
	_ = limiter.Init(map[string]interface{}{"default_rps": 1000, "default_tpm": 1000000})
	pc := NewProxyCore(NewPipeline(storage, limiter, oss.NewSimpleAuditor(storage), registry), registry)
	return storage, pc
}

// dualModel 组装模型配置的公共字段
func dualModel(name, provider, providerModel, baseURL, apiKey string, maxTokens int) *plugin.ModelConfig {
	now := time.Now()
	return &plugin.ModelConfig{
		ID: name + "-id", ModelName: name, Provider: provider, ProviderModel: providerModel,
		BaseURL: baseURL, APIKey: apiKey, MaxTokens: maxTokens, Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	}
}

// dualReq 发一个经完整中间件链的代理请求,返回响应
func dualReq(t *testing.T, pc *ProxyCore, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	pc.Handler().ServeHTTP(rec, req)
	return rec
}

// jsonDoc 解析 JSON 到 map(断言用)
func jsonDoc(t *testing.T, body string) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("invalid json %s: %v", body, err)
	}
	return m
}

// strAt 沿 object/array 路径取字符串字段;缺失/类型不符返回 ""(不 fail)
func strAt(v interface{}, keys ...interface{}) string {
	cur := v
	for _, k := range keys {
		switch kk := k.(type) {
		case string:
			mm, ok := cur.(map[string]interface{})
			if !ok {
				return ""
			}
			cur = mm[kk]
		case int:
			aa, ok := cur.([]interface{})
			if !ok || kk >= len(aa) {
				return ""
			}
			cur = aa[kk]
		default:
			return ""
		}
	}
	s, _ := cur.(string)
	return s
}

// sseData 从 SSE 响应体提取全部 data: 负载(空行/event 行忽略)
func sseData(t *testing.T, body string) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		p := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ===== 1. openai 入口 × anthropic 上游:非流式转换 + 默认 max_tokens 注入 =====

func TestAnthropicMatrix_OpenAIEntry_AnthropicUpstream_NonStream(t *testing.T) {
	upstreamCalled := false
	var upReq struct {
		model string
		body  string
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
		body, _ := io.ReadAll(r.Body)
		upReq.model = strAt(jsonDoc(t, string(body)), "model")
		upReq.body = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-3-5-sonnet",
			"content":[{"type":"text","text":"hello from claude"}],"stop_reason":"end_turn","stop_sequence":null,
			"usage":{"input_tokens":10,"output_tokens":5}}`))
	}))
	defer upstream.Close()

	storage, pc := newDualProxy(t, dualModel("claude-alias", "anthropic", "claude-3-5-sonnet", upstream.URL, "sk-ant", 4096))

	rec := dualReq(t, pc, "/v1/chat/completions",
		`{"model":"claude-alias","messages":[{"role":"user","content":"hi"}]}`,
		map[string]string{"Authorization": "Bearer ng-test"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	// 客户端侧线是 OpenAI chat.completion 形状
	doc := jsonDoc(t, rec.Body.String())
	if got := strAt(doc, "object"); got != "chat.completion" {
		t.Fatalf("client object = %q; body=%s", got, rec.Body.String())
	}
	if got := strAt(doc, "choices", 0, "message", "content"); got != "hello from claude" {
		t.Fatalf("client content = %q; body=%s", got, rec.Body.String())
	}
	// 上游收到 anthropic 请求:model 替换为 ProviderModel、默认 max_tokens 注入
	if !upstreamCalled {
		t.Fatal("upstream not called")
	}
	if upReq.model != "claude-3-5-sonnet" {
		t.Fatalf("upstream model = %q; want claude-3-5-sonnet", upReq.model)
	}
	mt := jsonDoc(t, upReq.body)["max_tokens"]
	if mt == nil || int(mt.(float64)) != 4096 {
		t.Fatalf("upstream max_tokens = %v; want 4096(cfg 默认注入); body=%s", mt, upReq.body)
	}
	// 审计 usage 与客户端侧线 ResponseBody
	logs, total, err := storage.QueryAuditLogs(plugin.AuditLogFilter{}, 1, 10)
	if err != nil || total != 1 {
		t.Fatalf("audit total = %d err=%v; want 1", total, err)
	}
	if logs[0].PromptTokens != 10 || logs[0].CompletionTokens != 5 || logs[0].TotalTokens != 15 {
		t.Fatalf("audit tokens = %d/%d/%d; want 10/5/15", logs[0].PromptTokens, logs[0].CompletionTokens, logs[0].TotalTokens)
	}
	if !strings.Contains(logs[0].ResponseBody, `"object":"chat.completion"`) {
		t.Fatalf("audit ResponseBody = %s; want openai shape(客户端侧线)", logs[0].ResponseBody)
	}
}

// ===== 2. openai 入口 × anthropic 上游:流式解码 + usage 尾块 + [DONE] =====

func TestAnthropicMatrix_OpenAIEntry_AnthropicUpstream_Stream(t *testing.T) {
	// anthropic 上游 SSE(无 [DONE],结束于 message_stop)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		write := func(s string) { _, _ = io.WriteString(w, s); fl.Flush() }
		write("data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-3-5-sonnet\",\"content\":[],\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":10,\"output_tokens\":0}}}\n\n")
		write("data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		write("data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello from Claude\"}}\n\n")
		write("data: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		write("data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":5}}\n\n")
		write("data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer upstream.Close()

	storage, pc := newDualProxy(t, dualModel("claude-alias", "anthropic", "claude-3-5-sonnet", upstream.URL, "sk-ant", 0))

	rec := dualReq(t, pc, "/v1/chat/completions",
		`{"model":"claude-alias","messages":[{"role":"user","content":"hi"}],"stream":true,"stream_options":{"include_usage":true}}`,
		map[string]string{"Authorization": "Bearer ng-test"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	payloads := sseData(t, rec.Body.String())
	if len(payloads) == 0 {
		t.Fatal("no SSE data payloads")
	}
	// 首个分片带 role=assistant
	if got := strAt(jsonDoc(t, payloads[0]), "choices", 0, "delta", "role"); got != "assistant" {
		t.Fatalf("first chunk role = %q; body=%s", got, payloads[0])
	}
	var sawText, sawFinish, sawUsage, sawDone bool
	for _, p := range payloads {
		if p == "[DONE]" {
			sawDone = true
			continue
		}
		doc := jsonDoc(t, p)
		if strAt(doc, "choices", 0, "delta", "content") == "Hello from Claude" {
			sawText = true
		}
		if strAt(doc, "choices", 0, "finish_reason") == "stop" {
			sawFinish = true
		}
		if choices, ok := doc["choices"].([]interface{}); ok && len(choices) == 0 {
			if u, ok := doc["usage"].(map[string]interface{}); ok {
				if total, _ := u["total_tokens"].(float64); total == 15 {
					sawUsage = true
				}
			}
		}
	}
	if !sawText || !sawFinish {
		t.Fatalf("missing content/finish chunk; payloads=%v", payloads)
	}
	if !sawUsage {
		t.Fatalf("missing usage tail chunk(include_usage); payloads=%v", payloads)
	}
	if !sawDone || payloads[len(payloads)-1] != "[DONE]" {
		t.Fatalf("want [DONE] 收尾; got last=%q", payloads[len(payloads)-1])
	}
	// 审计 usage(observer 增量:message_start→10、message_delta→5)
	logs, total, err := storage.QueryAuditLogs(plugin.AuditLogFilter{}, 1, 10)
	if err != nil || total != 1 {
		t.Fatalf("audit total = %d err=%v; want 1", total, err)
	}
	if logs[0].PromptTokens != 10 || logs[0].CompletionTokens != 5 || logs[0].TotalTokens != 15 {
		t.Fatalf("audit tokens = %d/%d/%d; want 10/5/15", logs[0].PromptTokens, logs[0].CompletionTokens, logs[0].TotalTokens)
	}
}

// ===== 3. anthropic 入口 × openai 上游:非流式反向转换 =====

func TestAnthropicMatrix_AnthropicEntry_OpenAIUpstream_NonStream(t *testing.T) {
	var upReq struct {
		path  string
		model string
		auth  string
		body  string
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		upReq.path = r.URL.Path
		upReq.model = strAt(jsonDoc(t, string(body)), "model")
		upReq.auth = r.Header.Get("Authorization")
		upReq.body = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","object":"chat.completion","created":1700000000,"model":"gpt-4o",
			"choices":[{"index":0,"message":{"role":"assistant","content":"hello from openai"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`))
	}))
	defer upstream.Close()

	storage, pc := newDualProxy(t, dualModel("gpt4", "openai", "gpt-4o", upstream.URL, "sk-openai", 0))

	rec := dualReq(t, pc, "/v1/messages",
		`{"model":"gpt4","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`,
		map[string]string{"x-api-key": "ng-test", "anthropic-version": "2023-06-01"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	// 上游收到 openai 形状:/v1/chat/completions、Bearer、model=ProviderModel、客户端 max_tokens 保留
	if upReq.path != "/v1/chat/completions" {
		t.Fatalf("upstream path = %s; want /v1/chat/completions", upReq.path)
	}
	if upReq.auth != "Bearer sk-openai" {
		t.Fatalf("upstream auth = %q; want Bearer sk-openai", upReq.auth)
	}
	if upReq.model != "gpt-4o" {
		t.Fatalf("upstream model = %q; want gpt-4o", upReq.model)
	}
	if mt := jsonDoc(t, upReq.body)["max_tokens"]; mt == nil || int(mt.(float64)) != 100 {
		t.Fatalf("upstream max_tokens = %v; want 100(客户端显式值优先)", mt)
	}
	// 客户端侧线是 Anthropic Message 形状
	doc := jsonDoc(t, rec.Body.String())
	if got := strAt(doc, "type"); got != "message" {
		t.Fatalf("client type = %q; want message; body=%s", got, rec.Body.String())
	}
	if got := strAt(doc, "content", 0, "text"); got != "hello from openai" {
		t.Fatalf("client content = %q; body=%s", got, rec.Body.String())
	}
	if got := strAt(doc, "stop_reason"); got != "end_turn" {
		t.Fatalf("client stop_reason = %q; want end_turn", got)
	}
	// 审计 usage 与客户端侧线 ResponseBody
	logs, total, err := storage.QueryAuditLogs(plugin.AuditLogFilter{}, 1, 10)
	if err != nil || total != 1 {
		t.Fatalf("audit total = %d err=%v; want 1", total, err)
	}
	if logs[0].PromptTokens != 7 || logs[0].CompletionTokens != 3 || logs[0].TotalTokens != 10 {
		t.Fatalf("audit tokens = %d/%d/%d; want 7/3/10", logs[0].PromptTokens, logs[0].CompletionTokens, logs[0].TotalTokens)
	}
	if !strings.Contains(logs[0].ResponseBody, `"type":"message"`) {
		t.Fatalf("audit ResponseBody = %s; want anthropic shape(客户端侧线)", logs[0].ResponseBody)
	}
}

// ===== 3.5 Claude Code 形态请求:system 为内容块数组(带 cache_control)=====

// TestAnthropicEntryClaudeCodeShape Claude Code 发给 /v1/messages 的请求中,
// system 是内容块数组而非字符串。该形态曾因 System 字段声明为 string 而在解析阶段
// 直接报错,整条请求被判 400——且只发生在反向转换路径(原生 anthropic 上游走 map 透传,不受影响)
func TestAnthropicEntryClaudeCodeShape(t *testing.T) {
	var upBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		upBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","object":"chat.completion","created":1700000000,"model":"gpt-4o",
			"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`))
	}))
	defer upstream.Close()

	_, pc := newDualProxy(t, dualModel("gpt4", "openai", "gpt-4o", upstream.URL, "sk-openai", 0))

	// 线上实测形态:一条用户消息含多个文本块,其后跟一条 role 为 system 的消息
	//(Anthropic 规范只允许 user/assistant,此处按 out-of-contract 输入验证不阻断);
	// 另含若干本网关不认识的新版顶层字段,须被忽略而非报错
	body := `{
	  "model":"gpt4","max_tokens":32000,"stream":false,
	  "system":[
	    {"type":"text","text":"You are Claude Code.","cache_control":{"type":"ephemeral"}},
	    {"type":"text","text":"Be concise."}
	  ],
	  "messages":[
	    {"role":"user","content":[
	      {"type":"text","text":"hi"},
	      {"type":"text","text":"second block","cache_control":{"type":"ephemeral"}}
	    ]},
	    {"role":"system","content":"session context"}
	  ],
	  "tools":[{"name":"Read","description":"read a file",
	    "input_schema":{"type":"object","properties":{"path":{"type":"string"}}}}],
	  "metadata":{"user_id":"u1"},
	  "thinking":{"type":"adaptive"},
	  "context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]},
	  "output_config":{"effort":"high"}
	}`
	rec := dualReq(t, pc, "/v1/messages", body,
		map[string]string{"x-api-key": "ng-test", "anthropic-version": "2023-06-01"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	// system 块数组须转成上游的一条 system 消息,两块按换行拼接(JSON 里换行为 \n 两字符)
	if !strings.Contains(upBody, `You are Claude Code.\nBe concise.`) {
		t.Fatalf("上游 system 未正确转换: %s", upBody)
	}
	// tools 与 metadata 不得因 system 形态而丢失
	if !strings.Contains(upBody, `"Read"`) {
		t.Fatalf("上游 tools 丢失: %s", upBody)
	}
	if !strings.Contains(upBody, `"u1"`) {
		t.Fatalf("上游 metadata.user_id 丢失: %s", upBody)
	}
	// 多块 user 消息与 role=system 消息的内容都不得丢失
	for _, want := range []string{"hi", "second block", "session context"} {
		if !strings.Contains(upBody, want) {
			t.Fatalf("上游缺少内容 %q: %s", want, upBody)
		}
	}
	// 客户端侧线仍为 Anthropic Message 形状
	if got := strAt(jsonDoc(t, rec.Body.String()), "type"); got != "message" {
		t.Fatalf("client type = %q; want message; body=%s", got, rec.Body.String())
	}
}

// ===== 4. anthropic 入口 × openai 上游:流式编码(message_start→message_stop,无 [DONE]) =====

func TestAnthropicMatrix_AnthropicEntry_OpenAIUpstream_Stream(t *testing.T) {
	var upBody struct {
		Stream        bool `json:"stream"`
		StreamOptions struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &upBody)
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		write := func(s string) { _, _ = io.WriteString(w, s); fl.Flush() }
		write("data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\"},\"finish_reason\":null}]}\n\n")
		write("data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hello\"},\"finish_reason\":null}]}\n\n")
		write("data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		write("data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-4o\",\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3,\"total_tokens\":10}}\n\n")
		write("data: [DONE]\n\n")
	}))
	defer upstream.Close()

	storage, pc := newDualProxy(t, dualModel("gpt4", "openai", "gpt-4o", upstream.URL, "sk-openai", 0))

	rec := dualReq(t, pc, "/v1/messages",
		`{"model":"gpt4","max_tokens":100,"messages":[{"role":"user","content":"hi"}],"stream":true}`,
		map[string]string{"x-api-key": "ng-test", "anthropic-version": "2023-06-01"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	// 反向流强制 include_usage(取回上游 usage 供 message_delta 与审计)
	if !upBody.Stream || !upBody.StreamOptions.IncludeUsage {
		t.Fatalf("upstream body stream=%v include_usage=%v; want true/true", upBody.Stream, upBody.StreamOptions.IncludeUsage)
	}
	payloads := sseData(t, rec.Body.String())
	if len(payloads) == 0 {
		t.Fatal("no SSE payloads")
	}
	// 事件序:message_start 开头、message_stop 结尾,且无 [DONE]
	first := strAt(jsonDoc(t, payloads[0]), "type")
	last := strAt(jsonDoc(t, payloads[len(payloads)-1]), "type")
	if first != "message_start" || last != "message_stop" {
		t.Fatalf("event seq first=%s last=%s; want message_start…message_stop", first, last)
	}
	var sawText, sawDelta bool
	for _, p := range payloads {
		if p == "[DONE]" {
			t.Fatalf("anthropic 流不应含 [DONE]: %v", payloads)
		}
		doc := jsonDoc(t, p)
		typ, _ := doc["type"].(string)
		switch typ {
		case "content_block_delta":
			if delta, ok := doc["delta"].(map[string]interface{}); ok {
				if dt, _ := delta["type"].(string); dt == "text_delta" {
					if txt, _ := delta["text"].(string); txt == "Hello" {
						sawText = true
					}
				}
			}
		case "message_delta":
			stop := strAt(doc, "delta", "stop_reason")
			var out float64
			if u, ok := doc["usage"].(map[string]interface{}); ok {
				out, _ = u["output_tokens"].(float64)
			}
			if stop == "end_turn" && out == 3 {
				sawDelta = true
			}
		}
	}
	if !sawText || !sawDelta {
		t.Fatalf("missing text_delta or message_delta(usage/end_turn); payloads=%v", payloads)
	}
	// 审计 usage(openai usage 尾块整取)
	logs, total, err := storage.QueryAuditLogs(plugin.AuditLogFilter{}, 1, 10)
	if err != nil || total != 1 {
		t.Fatalf("audit total = %d err=%v; want 1", total, err)
	}
	if logs[0].PromptTokens != 7 || logs[0].CompletionTokens != 3 || logs[0].TotalTokens != 10 {
		t.Fatalf("audit tokens = %d/%d/%d; want 7/3/10", logs[0].PromptTokens, logs[0].CompletionTokens, logs[0].TotalTokens)
	}
	joined := make([]string, 0, len(logs[0].SSEChunks))
	for _, c := range logs[0].SSEChunks {
		joined = append(joined, c.Data)
	}
	if !strings.Contains(strings.Join(joined, "\n"), "message_stop") {
		t.Fatalf("audit SSEChunks = %v; want message_stop(客户端侧线)", logs[0].SSEChunks)
	}
}

// ===== 5. anthropic 入口 × anthropic 上游:原生透传 =====

func TestAnthropicMatrix_AnthropicEntry_AnthropicUpstream_Passthrough(t *testing.T) {
	var upReq struct {
		model  string
		apiKey string
		auth   string
		ver    string
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		upReq.model = strAt(jsonDoc(t, string(body)), "model")
		upReq.apiKey = r.Header.Get("X-Api-Key")
		upReq.auth = r.Header.Get("Authorization")
		upReq.ver = r.Header.Get("Anthropic-Version")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-3-5-sonnet",
			"content":[{"type":"text","text":"claude native"}],"stop_reason":"end_turn","stop_sequence":null,
			"usage":{"input_tokens":10,"output_tokens":5}}`))
	}))
	defer upstream.Close()

	storage, pc := newDualProxy(t, dualModel("claude", "anthropic", "claude-3-5-sonnet", upstream.URL, "sk-ant", 0))

	rec := dualReq(t, pc, "/v1/messages",
		`{"model":"claude","messages":[{"role":"user","content":"hi"}]}`,
		map[string]string{"x-api-key": "ng-test", "anthropic-version": "2023-06-01"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	// 上游:x-api-key + 无 Authorization + model 替换为 ProviderModel
	if upReq.apiKey != "sk-ant" {
		t.Fatalf("upstream x-api-key = %q; want sk-ant", upReq.apiKey)
	}
	if upReq.auth != "" {
		t.Fatalf("upstream leaked Authorization = %q", upReq.auth)
	}
	if upReq.ver != "2023-06-01" {
		t.Fatalf("upstream anthropic-version = %q", upReq.ver)
	}
	if upReq.model != "claude-3-5-sonnet" {
		t.Fatalf("upstream model = %q; want claude-3-5-sonnet", upReq.model)
	}
	// 透传响应原样(anthropic Message 形状)
	if got := strAt(jsonDoc(t, rec.Body.String()), "content", 0, "text"); got != "claude native" {
		t.Fatalf("client content = %q; body=%s", got, rec.Body.String())
	}
	// 审计 usage(anthropic 非流式 ParseTokenUsage)
	logs, total, err := storage.QueryAuditLogs(plugin.AuditLogFilter{}, 1, 10)
	if err != nil || total != 1 {
		t.Fatalf("audit total = %d err=%v; want 1", total, err)
	}
	if logs[0].PromptTokens != 10 || logs[0].CompletionTokens != 5 || logs[0].TotalTokens != 15 {
		t.Fatalf("audit tokens = %d/%d/%d; want 10/5/15", logs[0].PromptTokens, logs[0].CompletionTokens, logs[0].TotalTokens)
	}
}

// ===== 6. anthropic 入口 × qwen(其他协议)上游 → 400 入口不支持 =====

func TestAnthropicMatrix_AnthropicEntry_OtherUpstream_Rejected(t *testing.T) {
	_, pc := newDualProxy(t, dualModel("qwen", "qwen", "qwen-max", "http://unused", "sk-q", 0))

	rec := dualReq(t, pc, "/v1/messages",
		`{"model":"qwen","messages":[{"role":"user","content":"hi"}]}`,
		map[string]string{"x-api-key": "ng-test", "anthropic-version": "2023-06-01"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400; body=%s", rec.Code, rec.Body.String())
	}
	// 错误体为 anthropic 形 {type:error, error:{type,message}}
	doc := jsonDoc(t, rec.Body.String())
	if typ, _ := doc["type"].(string); typ != "error" {
		t.Fatalf("error top type = %q; want error; body=%s", typ, rec.Body.String())
	}
	errObj, _ := doc["error"].(map[string]interface{})
	if etype, _ := errObj["type"].(string); etype != "invalid_request_error" {
		t.Fatalf("error.type = %q; want invalid_request_error", etype)
	}
	if msg, _ := errObj["message"].(string); !strings.Contains(msg, "only supports") {
		t.Fatalf("error.message = %q; want contains only supports", msg)
	}
}

// ===== 7. 鉴权:x-api-key 入口 / Bearer 优先 / 审计头清洗 =====

// authProbe 走 AuthMiddleware,next 返回 APIKeyID 与请求头里是否残留鉴权头
func authProbe(storage plugin.StoragePlugin, path string, headers map[string]string) (int, string) {
	mw := AuthMiddleware(storage)
	rec := httptest.NewRecorder()
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc, _ := RequestContextFrom(r.Context())
		_, hasAuth := rc.RequestHeaders["Authorization"]
		_, hasX := rc.RequestHeaders["X-Api-Key"]
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(rc.APIKeyID + "|scrubAuth=" + boolStr(hasAuth) + "|scrubX=" + boolStr(hasX)))
	}))
	req := httptest.NewRequest(http.MethodPost, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	handler.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func boolStr(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// TestAuthXApiKeyOnMessages x-api-key(无 Bearer)在 /v1/messages 入口应鉴权通过
func TestAuthXApiKeyOnMessages(t *testing.T) {
	code, body := authProbe(newTestStorage(), "/v1/messages", map[string]string{"x-api-key": "ng-goodkey"})
	if code != http.StatusOK || !strings.HasPrefix(body, "k1|") {
		t.Fatalf("status=%d body=%q; want 200 k1|", code, body)
	}
	// /v1/chat/completions 不认 x-api-key(仍走 Bearer):无 Authorization → 401
	code2, body2 := authProbe(newTestStorage(), "/v1/chat/completions", map[string]string{"x-api-key": "ng-goodkey"})
	if code2 != http.StatusUnauthorized {
		t.Fatalf("openai entry x-api-key status=%d body=%q; want 401", code2, body2)
	}
}

// TestAuthBearerTakesPrecedence 两种头并存时 Bearer 优先
func TestAuthBearerTakesPrecedence(t *testing.T) {
	code, body := authProbe(newTestStorage(), "/v1/messages", map[string]string{
		"x-api-key":     "ng-disabled", // 若误用 x-api-key 会被拒
		"Authorization": "Bearer ng-goodkey",
	})
	if code != http.StatusOK || !strings.HasPrefix(body, "k1|") {
		t.Fatalf("status=%d body=%q; want Bearer 优先 → 200 k1|", code, body)
	}
}

// TestAuthAuditHeaderScrub 审计 RequestHeaders 应剔除 Authorization 与 X-Api-Key
func TestAuthAuditHeaderScrub(t *testing.T) {
	_, body := authProbe(newTestStorage(), "/v1/messages", map[string]string{
		"x-api-key":     "ng-goodkey",
		"Authorization": "Bearer ng-goodkey",
	})
	if strings.Contains(body, "scrubAuth=yes") || strings.Contains(body, "scrubX=yes") {
		t.Fatalf("审计头泄漏鉴权键: body=%q", body)
	}
}

// ===== 8. 错误体按入口分形(auth 层无 rc,按 r.URL.Path 判定) =====

// TestAuthErrorShapeByEntry 401 错误体:/v1/messages → anthropic 形,其余 → openai 形
func TestAuthErrorShapeByEntry(t *testing.T) {
	storage := newTestStorage()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	AuthMiddleware(storage)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).
		ServeHTTP(rec, req) // 无任何 Key → 401
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anthropic status = %d; want 401", rec.Code)
	}
	doc := jsonDoc(t, rec.Body.String())
	if typ, _ := doc["type"].(string); typ != "error" {
		t.Fatalf("anthropic error body top type = %q; want error; body=%s", typ, rec.Body.String())
	}
	if _, ok := doc["error"].(map[string]interface{}); !ok {
		t.Fatalf("anthropic error body missing error obj; body=%s", rec.Body.String())
	}

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	AuthMiddleware(storage)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).
		ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("openai status = %d; want 401", rec2.Code)
	}
	if doc2 := jsonDoc(t, rec2.Body.String()); doc2["type"] != nil {
		t.Fatalf("openai error body不应含顶层 type; body=%s", rec2.Body.String())
	}
	if _, ok := jsonDoc(t, rec2.Body.String())["error"].(map[string]interface{}); !ok {
		t.Fatalf("openai error body missing error obj; body=%s", rec2.Body.String())
	}
}
