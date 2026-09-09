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
)

// StreamUsageObserver 可选能力:观察上游 SSE 事件负载里的 Token 用量。
// Anthropic 用量分散在 message_start(input_tokens)与 message_delta(output_tokens),
// 两事件都是「到该时刻为止的累计值」:命中即上报,代理取最后一次非零即可
type StreamUsageObserver interface {
	ObserveUsage(chunk []byte) (prompt, completion int, ok bool)
}

// ===== 上游 Anthropic SSE → OpenAI chunk 解码 =====

// AnthropicStreamDecoder 把 Anthropic 流式事件逐条转成 OpenAI chunk。
// 有状态(tool content block index → openai tool index 的映射、id/model 回填),
// 每次请求 new 一个实例,全局安全
type AnthropicStreamDecoder struct {
	id       string
	model    string
	toolIdx  map[int]int // anthropic content block index -> openai tool_calls index
	nextTool int
	stopSent bool
}

// NewAnthropicStreamDecoder 创建流解码器
func NewAnthropicStreamDecoder() *AnthropicStreamDecoder {
	return &AnthropicStreamDecoder{toolIdx: make(map[int]int)}
}

// Decode 处理一条 anthropic data 负载,返回 0..n 条 OpenAI chunk
func (d *AnthropicStreamDecoder) Decode(payload []byte) ([]UnifiedSSEChunk, error) {
	var ev struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(payload, &ev); err != nil {
		return nil, err
	}
	switch ev.Type {
	case "message_start":
		var e struct {
			Message anthropicMessage `json:"message"`
		}
		if err := json.Unmarshal(payload, &e); err != nil {
			return nil, err
		}
		d.id, d.model = e.Message.ID, e.Message.Model
		// openai 惯例首块带 role,后续块只给增量
		return []UnifiedSSEChunk{{
			ID: d.id, Object: "chat.completion.chunk", Model: d.model,
			Choices: []SSEChoice{{Index: 0, Delta: Message{Role: "assistant"}}},
		}}, nil
	case "content_block_start":
		var e struct {
			Index        int            `json:"index"`
			ContentBlock anthropicBlock `json:"content_block"`
		}
		if err := json.Unmarshal(payload, &e); err != nil {
			return nil, err
		}
		if e.ContentBlock.Type != "tool_use" {
			return nil, nil // text 块开始:内容随 text_delta 到,无独立产出
		}
		oi := d.openAIToolIndex(e.Index)
		delta := Message{ToolCalls: []ToolCall{{
			Index: oi, ID: e.ContentBlock.ID, Type: "function",
			Function: ToolCallFunction{Name: e.ContentBlock.Name},
		}}}
		return d.chunk(delta, nil), nil
	case "content_block_delta":
		var e struct {
			Index int             `json:"index"`
			Delta json.RawMessage `json:"delta"`
		}
		if err := json.Unmarshal(payload, &e); err != nil {
			return nil, err
		}
		var inner struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			PartialJSON string `json:"partial_json"`
		}
		if err := json.Unmarshal(e.Delta, &inner); err != nil {
			return nil, err
		}
		switch inner.Type {
		case "text_delta":
			return d.chunk(Message{Content: inner.Text}, nil), nil
		case "input_json_delta":
			delta := Message{ToolCalls: []ToolCall{{
				Index: d.openAIToolIndex(e.Index), Type: "function",
				Function: ToolCallFunction{Arguments: inner.PartialJSON},
			}}}
			return d.chunk(delta, nil), nil
		}
		return nil, nil
	case "message_delta":
		var e struct {
			Delta struct {
				StopReason   *string `json:"stop_reason"`
				StopSequence *string `json:"stop_sequence"`
			} `json:"delta"`
		}
		if err := json.Unmarshal(payload, &e); err != nil {
			return nil, err
		}
		if d.stopSent {
			return nil, nil
		}
		d.stopSent = true
		fr := mapAnthropicStopReason(e.Delta.StopReason)
		return d.chunk(Message{}, &fr), nil
	default:
		// message_stop / content_block_stop / ping / error 等:无对等产出
		return nil, nil
	}
}

// openAIToolIndex anthropic content block index → openai tool_calls index(首次分配)
func (d *AnthropicStreamDecoder) openAIToolIndex(blockIndex int) int {
	if oi, ok := d.toolIdx[blockIndex]; ok {
		return oi
	}
	oi := d.nextTool
	d.nextTool++
	d.toolIdx[blockIndex] = oi
	return oi
}

func (d *AnthropicStreamDecoder) chunk(delta Message, fr *string) []UnifiedSSEChunk {
	return []UnifiedSSEChunk{{
		ID: d.id, Object: "chat.completion.chunk", Model: d.model,
		Choices: []SSEChoice{{Index: 0, Delta: delta, FinishReason: fr}},
	}}
}

// ===== OpenAI chunk → Anthropic 事件 编码(Anthropic 入口 × openai 上游) =====

// AnthropicSSEWriter 把 openai 流式 chunk 增量翻译成 anthropic 事件 data 负载。
// 有状态(content block 开/闭、文本/工具 block index、message_start 合成、stop_reason 记账),
// 每次请求 new 一个实例
type AnthropicSSEWriter struct {
	id         string
	model      string
	started    bool // message_start 是否已发
	textOpen   bool
	textIndex  int         // 文本 block 的 content index(首次出现时分配)
	nextBlock  int         // 下个待分配的 content index
	toolIdx    map[int]int // openai tool index -> anthropic content block index
	openBlocks []int       // 尚未 content_block_stop 的 content index(按打开序)
	stopReason string      // 已收到的 anthropic stop_reason(空则默认 end_turn)
	usageOut   int         // 上游 usage chunk 的 output_tokens(message_delta 携带)
	finished   bool
}

// NewAnthropicSSEWriter 创建流编码器(id/model 回显到 message_start)
func NewAnthropicSSEWriter(id, model string) *AnthropicSSEWriter {
	return &AnthropicSSEWriter{id: id, model: model, toolIdx: make(map[int]int)}
}

// Process 处理一条 openai data 负载,返回要写给客户端的 anthropic data 负载序列(可空)
func (w *AnthropicSSEWriter) Process(payload []byte) ([][]byte, error) {
	var u UnifiedSSEChunk
	if err := json.Unmarshal(payload, &u); err != nil {
		return nil, err
	}
	var out [][]byte
	// 首个实际负载:先合成 message_start
	if !w.started {
		w.started = true
		out = append(out, w.event("message_start", map[string]interface{}{
			"message": map[string]interface{}{
				"id": w.id, "type": "message", "role": "assistant", "model": w.model,
				"content":     []interface{}{},
				"stop_reason": nil, "stop_sequence": nil,
				"usage": map[string]interface{}{"input_tokens": 0, "output_tokens": 0},
			},
		}))
	}
	// usage 独立块(choices 为空):记录 output 供 message_delta,不产生事件
	if u.Usage != nil && len(u.Choices) == 0 {
		w.usageOut = u.Usage.CompletionTokens
		return out, nil
	}
	for _, ch := range u.Choices {
		delta := ch.Delta
		if s, ok := delta.Content.(string); ok && s != "" {
			if !w.textOpen {
				w.textOpen = true
				w.textIndex = w.allocBlock()
				w.openBlocks = append(w.openBlocks, w.textIndex)
				out = append(out, w.event("content_block_start", map[string]interface{}{
					"index":         w.textIndex,
					"content_block": map[string]interface{}{"type": "text", "text": ""},
				}))
			}
			out = append(out, w.event("content_block_delta", map[string]interface{}{
				"index": w.textIndex,
				"delta": map[string]interface{}{"type": "text_delta", "text": s},
			}))
		}
		for _, tc := range delta.ToolCalls {
			oi := tc.Index
			ci, opened := w.toolIdx[oi]
			if !opened {
				ci = w.allocBlock()
				w.toolIdx[oi] = ci
				w.openBlocks = append(w.openBlocks, ci)
				// openai 首个片段已含 id/name;缺省给占位,保证 content_block_start 完整
				name := tc.Function.Name
				if name == "" {
					name = "function"
				}
				out = append(out, w.event("content_block_start", map[string]interface{}{
					"index": ci,
					"content_block": map[string]interface{}{
						"type": "tool_use", "id": tc.ID, "name": name, "input": map[string]interface{}{},
					},
				}))
			}
			if tc.Function.Arguments != "" {
				out = append(out, w.event("content_block_delta", map[string]interface{}{
					"index": ci,
					"delta": map[string]interface{}{"type": "input_json_delta", "partial_json": tc.Function.Arguments},
				}))
			}
		}
		if ch.FinishReason != nil && *ch.FinishReason != "" {
			w.stopReason = mapOpenAIToAnthropicStop(*ch.FinishReason)
		}
	}
	return out, nil
}

// Finish 上游收尾([DONE])时调用:关闭未闭 block,补 message_delta 与 message_stop
func (w *AnthropicSSEWriter) Finish() ([][]byte, error) {
	if w.finished {
		return nil, nil
	}
	w.finished = true
	var out [][]byte
	// 从未收到任何数据也要补 message_start,保证流完整
	if !w.started {
		w.started = true
		out = append(out, w.event("message_start", map[string]interface{}{
			"message": map[string]interface{}{
				"id": w.id, "type": "message", "role": "assistant", "model": w.model,
				"content": []interface{}{}, "stop_reason": nil, "stop_sequence": nil,
				"usage": map[string]interface{}{"input_tokens": 0, "output_tokens": 0},
			},
		}))
	}
	for _, ci := range w.openBlocks {
		out = append(out, w.event("content_block_stop", map[string]interface{}{"index": ci}))
	}
	stop := w.stopReason
	if stop == "" {
		stop = "end_turn"
	}
	usage := map[string]interface{}{"output_tokens": w.usageOut}
	out = append(out, w.event("message_delta", map[string]interface{}{
		"delta": map[string]interface{}{"stop_reason": stop, "stop_sequence": nil},
		"usage": usage,
	}))
	out = append(out, w.event("message_stop", map[string]interface{}{}))
	return out, nil
}

// allocBlock 分配下一个 content block index
func (w *AnthropicSSEWriter) allocBlock() int {
	i := w.nextBlock
	w.nextBlock++
	return i
}

// event 组装 {type, ...} 的 data 负载
func (w *AnthropicSSEWriter) event(typ string, fields map[string]interface{}) []byte {
	m := map[string]interface{}{"type": typ}
	for k, v := range fields {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return b
}

// mapOpenAIToAnthropicStop openai finish_reason → anthropic stop_reason
func mapOpenAIToAnthropicStop(fr string) string {
	switch fr {
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	case "content_filter":
		return "content_filter"
	default: // stop / 空
		return "end_turn"
	}
}

// ObserveUsage Anthropic 上游流式事件用量上报(message_start → prompt,message_delta → completion)
func (a *AnthropicAdapter) ObserveUsage(payload []byte) (int, int, bool) {
	var ev struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(payload, &ev); err != nil {
		return 0, 0, false
	}
	switch ev.Type {
	case "message_start":
		var e struct {
			Message struct {
				Usage anthropicUsage `json:"usage"`
			} `json:"message"`
		}
		if err := json.Unmarshal(payload, &e); err == nil && e.Message.Usage.InputTokens > 0 {
			return e.Message.Usage.InputTokens, 0, true
		}
	case "message_delta":
		var e struct {
			Usage struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(payload, &e); err == nil && e.Usage.OutputTokens > 0 {
			return 0, e.Usage.OutputTokens, true
		}
	}
	return 0, 0, false
}
