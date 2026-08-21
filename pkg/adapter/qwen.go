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
	"bytes"
	"encoding/json"
	"io"
	"net/http"
)

// QwenAdapter Qwen(通义千问)适配器（DashScope 协议，需转换）
type QwenAdapter struct{}

// NewQwenAdapter 创建通义千问适配器
func NewQwenAdapter() *QwenAdapter { return &QwenAdapter{} }

func (a *QwenAdapter) Name() string { return "qwen" }

func (a *QwenAdapter) SupportsNativeProxy() bool { return false }

// dashScopeMessage DashScope 消息
type dashScopeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// dashScopeRequest DashScope chat 请求体
type dashScopeRequest struct {
	Model      string         `json:"model"`
	Input      map[string]any `json:"input"`
	Parameters map[string]any `json:"parameters,omitempty"`
}

// TransformRequest OpenAI 格式 → DashScope 格式
func (a *QwenAdapter) TransformRequest(req *UnifiedRequest, rawBody []byte) (*http.Request, error) {
	dash := dashScopeRequest{
		Model: req.Model,
		Input: map[string]any{"messages": toDashMessages(req.Messages)},
	}
	params := map[string]any{}
	if req.Temperature != nil {
		params["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		params["top_p"] = *req.TopP
	}
	if req.MaxTokens != nil {
		params["max_tokens"] = *req.MaxTokens
	}
	if len(params) > 0 {
		dash.Parameters = params
	}
	body, err := json.Marshal(dash)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequest(http.MethodPost, "", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	return httpReq, nil
}

func toDashMessages(msgs []Message) []dashScopeMessage {
	out := make([]dashScopeMessage, 0, len(msgs))
	for _, m := range msgs {
		content := ""
		switch c := m.Content.(type) {
		case string:
			content = c
		case []ContentPart:
			var sb bytes.Buffer
			for _, p := range c {
				if p.Type == "text" || p.Text != "" {
					sb.WriteString(p.Text)
				} else if p.ImageURL != nil {
					sb.WriteString("[image]")
				} else if p.InputAudio != nil {
					sb.WriteString("[audio]")
				}
			}
			content = sb.String()
		case []interface{}:
			// 内核经 json.Unmarshal 构造的 UnifiedRequest,content 为 []interface{}(元素为 map)
			var sb bytes.Buffer
			for _, item := range c {
				mm, ok := item.(map[string]interface{})
				if !ok {
					continue
				}
				if text, ok := mm["text"].(string); ok {
					sb.WriteString(text)
					continue
				}
				if t, _ := mm["type"].(string); t == "image_url" || mm["image_url"] != nil {
					sb.WriteString("[image]")
				} else if t == "input_audio" || mm["input_audio"] != nil {
					sb.WriteString("[audio]")
				}
			}
			content = sb.String()
		}
		out = append(out, dashScopeMessage{Role: m.Role, Content: content})
	}
	return out
}

// dashScopeResponse DashScope 非流式响应体
type dashScopeResponse struct {
	Output struct {
		Choices []struct {
			Message      dashScopeMessage `json:"message"`
			FinishReason string           `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
	} `json:"output"`
}

// TransformResponse DashScope → OpenAI 格式（非流式）
func (a *QwenAdapter) TransformResponse(resp *http.Response) (*UnifiedResponse, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	var ds dashScopeResponse
	if err := json.Unmarshal(body, &ds); err != nil {
		return nil, err
	}
	ur := &UnifiedResponse{Object: "chat.completion"}
	// resp.Request 可能为 nil（如测试直接构造），取不到 ID/Model 时留空
	if resp.Request != nil {
		ur.ID = "chatcmpl-" + resp.Request.Header.Get("X-Request-Id")
		ur.Model = resp.Request.Header.Get("X-Model")
	}
	for i, c := range ds.Output.Choices {
		ur.Choices = append(ur.Choices, Choice{
			Index:        i,
			Message:      Message{Role: c.Message.Role, Content: c.Message.Content},
			FinishReason: c.FinishReason,
		})
	}
	ur.Usage = &TokenUsage{
		PromptTokens:     ds.Output.Usage.InputTokens,
		CompletionTokens: ds.Output.Usage.OutputTokens,
		TotalTokens:      ds.Output.Usage.TotalTokens,
	}
	return ur, nil
}

// TransformStreamChunk DashScope 流式分片 → OpenAI 格式
func (a *QwenAdapter) TransformStreamChunk(chunk []byte) (*UnifiedSSEChunk, error) {
	var ds struct {
		Output struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
					Role    string `json:"role"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		} `json:"output"`
	}
	if err := json.Unmarshal(chunk, &ds); err != nil {
		return nil, err
	}
	usc := &UnifiedSSEChunk{Object: "chat.completion.chunk"}
	for i, c := range ds.Output.Choices {
		delta := Message{}
		if c.Delta.Role != "" {
			delta.Role = c.Delta.Role
		}
		if c.Delta.Content != "" {
			delta.Content = c.Delta.Content
		}
		var fr *string
		if c.FinishReason != "" {
			fr = &c.FinishReason
		}
		usc.Choices = append(usc.Choices, SSEChoice{Index: i, Delta: delta, FinishReason: fr})
	}
	return usc, nil
}

// ParseTokenUsage 从已读 body 解析 DashScope 用量
func (a *QwenAdapter) ParseTokenUsage(resp *http.Response) (int, int, int) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, 0, 0
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	var ds dashScopeResponse
	if err := json.Unmarshal(body, &ds); err != nil {
		return 0, 0, 0
	}
	return ds.Output.Usage.InputTokens, ds.Output.Usage.OutputTokens, ds.Output.Usage.TotalTokens
}

// ParseStreamUsage DashScope 流式分片暂无用量，返回 0
func (a *QwenAdapter) ParseStreamUsage(chunk []byte) (int, int, int) { return 0, 0, 0 }

// ParseError DashScope 错误格式：{"code":"...","message":"..."}
func (a *QwenAdapter) ParseError(resp *http.Response) (int, string) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, ""
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	var eb struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &eb); err != nil || eb.Message == "" {
		return 0, ""
	}
	return resp.StatusCode, eb.Message
}
