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

	"go.uber.org/zap"
)

func nopLogger() *zap.Logger { return zap.NewNop() }

// TestRunWithRecoverRecovers panic 被兜底后调用方正常返回
func TestRunWithRecoverRecovers(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic escaped runWithRecover: %v", r)
		}
	}()
	runWithRecover(nopLogger(), "unit-test", func() {
		panic("boom")
	})
}

// TestCycleLoopSurvivesPanic 循环体 panic 后循环应存活并可正常停止
// （无恢复时该测试进程会因未捕获 panic 直接崩溃）
func TestCycleLoopSurvivesPanic(t *testing.T) {
	tasks := NewTasks(nil, "sha256", time.Hour, 100, 0, nopLogger())
	done := make(chan struct{})
	go func() {
		tasks.cycleLoop(10*time.Millisecond, func() {
			panic("tick boom")
		})
		close(done)
	}()
	select {
	case <-time.After(80 * time.Millisecond):
	case <-done:
		t.Fatal("cycleLoop should keep running after panics")
	}
	close(tasks.stopCh) // 直接发停止信号(未走 Start，doneCh 由测试自身 goroutine 管理)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cycleLoop did not stop")
	}
}

// TestStopWithoutStart 未 Start 时 Stop 应为安全空操作
func TestStopWithoutStart(t *testing.T) {
	tasks := NewTasks(nil, "sha256", time.Hour, 100, 0, nopLogger())
	tasks.Stop() // 不应阻塞或 panic
}
