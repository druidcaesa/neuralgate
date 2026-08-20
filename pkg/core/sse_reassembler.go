package core

import (
	"errors"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// StreamReassembler 分片重组器（照设计文档 2.3）
// 骨架期 stub，Phase 4 实现：分片拼接生成完整应答
type StreamReassembler struct{}

// NewStreamReassembler 创建重组器
func NewStreamReassembler() *StreamReassembler { return &StreamReassembler{} }

// Reassemble 将分片列表重组为完整应答
func (r *StreamReassembler) Reassemble(chunks []plugin.SSEChunk) (string, error) {
	return "", errors.New("not implemented")
}
