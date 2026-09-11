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
	"fmt"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// sampleFixture 三个时间点各一条审计日志，末条恰好落在区间右边界外
func sampleFixture(t *testing.T) (start, end time.Time, want []*plugin.AuditLog) {
	t.Helper()
	loc := time.FixedZone("CST", 8*3600)
	start = time.Date(2026, 9, 11, 13, 0, 0, 0, loc)
	end = time.Date(2026, 9, 11, 14, 0, 0, 0, loc)
	want = []*plugin.AuditLog{
		{ID: "l1", CreatedAt: start, ResponseStatus: 200, TotalTokens: 10, Duration: 100}, // 左闭：计入
		{ID: "l2", CreatedAt: start.Add(30 * time.Minute), ResponseStatus: 500, TotalTokens: 20, Duration: 200},
		{ID: "l3", CreatedAt: end, ResponseStatus: 200, TotalTokens: 30, Duration: 300}, // 右开：排除
	}
	return start, end, want
}

func seedAuditLogs(t *testing.T, s plugin.StoragePlugin, logs []*plugin.AuditLog) {
	t.Helper()
	for _, l := range logs {
		if err := s.SaveAuditLog(l); err != nil {
			t.Fatalf("SaveAuditLog(%s): %v", l.ID, err)
		}
	}
}

// TestAuditSamplesHalfOpenRange 左闭右开：created_at == end 必须排除
func TestAuditSamplesHalfOpenRange(t *testing.T) {
	start, end, logs := sampleFixture(t)

	t.Run("MemStorage", func(t *testing.T) {
		s := NewMemStorage()
		seedAuditLogs(t, s, logs)
		got, truncated, err := s.AuditSamples(start, end, 100)
		if err != nil {
			t.Fatalf("AuditSamples: %v", err)
		}
		if truncated {
			t.Error("truncated = true, want false")
		}
		if len(got) != 2 {
			t.Fatalf("len = %d, want 2（created_at == end 的行须排除）", len(got))
		}
	})

	t.Run("SQLStorage", func(t *testing.T) {
		s := newTestSQLStorage(t)
		seedAuditLogs(t, s, logs)
		got, truncated, err := s.AuditSamples(start, end, 100)
		if err != nil {
			t.Fatalf("AuditSamples: %v", err)
		}
		if truncated {
			t.Error("truncated = true, want false")
		}
		if len(got) != 2 {
			t.Fatalf("len = %d, want 2（created_at == end 的行须排除）", len(got))
		}
	})
}

// TestAuditSamplesImplConsistency 两实现同数据下结果一致（倒序、窄列字段逐一对齐）
func TestAuditSamplesImplConsistency(t *testing.T) {
	start, end, logs := sampleFixture(t)

	mem := NewMemStorage()
	seedAuditLogs(t, mem, logs)
	memGot, _, err := mem.AuditSamples(start, end, 100)
	if err != nil {
		t.Fatalf("mem AuditSamples: %v", err)
	}

	sqlS := newTestSQLStorage(t)
	seedAuditLogs(t, sqlS, logs)
	sqlGot, _, err := sqlS.AuditSamples(start, end, 100)
	if err != nil {
		t.Fatalf("sql AuditSamples: %v", err)
	}

	if len(memGot) != len(sqlGot) {
		t.Fatalf("len(mem)=%d len(sql)=%d", len(memGot), len(sqlGot))
	}
	for i := range memGot {
		if !memGot[i].CreatedAt.Equal(sqlGot[i].CreatedAt) {
			t.Errorf("第 %d 行 created_at: mem=%v sql=%v", i, memGot[i].CreatedAt, sqlGot[i].CreatedAt)
		}
		if memGot[i].ResponseStatus != sqlGot[i].ResponseStatus ||
			memGot[i].TotalTokens != sqlGot[i].TotalTokens ||
			memGot[i].DurationMS != sqlGot[i].DurationMS {
			t.Errorf("第 %d 行字段不一致: mem=%+v sql=%+v", i, memGot[i], sqlGot[i])
		}
	}
}

// TestAuditSamplesTruncation 达上限时保留最新 max 行并置 truncated
func TestAuditSamplesTruncation(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	start := time.Date(2026, 9, 11, 13, 0, 0, 0, loc)
	end := time.Date(2026, 9, 11, 14, 0, 0, 0, loc)
	logs := []*plugin.AuditLog{
		{ID: "l1", CreatedAt: start.Add(10 * time.Minute), ResponseStatus: 200, TotalTokens: 1},
		{ID: "l2", CreatedAt: start.Add(20 * time.Minute), ResponseStatus: 200, TotalTokens: 2},
		{ID: "l3", CreatedAt: start.Add(30 * time.Minute), ResponseStatus: 200, TotalTokens: 3},
	}

	for _, tc := range []struct {
		name string
		make func(*testing.T) plugin.StoragePlugin
	}{
		{"MemStorage", func(t *testing.T) plugin.StoragePlugin { return NewMemStorage() }},
		{"SQLStorage", func(t *testing.T) plugin.StoragePlugin { return newTestSQLStorage(t) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.make(t)
			seedAuditLogs(t, s, logs)
			got, truncated, err := s.AuditSamples(start, end, 2)
			if err != nil {
				t.Fatalf("AuditSamples: %v", err)
			}
			if !truncated {
				t.Error("truncated = false, want true")
			}
			if len(got) != 2 {
				t.Fatalf("len = %d, want 2", len(got))
			}
			// 倒序保留最新两行：l3(30min) 与 l2(20min)
			if got[0].CreatedAt.Before(got[1].CreatedAt) {
				t.Errorf("未按 created_at 倒序: %v, %v", got[0].CreatedAt, got[1].CreatedAt)
			}
			if got[0].TotalTokens != 3 || got[1].TotalTokens != 2 {
				t.Errorf("保留的不是最新两行: %d, %d", got[0].TotalTokens, got[1].TotalTokens)
			}
		})
	}
}

// TestAuditSamplesNonPositiveMax max <= 0 为非法入参：两实现均须返回非 nil 空切片且不置 truncated
func TestAuditSamplesNonPositiveMax(t *testing.T) {
	start, end, logs := sampleFixture(t)

	for _, tc := range []struct {
		name string
		make func(*testing.T) plugin.StoragePlugin
	}{
		{"MemStorage", func(t *testing.T) plugin.StoragePlugin { return NewMemStorage() }},
		{"SQLStorage", func(t *testing.T) plugin.StoragePlugin { return newTestSQLStorage(t) }},
	} {
		for _, max := range []int{0, -1} {
			t.Run(fmt.Sprintf("%s/max=%d", tc.name, max), func(t *testing.T) {
				s := tc.make(t)
				seedAuditLogs(t, s, logs)
				got, truncated, err := s.AuditSamples(start, end, max)
				if err != nil {
					t.Fatalf("max=%d AuditSamples: %v", max, err)
				}
				if len(got) != 0 {
					t.Errorf("max=%d len = %d, want 0", max, len(got))
				}
				if got == nil {
					t.Errorf("max=%d 返回 nil 切片, want 非 nil 空切片", max)
				}
				if truncated {
					t.Errorf("max=%d truncated = true, want false", max)
				}
			})
		}
	}
}

// TestAuditSamplesEmptyRange 空区间返回空结果且不报错
func TestAuditSamplesEmptyRange(t *testing.T) {
	start, end, _ := sampleFixture(t)

	for _, tc := range []struct {
		name string
		make func(*testing.T) plugin.StoragePlugin
	}{
		{"MemStorage", func(t *testing.T) plugin.StoragePlugin { return NewMemStorage() }},
		{"SQLStorage", func(t *testing.T) plugin.StoragePlugin { return newTestSQLStorage(t) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.make(t)
			got, truncated, err := s.AuditSamples(start, end, 100)
			if err != nil {
				t.Fatalf("AuditSamples: %v", err)
			}
			if len(got) != 0 || truncated {
				t.Errorf("空库应得 (0 行, truncated=false)，实得 (%d 行, %v)", len(got), truncated)
			}
		})
	}
}
