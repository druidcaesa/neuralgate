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
	"sync"
	"time"
)

// breakerState 上游熔断状态
type breakerState int

const (
	Closed   breakerState = iota // 正常:放行并按失败计数
	Open                         // 剔除:候选过滤时不可选
	HalfOpen                     // 放行有限试探,连续成功转 closed,失败回 open
)

// BreakerConfig 熔断参数;now 可注入(测试/统一时钟)
type BreakerConfig struct {
	FailureThreshold int           // 采样窗内失败数 → open
	SampleWindow     time.Duration // 失败计数窗口(滑动:越过即重置)
	OpenFor          time.Duration // open 时长 → half-open
	SuccessThreshold int           // half-open 连续成功 → closed
	ProbeConcurrency int           // half-open 并发试探上限;<=0 取 1
	now              func() time.Time
}

// NewBreakerConfig 构造;threshold/open/succ 非法值(<=0)不在此兜底(由 config 默认保证)
func NewBreakerConfig(threshold int, window, openFor time.Duration, succ, probe int, now func() time.Time) *BreakerConfig {
	if probe <= 0 {
		probe = 1
	}
	return &BreakerConfig{
		FailureThreshold: threshold, SampleWindow: window, OpenFor: openFor,
		SuccessThreshold: succ, ProbeConcurrency: probe, now: now,
	}
}

// upstreamBreaker 单个上游的状态机;同一注册表锁内访问
type upstreamBreaker struct {
	cfg       *BreakerConfig
	state     breakerState
	failures  int // closed 窗内失败数
	winStart  time.Time
	openedAt  time.Time // open 起始(决定转 half-open 时刻)
	successes int       // half-open 连续成功
	inFlight  int       // half-open 在途试探
	lastSeen  time.Time
}

// Allow 当前是否放行;调用方在放行(或试探)后须调 Record(ok) 配对
func (b *upstreamBreaker) Allow() bool {
	now := b.cfg.now()
	switch b.state {
	case Closed:
		if now.Sub(b.winStart) > b.cfg.SampleWindow {
			b.failures, b.winStart = 0, now
		}
		return true
	case Open:
		if now.Sub(b.openedAt) >= b.cfg.OpenFor {
			b.state = HalfOpen
			b.successes, b.inFlight = 0, 0
			return b.allowProbe()
		}
		return false
	default: // HalfOpen
		return b.allowProbe()
	}
}

func (b *upstreamBreaker) allowProbe() bool {
	if b.inFlight < b.cfg.ProbeConcurrency {
		b.inFlight++
		return true
	}
	return false
}

// Selectable 判定当前是否可参与选路:只推进到期状态(open 到期 → half-open),不占试探槽;
// 槽位由真正选中后的 Allow 占用。与 Allow 的时间推进一致,HalfOpen 容量仅在实际占槽时判定
func (b *upstreamBreaker) Selectable() bool {
	now := b.cfg.now()
	switch b.state {
	case Closed:
		if now.Sub(b.winStart) > b.cfg.SampleWindow {
			b.failures, b.winStart = 0, now
		}
		return true
	case Open:
		if now.Sub(b.openedAt) >= b.cfg.OpenFor {
			b.state = HalfOpen
			b.successes, b.inFlight = 0, 0
			return true
		}
		return false
	default: // HalfOpen
		return true
	}
}

// Release 释放未成行的试探槽:仅当 half-open 且槽位被 Allow 占用时递减 inFlight,不改状态。
// 供选路后、转发前中止的路径调用,与 Record 二选一配对 Allow,防止槽位被占至空闲清扫
func (b *upstreamBreaker) Release() {
	if b.state == HalfOpen && b.inFlight > 0 {
		b.inFlight--
	}
}

// Record 上报一次结果;调用方在 Allow()==true 的尝试结束后调用(ok=连通且状态<500)
func (b *upstreamBreaker) Record(ok bool) {
	now := b.cfg.now()
	b.lastSeen = now
	switch b.state {
	case HalfOpen:
		b.inFlight--
		if ok {
			b.successes++
			if b.successes >= b.cfg.SuccessThreshold {
				b.reset(now)
			}
		} else {
			b.failures, b.winStart = 0, now
			b.state = Open
			b.openedAt = now
		}
	case Closed:
		if ok {
			b.failures, b.winStart = 0, now
			return
		}
		b.failures++
		b.winStart = now
		if b.failures >= b.cfg.FailureThreshold {
			b.state = Open
			b.openedAt = now
		}
	}
}

// recordProbe 上报一次主动探活结果(仅连通性)。与流量 Record 的关键区别:探针未经 Allow,
// 未占用试探槽,因此任何分支都不增减 inFlight,绝不虚增/打负槽位。状态语义:
//   - Closed:  与 Record 一致(成功清失败窗;失败累积——连通性失败即最强失败信号,达阈值 → open)
//   - Open:    成功且已过 OpenFor → HalfOpen(清零 successes/inFlight,加速恢复);未到期忽略
//   - HalfOpen:忽略探针结果——恢复期的试探槽与连续成功由流量 Allow/Record 配对精确记账,
//     探针不插队,以免打乱槽位计数或弱化连续成功保证
func (b *upstreamBreaker) recordProbe(ok bool) {
	now := b.cfg.now()
	b.lastSeen = now
	switch b.state {
	case HalfOpen:
		// 忽略:见方法注释
	case Open:
		if ok && now.Sub(b.openedAt) >= b.cfg.OpenFor {
			b.state = HalfOpen
			b.successes, b.inFlight = 0, 0
		}
	case Closed:
		if ok {
			b.failures, b.winStart = 0, now
			return
		}
		b.failures++
		b.winStart = now
		if b.failures >= b.cfg.FailureThreshold {
			b.state = Open
			b.openedAt = now
		}
	}
}

// reset 转 closed 并清零
func (b *upstreamBreaker) reset(now time.Time) {
	b.state = Closed
	b.failures, b.successes, b.inFlight = 0, 0, 0
	b.winStart, b.openedAt = now, now
}

// State 供 gauge/日志
func (b *upstreamBreaker) State() breakerState { return b.state }

// idleTTL 空闲淘汰:上游删除/禁用后清理注册项
const breakerIdleTTL = time.Hour

// BreakerRegistry 按 upstream ID 维护熔断状态;惰性登记 + 访问计数触发清扫
type BreakerRegistry struct {
	cfg    *BreakerConfig
	now    func() time.Time
	mu     sync.Mutex
	states map[string]*upstreamBreaker
	visits int
}

// NewRegistry 创建注册表;cfg 为 nil 时启用方不会构造
func NewRegistry(cfg *BreakerConfig) *BreakerRegistry {
	if cfg.now == nil {
		cfg.now = time.Now
	}
	return &BreakerRegistry{cfg: cfg, now: cfg.now, states: make(map[string]*upstreamBreaker)}
}

// breakerFor 取/建条目;访问计数触发惰性清扫(上限防无界)
func (r *BreakerRegistry) breakerFor(id string) *upstreamBreaker {
	r.visits++
	if r.visits >= 256 {
		r.visits = 0
		cut := r.now().Add(-breakerIdleTTL)
		for k, b := range r.states {
			if b.lastSeen.Before(cut) {
				delete(r.states, k)
			}
		}
	}
	b, ok := r.states[id]
	if !ok {
		b = &upstreamBreaker{cfg: r.cfg, state: Closed, winStart: r.now(), lastSeen: r.now()}
		r.states[id] = b
	}
	return b
}

// Allow 某上游当前是否可参与选路(放行调用方须在尝试后调 Record 配对)
func (r *BreakerRegistry) Allow(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.breakerFor(id).Allow()
}

// Selectable 判定某上游当前是否可参与选路(只推进到期状态,不占试探槽;槽位由选中后的 Allow 占用)
func (r *BreakerRegistry) Selectable(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.breakerFor(id).Selectable()
}

// Record 记录某上游一次尝试结果
func (r *BreakerRegistry) Record(id string, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.breakerFor(id).Record(ok)
}

// recordProbe 记录某上游一次主动探活结果;不占/不改 half-open 试探槽(供 StartHealthProbe 使用)
func (r *BreakerRegistry) recordProbe(id string, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.breakerFor(id).recordProbe(ok)
}

// Release 释放某上游未成行的试探槽(仅递减 inFlight,不改状态);与 Record 二选一配对 Allow
func (r *BreakerRegistry) Release(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.breakerFor(id).Release()
}

// sweep 显式清扫(测试用)
func (r *BreakerRegistry) sweep() {
	r.mu.Lock()
	defer r.mu.Unlock()
	cut := r.now().Add(-breakerIdleTTL)
	for k, b := range r.states {
		if b.lastSeen.Before(cut) {
			delete(r.states, k)
		}
	}
}

// Snapshot 供 gauge:各上游状态值(0 closed/1 open/2 half-open)
func (r *BreakerRegistry) Snapshot() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]int, len(r.states))
	for k, b := range r.states {
		out[k] = int(b.State())
	}
	return out
}
