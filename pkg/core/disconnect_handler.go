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
	"context"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// DisconnectHandler 断连检测与补全（照设计文档 2.3/5.4）
type DisconnectHandler struct {
	auditor plugin.AuditPipeline
}

// NewDisconnectHandler 创建断连处理器
func NewDisconnectHandler(auditor plugin.AuditPipeline) *DisconnectHandler {
	return &DisconnectHandler{auditor: auditor}
}

// Watch 监听请求上下文取消信号；断开后标记审计日志（骨架期可用行为，补全逻辑 Phase 4 细化）
func (h *DisconnectHandler) Watch(ctx context.Context, requestID string) {
	<-ctx.Done()
	if h.auditor != nil {
		_ = h.auditor.MarkDisconnect(requestID, "client_disconnected")
	}
}
