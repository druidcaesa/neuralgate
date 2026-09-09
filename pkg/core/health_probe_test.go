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
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestProbeClassify 连通性判定:任何 HTTP 响应视为可达,仅连接/读取错误视为失败
func TestProbeClassify(t *testing.T) {
	if !probeOK(nil, &http.Response{StatusCode: 404}) {
		t.Error("404 也视为可达")
	}
	if !probeOK(nil, &http.Response{StatusCode: 500}) {
		t.Error("5xx 也视为可达(连通性)")
	}
	if probeOK(errors.New("dial"), nil) {
		t.Error("连接错误应视为失败")
	}
	if probeOK(nil, nil) {
		t.Error("nil+无 err 不可能,保守算失败")
	}
}

// TestRegistryProbeRespectsOpenCooldown 探针成功未到 OpenFor 不得转 half-open(尊重熔断冷却)
func TestRegistryProbeRespectsOpenCooldown(t *testing.T) {
	now := time.Unix(1000, 0)
	reg := NewRegistry(NewBreakerConfig(2, time.Minute, 3*time.Second, 1, 1, func() time.Time { return now }))
	reg.Record("u", false)
	reg.Record("u", false) // 阈值 2 → open
	if st := reg.states["u"].State(); st != Open {
		t.Fatalf("应 open, got %v", st)
	}

	// 冷却期内:探针成功不得推进状态,也不得占用/改动 inFlight
	reg.recordProbe("u", true)
	if st := reg.states["u"].State(); st != Open {
		t.Fatalf("OpenFor 未到期探针成功应保持 open, got %v", st)
	}
	if b := reg.states["u"]; b.inFlight != 0 || b.successes != 0 {
		t.Fatalf("冷却期探针不得改动 success/inFlight, got %+v", b)
	}
}

// TestRegistryProbeAcceleratesOpenToHalfOpen 到期后探针成功把 open 推进 half-open(加速恢复),
// 且只重置 success/inFlight 不占槽;half-open 期探针结果被忽略,不干预流量记账
func TestRegistryProbeAcceleratesOpenToHalfOpen(t *testing.T) {
	now := time.Unix(1000, 0)
	reg := NewRegistry(NewBreakerConfig(2, time.Minute, 3*time.Second, 1, 1, func() time.Time { return now }))
	reg.Record("u", false)
	reg.Record("u", false) // → open
	now = now.Add(4 * time.Second)

	reg.recordProbe("u", true)
	b := reg.states["u"]
	if st := b.State(); st != HalfOpen {
		t.Fatalf("到期后探针成功应转 half-open, got %v", st)
	}
	if b.inFlight != 0 || b.successes != 0 {
		t.Fatalf("转 half-open 应清零 success/inFlight 且不占槽, got success=%d inFlight=%d", b.successes, b.inFlight)
	}

	// 大量探针 tick(ok/fail 交替)在 half-open 下必须被忽略:状态、槽位、连续成功都不变
	for i := 0; i < 200; i++ {
		reg.recordProbe("u", i%3 != 0)
	}
	if b.State() != HalfOpen {
		t.Fatalf("half-open 期探针不得改状态, got %v", b.State())
	}
	if b.inFlight != 0 {
		t.Fatalf("half-open 期探针不得增减 inFlight, got %d", b.inFlight)
	}
	if b.successes != 0 {
		t.Fatalf("half-open 期探针成功不得计入连续成功, got %d", b.successes)
	}
}

// TestRegistryProbeNeverDrivesInFlightNegative 回归:探针在 half-open 下绝不把 inFlight 打负
// 或虚增;槽位只由流量 Allow/Record/Release 配对精确记账
func TestRegistryProbeNeverDrivesInFlightNegative(t *testing.T) {
	now := time.Unix(1000, 0)
	reg := NewRegistry(NewBreakerConfig(2, time.Minute, 3*time.Second, 2, 2, func() time.Time { return now }))
	reg.Record("u", false)
	reg.Record("u", false) // → open
	now = now.Add(4 * time.Second)
	reg.recordProbe("u", true) // → half-open,inFlight=0
	b := reg.states["u"]

	// 流量占满 ProbeConcurrency=2 槽
	if !reg.Allow("u") || !reg.Allow("u") {
		t.Fatal("ProbeConcurrency=2 首两个 Allow 应放行")
	}
	if b.inFlight != 2 {
		t.Fatalf("Allow 占槽后 inFlight 应为 2, got %d", b.inFlight)
	}

	// 占满期间大量探针 tick:不得把槽打负,也不得虚增
	for i := 0; i < 200; i++ {
		reg.recordProbe("u", i%2 == 0)
	}
	if b.inFlight != 2 {
		t.Fatalf("half-open 探针在占满时不得增减 inFlight, got %d", b.inFlight)
	}
	if b.successes != 0 {
		t.Fatalf("half-open 探针成功不得计入 success, got %d", b.successes)
	}

	// 槽清空(inFlight=0)后探针仍不得打负
	reg.Release("u")
	reg.Release("u")
	if b.inFlight != 0 {
		t.Fatalf("Release 后 inFlight 应为 0, got %d", b.inFlight)
	}
	for i := 0; i < 200; i++ {
		reg.recordProbe("u", true)
		if b.inFlight < 0 {
			t.Fatalf("inFlight 不得为负, got %d", b.inFlight)
		}
	}
	if b.inFlight != 0 {
		t.Fatalf("inFlight=0 时探针也不得虚增/打负, got %d", b.inFlight)
	}

	// 槽位仍可被流量精确配对:连续成功达 SuccessThreshold=2 → closed
	if !reg.Allow("u") {
		t.Fatal("槽空应放行")
	}
	reg.Record("u", true)
	if b.State() != HalfOpen || b.inFlight != 0 {
		t.Fatalf("第 1 次成功未达阈值应保持 half-open 且归还槽位, got state=%v inFlight=%d", b.State(), b.inFlight)
	}
	if !reg.Allow("u") {
		t.Fatal("槽空应放行")
	}
	reg.Record("u", true)
	if b.State() != Closed || b.inFlight != 0 || b.successes != 0 {
		t.Fatalf("连续成功达阈值应 closed 并清零, got state=%v inFlight=%d success=%d", b.State(), b.inFlight, b.successes)
	}
}

// TestRegistryProbeOnClosedKeepsFailureWindow 探针成功对 closed 清失败窗(与 Record 一致,计划接受);
// 探针失败对 closed 与流量失败同语义累积(连通性即最强失败信号,不占槽)
func TestRegistryProbeOnClosedKeepsFailureWindow(t *testing.T) {
	now := time.Unix(1000, 0)
	reg := NewRegistry(NewBreakerConfig(2, time.Minute, 3*time.Second, 1, 1, func() time.Time { return now }))
	reg.Record("u", false) // 流量失败 1 次
	if b := reg.states["u"]; b.failures != 1 {
		t.Fatalf("流量失败后 failures 应为 1, got %d", b.failures)
	}
	reg.recordProbe("u", true) // 探针成功 → 清窗
	if b := reg.states["u"]; b.failures != 0 || b.State() != Closed {
		t.Fatalf("closed 探针成功应清失败窗并保持 closed, got failures=%d state=%v", b.failures, b.State())
	}

	// 探针失败累积:连续 2 次探针失败(阈值 2)→ open,与流量失败一致但全程无槽
	reg.recordProbe("u", false)
	reg.recordProbe("u", false)
	b := reg.states["u"]
	if b.State() != Open {
		t.Fatalf("探针失败达阈值应 open, got %v", b.State())
	}
	if b.inFlight != 0 {
		t.Fatalf("探针触发的 open 不得带 inFlight, got %d", b.inFlight)
	}
}

// waitCond 带截止的轮询断言,避免脆弱的固定 sleep
func waitCond(t *testing.T, timeout time.Duration, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待超时(%s): %s", timeout, what)
}

// closedAddr 返回一个确定已关闭的本地端口(先占后放),供连接被拒场景
func closedAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("预留端口失败: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

// TestStartHealthProbeRecordsAndStops 集成:StartHealthProbe 按 interval 周期探活并把结果写入
// 注册表(open → half-open 加速);stop 返回后探针不再写注册表
func TestStartHealthProbeRecordsAndStops(t *testing.T) {
	reg := NewRegistry(NewBreakerConfig(1, time.Minute, 50*time.Millisecond, 1, 1, time.Now))
	reg.Record("up", false)   // → open(可达目标,由探针加速恢复)
	reg.Record("down", false) // → open(不可达目标,探针失败保持 open)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	down := "http://" + closedAddr(t)

	// probeTicks 统计 baseURLs 求值次数,用于断言 stop 后探针 goroutine 已退出(不再有新 tick)
	var probeTicks atomic.Int32
	stop := StartHealthProbe(reg, func() map[string]string {
		probeTicks.Add(1)
		return map[string]string{"up": srv.URL, "down": down}
	}, 10*time.Millisecond, "/healthz", time.Second)

	// up:探针成功 + OpenFor(50ms)到期 → half-open(无需流量驱动)
	waitCond(t, 3*time.Second, "up 应被探针推进 half-open", func() bool {
		return reg.Snapshot()["up"] == int(HalfOpen)
	})
	// down:不可达,探针失败不推进(仍 open)
	if got := reg.Snapshot()["down"]; got != int(Open) {
		t.Fatalf("down 应保持 open(探针失败不推进), got %v", got)
	}

	// half-open 不占槽:流量 Allow→Record 配对把 up 补成 closed(SuccessThreshold=1)
	if !reg.Allow("up") {
		t.Fatal("half-open 槽空应放行流量试探")
	}
	reg.Record("up", true)
	if got := reg.Snapshot()["up"]; got != int(Closed) {
		t.Fatalf("流量配对成功应 closed, got %v", got)
	}

	// stop 返回即探针 goroutine 已退出:等待大于数个 interval,baseURLs 不再被求值
	stop()
	n := probeTicks.Load()
	time.Sleep(60 * time.Millisecond)
	if got := probeTicks.Load(); got != n {
		t.Fatalf("stop 后探针仍在运行: ticks %d → %d", n, got)
	}
}
