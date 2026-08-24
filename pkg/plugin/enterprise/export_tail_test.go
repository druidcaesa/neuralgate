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
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
)

// collectingTarget 测试用收集目标：可控前 N 次失败
type collectingTarget struct {
	mu       sync.Mutex
	received []*plugin.AuditLog
	failNext int
	sends    int
}

func (c *collectingTarget) Send(logs []*plugin.AuditLog) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sends++
	if c.failNext > 0 {
		c.failNext--
		return errors.New("模拟故障")
	}
	c.received = append(c.received, logs...)
	return nil
}
func (c *collectingTarget) TestConnection() error { return nil }
func (c *collectingTarget) Close() error          { return nil }

func (c *collectingTarget) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.received)
}

// seedLogs 向存储写入测试审计日志
func seedLogs(t *testing.T, storage plugin.StoragePlugin, ids []string, created time.Time) {
	t.Helper()
	for i, id := range ids {
		if err := storage.SaveAuditLog(&plugin.AuditLog{
			ID:             id,
			RequestID:      id,
			ModelName:      "gpt-x",
			ResponseStatus: 200,
			TotalTokens:    i,
			CreatedAt:      created,
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPullSendRoundTripNoDuplication(t *testing.T) {
	storage := oss.NewMemStorage()
	base := time.Now().Add(-time.Minute)
	seedLogs(t, storage, []string{"a", "b", "c"}, base)

	e := NewTailExporter(storage)
	e.cursor = base.Add(-time.Minute)
	target := &collectingTarget{}
	e.target = target

	e.drain()
	if got := target.count(); got != 3 {
		t.Fatalf("首轮应推出 3 条, got %d", got)
	}
	second := &collectingTarget{}
	e.target = second
	e.drain()
	if second.count() != 0 {
		t.Fatalf("游标推进后不应重复拉取, got %d", second.count())
	}
}

func TestSameTimestampDedupedBySeen(t *testing.T) {
	storage := oss.NewMemStorage()
	same := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC) // 同一时刻三条
	seedLogs(t, storage, []string{"x1", "x2", "x3"}, same)

	e := NewTailExporter(storage)
	e.cursor = same
	target := &collectingTarget{}
	e.target = target

	// StartTime=cursor 闭区间会重复带回同刻日志，靠 seen 去重
	e.drain()
	e.drain()
	if got := target.count(); got != 3 {
		t.Fatalf("同刻日志应恰好推出一次, got %d", got)
	}
}

func TestFailureBuffersThenRetries(t *testing.T) {
	storage := oss.NewMemStorage()
	seedLogs(t, storage, []string{"r1"}, time.Now().Add(-time.Minute))

	e := NewTailExporter(storage)
	e.cursor = time.Now().Add(-time.Hour)
	target := &collectingTarget{failNext: 1}
	e.target = target

	e.drain() // Send 失败 → 入缓冲
	if got := target.count(); got != 0 {
		t.Fatalf("失败时不应送达, got %d", got)
	}
	e.mu.Lock()
	e.nextAttempt = time.Time{} // 测试跳过退避等待
	e.mu.Unlock()
	e.drain()
	if got := target.count(); got != 1 {
		t.Fatalf("恢复后应补投, got %d", got)
	}
}

func TestBackoffDelayCapsAt30s(t *testing.T) {
	cases := map[int]time.Duration{1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second, 20: maxBackoff}
	for failures, want := range cases {
		if got := backoffDelay(failures); got != want {
			t.Errorf("backoffDelay(%d) = %v, want %v", failures, got, want)
		}
	}
}

func TestBufferOverflowDropsOldest(t *testing.T) {
	e := NewTailExporter(oss.NewMemStorage())
	e.bufferLimit = 3
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("id-%d", i)
		if err := e.Export(&plugin.AuditLog{ID: id, RequestID: id}); err != nil {
			t.Fatal(err)
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.buffer) != 3 {
		t.Fatalf("缓冲应为 3, got %d", len(e.buffer))
	}
	if e.buffer[0].ID != "id-2" || e.buffer[2].ID != "id-4" {
		t.Errorf("应丢弃最旧保留最新: 首=%s 尾=%s", e.buffer[0].ID, e.buffer[2].ID)
	}
}

func TestCloseDrainsRemainingAndSafeOnUninit(t *testing.T) {
	// 未 Init 直接 Close 安全
	if err := NewTailExporter(oss.NewMemStorage()).Close(); err != nil {
		t.Fatalf("未初始化 Close 应安全: %v", err)
	}

	storage := oss.NewMemStorage()
	seedLogs(t, storage, []string{"z1"}, time.Now().Add(-time.Minute))
	e := NewTailExporter(storage)
	e.cursor = time.Now().Add(-time.Hour)
	target := &collectingTarget{failNext: 1}
	e.target = target
	e.drain() // 缓冲 1 条未送
	target.failNext = 0
	if err := e.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := target.count(); got != 1 {
		t.Fatalf("Close 应无视退避清空缓冲, got %d", got)
	}
}
