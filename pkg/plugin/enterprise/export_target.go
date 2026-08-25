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

//go:build enterprise

package enterprise

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// ExportTarget 外推目标：把一批审计日志推送到外部系统
type ExportTarget interface {
	// Send 批量推送；返回 error 表示本批失败需重试
	Send(logs []*plugin.AuditLog) error
	// TestConnection 连通性自检
	TestConnection() error
	// Close 释放底层资源
	Close() error
}

// NewExportTarget 按类型构造外推目标（siem/syslog/kafka）
func NewExportTarget(exportType, endpoint, apiKey, topic string) (ExportTarget, error) {
	switch strings.ToLower(exportType) {
	case "siem":
		return NewSIEMTarget(endpoint, apiKey), nil
	case "syslog":
		return NewSyslogTarget(endpoint)
	case "kafka":
		return NewKafkaTarget(endpoint, topic)
	default:
		return nil, fmt.Errorf("不支持的外推类型: %s", exportType)
	}
}

// ===== SIEM =====

// SIEMTarget SIEM 外推：HTTP POST 全量 JSON 数组，Bearer 认证
type SIEMTarget struct {
	endpoint string
	apiKey   string
	client   *http.Client
}

// NewSIEMTarget 创建 SIEM 目标
func NewSIEMTarget(endpoint, apiKey string) *SIEMTarget {
	return &SIEMTarget{endpoint: endpoint, apiKey: apiKey, client: &http.Client{Timeout: 10 * time.Second}}
}

// Send 整批单次 POST；2xx 视为成功
func (t *SIEMTarget) Send(logs []*plugin.AuditLog) error {
	body, err := json.Marshal(logs)
	if err != nil {
		return fmt.Errorf("序列化审计日志失败: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, t.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if t.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+t.apiKey)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("推送 SIEM 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("SIEM 返回非 2xx: %d", resp.StatusCode)
	}
	return nil
}

// TestConnection 以空批次探测连通性（只关心传输层可达）
func (t *SIEMTarget) TestConnection() error {
	req, err := http.NewRequest(http.MethodPost, t.endpoint, bytes.NewReader([]byte("[]")))
	if err != nil {
		return fmt.Errorf("构造请求失败: %w", err)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("SIEM 不可达: %w", err)
	}
	resp.Body.Close()
	return nil
}

// Close HTTP 客户端无需释放
func (t *SIEMTarget) Close() error { return nil }

// ===== Syslog =====

// SyslogTarget Syslog 外推：RFC5424 报文；TCP 采用 RFC6587 octet-count 分帧并维持常连接
type SyslogTarget struct {
	network string // udp / tcp
	addr    string
	conn    net.Conn // 仅 tcp 使用
}

// NewSyslogTarget 创建 Syslog 目标；endpoint 支持 udp://host:port、tcp://host:port，
// 无前缀默认 udp
func NewSyslogTarget(endpoint string) (*SyslogTarget, error) {
	network, addr := "udp", endpoint
	if i := strings.Index(endpoint, "://"); i >= 0 {
		network = strings.ToLower(endpoint[:i])
		addr = endpoint[i+3:]
	}
	switch network {
	case "udp", "tcp":
	default:
		return nil, fmt.Errorf("不支持的 syslog 协议: %s", network)
	}
	t := &SyslogTarget{network: network, addr: addr}
	if network == "tcp" {
		if err := t.dial(); err != nil {
			return nil, err
		}
	}
	return t, nil
}

func (t *SyslogTarget) dial() error {
	conn, err := net.DialTimeout(t.network, t.addr, 5*time.Second)
	if err != nil {
		return fmt.Errorf("连接 syslog(%s) 失败: %w", t.addr, err)
	}
	t.conn = conn
	return nil
}

// Send 每条日志一帧；TCP 任一帧失败即中断返回，由上层重试整批
func (t *SyslogTarget) Send(logs []*plugin.AuditLog) error {
	for _, log := range logs {
		msg, err := syslogFrame(log)
		if err != nil {
			return err
		}
		if t.network == "tcp" {
			frame := append([]byte(strconv.Itoa(len(msg))+" "), msg...)
			if err := t.tcpWrite(frame); err != nil {
				return err
			}
			continue
		}
		if err := t.udpWrite(msg); err != nil {
			return err
		}
	}
	return nil
}

func (t *SyslogTarget) tcpWrite(frame []byte) error {
	if t.conn == nil {
		if err := t.dial(); err != nil {
			return err
		}
	}
	if _, err := t.conn.Write(frame); err != nil {
		t.conn.Close()
		t.conn = nil // 置空触发下次重连
		return fmt.Errorf("syslog 发送失败: %w", err)
	}
	return nil
}

func (t *SyslogTarget) udpWrite(msg []byte) error {
	conn, err := net.DialTimeout("udp", t.addr, 5*time.Second)
	if err != nil {
		return fmt.Errorf("syslog 不可达: %w", err)
	}
	defer conn.Close()
	if _, err := conn.Write(msg); err != nil {
		return fmt.Errorf("syslog 发送失败: %w", err)
	}
	return nil
}

// syslogFrame 组装 RFC5424 报文：
// <PRI>1 TIMESTAMP HOSTNAME APP-NAME PROCID MSGID STRUCTURED-DATA MSG
// facility=user(1)；severity 按 5xx/断连取 err(3)，否则 info(6)；SD 固定 "-"
func syslogFrame(log *plugin.AuditLog) ([]byte, error) {
	payload, err := json.Marshal(log)
	if err != nil {
		return nil, fmt.Errorf("序列化审计日志失败: %w", err)
	}
	severity := 6
	if log.ResponseStatus >= 500 || log.Disconnected {
		severity = 3
	}
	pri := 1*8 + severity
	header := fmt.Sprintf("<%d>1 %s - NeuralGate NeuralGate - %s - ",
		pri, log.CreatedAt.UTC().Format(time.RFC3339), log.RequestID)
	return append([]byte(header), payload...), nil
}

// TestConnection 探测地址可拨（UDP 拨通即可达，TCP 建连成功）
func (t *SyslogTarget) TestConnection() error {
	conn, err := net.DialTimeout(t.network, t.addr, 5*time.Second)
	if err != nil {
		return fmt.Errorf("syslog 不可达: %w", err)
	}
	conn.Close()
	return nil
}

// Close 关闭 TCP 常连接（UDP 无资源）
func (t *SyslogTarget) Close() error {
	if t.conn != nil {
		err := t.conn.Close()
		t.conn = nil
		return err
	}
	return nil
}
