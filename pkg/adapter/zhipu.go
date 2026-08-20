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

// ZhipuAdapter 智谱 AI 适配器（异构上游，需转换）
type ZhipuAdapter struct{}

// NewZhipuAdapter 创建智谱 AI 适配器
func NewZhipuAdapter() *ZhipuAdapter { return &ZhipuAdapter{} }

func (a *ZhipuAdapter) Name() string { return "zhipu" }

func (a *ZhipuAdapter) SupportsNativeProxy() bool { return false }

// zhipuMessage GLM 消息（内容扁平化为 string）
type zhipuMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// zhipuRequest GLM chat 请求体（结构接近 OpenAI）
type zhipuRequest struct {
	Model       string         `json:"model"`
	Messages    []zhipuMessage `json:"messages"`
	Stream      bool           `json:"stream,omitempty"`
	Temperature *float64       `json:"temperature,omitempty"`
	TopP        *float64       `json:"top_p,omitempty"`
	MaxTokens   *int           `json:"max_tokens,omitempty"`
}

// TransformRequest OpenAI 格式 → GLM 格式
func (a *ZhipuAdapter) TransformRequest(req *UnifiedRequest, rawBody []byte) (*http.Request, error) {
	zr := zhipuRequest{
		Model:       req.Model,
		Messages:    toZhipuMessages(req.Messages),
		Stream:      req.Stream,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		MaxTokens:   req.MaxTokens,
	}
	body, err := json.Marshal(zr)
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

func toZhipuMessages(msgs []Message) []zhipuMessage {
	out := make([]zhipuMessage, 0, len(msgs))
	for _, m := range msgs {
		content := ""
		switch c := m.Content.(type) {
		case string:
			content = c
		case []ContentPart:
			for _, p := range c {
				if p.Text != "" {
					content += p.Text
				} else if p.ImageURL != nil {
					content += "[image]"
				} else if p.InputAudio != nil {
					content += "[audio]"
				}
			}
		}
		out = append(out, zhipuMessage{Role: m.Role, Content: content})
	}
	return out
}

// zhipuResponse GLM 非流式响应体
type zhipuResponse struct {
	Choices []struct {
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// TransformResponse GLM → OpenAI 格式（非流式）
func (a *ZhipuAdapter) TransformResponse(resp *http.Response) (*UnifiedResponse, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	var zr zhipuResponse
	if err := json.Unmarshal(body, &zr); err != nil {
		return nil, err
	}
	ur := &UnifiedResponse{Object: "chat.completion"}
	for i, c := range zr.Choices {
		ur.Choices = append(ur.Choices, Choice{
			Index:        i,
			Message:      Message{Role: c.Message.Role, Content: c.Message.Content},
			FinishReason: c.FinishReason,
		})
	}
	ur.Usage = &TokenUsage{
		PromptTokens:     zr.Usage.PromptTokens,
		CompletionTokens: zr.Usage.CompletionTokens,
		TotalTokens:      zr.Usage.TotalTokens,
	}
	return ur, nil
}

// TransformStreamChunk GLM 流式分片 → OpenAI 格式（choices[].delta 与 OpenAI 一致）
func (a *ZhipuAdapter) TransformStreamChunk(chunk []byte) (*UnifiedSSEChunk, error) {
	var usc UnifiedSSEChunk
	if err := json.Unmarshal(chunk, &usc); err != nil {
		return nil, err
	}
	usc.Object = "chat.completion.chunk"
	return &usc, nil
}

// ParseTokenUsage 解析 GLM 用量
func (a *ZhipuAdapter) ParseTokenUsage(resp *http.Response) (int, int, int) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, 0, 0
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	var zr zhipuResponse
	if err := json.Unmarshal(body, &zr); err != nil {
		return 0, 0, 0
	}
	return zr.Usage.PromptTokens, zr.Usage.CompletionTokens, zr.Usage.TotalTokens
}

// ParseStreamUsage 从含 usage 的流式分片解析
func (a *ZhipuAdapter) ParseStreamUsage(chunk []byte) (int, int, int) {
	var zr zhipuResponse
	if err := json.Unmarshal(chunk, &zr); err != nil {
		return 0, 0, 0
	}
	return zr.Usage.PromptTokens, zr.Usage.CompletionTokens, zr.Usage.TotalTokens
}

// ParseError GLM 错误格式：{"error":{"message":"..."}}
func (a *ZhipuAdapter) ParseError(resp *http.Response) (int, string) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, ""
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	var eb struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &eb); err != nil || eb.Error.Message == "" {
		return 0, ""
	}
	return resp.StatusCode, eb.Error.Message
}
