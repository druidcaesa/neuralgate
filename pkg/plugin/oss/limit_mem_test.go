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
)

func TestMemRateLimiterAllow(t *testing.T) {
	rl := NewMemRateLimiter()
	if err := rl.Init(map[string]interface{}{"default_rps": 3}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		allowed, _, err := rl.Allow("t1", "gpt-4", 10)
		if err != nil || !allowed {
			t.Fatalf("Allow() #%d = %v, %v; want allowed", i+1, allowed, err)
		}
	}
	allowed, remaining, _ := rl.Allow("t1", "gpt-4", 10)
	if allowed || remaining != 0 {
		t.Errorf("Allow() #4 = %v/%d, want false/0", allowed, remaining)
	}
}

func TestMemRateLimiterWindowReset(t *testing.T) {
	rl := NewMemRateLimiter()
	_ = rl.Init(map[string]interface{}{"default_rps": 1})
	allowed, _, _ := rl.Allow("t1", "gpt-4", 0)
	if !allowed {
		t.Fatal("first Allow() should be allowed")
	}
	allowed, _, _ = rl.Allow("t1", "gpt-4", 0)
	if allowed {
		t.Fatal("second Allow() should be rejected")
	}
	// Status 应显示重置时间在当前窗口之后
	current, limit, _ := rl.Status("t1", "gpt-4")
	if current != 2 || limit != 1 {
		t.Errorf("Status() = %d/%d, want 2/1", current, limit)
	}
}

func TestMemRateLimiterReset(t *testing.T) {
	rl := NewMemRateLimiter()
	_ = rl.Init(map[string]interface{}{"default_rps": 1})
	_, _, _ = rl.Allow("t1", "gpt-4", 0)
	_ = rl.Reset("t1", "gpt-4")
	allowed, _, _ := rl.Allow("t1", "gpt-4", 0)
	if !allowed {
		t.Error("Allow() after Reset() should be allowed")
	}
}

func TestMemRateLimiterStatus(t *testing.T) {
	rl := NewMemRateLimiter()
	_ = rl.Init(map[string]interface{}{"default_rps": 10})
	current, limit, resetAt := rl.Status("t1", "gpt-4")
	if current != 0 || limit != 10 || resetAt.IsZero() {
		t.Errorf("Status() = %d/%d/%v, want 0/10/non-zero", current, limit, resetAt)
	}
}
