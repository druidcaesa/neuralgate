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

func TestBuiltinAdapters(t *testing.T) {
	cases := []struct {
		name    string
		adapter ModelAdapter
		native  bool
	}{
		{"openai", NewOpenAIAdapter(), true},
		{"tongyi", NewTongyiAdapter(), false},
		{"zhipu", NewZhipuAdapter(), false},
		{"deepseek", NewDeepSeekAdapter(), true},
	}
	for _, c := range cases {
		if c.adapter.Name() != c.name {
			t.Errorf("%s Name() = %q", c.name, c.adapter.Name())
		}
		if c.adapter.SupportsNativeProxy() != c.native {
			t.Errorf("%s SupportsNativeProxy() = %v, want %v", c.name, c.adapter.SupportsNativeProxy(), c.native)
		}
	}
}

func TestOpenAIParseTokenUsage(t *testing.T) {
	body := `{"id":"chatcmpl-1","object":"chat.completion","created":1700000000,"model":"gpt-4",
	  "choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],
	  "usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}
	a := NewOpenAIAdapter()
	p, c, total := a.ParseTokenUsage(resp)
	if p != 10 || c != 5 || total != 15 {
		t.Fatalf("ParseTokenUsage = %d,%d,%d; want 10,5,15", p, c, total)
	}
}

func TestOpenAIParseStreamUsage(t *testing.T) {
	chunk := `{"id":"chatcmpl-1","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4",
	  "choices":[{"index":0,"delta":{},"finish_reason":"stop"}],
	  "usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`
	a := NewOpenAIAdapter()
	p, c, total := a.ParseStreamUsage([]byte(chunk))
	if p != 10 || c != 5 || total != 15 {
		t.Fatalf("ParseStreamUsage = %d,%d,%d; want 10,5,15", p, c, total)
	}
	// 无 usage 的分片返回 0
	if p, c, total := a.ParseStreamUsage([]byte(`{"choices":[{"delta":{"content":"x"}}]}`)); p != 0 || c != 0 || total != 0 {
		t.Fatalf("ParseStreamUsage no-usage = %d,%d,%d; want 0,0,0", p, c, total)
	}
}

func TestOpenAIParseError(t *testing.T) {
	body := `{"error":{"message":"Incorrect API key","type":"invalid_request_error","param":null,"code":"invalid_api_key"}}`
	resp := &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader(body))}
	a := NewOpenAIAdapter()
	code, msg := a.ParseError(resp)
	if code != 401 || msg != "Incorrect API key" {
		t.Fatalf("ParseError = %d,%q", code, msg)
	}
}

func TestDeepSeekParseTokenUsage(t *testing.T) {
	// DeepSeek 与 OpenAI 格式一致
	body := `{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}
	a := NewDeepSeekAdapter()
	p, c, total := a.ParseTokenUsage(resp)
	if p != 2 || c != 1 || total != 3 {
		t.Fatalf("DeepSeek ParseTokenUsage = %d,%d,%d", p, c, total)
	}
}

func TestTongyiTransformRequest(t *testing.T) {
	a := NewTongyiAdapter()
	req := &UnifiedRequest{
		Model: "qwen-max", Messages: []Message{{Role: "user", Content: "你好"}},
		Temperature: float64Ptr(0.7), Stream: true,
	}
	httpReq, err := a.TransformRequest(req, nil)
	if err != nil {
		t.Fatalf("TransformRequest: %v", err)
	}
	var body map[string]interface{}
	if err := json.Unmarshal([]byte(mustRead(httpReq.Body)), &body); err != nil {
		t.Fatalf("body not json: %v", err)
	}
	if body["model"] != "qwen-max" {
		t.Fatalf("model = %v; want qwen-max", body["model"])
	}
	input := body["input"].(map[string]interface{})
	msgs := input["messages"].([]interface{})
	first := msgs[0].(map[string]interface{})
	if first["role"] != "user" || first["content"] != "你好" {
		t.Fatalf("message = %v", first)
	}
	params := body["parameters"].(map[string]interface{})
	if params["temperature"] != 0.7 {
		t.Fatalf("temperature = %v; want 0.7", params["temperature"])
	}
}

func TestTongyiTransformResponse(t *testing.T) {
	a := NewTongyiAdapter()
	body := `{"output":{"choices":[{"message":{"role":"assistant","content":"你好，世界"}}],
	  "usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}},"request_id":"r1"}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}
	ur, err := a.TransformResponse(resp)
	if err != nil {
		t.Fatalf("TransformResponse: %v", err)
	}
	if len(ur.Choices) != 1 || ur.Choices[0].Message.Content != "你好，世界" {
		t.Fatalf("choices = %+v", ur.Choices)
	}
	if ur.Usage == nil || ur.Usage.PromptTokens != 5 || ur.Usage.CompletionTokens != 3 || ur.Usage.TotalTokens != 8 {
		t.Fatalf("usage = %+v", ur.Usage)
	}
	if ur.Object != "chat.completion" {
		t.Fatalf("object = %q", ur.Object)
	}
}

func TestTongyiTransformResponseFinishReason(t *testing.T) {
	// DashScope 非流式响应透传 finish_reason,不硬编码 "stop"
	a := NewTongyiAdapter()
	body := `{"output":{"choices":[{"message":{"role":"assistant","content":"截断了"},"finish_reason":"length"}],
	  "usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}},"request_id":"r1"}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}
	ur, err := a.TransformResponse(resp)
	if err != nil {
		t.Fatalf("TransformResponse: %v", err)
	}
	if ur.Choices[0].FinishReason != "length" {
		t.Fatalf("finish_reason = %q; want length", ur.Choices[0].FinishReason)
	}
}

func TestTongyiToDashMessagesInterfaceContent(t *testing.T) {
	// 内核经 json.Unmarshal 构造的 UnifiedRequest:content 为 []interface{}(元素为 map)
	content := []interface{}{
		map[string]interface{}{"type": "text", "text": "hi"},
		map[string]interface{}{"type": "image_url", "image_url": map[string]interface{}{"url": "x"}},
		map[string]interface{}{"type": "input_audio", "input_audio": map[string]interface{}{"data": "y"}},
	}
	msgs := toDashMessages([]Message{{Role: "user", Content: content}})
	if len(msgs) != 1 || msgs[0].Content != "hi[image][audio]" {
		t.Fatalf("toDashMessages content = %q; want hi[image][audio]", msgs[0].Content)
	}
}

func TestZhipuTransformRequest(t *testing.T) {
	a := NewZhipuAdapter()
	req := &UnifiedRequest{Model: "glm-4", Messages: []Message{{Role: "user", Content: "hi"}}}
	httpReq, err := a.TransformRequest(req, nil)
	if err != nil {
		t.Fatalf("TransformRequest: %v", err)
	}
	var body map[string]interface{}
	_ = json.Unmarshal([]byte(mustRead(httpReq.Body)), &body)
	if body["model"] != "glm-4" {
		t.Fatalf("model = %v; want glm-4", body["model"])
	}
	msgs := body["messages"].([]interface{})
	if msgs[0].(map[string]interface{})["role"] != "user" {
		t.Fatalf("messages = %v", msgs)
	}
}

func TestZhipuTransformResponse(t *testing.T) {
	a := NewZhipuAdapter()
	body := `{"choices":[{"message":{"role":"assistant","content":"hi there"},"finish_reason":"stop"}],
	  "usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}
	ur, err := a.TransformResponse(resp)
	if err != nil {
		t.Fatalf("TransformResponse: %v", err)
	}
	if ur.Choices[0].Message.Content != "hi there" {
		t.Fatalf("content = %+v", ur.Choices[0].Message)
	}
	if ur.Usage.TotalTokens != 6 {
		t.Fatalf("usage = %+v", ur.Usage)
	}
}

func TestZhipuToZhipuMessagesInterfaceContent(t *testing.T) {
	// 内核经 json.Unmarshal 构造的 UnifiedRequest:content 为 []interface{}(元素为 map)
	content := []interface{}{
		map[string]interface{}{"type": "text", "text": "hi"},
		map[string]interface{}{"type": "image_url", "image_url": map[string]interface{}{"url": "x"}},
		map[string]interface{}{"type": "input_audio", "input_audio": map[string]interface{}{"data": "y"}},
	}
	msgs := toZhipuMessages([]Message{{Role: "user", Content: content}})
	if len(msgs) != 1 || msgs[0].Content != "hi[image][audio]" {
		t.Fatalf("toZhipuMessages content = %q; want hi[image][audio]", msgs[0].Content)
	}
}

func float64Ptr(f float64) *float64 { return &f }

func mustRead(r io.Reader) string {
	b, _ := io.ReadAll(r)
	return string(b)
}
