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
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// newBreaker 直接驱动单个上游状态机:与注册表条目同构,便于白盒断言状态流转
func newBreaker(cfg *BreakerConfig) *upstreamBreaker {
	n := cfg.now()
	return &upstreamBreaker{cfg: cfg, state: Closed, winStart: n, lastSeen: n}
}

func TestBreakerLifecycle(t *testing.T) {
	now := time.Unix(1000, 0)
	b := newBreaker(NewBreakerConfig(2, time.Minute, 3*time.Second, 1, 0, func() time.Time { return now })) // 阈值2/openFor3s/succ1/window1min

	// closed 正常放行
	if !b.Allow() {
		t.Fatal("closed 应放行")
	}
	b.Record(false)
	b.Record(false) // 达阈值 2 → open
	if b.State() != Open {
		t.Fatalf("应 open, got %v", b.State())
	}
	if b.Allow() {
		t.Fatal("open 应拒绝")
	}

	// open 3s 后,首个 Allow 触发转 half-open 并放行试探(探测槽空)
	now = now.Add(4 * time.Second)
	if !b.Allow() {
		t.Fatal("到期应转 half-open 且首个试探放行(探测槽空)")
	}
	if b.State() != HalfOpen {
		t.Fatalf("应 half-open, got %v", b.State())
	}
	if b.Allow() {
		t.Fatal("探测槽占满时其余应拒绝") // probeConcurrency=1
	}

	b.Record(true) // 试探成功
	if b.State() != Closed {
		t.Fatalf("成功达阈值应 closed, got %v", b.State())
	}
	if !b.Allow() {
		t.Fatal("closed 应放行")
	}
}

func TestBreakerHalfOpenFailureReopens(t *testing.T) {
	now := time.Unix(1000, 0)
	b := newBreaker(NewBreakerConfig(2, time.Minute, 3*time.Second, 1, 0, func() time.Time { return now }))
	b.Record(false)
	b.Record(false) // open
	now = now.Add(4 * time.Second)
	if !b.Allow() {
		t.Fatal("到期应转 half-open 且试探放行")
	}
	b.Record(false)
	if b.State() != Open {
		t.Fatalf("half-open 失败应回 open, got %v", b.State())
	}
}

func TestBreakerWindowSlides(t *testing.T) {
	now := time.Unix(1000, 0)
	b := newBreaker(NewBreakerConfig(2, 10*time.Second, time.Second, 1, 0, func() time.Time { return now }))
	b.Record(false)                 // 窗内第 1 次失败
	now = now.Add(11 * time.Second) // 越过采样窗
	if !b.Allow() {
		t.Fatal("closed 应放行")
	} // Allow 触发窗口滑动,计数清零
	b.Record(false) // 新窗口: 计数从 1 起, 不应 open
	if b.State() != Closed {
		t.Fatalf("窗口外失败应重置计数, got %v", b.State())
	}
}

func TestRegistryAllowFiltersAndExpires(t *testing.T) {
	now := time.Unix(1000, 0)
	reg := NewRegistry(NewBreakerConfig(1, time.Minute, time.Second, 1, 0, func() time.Time { return now }))
	reg.Record("u1", false) // u1 open
	if reg.Allow("u1") {
		t.Error("u1 应拒绝")
	}
	if !reg.Allow("u2") {
		t.Error("u2 应放行(惰性登记)")
	}
	// 空闲过期
	now = now.Add(2 * time.Hour)
	reg.sweep()
	if _, ok := reg.states["u1"]; ok {
		t.Error("u1 应被空闲清扫")
	}
	if !reg.Allow("u1") {
		t.Fatal("重新出现应重建 closed")
	}
}

// pickHealthy 选路:剔除 open 后加权随机;全开 → (nil,true)
func TestPickHealthyFiltersOpenAndFlagsAllBlocked(t *testing.T) {
	now := time.Unix(1000, 0)
	reg := NewRegistry(NewBreakerConfig(1, time.Minute, time.Second, 1, 0, func() time.Time { return now }))
	ups := []plugin.Upstream{
		{ID: "a", Enabled: true, Weight: 1},
		{ID: "b", Enabled: true, Weight: 1},
		{ID: "c", Enabled: false, Weight: 1}, // disabled 不参与
	}
	reg.Record("a", false) // a → open
	sel, blocked := pickHealthy(ups, reg)
	if blocked {
		t.Error("存在 b 可用, 不应 blocked")
	}
	if sel == nil || sel.ID != "b" {
		t.Fatalf("应只选中 b, got %+v", sel)
	}

	reg.Record("b", false) // b → open; 全部 open
	sel, blocked = pickHealthy(ups, reg)
	if !blocked || sel != nil {
		t.Fatalf("全 open 应 blocked, got sel=%+v blocked=%v", sel, blocked)
	}
}

// Selectable 只读 peek:推进到期状态但不占试探槽;槽位由选中后的 Allow 占用
func TestSelectableDoesNotConsumeProbeSlot(t *testing.T) {
	now := time.Unix(1000, 0)
	reg := NewRegistry(NewBreakerConfig(1, time.Minute, 3*time.Second, 1, 1, func() time.Time { return now }))
	reg.Record("u", false) // u → open
	if reg.Selectable("u") {
		t.Fatal("open 未到期应不可选")
	}
	now = now.Add(4 * time.Second)
	if !reg.Selectable("u") {
		t.Fatal("到期 open 应可选(推进 half-open,不占槽)")
	}
	if !reg.Selectable("u") {
		t.Fatal("half-open 未占槽仍应可选(peek 不占槽)")
	}
	// 槽位未被 Selectable 消耗:首个 Allow 占槽放行,占满后第二个拒绝
	if !reg.Allow("u") {
		t.Fatal("槽位空, Allow 应放行")
	}
	if reg.Allow("u") {
		t.Fatal("ProbeConcurrency=1 下第二 Allow 应拒绝")
	}
	// Release 归还未成行试探槽,状态保持 half-open 且可再次被 Allow 占用
	reg.Release("u")
	if !reg.Allow("u") {
		t.Fatal("Release 归还槽位后 Allow 应放行")
	}
	reg.Release("u") // 配对:归还最后一次 Allow 的槽位
	if st := reg.states["u"].State(); st != HalfOpen {
		t.Fatalf("Release 不应改状态, got %v", st)
	}
}

// 回归:恢复中的 half-open 与 closed peer 并存时,选路不得烧掉其试探槽。
// a 权重 0 使 peer b 必然胜出(确定性);旧实现对每个候选调 Allow,会消耗 a 的槽位
func TestPickHealthyHalfOpenSlotNotBurnedWhenPeerWins(t *testing.T) {
	now := time.Unix(1000, 0)
	reg := NewRegistry(NewBreakerConfig(1, time.Minute, 3*time.Second, 1, 1, func() time.Time { return now }))
	ups := []plugin.Upstream{
		{ID: "a", Enabled: true, Weight: 0},
		{ID: "b", Enabled: true, Weight: 1},
	}
	reg.Record("a", false) // a → open
	now = now.Add(4 * time.Second)
	if !reg.Selectable("a") {
		t.Fatal("a 到期应转 half-open(可参与选路)")
	}
	sel, blocked := pickHealthy(ups, reg)
	if blocked || sel == nil || sel.ID != "b" {
		t.Fatalf("b 应作为唯一权重胜出, got %+v blocked=%v", sel, blocked)
	}
	// 核心断言:peer 胜出后 a 的试探槽未被烧掉(仍可被 Allow 占用)
	if !reg.Allow("a") {
		t.Fatal("peer 胜出后 a 的试探槽应仍可占用(槽未被 pickHealthy 泄漏)")
	}
	reg.Release("a")
	// 仅剩 a 一个候选时仍可被选中(不再被占满槽位挡住)
	only := []plugin.Upstream{{ID: "a", Enabled: true, Weight: 1}}
	sel, blocked = pickHealthy(only, reg)
	if blocked || sel == nil || sel.ID != "a" {
		t.Fatalf("a 唯一候选时应被选中, got %+v blocked=%v", sel, blocked)
	}
	reg.Release("a") // 配对归还
}

// 注册表为 nil 时(特性关闭)行为逐字节不变:回退原 selectUpstream
func TestPickHealthyNilRegistryFallsBack(t *testing.T) {
	ups := []plugin.Upstream{{ID: "a", Enabled: true, Weight: 1}}
	sel, blocked := pickHealthy(ups, nil)
	if blocked || sel == nil || sel.ID != "a" {
		t.Fatalf("nil 注册表应回退原选择, got %+v blocked=%v", sel, blocked)
	}
	sel, blocked = pickHealthy(nil, nil) // 空列表: 不 blocked, 返回 nil 由调用方回退默认上游
	if blocked || sel != nil {
		t.Fatalf("空列表不应 blocked, sel=%v", sel)
	}
}
