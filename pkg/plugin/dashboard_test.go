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
	"fmt"
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

// TestComputeDashboardLatencyPercentiles 最近秩法分位：
// 升序排列后取 v[ceil(k*n/100)-1]，不插值
func TestComputeDashboardLatencyPercentiles(t *testing.T) {
	now, _ := dashboardTestNow()

	// 1..100 毫秒共 100 个样本：P50=50、P95=95、P99=99
	hundred := make([]int64, 100)
	for i := range hundred {
		hundred[i] = int64(i + 1)
	}

	for _, tc := range []struct {
		name          string
		ms            []int64
		p50, p95, p99 int64
	}{
		{"单样本", []int64{7}, 7, 7, 7},
		{"两样本取最近秩", []int64{1, 3}, 1, 3, 3},
		{"五样本", []int64{10, 20, 30, 40, 50}, 30, 50, 50},
		{"全等值", []int64{5, 5, 5, 5}, 5, 5, 5},
		{"乱序输入", []int64{50, 10, 40, 20, 30}, 30, 50, 50},
		{"100 样本", hundred, 50, 95, 99},
	} {
		t.Run(tc.name, func(t *testing.T) {
			samples := make([]*AuditSample, 0, len(tc.ms))
			for _, d := range tc.ms {
				samples = append(samples, &AuditSample{
					CreatedAt: now, ResponseStatus: 200, DurationMS: d})
			}
			d, err := ComputeDashboard(samples, DashboardWindow24h, now, false)
			if err != nil {
				t.Fatalf("ComputeDashboard: %v", err)
			}
			if d.Latency.P50MS != tc.p50 || d.Latency.P95MS != tc.p95 || d.Latency.P99MS != tc.p99 {
				t.Errorf("P50/P95/P99 = %d/%d/%d, want %d/%d/%d",
					d.Latency.P50MS, d.Latency.P95MS, d.Latency.P99MS, tc.p50, tc.p95, tc.p99)
			}
			// 排序必须发生在副本上：入参顺序不得被改动
			for i, want := range tc.ms {
				if samples[i].DurationMS != want {
					t.Fatalf("入参第 %d 项被改动: %d, want %d", i, samples[i].DurationMS, want)
				}
			}
		})
	}
}

// TestComputeDashboardLatencyBuckets 延迟分档边界：左闭右开，末档为 ≥10s
func TestComputeDashboardLatencyBuckets(t *testing.T) {
	now, _ := dashboardTestNow()

	for _, tc := range []struct {
		ms   int64
		want int
	}{
		{0, 0}, {99, 0},
		{100, 1}, {299, 1},
		{300, 2}, {999, 2},
		{1000, 3}, {2999, 3},
		{3000, 4}, {9999, 4},
		{10000, 5}, {60000, 5},
	} {
		d, err := ComputeDashboard(
			[]*AuditSample{{CreatedAt: now, ResponseStatus: 200, DurationMS: tc.ms}},
			DashboardWindow24h, now, false)
		if err != nil {
			t.Fatalf("耗时 %dms ComputeDashboard: %v", tc.ms, err)
		}
		if len(d.Latency.Buckets) != 6 {
			t.Fatalf("耗时 %dms 档数 = %d, want 6", tc.ms, len(d.Latency.Buckets))
		}
		for i, b := range d.Latency.Buckets {
			want := int64(0)
			if i == tc.want {
				want = 1
			}
			if b.Count != want {
				t.Errorf("耗时 %dms: 档 %d(%s) 计数 = %d, want %d",
					tc.ms, i, b.Label, b.Count, want)
			}
		}
	}
}

// TestLatencyBucketIndexAlwaysInRange 钉住「返回值必须能安全索引 computeLatency 的 Buckets」：
// 桶按文案数建，故下标上界只由文案数决定，与上界表长度无关。
// 上界多于文案时循环内那条 return 曾直接返回越界下标，调用方索引即 panic
func TestLatencyBucketIndexAlwaysInRange(t *testing.T) {
	origBounds, origLabels := latencyBucketBounds, latencyBucketLabels
	defer func() {
		latencyBucketBounds, latencyBucketLabels = origBounds, origLabels
	}()

	// 文案不动、上界补两条，模拟「只加上界忘加文案」
	latencyBucketBounds = append(append([]int64{}, origBounds...), 50000, 200000)

	last := len(latencyBucketLabels) - 1
	for _, ms := range []int64{0, 99, 100, 299, 1000, 3000, 9999, 10000, 60000, 1 << 40} {
		if idx := latencyBucketIndex(ms); idx < 0 || idx > last {
			t.Errorf("耗时 %dms 档位下标 = %d，越出 [0,%d]，调用方索引 Buckets 即 panic",
				ms, idx, last)
		}
	}
	// 未落入任何上界者归末档，不得溢出到不存在的档位
	if idx := latencyBucketIndex(60000); idx != last {
		t.Errorf("超出全部上界的耗时归入档 %d, want %d", idx, last)
	}
}

// TestComputeDashboardLatencyBucketLabels 档位文案即下发前端的精确取值，
// 空样本下也须返回完整骨架
func TestComputeDashboardLatencyBucketLabels(t *testing.T) {
	now, _ := dashboardTestNow()

	d, err := ComputeDashboard(nil, DashboardWindow24h, now, false)
	if err != nil {
		t.Fatalf("ComputeDashboard: %v", err)
	}
	want := []string{"<100ms", "100–300ms", "0.3–1s", "1–3s", "3–10s", "≥10s"}
	if len(d.Latency.Buckets) != len(want) {
		t.Fatalf("档数 = %d, want %d", len(d.Latency.Buckets), len(want))
	}
	for i, w := range want {
		if d.Latency.Buckets[i].Label != w {
			t.Errorf("档 %d 文案 = %q, want %q", i, d.Latency.Buckets[i].Label, w)
		}
		if d.Latency.Buckets[i].Count != 0 {
			t.Errorf("档 %d 空样本计数 = %d, want 0", i, d.Latency.Buckets[i].Count)
		}
	}
	if d.Latency.P50MS != 0 || d.Latency.P95MS != 0 || d.Latency.P99MS != 0 {
		t.Errorf("空样本分位应为 0，实得 %d/%d/%d",
			d.Latency.P50MS, d.Latency.P95MS, d.Latency.P99MS)
	}
}

// TestComputeDashboardStatusBuckets 状态分布恒定五档，
// 且分桶之和恒等于 requests——other 兜底使分类全量覆盖
func TestComputeDashboardStatusBuckets(t *testing.T) {
	now, _ := dashboardTestNow()

	samples := []*AuditSample{
		{CreatedAt: now, ResponseStatus: 200},
		{CreatedAt: now, ResponseStatus: 204},
		{CreatedAt: now, ResponseStatus: 300},
		{CreatedAt: now, ResponseStatus: 302},
		{CreatedAt: now, ResponseStatus: 404},
		{CreatedAt: now, ResponseStatus: 500},
		{CreatedAt: now, ResponseStatus: 503},
		{CreatedAt: now, ResponseStatus: 0},   // 未产生响应
		{CreatedAt: now, ResponseStatus: 100}, // 异常取值
		{CreatedAt: now, ResponseStatus: 999}, // 异常取值
	}
	d, err := ComputeDashboard(samples, DashboardWindow24h, now, false)
	if err != nil {
		t.Fatalf("ComputeDashboard: %v", err)
	}

	if len(d.Status) != 5 {
		t.Fatalf("档数 = %d, want 5", len(d.Status))
	}
	want := []struct {
		class string
		count int64
	}{
		{"2xx", 2},   // 200 204
		{"3xx", 2},   // 300 302
		{"4xx", 1},   // 404
		{"5xx", 2},   // 500 503
		{"other", 3}, // 0 100 999
	}
	var sum int64
	for i, w := range want {
		if d.Status[i].Class != w.class || d.Status[i].Count != w.count {
			t.Errorf("档 %d = %s/%d, want %s/%d",
				i, d.Status[i].Class, d.Status[i].Count, w.class, w.count)
		}
		sum += d.Status[i].Count
	}
	if sum != d.Summary.Requests {
		t.Errorf("分桶之和 = %d, want requests = %d", sum, d.Summary.Requests)
	}
	// 成功判定为 status ∈ [200,400)：200 204 300 302 共 4 个成功，其余 6 个失败
	if d.Summary.Failed != 6 {
		t.Errorf("failed = %d, want 6", d.Summary.Failed)
	}
}

// TestComputeDashboardEmptySkeletons 空样本各面板仍须返回骨架而非 null
func TestComputeDashboardEmptySkeletons(t *testing.T) {
	now, _ := dashboardTestNow()

	d, err := ComputeDashboard(nil, DashboardWindow24h, now, false)
	if err != nil {
		t.Fatalf("ComputeDashboard: %v", err)
	}
	if len(d.Status) != 5 {
		t.Errorf("status 档数 = %d, want 5", len(d.Status))
	}
	if d.Status == nil {
		t.Error("status 为 nil, want 非 nil 骨架")
	}
	for i, want := range []string{"2xx", "3xx", "4xx", "5xx", "other"} {
		if d.Status[i].Class != want {
			t.Errorf("档 %d = %q, want %q", i, d.Status[i].Class, want)
		}
		if d.Status[i].Count != 0 {
			t.Errorf("档 %d 空样本计数 = %d, want 0", i, d.Status[i].Count)
		}
	}
}

// sampleRepeat 生成 n 条同名模型样本，便于构造排行数据
func sampleRepeat(now time.Time, name string, n int) []*AuditSample {
	out := make([]*AuditSample, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, &AuditSample{CreatedAt: now, ResponseStatus: 200, ModelName: name})
	}
	return out
}

// TestComputeDashboardTopModels 按请求量降序；并列按模型名升序
func TestComputeDashboardTopModels(t *testing.T) {
	now, _ := dashboardTestNow()

	samples := sampleRepeat(now, "b", 3)
	samples = append(samples, sampleRepeat(now, "a", 3)...)
	samples = append(samples, sampleRepeat(now, "c", 5)...)

	d, err := ComputeDashboard(samples, DashboardWindow24h, now, false)
	if err != nil {
		t.Fatalf("ComputeDashboard: %v", err)
	}
	if len(d.TopModels) != 3 {
		t.Fatalf("排行条数 = %d, want 3", len(d.TopModels))
	}
	// c(5) 居首；a 与 b 并列 3 请求，按名升序 a 在 b 前
	for i, want := range []string{"c", "a", "b"} {
		if d.TopModels[i].ModelName != want {
			t.Errorf("第 %d 名 = %q, want %q", i, d.TopModels[i].ModelName, want)
		}
	}
}

// TestComputeDashboardTopModelsDeterministic 并列次序必须稳定：
// map 迭代顺序随机，缺次级键会让同一份数据两次请求给出不同顺序
func TestComputeDashboardTopModelsDeterministic(t *testing.T) {
	now, _ := dashboardTestNow()

	var samples []*AuditSample
	for _, n := range []string{"m1", "m2", "m3", "m4", "m5"} {
		samples = append(samples, sampleRepeat(now, n, 2)...)
	}

	var first []string
	for round := 0; round < 20; round++ {
		d, err := ComputeDashboard(samples, DashboardWindow24h, now, false)
		if err != nil {
			t.Fatalf("ComputeDashboard: %v", err)
		}
		got := make([]string, 0, len(d.TopModels))
		for _, m := range d.TopModels {
			got = append(got, m.ModelName)
		}
		if round == 0 {
			first = got
			continue
		}
		if len(got) != len(first) {
			t.Fatalf("第 %d 轮条数 = %d, 首轮 = %d", round, len(got), len(first))
		}
		for i := range got {
			if got[i] != first[i] {
				t.Fatalf("第 %d 轮次序 = %v, 首轮 = %v（并列需按名升序保证确定性）", round, got, first)
			}
		}
	}
}

// TestComputeDashboardTopModelsTruncatesToEight 超过 8 个模型时保留请求量最高的 8 个
func TestComputeDashboardTopModelsTruncatesToEight(t *testing.T) {
	now, _ := dashboardTestNow()

	// m01 请求量最低、m10 最高，各模型请求量互不相同
	var samples []*AuditSample
	for i := 1; i <= 10; i++ {
		samples = append(samples, sampleRepeat(now, fmt.Sprintf("m%02d", i), i)...)
	}

	d, err := ComputeDashboard(samples, DashboardWindow24h, now, false)
	if err != nil {
		t.Fatalf("ComputeDashboard: %v", err)
	}
	if len(d.TopModels) != 8 {
		t.Fatalf("排行条数 = %d, want 8", len(d.TopModels))
	}
	if d.TopModels[0].ModelName != "m10" {
		t.Errorf("榜首 = %q, want %q", d.TopModels[0].ModelName, "m10")
	}
	if d.TopModels[7].ModelName != "m03" {
		t.Errorf("末位 = %q, want %q", d.TopModels[7].ModelName, "m03")
	}
}

// TestComputeDashboardTopModelTotals 排行行携带各自的请求数、Token 与失败数
func TestComputeDashboardTopModelTotals(t *testing.T) {
	now, _ := dashboardTestNow()

	samples := []*AuditSample{
		{CreatedAt: now, ResponseStatus: 200, ModelName: "a", TotalTokens: 10},
		{CreatedAt: now, ResponseStatus: 500, ModelName: "a", TotalTokens: 5},
		{CreatedAt: now, ResponseStatus: 200, ModelName: "b", TotalTokens: 7},
	}
	d, err := ComputeDashboard(samples, DashboardWindow24h, now, false)
	if err != nil {
		t.Fatalf("ComputeDashboard: %v", err)
	}
	if len(d.TopModels) != 2 {
		t.Fatalf("排行条数 = %d, want 2", len(d.TopModels))
	}
	// a: 2 请求 / 15 Token / 1 失败（500 计失败）；b: 1 请求 / 7 Token / 0 失败
	want := []ModelStat{
		{ModelName: "a", Requests: 2, Tokens: 15, Failed: 1},
		{ModelName: "b", Requests: 1, Tokens: 7, Failed: 0},
	}
	for i, w := range want {
		if d.TopModels[i] != w {
			t.Errorf("第 %d 名 = %+v, want %+v", i, d.TopModels[i], w)
		}
	}
}

// TestComputeDashboardTopModelsUnnamed 不带模型名的调用（如未标模型的工具调用）
// 聚合为一行固定标签，与其他模型同样按请求量参与排序与截断：既不得因缺名而
// 丢失可见性，也不得因缺名而受优待（请求量低于截断线时照样落榜，不得先截断
// 具名模型再把标签行补挂末尾）；该标签收纳多条记录，不得按空名拆成多行或保持空名
func TestComputeDashboardTopModelsUnnamed(t *testing.T) {
	now, _ := dashboardTestNow()

	// 十个具名模型请求量 1..10，截断线落在 8 名：m10..m03 上榜，m02/m01 落榜
	namedSamples := func() []*AuditSample {
		var samples []*AuditSample
		for i := 1; i <= 10; i++ {
			samples = append(samples, sampleRepeat(now, fmt.Sprintf("m%02d", i), i)...)
		}
		return samples
	}
	// 无模型名样本：ok 条 200 各 10 Token，外加 1 条 500 计失败
	unnamedSamples := func(ok int) []*AuditSample {
		var samples []*AuditSample
		for i := 0; i < ok; i++ {
			samples = append(samples, &AuditSample{
				CreatedAt: now, ResponseStatus: 200, TotalTokens: 10,
			})
		}
		return append(samples, &AuditSample{
			CreatedAt: now, ResponseStatus: 500, TotalTokens: 5,
		})
	}

	// 标签行 4 请求与 m04 并列且落在第 8 名边界内：按名升序 m04 在先，
	// 标签行占末席并把 m03 挤出榜外
	t.Run("与具名模型同规则排序参与截断", func(t *testing.T) {
		d, err := ComputeDashboard(append(namedSamples(), unnamedSamples(3)...),
			DashboardWindow24h, now, false)
		if err != nil {
			t.Fatalf("ComputeDashboard: %v", err)
		}
		if len(d.TopModels) != 8 {
			t.Fatalf("排行条数 = %d, want 8（标签行须与具名模型同规则截断）", len(d.TopModels))
		}
		want := []string{"m10", "m09", "m08", "m07", "m06", "m05", "m04", dashboardUnnamedModel}
		for i, name := range want {
			if d.TopModels[i].ModelName != name {
				t.Errorf("第 %d 名 = %q, want %q", i, d.TopModels[i].ModelName, name)
			}
		}
		// 标签行收纳 4 条记录：3 条 200 各 10 Token，1 条 500 计失败
		top := d.TopModels[7]
		if top.ModelName != dashboardUnnamedModel {
			t.Fatalf("末席模型名 = %q, want %q（空名须映射到标签）", top.ModelName, dashboardUnnamedModel)
		}
		if top.Requests != 4 || top.Tokens != 35 || top.Failed != 1 {
			t.Errorf("标签行 = %+v, want Requests:4 Tokens:35 Failed:1", top)
		}
		for _, m := range d.TopModels {
			if m.ModelName == "m03" {
				t.Errorf("m03 请求量低于标签行，应与标签行同规则被挤出榜外: %+v", d.TopModels)
			}
		}
	})

	// 标签行 2 请求与 m02 并列、按名升序落在截断线外，须与具名模型一样落榜：
	// 截断后补挂标签行的实现会多出一行
	t.Run("低于截断线时同样被截掉", func(t *testing.T) {
		d, err := ComputeDashboard(append(namedSamples(), unnamedSamples(1)...),
			DashboardWindow24h, now, false)
		if err != nil {
			t.Fatalf("ComputeDashboard: %v", err)
		}
		if len(d.TopModels) != 8 {
			t.Fatalf("排行条数 = %d, want 8（标签行不得在截断后被补挂回榜内）", len(d.TopModels))
		}
		if d.TopModels[7].ModelName != "m03" {
			t.Errorf("末位 = %q, want m03", d.TopModels[7].ModelName)
		}
		for _, m := range d.TopModels {
			if m.ModelName == dashboardUnnamedModel {
				t.Errorf("标签行请求量低于截断线，不应上榜: %+v", d.TopModels)
			}
		}
	})
}

// TestComputeDashboardTokensSplit Token 构成与流式拆分各自累加
func TestComputeDashboardTokensSplit(t *testing.T) {
	now, _ := dashboardTestNow()

	samples := []*AuditSample{
		{CreatedAt: now, ResponseStatus: 200, PromptTokens: 10, CompletionTokens: 20,
			TotalTokens: 30, IsStream: true},
		{CreatedAt: now, ResponseStatus: 200, PromptTokens: 1, CompletionTokens: 2,
			TotalTokens: 3, IsStream: true},
		{CreatedAt: now, ResponseStatus: 200, PromptTokens: 100, CompletionTokens: 200,
			TotalTokens: 300, IsStream: false},
	}
	d, err := ComputeDashboard(samples, DashboardWindow24h, now, false)
	if err != nil {
		t.Fatalf("ComputeDashboard: %v", err)
	}

	want := DashboardTokens{
		PromptTokens:      111, // 10+1+100
		CompletionTokens:  222, // 20+2+200
		StreamRequests:    2,
		NonStreamRequests: 1,
		StreamTokens:      33, // 30+3
		NonStreamTokens:   300,
	}
	if d.Tokens != want {
		t.Errorf("tokens = %+v, want %+v", d.Tokens, want)
	}
}

// TestComputeDashboardTokensEmpty 空样本 Token 面板全零，不得为 null
func TestComputeDashboardTokensEmpty(t *testing.T) {
	now, _ := dashboardTestNow()

	d, err := ComputeDashboard(nil, DashboardWindow24h, now, false)
	if err != nil {
		t.Fatalf("ComputeDashboard: %v", err)
	}
	if d.Tokens != (DashboardTokens{}) {
		t.Errorf("空样本 tokens = %+v, want 全零", d.Tokens)
	}
	if d.TopModels == nil {
		t.Error("top_models 为 nil, want 非 nil 空切片")
	}
}
