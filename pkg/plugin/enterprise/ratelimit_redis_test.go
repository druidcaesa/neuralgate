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

	"github.com/alicebob/miniredis/v2"
	"github.com/druidcaesa/neuralgate/pkg/config"
	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
	"go.uber.org/zap"
)

func newDistFixture(t *testing.T, rps int, tpm int64) (*DistributedRateLimiter, *miniredis.Miniredis, plugin.RateLimitPlugin) {
	t.Helper()
	mr := miniredis.RunT(t)
	storage := oss.NewMemStorage()
	local := oss.NewRateLimiter(storage, rps, tpm, "token_bucket")
	dist, err := NewDistributedRateLimiter(local,
		config.DistributedRateLimitConfig{Enabled: true, RedisAddr: mr.Addr()},
		zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	dist.SetOverride("", "", rps, tpm)
	return dist, mr, local
}

// TestDistributedAllowAndDeny RPS 桶容量内全放行，超容拒绝
func TestDistributedAllowAndDeny(t *testing.T) {
	dist, _, _ := newDistFixture(t, 3, 100000)
	allowed := 0
	for i := 0; i < 10; i++ {
		ok, _, err := dist.Allow("t1", "m1", 0)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			allowed++
		}
	}
	if allowed != 3 {
		t.Errorf("容量 3 应恰好放行 3 次, got %d", allowed)
	}
}

// TestDistributedTPMWindow TPM 超限拒绝且拒绝请求不计消耗(窗口内不再累加)
func TestDistributedTPMWindow(t *testing.T) {
	dist, _, _ := newDistFixture(t, 1000, 100) // RPS 宽松, TPM 收紧
	if ok, _, _ := dist.Allow("t1", "m1", 40); !ok {
		t.Fatal("累计 40 应放行")
	}
	if ok, _, _ := dist.Allow("t1", "m1", 40); !ok {
		t.Fatal("累计 80 应放行")
	}
	if ok, _, err := dist.Allow("t1", "m1", 40); ok {
		t.Error("累计将达 120>100 应拒绝")
	} else if err != nil {
		t.Fatal(err)
	}
}

// TestDistributedFallbackOnRedisDown Redis 宕机回退本地判定(可用性优先)
func TestDistributedFallbackOnRedisDown(t *testing.T) {
	dist, mr, local := newDistFixture(t, 5, 100000)
	mr.Close() // 模拟宕机
	ok, _, err := dist.Allow("t1", "m1", 0)
	if err != nil {
		t.Fatalf("回退路径不应返回错误: %v", err)
	}
	if !ok {
		t.Error("回退到本地限流后首次请求应放行")
	}
	_ = local // local 判定已由 dist.Allow 内部调用覆盖
}

// TestDistributedResetClearsRedis Reset 同时清本地与 Redis 桶
func TestDistributedResetClearsRedis(t *testing.T) {
	dist, mr, _ := newDistFixture(t, 1, 100000)
	if ok, _, _ := dist.Allow("t", "m", 0); !ok {
		t.Fatal("首刷应放行")
	}
	if ok, _, _ := dist.Allow("t", "m", 0); ok {
		t.Fatal("桶空后应拒绝")
	}
	if err := dist.Reset("t", "m"); err != nil {
		t.Fatal(err)
	}
	if mr.Exists("rl:tb:t:m") {
		t.Error("Reset 后 Redis 桶键应被清除")
	}
}

var _ = time.Now
