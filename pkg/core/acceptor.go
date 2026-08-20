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
	"crypto/tls"
	"net"
	"net/http"
	"strings"
)

// Acceptor 接入层：连接管理、TLS、IP 过滤、协议解析；当前仅持有组件并透传 handler
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

// Handler 返回经接入层包装的 handler（当前直接返回原始 handler）
func (a *Acceptor) Handler() http.Handler { return a.handler }

// ConnectionManager 连接生命周期管理（当前空实现）
type ConnectionManager struct{}

func NewConnectionManager() *ConnectionManager { return &ConnectionManager{} }

// OnStateChange ConnState 回调（http.Server.ConnState 挂接点）
func (m *ConnectionManager) OnStateChange(c net.Conn, state http.ConnState) {}

// TLSHandler TLS 终止（当前空实现）
type TLSHandler struct{}

func NewTLSHandler() *TLSHandler { return &TLSHandler{} }

// TLSConfig 返回 TLS 配置（当前返回 nil，表示不启用 TLS）
func (h *TLSHandler) TLSConfig() *tls.Config { return nil }

// IPFilter IP 黑白名单（当前默认全部放行）
type IPFilter struct{}

func NewIPFilter() *IPFilter { return &IPFilter{} }

// Allow 是否允许该 IP 访问（当前恒为 true）
func (f *IPFilter) Allow(ip string) bool { return true }

// ProtocolParser HTTP 协议解析（当前仅提供 SSE 判断）
type ProtocolParser struct{}

func NewProtocolParser() *ProtocolParser { return &ProtocolParser{} }

// IsSSE 检测流式请求：Accept 头含 text/event-stream 即视为 SSE
func (p *ProtocolParser) IsSSE(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/event-stream")
}
