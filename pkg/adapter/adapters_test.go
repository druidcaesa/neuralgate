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
