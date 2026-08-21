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

package oss

import (
	"testing"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// rlTestStorage 预置限流配置的内存存储
func rlTestStorage(t *testing.T, cfgs ...*plugin.RateLimitConfig) *MemStorage {
	t.Helper()
	s := NewMemStorage()
	for _, c := range cfgs {
		if err := s.SaveRateLimitConfig(c); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestRateLimiterDefaultWhenNoConfig(t *testing.T) {
	s := NewMemStorage()
	rl := NewRateLimiter(s, 2, 100000, "token_bucket") // 默认 rps=2
	_ = rl.ReloadConfig()
	// 无配置走默认 rps=2:前 2 次放行,第 3 次拒
	a1, _, _ := rl.Allow("t1", "gpt-4", 0)
	a2, _, _ := rl.Allow("t1", "gpt-4", 0)
	a3, _, _ := rl.Allow("t1", "gpt-4", 0)
	if !a1 || !a2 || a3 {
		t.Fatalf("default rps=2: got %v,%v,%v; want true,true,false", a1, a2, a3)
	}
}

func TestRateLimiterModelOverridesGlobal(t *testing.T) {
	// 全局 rps=1;模型级 gpt-4 rps=5 → gpt-4 用 5
	s := rlTestStorage(t,
		&plugin.RateLimitConfig{ID: "g", TenantID: "", ModelName: "", RequestsPerSec: 1, TokensPerMin: 100000, Strategy: "token_bucket", Enabled: true},
		&plugin.RateLimitConfig{ID: "m", TenantID: "", ModelName: "gpt-4", RequestsPerSec: 5, TokensPerMin: 100000, Strategy: "token_bucket", Enabled: true},
	)
	rl := NewRateLimiter(s, 10, 100000, "token_bucket")
	_ = rl.ReloadConfig()
	allowed := 0
	for i := 0; i < 6; i++ {
		if a, _, _ := rl.Allow("", "gpt-4", 0); a {
			allowed++
		}
	}
	if allowed != 5 {
		t.Fatalf("model-level rps=5: allowed %d; want 5", allowed)
	}
}

func TestRateLimiterTPMRejectAndRecord(t *testing.T) {
	// tpm=10,sliding_window;RecordTokens 累计超限后拒绝
	s := rlTestStorage(t,
		&plugin.RateLimitConfig{ID: "g", RequestsPerSec: 1000, TokensPerMin: 10, Strategy: "sliding_window", Enabled: true},
	)
	rl := NewRateLimiter(s, 1000, 1000000, "sliding_window")
	_ = rl.ReloadConfig()
	// 预检(tokens=0)放行
	if a, _, _ := rl.Allow("t1", "m", 0); !a {
		t.Fatal("first Allow should pass (tpm not yet consumed)")
	}
	// 回补 10 tokens,达到 tpm 上限
	if err := rl.RecordTokens("t1", "m", 10); err != nil {
		t.Fatal(err)
	}
	// 下次预检:TPM 已满 → 拒绝
	if a, _, _ := rl.Allow("t1", "m", 0); a {
		t.Fatal("Allow after TPM exhausted should be rejected")
	}
}

func TestRateLimiterReloadPicksUpNewConfig(t *testing.T) {
	s := NewMemStorage()
	rl := NewRateLimiter(s, 100, 100000, "token_bucket")
	_ = rl.ReloadConfig()
	// 新增严格配置后 Reload 生效
	_ = s.SaveRateLimitConfig(&plugin.RateLimitConfig{ID: "g", RequestsPerSec: 1, TokensPerMin: 100000, Strategy: "token_bucket", Enabled: true})
	_ = rl.ReloadConfig()
	a1, _, _ := rl.Allow("t1", "m", 0)
	a2, _, _ := rl.Allow("t1", "m", 0)
	if !a1 || a2 {
		t.Fatalf("after reload rps=1: got %v,%v; want true,false", a1, a2)
	}
}
