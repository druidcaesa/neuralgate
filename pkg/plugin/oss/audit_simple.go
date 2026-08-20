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

package oss

import (
	"sync"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// SimpleAuditor 简单同步审计：分片与元数据在请求结束时同步组装落库
type SimpleAuditor struct {
	storage plugin.StoragePlugin
	mu      sync.Mutex
	pending map[string]*plugin.AuditLog // requestID -> 组装中的日志
}

// NewSimpleAuditor 创建简单审计器
func NewSimpleAuditor(storage plugin.StoragePlugin) *SimpleAuditor {
	return &SimpleAuditor{
		storage: storage,
		pending: make(map[string]*plugin.AuditLog),
	}
}

// Init 初始化审计管道
func (a *SimpleAuditor) Init(config plugin.AuditConfig) error { return nil }

// Submit 提交审计事件;Data 可携带 *AuditLog 作为基础元数据(请求开始事件)
func (a *SimpleAuditor) Submit(event *plugin.AuditEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	log, ok := a.pending[event.RequestID]
	if !ok {
		log = &plugin.AuditLog{ID: event.RequestID, RequestID: event.RequestID, CreatedAt: event.Timestamp}
		if base, isLog := event.Data.(*plugin.AuditLog); isLog && base != nil {
			log = base
		}
		a.pending[event.RequestID] = log
	}
	return nil
}

// BatchSubmit 批量提交
func (a *SimpleAuditor) BatchSubmit(events []*plugin.AuditEvent) error {
	for _, ev := range events {
		if err := a.Submit(ev); err != nil {
			return err
		}
	}
	return nil
}

// SubmitSSEChunk 提交流式分片，追加到组装中的日志
func (a *SimpleAuditor) SubmitSSEChunk(requestID string, chunk *plugin.SSEChunk) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	log, ok := a.pending[requestID]
	if !ok {
		log = &plugin.AuditLog{ID: requestID, RequestID: requestID}
		a.pending[requestID] = log
	}
	log.SSEChunks = append(log.SSEChunks, *chunk)
	return nil
}

// Finalize 标记请求结束，组装完整日志并落库
func (a *SimpleAuditor) Finalize(requestID string, meta *plugin.AuditMeta) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	log, ok := a.pending[requestID]
	if !ok {
		log = &plugin.AuditLog{ID: requestID, RequestID: requestID}
		a.pending[requestID] = log
	}
	log.ResponseStatus = meta.ResponseStatus
	log.PromptTokens = meta.PromptTokens
	log.CompletionTokens = meta.CompletionTokens
	log.TotalTokens = meta.TotalTokens
	log.Duration = meta.Duration
	if err := a.storage.SaveAuditLog(log); err != nil {
		return err
	}
	delete(a.pending, requestID)
	return nil
}

// MarkDisconnect 标记客户端断连，保存已收集内容
func (a *SimpleAuditor) MarkDisconnect(requestID string, reason string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	log, ok := a.pending[requestID]
	if !ok {
		log = &plugin.AuditLog{ID: requestID, RequestID: requestID}
		a.pending[requestID] = log
	}
	log.Disconnected = true
	log.DisconnectReason = reason
	if err := a.storage.SaveAuditLog(log); err != nil {
		return err
	}
	delete(a.pending, requestID)
	return nil
}

// Shutdown 关闭管道，flush 剩余数据
func (a *SimpleAuditor) Shutdown() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for requestID, log := range a.pending {
		if err := a.storage.SaveAuditLog(log); err != nil {
			return err
		}
		delete(a.pending, requestID)
	}
	return nil
}
