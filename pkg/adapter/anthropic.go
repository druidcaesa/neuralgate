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
	"strings"
)

// 上游/入口协议名。协议判定以「能理解的语言」为准：
//   - anthropic:上游为 Anthropic Messages,需专用路径/鉴权/默认 max_tokens
//   - 其余(openai 兼容族 + 内置转换供应商)一律视为 OpenAI 形,保持现有透传行为
const (
	ProtocolAnthropic = "anthropic"
)

// AnthropicAdapter Anthropic 上游适配器(Messages 协议,需转换)。
// 同时作为统一格式 ↔ Anthropic wire 的编解码宿主;流式转换无状态由
// AnthropicStreamDecoder / AnthropicSSEWriter 承载(每次请求 new 实例)。
type AnthropicAdapter struct{}

// NewAnthropicAdapter 创建 Anthropic 适配器
func NewAnthropicAdapter() *AnthropicAdapter { return &AnthropicAdapter{} }

func (a *AnthropicAdapter) Name() string { return ProtocolAnthropic }

func (a *AnthropicAdapter) SupportsNativeProxy() bool { return false }

// Protocol 声明上游协议为 Anthropic(proxy 用其挑选路径/鉴权/默认注入)
func (a *AnthropicAdapter) Protocol() string { return ProtocolAnthropic }

// IsAnthropicProtocol 判断适配器是否声明 Anthropic 上游协议
func IsAnthropicProtocol(a ModelAdapter) bool {
	p, ok := a.(interface{ Protocol() string })
	return ok && p.Protocol() == ProtocolAnthropic
}

// ===== Anthropic wire 数据结构(仅覆盖 v1 聊天+工具子集) =====

type anthropicTextBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type anthropicBlock struct {
	Type      string          `json:"type"` // text / tool_use / tool_result
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`          // tool_use / tool_result
	Name      string          `json:"name,omitempty"`        // tool_use
	Input     json.RawMessage `json:"input,omitempty"`       // tool_use(对象)
	ToolUseID string          `json:"tool_use_id,omitempty"` // tool_result
	IsError   *bool           `json:"is_error,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"` // tool_result(对象/字符串)
}

type anthropicRequest struct {
	Model         string                `json:"model"`
	MaxTokens     *int                  `json:"max_tokens,omitempty"` // anthropic 必填;缺省由代理层按模型默认注入
	Temperature   *float64              `json:"temperature,omitempty"`
	TopP          *float64              `json:"top_p,omitempty"`
	StopSequences []string              `json:"stop_sequences,omitempty"`
	Stream        bool                  `json:"stream,omitempty"`
	System        string                `json:"system,omitempty"`
	Messages      []anthropicRawMessage `json:"messages"`
	Tools         []anthropicTool       `json:"tools,omitempty"`
	ToolChoice    *anthropicToolChoice  `json:"tool_choice,omitempty"`
	Metadata      *anthropicMetadata    `json:"metadata,omitempty"`
}

// anthropicRawMessage content 保留原文(兼容 string 与 []block 两种形态)
type anthropicRawMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content,omitempty"`
}

type anthropicTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

type anthropicToolChoice struct {
	Type string `json:"type"` // auto / any / tool / none
	Name string `json:"name,omitempty"`
}

type anthropicMetadata struct {
	UserID string `json:"user_id,omitempty"`
}

// anthropicMessage 非流式响应 / 流 message_start 里的 message
type anthropicMessage struct {
	ID           string           `json:"id"`
	Type         string           `json:"type"`
	Role         string           `json:"role"`
	Model        string           `json:"model"`
	Content      []anthropicBlock `json:"content"`
	StopReason   *string          `json:"stop_reason"`
	StopSequence *string          `json:"stop_sequence"`
	Usage        anthropicUsage   `json:"usage"`
}

type anthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

// TransformRequest 统一(OpenAI 形)→ Anthropic Messages 请求体
func (a *AnthropicAdapter) TransformRequest(req *UnifiedRequest, rawBody []byte) (*http.Request, error) {
	body, err := AnthropicEncodeRequest(req, req.Model)
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

// AnthropicEncodeRequest 统一格式 → Anthropic 请求 JSON。model 传上游 ProviderModel。
func AnthropicEncodeRequest(u *UnifiedRequest, model string) ([]byte, error) {
	req := anthropicRequest{Model: model, Stream: u.Stream}
	// 采样与输出控制(客户端显式值原样带;v1 不支持的字段丢弃)
	req.Temperature = u.Temperature
	req.TopP = u.TopP
	if len(u.Stop) > 0 {
		req.StopSequences = u.Stop
	}
	if u.MaxTokens != nil {
		req.MaxTokens = u.MaxTokens
	} else if u.MaxCompletionTokens != nil {
		req.MaxTokens = u.MaxCompletionTokens
	}
	if u.User != "" {
		req.Metadata = &anthropicMetadata{UserID: u.User}
	}

	var systemParts []string
	var conv []anthropicBlockMessage
	for _, m := range u.Messages {
		switch m.Role {
		case "system", "developer":
			if s := flattenText(m.Content); s != "" {
				systemParts = append(systemParts, s)
			}
		case "tool":
			// OpenAI tool 消息 → user 消息里的 tool_result 块(连续多条并入同一 user)
			pushBlocks(&conv, "user", toolResultBlock(m))
		case "user":
			pushBlocks(&conv, "user", anthropicBlock{Type: "text", Text: flattenText(m.Content)})
		case "assistant":
			pushBlocks(&conv, "assistant", assistantBlocks(m)...)
		default:
			pushBlocks(&conv, "user", anthropicBlock{Type: "text", Text: flattenText(m.Content)})
		}
	}
	if len(systemParts) > 0 {
		req.System = strings.Join(systemParts, "\n")
	}
	// system/developer 之外的对话逐条物化为 anthropic 消息
	req.Messages = make([]anthropicRawMessage, 0, len(conv))
	for _, m := range conv {
		raw, _ := json.Marshal(m.Content)
		req.Messages = append(req.Messages, anthropicRawMessage{Role: m.Role, Content: raw})
	}

	if len(u.Tools) > 0 {
		for _, t := range u.Tools {
			if t.Type != "function" && t.Type != "" {
				continue
			}
			req.Tools = append(req.Tools, anthropicTool{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				InputSchema: t.Function.Parameters,
			})
		}
	}
	if u.ToolChoice != nil {
		if tc := mapOpenAIToolChoice(u.ToolChoice); tc != nil {
			req.ToolChoice = tc
		}
	}
	return json.Marshal(req)
}

// anthropicBlockMessage 构建中的 message(role 仅 user/assistant)
type anthropicBlockMessage struct {
	Role    string
	Content []anthropicBlock
}

// pushBlocks 把块并入同 role 的最后一条消息(天然合并连续同 role),否则新起一条
func pushBlocks(conv *[]anthropicBlockMessage, role string, blocks ...anthropicBlock) {
	if len(blocks) == 0 {
		return
	}
	if n := len(*conv); n > 0 && (*conv)[n-1].Role == role {
		(*conv)[n-1].Content = append((*conv)[n-1].Content, blocks...)
		return
	}
	*conv = append(*conv, anthropicBlockMessage{Role: role, Content: blocks})
}

// toolResultBlock OpenAI tool 消息 → anthropic tool_result 块
func toolResultBlock(m Message) anthropicBlock {
	content, _ := json.Marshal(flattenText(m.Content))
	return anthropicBlock{Type: "tool_result", ToolUseID: m.ToolCallID, Content: content}
}

// assistantBlocks 把 assistant 文本 + 工具调用转成 anthropic 内容块(文本在前、tool_use 在后)
func assistantBlocks(m Message) []anthropicBlock {
	var out []anthropicBlock
	if s := flattenText(m.Content); s != "" {
		out = append(out, anthropicBlock{Type: "text", Text: s})
	}
	for _, tc := range m.ToolCalls {
		name := tc.Function.Name
		if name == "" && tc.ID == "" {
			continue
		}
		// arguments 为 JSON 字符串,解析成对象塞回 input(解析失败用空对象,别丢请求)
		input := json.RawMessage(`{}`)
		if s := strings.TrimSpace(tc.Function.Arguments); s != "" {
			if json.Valid([]byte(s)) {
				input = json.RawMessage(s)
			}
		}
		out = append(out, anthropicBlock{Type: "tool_use", ID: tc.ID, Name: name, Input: input})
	}
	return out
}

// mapOpenAIToolChoice openai tool_choice → anthropic;不支持形态返回 nil(不注入)
func mapOpenAIToolChoice(v interface{}) *anthropicToolChoice {
	switch tc := v.(type) {
	case string:
		switch tc {
		case "auto":
			return &anthropicToolChoice{Type: "auto"}
		case "none":
			return &anthropicToolChoice{Type: "none"}
		case "required":
			return &anthropicToolChoice{Type: "any"}
		}
	case map[string]interface{}:
		if t, _ := tc["type"].(string); t == "function" {
			if fn, ok := tc["function"].(map[string]interface{}); ok {
				if name, _ := fn["name"].(string); name != "" {
					return &anthropicToolChoice{Type: "tool", Name: name}
				}
			}
		}
	}
	return nil
}

// flattenText 把 Message.Content(string / []ContentPart / json 解码出的 []interface{})摊平成文本;
// 图片/音频 v1 不支持,直接丢弃
func flattenText(v interface{}) string {
	switch c := v.(type) {
	case nil:
		return ""
	case string:
		return c
	case []ContentPart:
		var sb strings.Builder
		for _, p := range c {
			if p.Text != "" {
				sb.WriteString(p.Text)
			}
		}
		return sb.String()
	case []interface{}:
		var sb strings.Builder
		for _, it := range c {
			if mm, ok := it.(map[string]interface{}); ok {
				if s, _ := mm["text"].(string); s != "" {
					sb.WriteString(s)
				}
			}
		}
		return sb.String()
	default:
		if b, err := json.Marshal(c); err == nil {
			return string(b)
		}
	}
	return ""
}

// ParseRequestBody Anthropic 入口请求 → 统一格式(供反向转 OpenAI 上游)
func ParseAnthropicRequest(body []byte) (*UnifiedRequest, error) {
	var req anthropicRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	u := &UnifiedRequest{Model: req.Model, Stream: req.Stream}
	u.MaxTokens = req.MaxTokens
	u.Temperature = req.Temperature
	u.TopP = req.TopP
	u.Stop = req.StopSequences
	if req.Metadata != nil {
		u.User = req.Metadata.UserID
	}
	for _, t := range req.Tools {
		u.Tools = append(u.Tools, Tool{Type: "function", Function: ToolFunction{
			Name: t.Name, Description: t.Description, Parameters: t.InputSchema,
		}})
	}
	if req.ToolChoice != nil {
		u.ToolChoice = mapAnthropicToolChoice(req.ToolChoice)
	}

	// system 前置为一条 system 消息
	if sys := stringContent(req.System); sys != "" {
		u.Messages = append(u.Messages, Message{Role: "system", Content: sys})
	}
	for _, m := range req.Messages {
		content, err := decodeContent(m.Content)
		if err != nil {
			continue
		}
		switch m.Role {
		case "assistant":
			u.Messages = append(u.Messages, assistantFromAnthropic(content))
		case "tool": // 容错:极端输入直接当作 tool_result user
			appendToolResults(&u.Messages, content)
		default: // user 及未知
			appendUserFromAnthropic(&u.Messages, content)
		}
	}
	return u, nil
}

// stringContent string 或 [text blocks] → 文本
func stringContent(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	if blocks, ok := v.([]interface{}); ok {
		var sb strings.Builder
		for _, it := range blocks {
			if mm, ok := it.(map[string]interface{}); ok {
				if s, _ := mm["text"].(string); s != "" {
					sb.WriteString(s)
				}
			}
		}
		return sb.String()
	}
	return ""
}

// decodeContent anthropic 消息 content 解码为 []block(map 形式);兼容 string 与 block 数组
func decodeContent(raw json.RawMessage) ([]map[string]interface{}, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		return []map[string]interface{}{{"type": "text", "text": s}}, nil
	}
	var blocks []map[string]interface{}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, err
	}
	return blocks, nil
}

// appendUserFromAnthropic user 消息:文本 → user;tool_result → 拆成 role=tool 消息
// (tool 消息须紧跟声明其的 assistant;openai 语义与 anthropic 工具流一致)
func appendUserFromAnthropic(msgs *[]Message, content []map[string]interface{}) {
	var textParts []string
	var tools []Message
	for _, b := range content {
		switch b["type"] {
		case "tool_result":
			tools = append(tools, Message{
				Role:       "tool",
				ToolCallID: strOf(b["tool_use_id"]),
				Content:    contentToText(b["content"]),
			})
		case "text":
			if s := strOf(b["text"]); s != "" {
				textParts = append(textParts, s)
			}
		default: // image 等 v1 不支持,丢弃
		}
	}
	if len(tools) > 0 {
		*msgs = append(*msgs, tools...)
	}
	if t := strings.Join(textParts, "\n"); t != "" {
		*msgs = append(*msgs, Message{Role: "user", Content: t})
	}
}

// appendToolResults 容错:整体按 tool 消息追加(保 ToolCallID)
func appendToolResults(msgs *[]Message, content []map[string]interface{}) {
	for _, b := range content {
		if b["type"] != "tool_result" {
			continue
		}
		*msgs = append(*msgs, Message{
			Role: "tool", ToolCallID: strOf(b["tool_use_id"]), Content: contentToText(b["content"]),
		})
	}
}

// assistantFromAnthropic assistant 消息:文本拼 content, tool_use 转 tool_calls
func assistantFromAnthropic(content []map[string]interface{}) Message {
	msg := Message{Role: "assistant"}
	var textParts []string
	var toolCalls []ToolCall
	for _, b := range content {
		switch b["type"] {
		case "tool_use":
			input := b["input"]
			args := "{}"
			if raw, err := json.Marshal(input); err == nil {
				args = string(raw)
			}
			toolCalls = append(toolCalls, ToolCall{
				ID:   strOf(b["id"]),
				Type: "function",
				Function: ToolCallFunction{
					Name: strOf(b["name"]), Arguments: args,
				},
			})
		case "text":
			if s := strOf(b["text"]); s != "" {
				textParts = append(textParts, s)
			}
		}
	}
	if t := strings.Join(textParts, "\n"); t != "" {
		msg.Content = t
	}
	if len(toolCalls) > 0 {
		msg.ToolCalls = toolCalls
	}
	return msg
}

// contentToText tool_result content(string / [text blocks]) → 文本
func contentToText(v interface{}) string {
	return stringContent(v)
}

func strOf(v interface{}) string {
	s, _ := v.(string)
	return s
}

// mapAnthropicToolChoice anthropic tool_choice → openai 形态
func mapAnthropicToolChoice(tc *anthropicToolChoice) interface{} {
	switch tc.Type {
	case "auto":
		return "auto"
	case "none":
		return "none"
	case "any":
		return "required"
	case "tool":
		return map[string]interface{}{
			"type": "function", "function": map[string]interface{}{"name": tc.Name},
		}
	}
	return nil
}

// TransformResponse Anthropic 响应 → 统一格式(非流式)
func (a *AnthropicAdapter) TransformResponse(resp *http.Response) (*UnifiedResponse, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return AnthropicParseResponse(body)
}

// AnthropicParseResponse Anthropic Messages 响应 → 统一(OpenAI 形)
func AnthropicParseResponse(body []byte) (*UnifiedResponse, error) {
	var am anthropicMessage
	if err := json.Unmarshal(body, &am); err != nil {
		return nil, err
	}
	ur := &UnifiedResponse{ID: am.ID, Object: "chat.completion", Model: am.Model}
	msg := Message{Role: "assistant"}
	for _, b := range am.Content {
		switch b.Type {
		case "tool_use":
			args := "{}"
			if len(b.Input) > 0 && string(b.Input) != "null" {
				args = string(b.Input)
			}
			msg.ToolCalls = append(msg.ToolCalls, ToolCall{
				ID: b.ID, Type: "function",
				Function: ToolCallFunction{Name: b.Name, Arguments: args},
			})
		default: // text
			if msg.Content == nil {
				msg.Content = b.Text
			} else if s, ok := msg.Content.(string); ok && b.Text != "" {
				msg.Content = s + b.Text
			}
		}
	}
	ur.Choices = []Choice{{
		Index:        0,
		Message:      msg,
		FinishReason: mapAnthropicStopReason(am.StopReason),
	}}
	ur.Usage = &TokenUsage{
		PromptTokens:     am.Usage.InputTokens,
		CompletionTokens: am.Usage.OutputTokens,
		TotalTokens:      am.Usage.InputTokens + am.Usage.OutputTokens,
	}
	return ur, nil
}

// mapAnthropicStopReason anthropic stop_reason → openai finish_reason
func mapAnthropicStopReason(s *string) string {
	if s == nil {
		return ""
	}
	switch *s {
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	case "end_turn", "stop_sequence":
		return "stop"
	case "content_filter":
		return "content_filter"
	}
	return "stop"
}

// AnthropicEncodeResponse 统一/openai 响应 → Anthropic Message JSON。
// model 用于填充回显;ID 为空时派生自 model,保证确定性(供测试/审计断言)
func AnthropicEncodeResponse(u *UnifiedResponse, model string) ([]byte, error) {
	id := u.ID
	if id == "" {
		id = "msg_" + model
	}
	am := anthropicMessage{ID: id, Type: "message", Role: "assistant", Model: model}
	stopReason := "end_turn"
	if len(u.Choices) > 0 {
		ch := u.Choices[0]
		switch ch.FinishReason {
		case "length":
			stopReason = "max_tokens"
		case "tool_calls":
			stopReason = "tool_use"
		case "content_filter":
			stopReason = "content_filter"
		case "stop", "":
			stopReason = "end_turn"
		}
		am.Content = responseBlocks(ch.Message)
	} else {
		am.Content = []anthropicBlock{}
	}
	if u.Usage != nil {
		am.Usage = anthropicUsage{
			InputTokens:  u.Usage.PromptTokens,
			OutputTokens: u.Usage.CompletionTokens,
		}
	}
	am.StopReason = &stopReason
	return json.Marshal(am)
}

// responseBlocks 统一响应 assistant 消息 → anthropic 内容块(文本在前,tool_use 在后)
func responseBlocks(m Message) []anthropicBlock {
	var out []anthropicBlock
	if s := flattenText(m.Content); s != "" {
		out = append(out, anthropicBlock{Type: "text", Text: s})
	}
	for _, tc := range m.ToolCalls {
		input := json.RawMessage(`{}`)
		if s := strings.TrimSpace(tc.Function.Arguments); s != "" && json.Valid([]byte(s)) {
			input = json.RawMessage(s)
		}
		out = append(out, anthropicBlock{Type: "tool_use", ID: tc.ID, Name: tc.Function.Name, Input: input})
	}
	return out
}

// ParseTokenUsage 从已读 body 解析 Anthropic 用量(input/output)
func (a *AnthropicAdapter) ParseTokenUsage(resp *http.Response) (int, int, int) {
	if resp == nil || resp.Body == nil {
		return 0, 0, 0
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, 0, 0
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	var am anthropicMessage
	if err := json.Unmarshal(body, &am); err != nil {
		return 0, 0, 0
	}
	return am.Usage.InputTokens, am.Usage.OutputTokens, am.Usage.InputTokens + am.Usage.OutputTokens
}

// ParseStreamUsage 流式事件无单块完整用量(分散在 message_start/message_delta),
// 代理侧走 StreamUsageObserver 增量累计;此处恒 0 防误用
func (a *AnthropicAdapter) ParseStreamUsage(chunk []byte) (int, int, int) { return 0, 0, 0 }

// anthropicErrorBody Anthropic 错误响应体
type anthropicErrorBody struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// ParseError 解析 Anthropic 错误状态码与消息
func (a *AnthropicAdapter) ParseError(resp *http.Response) (int, string) {
	if resp == nil || resp.Body == nil {
		return 0, ""
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, ""
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	var eb anthropicErrorBody
	if err := json.Unmarshal(body, &eb); err != nil || eb.Error.Message == "" {
		return 0, ""
	}
	return resp.StatusCode, eb.Error.Message
}

// TransformStreamChunk 单事件负载 → OpenAI 统一分片(经一次性解码器);
// 代理流式解码走 NewAnthropicStreamDecoder 的状态ful版本,此处保接口语义
func (a *AnthropicAdapter) TransformStreamChunk(chunk []byte) (*UnifiedSSEChunk, error) {
	chunks, err := NewAnthropicStreamDecoder().Decode(chunk)
	if err != nil || len(chunks) == 0 {
		return nil, err
	}
	return &chunks[0], nil
}
