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
