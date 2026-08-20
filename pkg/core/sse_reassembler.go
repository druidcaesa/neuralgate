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
	"errors"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// StreamReassembler 分片重组器：将分片列表拼接为完整应答（当前未实现）
type StreamReassembler struct{}

// NewStreamReassembler 创建重组器
func NewStreamReassembler() *StreamReassembler { return &StreamReassembler{} }

// Reassemble 将分片列表重组为完整应答
func (r *StreamReassembler) Reassemble(chunks []plugin.SSEChunk) (string, error) {
	return "", errors.New("not implemented")
}
