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
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// tamperSample 构造指纹测试样本日志
func tamperSample() *plugin.AuditLog {
	return &plugin.AuditLog{
		ID:             "id-1",
		RequestID:      "req-1",
		TenantID:       "t1",
		APIKeyID:       "k1",
		ModelName:      "gpt-x",
		Provider:       "openai",
		RequestMethod:  "POST",
		RequestPath:    "/v1/chat/completions",
		RequestHeaders: map[string]string{"Authorization": "***", "X-Req": "a"},
		RequestBody:    `{"model":"gpt-x"}`,
		ResponseStatus: 200,
		ResponseBody:   `{"ok":true}`,
		SSEChunks: []plugin.SSEChunk{
			{Index: 0, EventType: "data", Data: `{"d":1}`, Timestamp: time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)},
		},
		PromptTokens:     10,
		CompletionTokens: 5,
		TotalTokens:      15,
		Duration:         120,
		ClientIP:         "10.0.0.1",
		IsStream:         true,
		CreatedAt:        time.Date(2026, 8, 24, 12, 0, 5, 0, time.UTC),
	}
}

func TestFingerprintDeterministic(t *testing.T) {
	a := Fingerprint("sha256", tamperSample())
	b := Fingerprint("sha256", tamperSample())
	if a == "" || a != b {
		t.Fatalf("同内容指纹应稳定: %q vs %q", a, b)
	}
	if len(a) != 64 {
		t.Fatalf("sha256 hex 长度应为 64, got %d", len(a))
	}
}

func TestFingerprintFieldSensitive(t *testing.T) {
	base := Fingerprint("sha256", tamperSample())
	mutations := map[string]func(*plugin.AuditLog){
		"request_body":  func(l *plugin.AuditLog) { l.RequestBody = `{"model":"fake"}` },
		"response_body": func(l *plugin.AuditLog) { l.ResponseBody = `{}` },
		"status":        func(l *plugin.AuditLog) { l.ResponseStatus = 404 },
		"tokens":        func(l *plugin.AuditLog) { l.TotalTokens = 999 },
		"sse_chunk": func(l *plugin.AuditLog) {
			l.SSEChunks[0].Data = `{"d":2}`
		},
		"created_at": func(l *plugin.AuditLog) { l.CreatedAt = l.CreatedAt.Add(time.Second) },
		"client_ip":  func(l *plugin.AuditLog) { l.ClientIP = "10.9.9.9" },
	}
	for name, mutate := range mutations {
		sample := tamperSample()
		mutate(sample)
		if Fingerprint("sha256", sample) == base {
			t.Errorf("%s 变化未影响指纹", name)
		}
	}
}

func TestFingerprintHeaderOrderInsensitive(t *testing.T) {
	a := tamperSample()
	a.RequestHeaders = map[string]string{"A": "1", "B": "2"}
	b := tamperSample()
	b.RequestHeaders = map[string]string{"B": "2", "A": "1"} // map 字面量顺序无关,构造两个不同插入序
	am := map[string]string{}
	for k, v := range b.RequestHeaders {
		am[k] = v
	}
	delete(am, "A")
	am["A"] = "1"
	b.RequestHeaders = am
	if Fingerprint("sha256", a) != Fingerprint("sha256", b) {
		t.Error("Headers 内容相同但遍历顺序不同时指纹应一致")
	}
}

func TestFingerprintExcludesFingerprintField(t *testing.T) {
	sample := tamperSample()
	sample.SHA256Fingerprint = "garbage-value"
	clean := tamperSample()
	if Fingerprint("sha256", sample) != Fingerprint("sha256", clean) {
		t.Error("指纹字段自身不应参与计算")
	}
}

func TestFingerprintUnknownAlgoFallsBack(t *testing.T) {
	sample := tamperSample()
	if Fingerprint("sm3-not-registered", sample) != Fingerprint("sha256", sample) {
		t.Error("未注册算法应回退 sha256")
	}
}
