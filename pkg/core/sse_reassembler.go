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
	"strings"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// StreamReassembler 分片重组器：解析每个分片的 delta.content，拼接为完整应答
type StreamReassembler struct{}

// NewStreamReassembler 创建重组器
func NewStreamReassembler() *StreamReassembler { return &StreamReassembler{} }

// Reassemble 重组:提取每分片 choices[0].delta.content(OpenAI 格式)拼接
func (r *StreamReassembler) Reassemble(chunks []plugin.SSEChunk) (string, error) {
	var sb strings.Builder
	for _, c := range chunks {
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(c.Data), &chunk); err != nil {
			continue // 跳过无法解析的分片
		}
		if len(chunk.Choices) > 0 {
			sb.WriteString(chunk.Choices[0].Delta.Content)
		}
	}
	return sb.String(), nil
}
