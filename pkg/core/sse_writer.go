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
	"bufio"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// SSEResponseWriter 劫持 SSE 流量（照设计文档 5.2 结构）
// 骨架期：原样写入客户端；分片解析与审计投递 Phase 4/7 填充
type SSEResponseWriter struct {
	http.ResponseWriter // 嵌入原始 Writer
	requestID           string
	auditor             plugin.AuditPipeline
	mu                  sync.Mutex
	startWrite          time.Time
	headerWritten       bool
}

// NewSSEResponseWriter 包装 ResponseWriter 为 SSE 劫持器
func NewSSEResponseWriter(w http.ResponseWriter, requestID string, auditor plugin.AuditPipeline) *SSEResponseWriter {
	return &SSEResponseWriter{
		ResponseWriter: w,
		requestID:      requestID,
		auditor:        auditor,
		startWrite:     time.Now(),
	}
}

// Write 写入原始 Writer（推送客户端）；骨架期不做分片解析与审计投递
func (w *SSEResponseWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.headerWritten = true
	return w.ResponseWriter.Write(data)
}

// WriteHeader 记录状态码并转发
func (w *SSEResponseWriter) WriteHeader(code int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.headerWritten = true
	w.ResponseWriter.WriteHeader(code)
}

// Flush 刷新，确保客户端实时收到数据
func (w *SSEResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack 支持连接劫持（断连检测用）
func (w *SSEResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("underlying writer does not support hijacking")
	}
	return hj.Hijack()
}
