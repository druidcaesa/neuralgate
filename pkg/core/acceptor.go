package core

import (
	"crypto/tls"
	"net"
	"net/http"
	"strings"
)

// Acceptor 接入层（照设计文档 2.1）：连接管理、TLS、IP 过滤、协议解析
// 骨架期：仅持有组件并透传 handler；各组件逻辑 Phase 4 填充
type Acceptor struct {
	handler http.Handler
	connMgr *ConnectionManager
	tls     *TLSHandler
	ipf     *IPFilter
	parser  *ProtocolParser
}

// NewAcceptor 创建接入层
func NewAcceptor(handler http.Handler) *Acceptor {
	return &Acceptor{
		handler: handler,
		connMgr: NewConnectionManager(),
		tls:     NewTLSHandler(),
		ipf:     NewIPFilter(),
		parser:  NewProtocolParser(),
	}
}

// Handler 返回经接入层包装的 handler（骨架期直接返回；IP 过滤 Phase 4 接入）
func (a *Acceptor) Handler() http.Handler { return a.handler }

// ConnectionManager 连接生命周期管理（骨架期空实现；最大连接数/空闲超时 Phase 4）
type ConnectionManager struct{}

func NewConnectionManager() *ConnectionManager { return &ConnectionManager{} }

// OnStateChange ConnState 回调（http.Server.ConnState 挂接点）
func (m *ConnectionManager) OnStateChange(c net.Conn, state http.ConnState) {}

// TLSHandler TLS 终止（骨架期空实现；证书加载 Phase 4）
type TLSHandler struct{}

func NewTLSHandler() *TLSHandler { return &TLSHandler{} }

// TLSConfig 返回 TLS 配置（骨架期返回 nil，表示不启用 TLS）
func (h *TLSHandler) TLSConfig() *tls.Config { return nil }

// IPFilter IP 黑白名单（骨架期默认全部放行；规则匹配 Phase 4）
type IPFilter struct{}

func NewIPFilter() *IPFilter { return &IPFilter{} }

// Allow 是否允许该 IP 访问（骨架期恒为 true）
func (f *IPFilter) Allow(ip string) bool { return true }

// ProtocolParser HTTP 协议解析（骨架期仅提供 SSE 判断）
type ProtocolParser struct{}

func NewProtocolParser() *ProtocolParser { return &ProtocolParser{} }

// IsSSE 检测流式请求（Accept: text/event-stream 时动态取消 WriteTimeout，照设计文档 2.1）
func (p *ProtocolParser) IsSSE(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/event-stream")
}
