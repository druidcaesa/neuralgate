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

package adapter

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestAnthropicProtocol(t *testing.T) {
	a := NewAnthropicAdapter()
	if a.Name() != "anthropic" || a.SupportsNativeProxy() {
		t.Fatalf("Name/native = %q/%v", a.Name(), a.SupportsNativeProxy())
	}
	if !IsAnthropicProtocol(a) {
		t.Fatal("AnthropicAdapter 应声明 anthropic 协议")
	}
	if IsAnthropicProtocol(NewOpenAIAdapter()) {
		t.Fatal("OpenAIAdapter 不应声明 anthropic 协议")
	}
}

// TestAnthropicEncodeRequest 统一格式 → Anthropic 请求:system 上提、角色交替合并、tool_result 并入 user、参数映射
func TestAnthropicEncodeRequest(t *testing.T) {
	toks := 1024
	u := &UnifiedRequest{
		Model:     "alias",
		MaxTokens: &toks,
		Messages: []Message{
			{Role: "system", Content: "你是一名助手"},
			{Role: "user", Content: []ContentPart{{Type: "text", Text: "hi"}, {Type: "image_url", ImageURL: &ImageURLPart{URL: "x"}}}},
			{Role: "assistant", Content: "好的", ToolCalls: []ToolCall{{ID: "call_1", Type: "function", Function: ToolCallFunction{Name: "get_weather", Arguments: `{"city":"北京"}`}}}},
			{Role: "tool", ToolCallID: "call_1", Content: "晴"},
			{Role: "user", Content: "谢谢"},
		},
		Tools: []Tool{{Type: "function", Function: ToolFunction{Name: "get_weather", Description: "天气", Parameters: map[string]interface{}{"type": "object"}}}},
	}
	body, err := AnthropicEncodeRequest(u, "claude-x")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var req struct {
		Model    string `json:"model"`
		System   string `json:"system"`
		Messages []struct {
			Role    string           `json:"role"`
			Content []map[string]any `json:"content"`
		} `json:"messages"`
		MaxTokens *int `json:"max_tokens"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("body not anthropic json: %v; %s", err, body)
	}
	if req.Model != "claude-x" || req.MaxTokens == nil || *req.MaxTokens != 1024 {
		t.Fatalf("model/max_tokens = %q/%v", req.Model, req.MaxTokens)
	}
	if req.System != "你是一名助手" {
		t.Fatalf("system = %q", req.System)
	}
	if len(req.Messages) != 3 {
		t.Fatalf("messages = %d; want 3(图像块丢弃→user/assistant/tool并入user)", len(req.Messages))
	}
	// 首条 user:图像丢弃只留文本
	if req.Messages[0].Role != "user" || req.Messages[0].Content[0]["text"] != "hi" || len(req.Messages[0].Content) != 1 {
		t.Fatalf("msg0 = %+v", req.Messages[0])
	}
	// assistant:文本 + tool_use
	if req.Messages[1].Role != "assistant" || len(req.Messages[1].Content) != 2 {
		t.Fatalf("msg1 = %+v", req.Messages[1])
	}
	if req.Messages[1].Content[1]["type"] != "tool_use" || req.Messages[1].Content[1]["name"] != "get_weather" {
		t.Fatalf("msg1 tool = %+v", req.Messages[1].Content[1])
	}
	// tool 结果并入下一条 user(tool_result)
	if req.Messages[2].Role != "user" || req.Messages[2].Content[0]["type"] != "tool_result" {
		t.Fatalf("msg2 = %+v", req.Messages[2])
	}
	if req.Messages[2].Content[0]["content"] != "晴" {
		t.Fatalf("tool_result content = %v", req.Messages[2].Content[0]["content"])
	}
}

// TestAnthropicParseRequest Anthropic 入口请求 → 统一(system→system,tool_use→tool_calls,tool_result→tool)
func TestAnthropicParseRequest(t *testing.T) {
	body := `{
	  "model":"gpt-x","max_tokens":64,"stream":true,
	  "system":"系统提示",
	  "messages":[
	    {"role":"user","content":"查天气"},
	    {"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"get_weather","input":{"city":"北京"}}]},
	    {"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_1","content":"晴"}]}
	  ],
	  "tools":[{"name":"get_weather","input_schema":{"type":"object"}}],
	  "tool_choice":{"type":"tool","name":"get_weather"}
	}`
	u, err := ParseAnthropicRequest([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if u.Model != "gpt-x" || u.MaxTokens == nil || *u.MaxTokens != 64 || !u.Stream {
		t.Fatalf("head = %+v", u)
	}
	if len(u.Messages) != 4 {
		t.Fatalf("messages = %d; want 4(system+user+assistant+tool)", len(u.Messages))
	}
	if u.Messages[0].Role != "system" || u.Messages[0].Content != "系统提示" {
		t.Fatalf("system = %+v", u.Messages[0])
	}
	ass := u.Messages[2]
	if ass.Role != "assistant" || len(ass.ToolCalls) != 1 || ass.ToolCalls[0].ID != "tu_1" {
		t.Fatalf("assistant = %+v", ass)
	}
	if !strings.Contains(ass.ToolCalls[0].Function.Arguments, "北京") {
		t.Fatalf("args = %q", ass.ToolCalls[0].Function.Arguments)
	}
	tool := u.Messages[3]
	if tool.Role != "tool" || tool.ToolCallID != "tu_1" || tool.Content != "晴" {
		t.Fatalf("tool = %+v", tool)
	}
	if len(u.Tools) != 1 || u.Tools[0].Function.Name != "get_weather" {
		t.Fatalf("tools = %+v", u.Tools)
	}
	if tc, ok := u.ToolChoice.(map[string]interface{}); !ok || tc["function"].(map[string]interface{})["name"] != "get_weather" {
		t.Fatalf("tool_choice = %+v", u.ToolChoice)
	}
}

// TestAnthropicParseRequestSystemBlocks system 为内容块数组时须与字符串形态等价。
// Claude Code 等客户端恒发数组形态并带 cache_control;若只在字段上做类型断言,
// 数组形态会静默退化成空串,提示词无声丢失,比直接报错更难排查
func TestAnthropicParseRequestSystemBlocks(t *testing.T) {
	body := `{
	  "model":"gpt-x","max_tokens":64,
	  "system":[
	    {"type":"text","text":"第一段","cache_control":{"type":"ephemeral"}},
	    {"type":"text","text":"第二段"}
	  ],
	  "messages":[{"role":"user","content":"hi"}]
	}`
	u, err := ParseAnthropicRequest([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(u.Messages) != 2 {
		t.Fatalf("messages = %d; want 2(system+user)", len(u.Messages))
	}
	if u.Messages[0].Role != "system" || u.Messages[0].Content != "第一段\n第二段" {
		t.Fatalf("system = %+v", u.Messages[0])
	}
}

// TestAnthropicParseRequestSystemAbsent 无 system 或 system 为 null 时不得编造 system 消息
func TestAnthropicParseRequestSystemAbsent(t *testing.T) {
	for _, body := range []string{
		`{"model":"gpt-x","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`,
		`{"model":"gpt-x","max_tokens":64,"system":null,"messages":[{"role":"user","content":"hi"}]}`,
		`{"model":"gpt-x","max_tokens":64,"system":[],"messages":[{"role":"user","content":"hi"}]}`,
	} {
		u, err := ParseAnthropicRequest([]byte(body))
		if err != nil {
			t.Fatalf("parse %s: %v", body, err)
		}
		if len(u.Messages) != 1 || u.Messages[0].Role != "user" {
			t.Fatalf("body %s → messages = %+v; want 仅 user", body, u.Messages)
		}
	}
}

// TestAnthropicParseResponse 上游 Anthropic 响应 → 统一(文本 + tool_use)
func TestAnthropicParseResponse(t *testing.T) {
	body := `{"id":"msg_1","type":"message","role":"assistant","model":"claude-x",
	  "content":[{"type":"text","text":"我来查"},{"type":"tool_use","id":"tu_1","name":"get_weather","input":{"city":"北京"}}],
	  "stop_reason":"tool_use","stop_sequence":null,
	  "usage":{"input_tokens":11,"output_tokens":7}}`
	a := NewAnthropicAdapter()
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}
	ur, err := a.TransformResponse(resp)
	if err != nil {
		t.Fatalf("TransformResponse: %v", err)
	}
	if ur.ID != "msg_1" || ur.Model != "claude-x" {
		t.Fatalf("head = %+v", ur)
	}
	if len(ur.Choices) != 1 {
		t.Fatalf("choices = %d", len(ur.Choices))
	}
	ch := ur.Choices[0]
	if ch.FinishReason != "tool_calls" {
		t.Fatalf("finish_reason = %q; want tool_calls", ch.FinishReason)
	}
	if ch.Message.Content != "我来查" || len(ch.Message.ToolCalls) != 1 {
		t.Fatalf("message = %+v", ch.Message)
	}
	if ur.Usage == nil || ur.Usage.PromptTokens != 11 || ur.Usage.CompletionTokens != 7 || ur.Usage.TotalTokens != 18 {
		t.Fatalf("usage = %+v", ur.Usage)
	}
	// body 已被恢复(ParseTokenUsage 可再次读)
	p, c, tot := a.ParseTokenUsage(resp)
	if p != 11 || c != 7 || tot != 18 {
		t.Fatalf("ParseTokenUsage = %d,%d,%d", p, c, tot)
	}
}

// TestAnthropicEncodeResponse 统一响应 → Anthropic Message(stop_reason/usage/content blocks)
func TestAnthropicEncodeResponse(t *testing.T) {
	u := &UnifiedResponse{
		ID: "chatcmpl-1", Model: "upstream-x",
		Choices: []Choice{{Index: 0, FinishReason: "tool_calls", Message: Message{
			Role: "assistant", Content: "好的",
			ToolCalls: []ToolCall{{ID: "call_1", Type: "function", Function: ToolCallFunction{Name: "get_weather", Arguments: `{"city":"北京"}`}}},
		}}},
		Usage: &TokenUsage{PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8},
	}
	body, err := AnthropicEncodeResponse(u, "upstream-x")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var msg struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Role       string `json:"role"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text,omitempty"`
			Name string `json:"name,omitempty"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Fatalf("not json: %v", err)
	}
	if msg.Type != "message" || msg.Role != "assistant" || msg.StopReason != "tool_use" {
		t.Fatalf("msg = %+v", msg)
	}
	if msg.Usage.InputTokens != 3 || msg.Usage.OutputTokens != 5 {
		t.Fatalf("usage = %+v", msg.Usage)
	}
	if len(msg.Content) != 2 || msg.Content[0].Type != "text" || msg.Content[1].Type != "tool_use" || msg.Content[1].Name != "get_weather" {
		t.Fatalf("content = %+v", msg.Content)
	}
}

func TestAnthropicParseError(t *testing.T) {
	body := `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`
	resp := &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader(body))}
	a := NewAnthropicAdapter()
	code, msg := a.ParseError(resp)
	if code != 401 || msg != "invalid x-api-key" {
		t.Fatalf("ParseError = %d,%q", code, msg)
	}
}

// TestAnthropicStreamDecode anthropic 事件 → openai chunk 序列(文本/工具/结束)
func TestAnthropicStreamDecode(t *testing.T) {
	d := NewAnthropicStreamDecoder()
	var collect []UnifiedSSEChunk
	events := []string{
		`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-x","content":[],"usage":{"input_tokens":12,"output_tokens":0}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"你好"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"世界"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"tu_1","name":"get_weather","input":{}}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"北京\"}"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":9}}`,
		`{"type":"message_stop"}`,
	}
	for _, ev := range events {
		cs, err := d.Decode([]byte(ev))
		if err != nil {
			t.Fatalf("decode %s: %v", ev, err)
		}
		collect = append(collect, cs...)
	}
	if len(collect) != 7 {
		t.Fatalf("chunks = %d; want 7(start role + 2 text + tool_start + 2 args + finish)", len(collect))
	}
	if collect[0].Choices[0].Delta.Role != "assistant" || collect[0].Model != "claude-x" {
		t.Fatalf("start = %+v", collect[0])
	}
	if collect[1].Choices[0].Delta.Content != "你好" || collect[2].Choices[0].Delta.Content != "世界" {
		t.Fatalf("text chunks = %+v %+v", collect[1], collect[2])
	}
	tc := collect[3].Choices[0].Delta.ToolCalls[0]
	if tc.Index != 0 || tc.ID != "tu_1" || tc.Function.Name != "get_weather" {
		t.Fatalf("tool start = %+v", tc)
	}
	if collect[6].Choices[0].FinishReason == nil || *collect[6].Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("finish = %+v", collect[6])
	}
}

// TestAnthropicSSEWriter openai chunk → anthropic 事件序列(文本块在前、工具块随后、收尾补 message_delta/message_stop)
func TestAnthropicSSEWriter(t *testing.T) {
	w := NewAnthropicSSEWriter("msg_1", "upstream-x")
	chunk := func(id, content, toolName, toolArgs string, fr *string) []byte {
		var tcs []ToolCall
		if toolName != "" || toolArgs != "" {
			tcs = []ToolCall{{Index: 0, ID: "call_1", Type: "function", Function: ToolCallFunction{Name: toolName, Arguments: toolArgs}}}
		}
		b, _ := json.Marshal(UnifiedSSEChunk{
			ID: id, Object: "chat.completion.chunk", Model: "upstream-x",
			Choices: []SSEChoice{{Index: 0, Delta: Message{Content: content, ToolCalls: tcs}, FinishReason: fr}},
		})
		return b
	}
	evs := [][]byte{
		chunk("1", "", "", "", nil), // 仅 role
		chunk("2", "好的", "", "", nil),
		chunk("3", "", "get_weather", `{"city"`, nil),
		chunk("4", "", "", `:"北京"}`, strPtr("tool_calls")),
	}
	var out [][]byte
	for _, e := range evs {
		got, err := w.Process(e)
		if err != nil {
			t.Fatalf("process: %v", err)
		}
		out = append(out, got...)
	}
	tail, err := w.Finish()
	if err != nil {
		t.Fatalf("finish: %v", err)
	}
	out = append(out, tail...)

	var kinds []string
	for _, p := range out {
		var ev struct {
			Type         string `json:"type"`
			ContentBlock struct {
				Type string `json:"type"`
			} `json:"content_block"`
			Delta struct {
				Type string `json:"type"`
			} `json:"delta"`
		}
		if err := json.Unmarshal(p, &ev); err != nil {
			t.Fatalf("bad event %s: %v", p, err)
		}
		switch {
		case ev.Type == "message_start":
			kinds = append(kinds, "start")
		case ev.Type == "content_block_start" && ev.ContentBlock.Type == "tool_use":
			kinds = append(kinds, "tool_start")
		case ev.Type == "content_block_delta" && ev.Delta.Type == "text_delta":
			kinds = append(kinds, "text")
		case ev.Type == "content_block_delta" && ev.Delta.Type == "input_json_delta":
			kinds = append(kinds, "tool_args")
		case ev.Type == "content_block_stop":
			kinds = append(kinds, "cstop")
		case ev.Type == "message_delta":
			kinds = append(kinds, "mdelta")
		case ev.Type == "message_stop":
			kinds = append(kinds, "mstop")
		}
	}
	want := []string{"start", "text", "tool_start", "tool_args", "tool_args", "cstop", "cstop", "mdelta", "mstop"}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("事件序列 = %v; want %v", kinds, want)
	}
	// message_delta 的 stop_reason 应映射为 tool_use
	for _, p := range out {
		var md struct {
			Type  string `json:"type"`
			Delta struct {
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
		}
		_ = json.Unmarshal(p, &md)
		if md.Type == "message_delta" && md.Delta.StopReason != "tool_use" {
			t.Fatalf("message_delta stop_reason = %q", md.Delta.StopReason)
		}
	}
}

func TestAnthropicObserveUsage(t *testing.T) {
	a := NewAnthropicAdapter()
	p, c, ok := a.ObserveUsage([]byte(`{"type":"message_start","message":{"usage":{"input_tokens":12}}}`))
	if !ok || p != 12 || c != 0 {
		t.Fatalf("message_start = %d,%d,%v", p, c, ok)
	}
	p, c, ok = a.ObserveUsage([]byte(`{"type":"message_delta","usage":{"output_tokens":9}}`))
	if !ok || p != 0 || c != 9 {
		t.Fatalf("message_delta = %d,%d,%v", p, c, ok)
	}
	if p, c, ok = a.ObserveUsage([]byte(`{"type":"content_block_delta","delta":{"type":"text_delta","text":"x"}}`)); ok || p != 0 || c != 0 {
		t.Fatalf("非用量事件应 false: %v,%d,%d", ok, p, c)
	}
}

func strPtr(s string) *string { return &s }
