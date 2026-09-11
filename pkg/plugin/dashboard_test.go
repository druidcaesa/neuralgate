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

func TestComputeDashboardBucketAlignment(t *testing.T) {
	now, _ := dashboardTestNow()

	t.Run("24h 桶键为连续整点", func(t *testing.T) {
		d, err := ComputeDashboard(nil, DashboardWindow24h, now, false)
		if err != nil {
			t.Fatalf("ComputeDashboard: %v", err)
		}
		if len(d.Trend) != 24 {
			t.Fatalf("trend len = %d, want 24", len(d.Trend))
		}
		if d.Trend[0].Date != "15:00" {
			t.Errorf("首桶 = %s, want 15:00", d.Trend[0].Date)
		}
		if d.Trend[23].Date != "14:00" {
			t.Errorf("末桶 = %s, want 14:00（当前小时）", d.Trend[23].Date)
		}
	})

	t.Run("7d 桶键为连续自然日且末桶为当日", func(t *testing.T) {
		d, err := ComputeDashboard(nil, DashboardWindow7d, now, false)
		if err != nil {
			t.Fatalf("ComputeDashboard: %v", err)
		}
		if len(d.Trend) != 7 {
			t.Fatalf("trend len = %d, want 7", len(d.Trend))
		}
		if d.Trend[0].Date != "2026-09-05" {
			t.Errorf("首桶 = %s, want 2026-09-05", d.Trend[0].Date)
		}
		if d.Trend[6].Date != "2026-09-11" {
			t.Errorf("末桶 = %s, want 2026-09-11（当日）", d.Trend[6].Date)
		}
	})

	t.Run("30d 桶数为 30", func(t *testing.T) {
		d, err := ComputeDashboard(nil, DashboardWindow30d, now, false)
		if err != nil {
			t.Fatalf("ComputeDashboard: %v", err)
		}
		if len(d.Trend) != 30 {
			t.Fatalf("trend len = %d, want 30", len(d.Trend))
		}
		if d.Trend[0].Date != "2026-08-13" {
			t.Errorf("首桶 = %s, want 2026-08-13", d.Trend[0].Date)
		}
	})

	t.Run("空样本补零占位不跳点", func(t *testing.T) {
		d, err := ComputeDashboard(nil, DashboardWindow7d, now, false)
		if err != nil {
			t.Fatalf("ComputeDashboard: %v", err)
		}
		for i, p := range d.Trend {
			if p.Requests != 0 || p.Tokens != 0 {
				t.Errorf("桶 %d (%s) = %+v, want 全零", i, p.Date, p)
			}
		}
		if d.Trend == nil || d.Alerts == nil {
			t.Fatal("Trend/Alerts 必须为空切片而非 nil，否则 JSON 序列化为 null")
		}
	})

	t.Run("非法 window 返回错误", func(t *testing.T) {
		if _, err := ComputeDashboard(nil, "1h", now, false); err == nil {
			t.Fatal("非法 window 应返回错误")
		}
	})
}

func TestComputeDashboardAggregation(t *testing.T) {
	now, loc := dashboardTestNow()
	at := func(h, m int) time.Time { return time.Date(2026, 9, 11, h, m, 0, 0, loc) }

	samples := []*AuditSample{
		{CreatedAt: at(14, 5), ResponseStatus: 200, TotalTokens: 100, DurationMS: 500},
		{CreatedAt: at(14, 30), ResponseStatus: 0, TotalTokens: 50, DurationMS: 1500},
		{CreatedAt: at(13, 10), ResponseStatus: 404, TotalTokens: 0, DurationMS: 100},
	}
	d, err := ComputeDashboard(samples, DashboardWindow24h, now, false)
	if err != nil {
		t.Fatalf("ComputeDashboard: %v", err)
	}

	if d.Summary.Requests != 3 {
		t.Errorf("requests = %d, want 3", d.Summary.Requests)
	}
	if d.Summary.Tokens != 150 {
		t.Errorf("tokens = %d, want 150", d.Summary.Tokens)
	}
	// (500+1500+100)/3 = 700
	if d.Summary.AvgLatencyMS != 700 {
		t.Errorf("avg_latency_ms = %d, want 700", d.Summary.AvgLatencyMS)
	}
	// 仅 200 计入成功；status==0 视为失败，404 视为失败 → 1/3 = 33.3
	if d.Summary.SuccessRate != 33.3 {
		t.Errorf("success_rate = %v, want 33.3", d.Summary.SuccessRate)
	}
	// 14:05 与 14:30 落在末桶（14:00 起），13:10 落在第 22 桶
	if d.Trend[23].Requests != 2 || d.Trend[23].Tokens != 150 {
		t.Errorf("末桶 = %+v, want requests=2 tokens=150", d.Trend[23])
	}
	if d.Trend[22].Requests != 1 || d.Trend[22].Tokens != 0 {
		t.Errorf("第 22 桶 = %+v, want requests=1 tokens=0", d.Trend[22])
	}
}

// TestComputeDashboardAvgLatencyRounds 平均延迟四舍五入为整数毫秒：截断实现得 1，本用例钉住取整口径
func TestComputeDashboardAvgLatencyRounds(t *testing.T) {
	now, loc := dashboardTestNow()
	at := func(m int) time.Time { return time.Date(2026, 9, 11, 14, m, 0, 0, loc) }

	samples := []*AuditSample{
		{CreatedAt: at(5), ResponseStatus: 200, DurationMS: 1},
		{CreatedAt: at(6), ResponseStatus: 200, DurationMS: 2},
	}
	d, err := ComputeDashboard(samples, DashboardWindow24h, now, false)
	if err != nil {
		t.Fatalf("ComputeDashboard: %v", err)
	}
	if d.Summary.AvgLatencyMS != 2 {
		t.Errorf("avg_latency_ms = %d, want 2（(1+2)/2 应四舍五入而非截断）", d.Summary.AvgLatencyMS)
	}
}

func TestComputeDashboardSuccessRateEdges(t *testing.T) {
	now, loc := dashboardTestNow()
	at := func(h int) time.Time { return time.Date(2026, 9, 11, h, 0, 0, 0, loc) }

	// 200/302 计成功，400/500/0 计失败 → 2/5 = 40.0
	samples := []*AuditSample{
		{CreatedAt: at(14), ResponseStatus: 200},
		{CreatedAt: at(14), ResponseStatus: 302},
		{CreatedAt: at(14), ResponseStatus: 400},
		{CreatedAt: at(14), ResponseStatus: 500},
		{CreatedAt: at(14), ResponseStatus: 0},
	}
	d, err := ComputeDashboard(samples, DashboardWindow24h, now, false)
	if err != nil {
		t.Fatalf("ComputeDashboard: %v", err)
	}
	if d.Summary.SuccessRate != 40.0 {
		t.Errorf("success_rate = %v, want 40.0（status==0 计失败）", d.Summary.SuccessRate)
	}
}

func TestComputeDashboardTruncated(t *testing.T) {
	now, loc := dashboardTestNow()
	d, err := ComputeDashboard(
		[]*AuditSample{{CreatedAt: time.Date(2026, 9, 11, 14, 0, 0, 0, loc), ResponseStatus: 200}},
		DashboardWindow24h, now, true)
	if err != nil {
		t.Fatalf("ComputeDashboard: %v", err)
	}
	if !d.Truncated {
		t.Error("truncated 标记未透传")
	}
}
