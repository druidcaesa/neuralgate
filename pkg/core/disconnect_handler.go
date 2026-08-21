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
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// DisconnectHandler 断连检测与补全
type DisconnectHandler struct {
	auditor plugin.AuditPipeline
}

// NewDisconnectHandler 创建断连处理器
func NewDisconnectHandler(auditor plugin.AuditPipeline) *DisconnectHandler {
	return &DisconnectHandler{auditor: auditor}
}

// Watch 监听请求取消;done 通道在流正常结束时关闭(防止正常结束误标断连)
// rc 的 tokens/status 由主循环写入,断连触发时已停止写入,读取安全;
// 为防 -race 误报(usage 分片解析与断连偶发重叠),读取为一次性快照语义
func (h *DisconnectHandler) Watch(ctx context.Context, requestID string, done <-chan struct{}, rc *RequestContext) {
	select {
	case <-ctx.Done():
		if h.auditor != nil {
			meta := &plugin.AuditMeta{
				ResponseStatus:   rc.ResponseStatus,
				PromptTokens:     rc.PromptTokens,
				CompletionTokens: rc.CompletionTokens,
				TotalTokens:      rc.TotalTokens,
				Duration:         time.Since(rc.StartTime).Milliseconds(),
			}
			_ = h.auditor.MarkDisconnect(requestID, "client_disconnected", meta)
		}
	case <-done:
	}
}
