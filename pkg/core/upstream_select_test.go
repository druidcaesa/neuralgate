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
	"testing"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

func TestSelectUpstreamEmpty(t *testing.T) {
	if selectUpstream(nil) != nil {
		t.Fatal("empty upstreams should return nil")
	}
}

func TestSelectUpstreamSingle(t *testing.T) {
	ups := []plugin.Upstream{{ID: "u1", BaseURL: "https://a", Weight: 1, Enabled: true}}
	got := selectUpstream(ups)
	if got == nil || got.ID != "u1" {
		t.Fatalf("single upstream = %v; want u1", got)
	}
}

func TestSelectUpstreamSkipsDisabled(t *testing.T) {
	ups := []plugin.Upstream{
		{ID: "u1", Weight: 1, Enabled: false},
		{ID: "u2", Weight: 1, Enabled: true},
	}
	// 仅 u2 enabled,恒选 u2
	for i := 0; i < 20; i++ {
		if got := selectUpstream(ups); got == nil || got.ID != "u2" {
			t.Fatalf("should always pick enabled u2; got %v", got)
		}
	}
}

func TestSelectUpstreamWeightedDistribution(t *testing.T) {
	// u1:weight 1、u2:weight 9 → u2 约占 90%
	ups := []plugin.Upstream{
		{ID: "u1", Weight: 1, Enabled: true},
		{ID: "u2", Weight: 9, Enabled: true},
	}
	counts := map[string]int{}
	const n = 10000
	for i := 0; i < n; i++ {
		counts[selectUpstream(ups).ID]++
	}
	// u2 占比应在 [0.82, 0.98](宽松统计断言,避免偶发失败)
	ratio := float64(counts["u2"]) / float64(n)
	if ratio < 0.82 || ratio > 0.98 {
		t.Fatalf("u2 ratio = %.3f; want ~0.90", ratio)
	}
}

func TestSelectUpstreamAllDisabled(t *testing.T) {
	ups := []plugin.Upstream{{ID: "u1", Weight: 1, Enabled: false}}
	if selectUpstream(ups) != nil {
		t.Fatal("all-disabled upstreams should return nil")
	}
}

// TestPickHealthySkipsUnreadableKeyUpstream 密钥不可解密的上游不得参与选路,
// 否则空密钥会覆盖模型配置的可用密钥
func TestPickHealthySkipsUnreadableKeyUpstream(t *testing.T) {
	ups := []plugin.Upstream{
		{ID: "bad", BaseURL: "https://bad", APIKey: "", APIKeyUnreadable: true, Enabled: true, Weight: 1},
		{ID: "good", BaseURL: "https://good", APIKey: "sk-good", Enabled: true, Weight: 1},
	}
	for i := 0; i < 20; i++ { // 选路含加权随机,多跑几轮确保不会偶发选中坏行
		sel, _ := pickHealthy(ups, nil)
		if sel == nil {
			t.Fatal("存在可用上游时不得返回 nil")
		}
		if sel.ID != "good" {
			t.Fatalf("选中了 %s,坏上游不得参与选路", sel.ID)
		}
	}
}

// TestPickHealthyAllUnreadableFallsBack 全部上游密钥不可解密时应判定无候选,
// 让调用方回退到模型配置自身的密钥
func TestPickHealthyAllUnreadableFallsBack(t *testing.T) {
	ups := []plugin.Upstream{
		{ID: "bad1", APIKeyUnreadable: true, Enabled: true, Weight: 1},
		{ID: "bad2", APIKeyUnreadable: true, Enabled: true, Weight: 1},
	}
	if sel, _ := pickHealthy(ups, nil); sel != nil {
		t.Fatalf("全为坏行时应返回 nil 以触发回退, got %s", sel.ID)
	}
}
