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
	"fmt"
	"net"
	"net/http"
	"strings"
)

// Acceptor 接入层：连接管理、TLS、IP 过滤、协议解析；当前仅持有组件并透传 handler
type Acceptor struct {
	handler http.Handler
	connMgr *ConnectionManager
	ipf     *IPFilter
	parser  *ProtocolParser
}

// NewAcceptor 创建接入层
func NewAcceptor(handler http.Handler, ipf *IPFilter) *Acceptor {
	return &Acceptor{
		handler: handler,
		connMgr: NewConnectionManager(),
		ipf:     ipf,
		parser:  NewProtocolParser(),
	}
}

// Handler 返回经接入层包装的 handler(IP 黑白名单在最外层)
func (a *Acceptor) Handler() http.Handler {
	inner := a.handler
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.ipf != nil && !a.ipf.Allow(clientIP(r)) {
			writeOpenAIError(w, http.StatusForbidden, "invalid_request_error", "forbidden", "access denied by IP filter")
			return
		}
		inner.ServeHTTP(w, r)
	})
}

// ConnectionManager 连接生命周期管理（当前空实现）
type ConnectionManager struct{}

func NewConnectionManager() *ConnectionManager { return &ConnectionManager{} }

// OnStateChange ConnState 回调（http.Server.ConnState 挂接点）
func (m *ConnectionManager) OnStateChange(c net.Conn, state http.ConnState) {}

// TLSHandler TLS 终止:按配置加载证书
type TLSHandler struct {
	enabled    bool
	certFile   string
	keyFile    string
	minVersion string
}

// NewTLSHandler 按配置构造
func NewTLSHandler(enabled bool, certFile, keyFile, minVersion string) *TLSHandler {
	return &TLSHandler{enabled: enabled, certFile: certFile, keyFile: keyFile, minVersion: minVersion}
}

// TLSConfig 未启用返回 (nil,nil);启用则加载证书对,失败返回 error
func (h *TLSHandler) TLSConfig() (*tls.Config, error) {
	if !h.enabled {
		return nil, nil
	}
	cert, err := tls.LoadX509KeyPair(h.certFile, h.keyFile)
	if err != nil {
		return nil, fmt.Errorf("load tls key pair: %w", err)
	}
	minVer := uint16(tls.VersionTLS12)
	if h.minVersion == "1.3" {
		minVer = tls.VersionTLS13
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   minVer,
	}, nil
}

// IPFilter IP 黑白名单(CIDR 或单 IP)
type IPFilter struct {
	mode      string // disabled/whitelist/blacklist
	whitelist []*net.IPNet
	blacklist []*net.IPNet
}

// NewIPFilter 按 mode 与规则列表(CIDR 或单 IP)构造
func NewIPFilter(mode string, whitelist, blacklist []string) *IPFilter {
	return &IPFilter{
		mode:      mode,
		whitelist: parseCIDRs(whitelist),
		blacklist: parseCIDRs(blacklist),
	}
}

// parseCIDRs 解析规则:CIDR 直接解析;单 IP 转为 /32(v4)或 /128(v6)
func parseCIDRs(rules []string) []*net.IPNet {
	var nets []*net.IPNet
	for _, r := range rules {
		if _, ipnet, err := net.ParseCIDR(r); err == nil {
			nets = append(nets, ipnet)
			continue
		}
		if ip := net.ParseIP(r); ip != nil {
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			nets = append(nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
		}
	}
	return nets
}

func ipInNets(nets []*net.IPNet, ip net.IP) bool {
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// Allow 按 mode 判定:disabled 全放行;whitelist 命中才放行;blacklist 命中则拒
func (f *IPFilter) Allow(ipStr string) bool {
	if f.mode == "disabled" || f.mode == "" {
		return true
	}
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return f.mode != "whitelist" // 白名单模式解析失败拒绝;黑名单模式放行
	}
	switch f.mode {
	case "whitelist":
		return ipInNets(f.whitelist, ip)
	case "blacklist":
		return !ipInNets(f.blacklist, ip)
	}
	return true
}

// ProtocolParser HTTP 协议解析（当前仅提供 SSE 判断）
type ProtocolParser struct{}

func NewProtocolParser() *ProtocolParser { return &ProtocolParser{} }

// IsSSE 检测流式请求：Accept 头含 text/event-stream 即视为 SSE
func (p *ProtocolParser) IsSSE(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/event-stream")
}
