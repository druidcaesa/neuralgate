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
	"strings"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

func sm3SampleLog() *plugin.AuditLog {
	return &plugin.AuditLog{
		ID: "fp-1", RequestID: "req-fp-1", ModelName: "gpt-x",
		RequestMethod: "POST", RequestPath: "/v1/chat/completions",
		ResponseStatus: 200, TotalTokens: 42, CreatedAt: time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC),
	}
}

// TestSM3FingerprintRegisteredAndDistinct sm3 已注册、输出 64hex 且与 sha256 不同
func TestSM3FingerprintRegisteredAndDistinct(t *testing.T) {
	log := sm3SampleLog()
	sm3 := Fingerprint("sm3", log)
	sha := Fingerprint("sha256", log)
	if len(sm3) != 64 {
		t.Fatalf("sm3 指纹应为 64 位 hex, got %d: %q", len(sm3), sm3)
	}
	if sm3 == sha {
		t.Error("sm3 与 sha256 指纹不应相同")
	}
	if strings.ToLower(sm3) != sm3 {
		t.Error("指纹应小写 hex")
	}
	// 同内容重算一致(确定性)
	if again := Fingerprint("sm3", log); again != sm3 {
		t.Error("同内容重算应一致")
	}
}

// TestSM3Sensitivity 内容变化指纹必变(断言灵敏度)
func TestSM3Sensitivity(t *testing.T) {
	log := sm3SampleLog()
	base := Fingerprint("sm3", log)
	log.TotalTokens++
	if Fingerprint("sm3", log) == base {
		t.Error("内容变化后指纹不应相同")
	}
}

// TestUnknownAlgoStillFallsBack 未知算法回退 sha256 兜底保持
func TestUnknownAlgoStillFallsBack(t *testing.T) {
	log := sm3SampleLog()
	if Fingerprint("no-such-algo", log) != Fingerprint("sha256", log) {
		t.Error("未知算法应回退 sha256")
	}
}
