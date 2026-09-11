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

package plugin

import (
	"testing"
	"time"
)

// dashboardTestNow 注入固定时间：分桶断言必须确定性，函数内不得读时钟
func dashboardTestNow() (time.Time, *time.Location) {
	loc := time.FixedZone("CST", 8*3600)
	return time.Date(2026, 9, 11, 14, 37, 21, 0, loc), loc
}

func TestDashboardRange(t *testing.T) {
	now, loc := dashboardTestNow()

	t.Run("24h 按整点对齐", func(t *testing.T) {
		start, end, interval, buckets, err := DashboardRange(DashboardWindow24h, now)
		if err != nil {
			t.Fatalf("DashboardRange: %v", err)
		}
		if buckets != 24 {
			t.Errorf("buckets = %d, want 24", buckets)
		}
		if interval != time.Hour {
			t.Errorf("interval = %v, want 1h", interval)
		}
		if !end.Equal(now) {
			t.Errorf("end = %v, want %v", end, now)
		}
		// 当前整点 14:00 往前 23 小时 = 前一日 15:00
		wantStart := time.Date(2026, 9, 10, 15, 0, 0, 0, loc)
		if !start.Equal(wantStart) {
			t.Errorf("start = %v, want %v", start, wantStart)
		}
	})

	t.Run("7d 按自然日对齐", func(t *testing.T) {
		start, end, interval, buckets, err := DashboardRange(DashboardWindow7d, now)
		if err != nil {
			t.Fatalf("DashboardRange: %v", err)
		}
		if buckets != 7 || interval != 24*time.Hour {
			t.Errorf("buckets/interval = %d/%v, want 7/24h", buckets, interval)
		}
		if !end.Equal(now) {
			t.Errorf("end = %v, want %v", end, now)
		}
		// 今日零点 2026-09-11 往前 6 天 = 2026-09-05 零点
		wantStart := time.Date(2026, 9, 5, 0, 0, 0, 0, loc)
		if !start.Equal(wantStart) {
			t.Errorf("start = %v, want %v", start, wantStart)
		}
	})

	t.Run("30d 按自然日对齐", func(t *testing.T) {
		start, _, interval, buckets, err := DashboardRange(DashboardWindow30d, now)
		if err != nil {
			t.Fatalf("DashboardRange: %v", err)
		}
		if buckets != 30 || interval != 24*time.Hour {
			t.Errorf("buckets/interval = %d/%v, want 30/24h", buckets, interval)
		}
		// 今日零点 2026-09-11 往前 29 天 = 2026-08-13 零点
		wantStart := time.Date(2026, 8, 13, 0, 0, 0, 0, loc)
		if !start.Equal(wantStart) {
			t.Errorf("start = %v, want %v", start, wantStart)
		}
	})

	t.Run("非法 window 返回错误", func(t *testing.T) {
		if _, _, _, _, err := DashboardRange("1h", now); err == nil {
			t.Fatal("非法 window 应返回错误")
		}
		if _, _, _, _, err := DashboardRange("", now); err == nil {
			t.Fatal("空 window 应返回错误")
		}
	})
}
