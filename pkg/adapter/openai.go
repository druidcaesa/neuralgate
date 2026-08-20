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
	"errors"
	"io"
	"net/http"
)

// OpenAIAdapter OpenAI 适配器（原生兼容：入口协议与上游一致，原样透传）
type OpenAIAdapter struct{}

// NewOpenAIAdapter 创建 OpenAI 适配器
func NewOpenAIAdapter() *OpenAIAdapter { return &OpenAIAdapter{} }

func (a *OpenAIAdapter) Name() string { return "openai" }

func (a *OpenAIAdapter) SupportsNativeProxy() bool { return true }

// TransformRequest 原生透传模式不调用;保留接口签名
func (a *OpenAIAdapter) TransformRequest(req *UnifiedRequest, rawBody []byte) (*http.Request, error) {
	return nil, errors.New("native proxy only")
}

// TransformResponse 原生透传不调用
func (a *OpenAIAdapter) TransformResponse(resp *http.Response) (*UnifiedResponse, error) {
	return nil, errors.New("native proxy only")
}

// TransformStreamChunk 原生透传不调用
func (a *OpenAIAdapter) TransformStreamChunk(chunk []byte) (*UnifiedSSEChunk, error) {
	return nil, errors.New("native proxy only")
}

// usageBody 非流式响应体(仅取 usage 字段)
type usageBody struct {
	Usage *TokenUsage `json:"usage"`
}

// ParseTokenUsage 从非流式响应体解析 Token 用量(OpenAI 格式)
func (a *OpenAIAdapter) ParseTokenUsage(resp *http.Response) (int, int, int) {
	if resp == nil || resp.Body == nil {
		return 0, 0, 0
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, 0, 0
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	var ub usageBody
	if err := json.Unmarshal(body, &ub); err != nil || ub.Usage == nil {
		return 0, 0, 0
	}
	return ub.Usage.PromptTokens, ub.Usage.CompletionTokens, ub.Usage.TotalTokens
}

// ParseStreamUsage 从流式最后一个分片解析 Token 用量(含 usage 字段的分片)
func (a *OpenAIAdapter) ParseStreamUsage(chunk []byte) (int, int, int) {
	var ub usageBody
	if err := json.Unmarshal(chunk, &ub); err != nil || ub.Usage == nil {
		return 0, 0, 0
	}
	return ub.Usage.PromptTokens, ub.Usage.CompletionTokens, ub.Usage.TotalTokens
}

// errorBody OpenAI 错误响应体
type errorBody struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

// ParseError 解析错误状态码与消息;无法解析时返回 (0, "")
func (a *OpenAIAdapter) ParseError(resp *http.Response) (int, string) {
	if resp == nil || resp.Body == nil {
		return 0, ""
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, ""
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	var eb errorBody
	if err := json.Unmarshal(body, &eb); err != nil || eb.Error.Message == "" {
		return 0, ""
	}
	return resp.StatusCode, eb.Error.Message
}
