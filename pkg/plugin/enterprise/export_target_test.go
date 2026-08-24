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
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// sampleLogs 构造 n 条用于测试的审计日志
func sampleLogs(n int) []*plugin.AuditLog {
	logs := make([]*plugin.AuditLog, n)
	for i := range logs {
		logs[i] = &plugin.AuditLog{
			ID:             fmt.Sprintf("id-%d", i),
			RequestID:      fmt.Sprintf("req-%d", i),
			ModelName:      "gpt-x",
			RequestMethod:  "POST",
			RequestPath:    "/v1/chat",
			ResponseStatus: 200,
			TotalTokens:    42,
			ClientIP:       "10.0.0.1",
			CreatedAt:      time.Now().UTC(),
		}
	}
	return logs
}

func TestNewExportTargetUnknownType(t *testing.T) {
	if _, err := NewExportTarget("kafka", "x", ""); err == nil {
		t.Fatal("未知类型应报错")
	}
}

func TestSIEMSendPostsJSONArray(t *testing.T) {
	var gotBody []byte
	var gotAuth, gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	target := NewSIEMTarget(srv.URL, "secret-key")
	logs := sampleLogs(2)
	if err := target.Send(logs); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotAuth != "Bearer secret-key" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if !strings.HasPrefix(gotCT, "application/json") {
		t.Errorf("Content-Type = %q", gotCT)
	}
	var parsed []map[string]any
	if err := json.Unmarshal(gotBody, &parsed); err != nil {
		t.Fatalf("body 不是 JSON 数组: %v", err)
	}
	if len(parsed) != 2 || parsed[0]["RequestID"] != "req-0" {
		t.Errorf("数组内容不符: %s", gotBody)
	}
}

func TestSIEMSendNon2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if err := NewSIEMTarget(srv.URL, "").Send(sampleLogs(1)); err == nil {
		t.Fatal("500 应返回 error")
	}
}

func TestSIEMTestConnection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	if err := NewSIEMTarget(srv.URL, "").TestConnection(); err != nil {
		t.Fatalf("可达端点应通过: %v", err)
	}
	if err := NewSIEMTarget("http://127.0.0.1:1/nope", "").TestConnection(); err == nil {
		t.Fatal("不可达端点应报错")
	}
}

func TestSyslogUDPSendRFC5424(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	frames := make(chan string, 4)
	go func() {
		buf := make([]byte, 65536)
		for {
			n, _, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			frames <- string(buf[:n])
		}
	}()

	target, err := NewSyslogTarget("udp://" + pc.LocalAddr().String())
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	ok := sampleLogs(1)[0]
	bad := sampleLogs(1)[0]
	bad.ResponseStatus = 502
	if err := target.Send([]*plugin.AuditLog{ok, bad}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	infoFrame := <-frames
	errFrame := <-frames
	if !strings.HasPrefix(infoFrame, "<14>1 ") { // user(1)*8+info(6)=14
		t.Errorf("info 报文 PRI 不符: %q", infoFrame[:12])
	}
	if !strings.Contains(infoFrame, " NeuralGate NeuralGate - req-0 - ") || !strings.Contains(infoFrame, `"RequestID":"req-0"`) {
		t.Errorf("info 报文头/MSG 不符: %s", infoFrame)
	}
	if !strings.HasPrefix(errFrame, "<11>1 ") { // user(1)*8+err(3)=11
		t.Errorf("5xx 应映射 err severity: %q", errFrame[:12])
	}
}

func TestSyslogTCPOctetFraming(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	frames := make(chan string, 2)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		reader := bufio.NewReader(conn)
		for {
			// RFC6587 octet-count 分帧："LEN MSG"
			head, err := reader.ReadString(' ')
			if err != nil {
				return
			}
			n, err := strconv.Atoi(strings.TrimSpace(head))
			if err != nil {
				return
			}
			body := make([]byte, n)
			if _, err := io.ReadFull(reader, body); err != nil {
				return
			}
			frames <- string(body)
		}
	}()

	target, err := NewSyslogTarget("tcp://" + ln.Addr().String())
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if err := target.Send(sampleLogs(2)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	frame := <-frames
	if !strings.HasPrefix(frame, "<14>1 ") || !strings.Contains(frame, `"RequestID":"req-0"`) {
		t.Errorf("首帧内容不符: %s", frame)
	}
}

func TestSyslogRejectsBadScheme(t *testing.T) {
	if _, err := NewSyslogTarget("amqp://127.0.0.1:514"); err == nil {
		t.Fatal("非法协议应报错")
	}
}
