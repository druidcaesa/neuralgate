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
	"time"
)

// TestBucketIdleEviction 空闲超 TTL 的桶被惰性清扫；活跃键保留
func TestBucketIdleEviction(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.Local)
	l := NewRateLimiter(NewMemStorage(), 10, 100000, "token_bucket")
	l.now = func() time.Time { return now }

	_, _, _ = l.Allow("idle-key", "m", 0)
	_, _, _ = l.Allow("live-key", "m", 0)

	// 推进时间并制造足够访问触发清扫
	now = now.Add(2 * bucketIdleTTL)
	for i := 0; i < 300; i++ {
		_, _, _ = l.Allow("live-key", "m", 0)
	}
	l.mu.Lock()
	_, idleAlive := l.buckets["idle-key|m"]
	_, liveAlive := l.buckets["live-key|m"]
	l.mu.Unlock()
	if idleAlive {
		t.Error("空闲桶应被惰性淘汰")
	}
	if !liveAlive {
		t.Error("活跃桶不应被淘汰")
	}
}

// TestReloadConfigPreservesBuckets 重载只换阈值缓存，既有桶计数保留
func TestReloadConfigPreservesBuckets(t *testing.T) {
	storage := NewMemStorage()
	l := NewRateLimiter(storage, 5, 100000, "token_bucket")
	_ = l.Init(nil)
	_, _, _ = l.Allow("t", "m", 0)
	_, _, _ = l.Allow("t", "m", 0)
	usedBefore, _, _ := l.Status("t", "m")

	if err := l.ReloadConfig(); err != nil {
		t.Fatal(err)
	}
	usedAfter, _, _ := l.Status("t", "m")
	if usedAfter != usedBefore {
		t.Errorf("重载不应清计数: before=%d after=%d", usedBefore, usedAfter)
	}
}

// TestDeleteAuditLogsBeforeBatched 分批删除语义与单批等价(sqlite 模拟)
func TestDeleteAuditLogsBeforeBatched(t *testing.T) {
	s := NewSQLStorage()
	if err := s.Init(map[string]interface{}{"driver": "sqlite",
		"dsn":         t.TempDir() + "/batch.db",
		"encrypt_key": "k"}); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cutoff := time.Now().Add(-time.Hour).UnixMilli()
	for i := 0; i < 7; i++ {
		if _, err := s.exec(
			"INSERT INTO audit_logs (id, request_id, created_at) VALUES (?, ?, ?)",
			string(rune('a'+i)), "r", int64(cutoff-100+int64(i))); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.DeleteAuditLogsBefore(time.Now())
	if err != nil || n != 7 {
		t.Fatalf("应删净 7 条: n=%d err=%v", n, err)
	}
	var left int64
	if err := s.queryRow("SELECT COUNT(*) FROM audit_logs").Scan(&left); err != nil || left != 0 {
		t.Errorf("表应为空: %d %v", left, err)
	}
}
