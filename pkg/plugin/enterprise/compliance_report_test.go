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

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
	"go.uber.org/zap"
)

func at(year int, month time.Month, day, hour int) time.Time {
	return time.Date(year, month, day, hour, 0, 0, 0, time.Local)
}

// TestBuildRangeDay day 周期：当日零点起 24 小时
func TestBuildRangeDay(t *testing.T) {
	start, end := BuildRange(plugin.PeriodDay, at(2026, 8, 25, 15))
	if !start.Equal(at(2026, 8, 25, 0)) || !end.Equal(at(2026, 8, 26, 0)) {
		t.Errorf("day range = %v ~ %v", start, end)
	}
}

// TestBuildRangeWeek week 周期：周一零点起 7 天，含跨月与跨年场景
func TestBuildRangeWeek(t *testing.T) {
	cases := []struct {
		ref       time.Time
		wantStart time.Time
	}{
		{at(2026, 8, 26, 9), at(2026, 8, 24, 0)}, // 周三 → 本周一
		{at(2026, 9, 2, 9), at(2026, 8, 31, 0)},  // 跨月:9月周三 → 8月31日周一
		{at(2026, 1, 4, 9), at(2025, 12, 29, 0)}, // 跨年:2026-01-04 周日 → 2025-12-29 周一
		{at(2026, 8, 24, 0), at(2026, 8, 24, 0)}, // 周一当天
	}
	for i, c := range cases {
		start, end := BuildRange(plugin.PeriodWeek, c.ref)
		if !start.Equal(c.wantStart) {
			t.Errorf("case %d start = %v, want %v", i, start, c.wantStart)
		}
		if !end.Equal(c.wantStart.AddDate(0, 0, 7)) {
			t.Errorf("case %d end = %v", i, end)
		}
	}
}

// TestBuildRangeMonth month 周期：自然月，闰年 2 月边界
func TestBuildRangeMonth(t *testing.T) {
	start, end := BuildRange(plugin.PeriodMonth, at(2024, 2, 15, 12))
	if !start.Equal(at(2024, 2, 1, 0)) || !end.Equal(at(2024, 3, 1, 0)) {
		t.Errorf("leap feb range = %v ~ %v", start, end)
	}
	start, end = BuildRange(plugin.PeriodMonth, at(2026, 12, 31, 23))
	if !start.Equal(at(2026, 12, 1, 0)) || !end.Equal(at(2027, 1, 1, 0)) {
		t.Errorf("dec range = %v ~ %v", start, end)
	}
}

// TestBuildRangeUnknownFallsBackToDay 未知周期兜底为 day
func TestBuildRangeUnknownFallsBackToDay(t *testing.T) {
	ref := at(2026, 8, 25, 8)
	start, _ := BuildRange("year", ref)
	if !start.Equal(at(2026, 8, 25, 0)) {
		t.Errorf("未知类型应兜底 day: %v", start)
	}
}

// aggLogs 构造聚合样本：3 条(200流式+404+502)、两模型两租户、含空租户
func aggLogs() []*plugin.AuditLog {
	base := at(2026, 8, 25, 10)
	return []*plugin.AuditLog{
		{RequestID: "r1", ModelName: "gpt-test", TenantID: "t-a",
			ResponseStatus: 200, TotalTokens: 100, IsStream: true, CreatedAt: base},
		{RequestID: "r2", ModelName: "gpt-test", TenantID: "",
			ResponseStatus: 404, TotalTokens: 20, CreatedAt: base.Add(time.Minute)},
		{RequestID: "r3", ModelName: "qwen", TenantID: "t-b",
			ResponseStatus: 502, TotalTokens: 30, CreatedAt: base.Add(2 * time.Minute)},
	}
}

// TestAggregateReport 六项总量与两维度分布逐字段精确断言
func TestAggregateReport(t *testing.T) {
	got := AggregateReport(aggLogs())
	if got.TotalRequests != 3 || got.TotalTokens != 150 ||
		got.Error4xx != 1 || got.Error5xx != 1 || got.StreamCount != 1 {
		t.Fatalf("汇总不符: %+v", got)
	}
	wantModel := []plugin.DimensionStat{
		{Key: "gpt-test", Requests: 2, Tokens: 120},
		{Key: "qwen", Requests: 1, Tokens: 30},
	}
	if len(got.ByModel) != len(wantModel) {
		t.Fatalf("ByModel 行数 = %d", len(got.ByModel))
	}
	for i := range wantModel {
		if got.ByModel[i] != wantModel[i] {
			t.Errorf("ByModel[%d] = %+v, want %+v", i, got.ByModel[i], wantModel[i])
		}
	}
	wantTenant := []plugin.DimensionStat{
		{Key: "(global)", Requests: 1, Tokens: 20}, // 同请求数按 Key 字典序,(global) 在 t-a 前
		{Key: "t-a", Requests: 1, Tokens: 100},
		{Key: "t-b", Requests: 1, Tokens: 30},
	}
	if len(got.ByTenant) != len(wantTenant) {
		t.Fatalf("ByTenant 行数 = %d", len(got.ByTenant))
	}
	for i := range wantTenant {
		if got.ByTenant[i] != wantTenant[i] {
			t.Errorf("ByTenant[%d] = %+v, want %+v", i, got.ByTenant[i], wantTenant[i])
		}
	}
}

// TestAggregateReportEmpty 空日志返回全零值与空维度行(JSON 序列化为 [] 而非 null)
func TestAggregateReportEmpty(t *testing.T) {
	got := AggregateReport(nil)
	if got == nil || got.TotalRequests != 0 ||
		len(got.ByModel) != 0 || len(got.ByTenant) != 0 {
		t.Errorf("空输入应为全零值: %+v", got)
	}
}

// TestGenerateComplianceReportIdempotent 同期重复生成覆盖且总数不变
func TestGenerateComplianceReportIdempotent(t *testing.T) {
	storage := oss.NewMemStorage()
	ref := at(2026, 8, 25, 10)
	dayStart := at(2026, 8, 25, 0)
	for _, l := range aggLogs() {
		if err := storage.SaveAuditLog(l); err != nil {
			t.Fatal(err)
		}
	}
	first, err := GenerateComplianceReport(storage, zap.NewNop(), plugin.PeriodDay, ref)
	if err != nil {
		t.Fatalf("首次生成: %v", err)
	}
	time.Sleep(time.Millisecond) // 保证 GeneratedAt 可区分
	second, err := GenerateComplianceReport(storage, zap.NewNop(), plugin.PeriodDay, ref)
	if err != nil {
		t.Fatalf("再次生成: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("UPSERT 应保留原 id: %s vs %s", first.ID, second.ID)
	}
	if n, _ := storage.CountComplianceReports(); n != 1 {
		t.Errorf("count = %d, want 1", n)
	}
	found, ferr := storage.FindComplianceReportByPeriod(plugin.PeriodDay, dayStart)
	if ferr != nil || found.Content.TotalRequests != 3 {
		t.Errorf("FindByPeriod 结果不符: %+v err=%v", found, ferr)
	}
}

// TestGenerateComplianceReportPeriodEndBoundary 周期右端点归属回归：
// 存储层时间过滤为闭区间，调用侧已把半开周期右端点换算为周期内最后一毫秒，
// 故恰落在 end 时刻的日志只归下一周期、不计入本周期，防止相邻周期重复计数
func TestGenerateComplianceReportPeriodEndBoundary(t *testing.T) {
	storage := oss.NewMemStorage()
	ref := at(2026, 8, 25, 10)
	dayStart := at(2026, 8, 25, 0)
	dayEnd := dayStart.AddDate(0, 0, 1)
	for _, l := range []*plugin.AuditLog{
		{RequestID: "at-end", ModelName: "m", ResponseStatus: 200,
			TotalTokens: 10, CreatedAt: dayEnd}, // 恰在周期 end：属下一周期
		{RequestID: "last-ms", ModelName: "m", ResponseStatus: 200,
			TotalTokens: 20, CreatedAt: dayEnd.Add(-time.Millisecond)}, // 周期内最后一毫秒：须计入
	} {
		if err := storage.SaveAuditLog(l); err != nil {
			t.Fatal(err)
		}
	}
	report, err := GenerateComplianceReport(storage, zap.NewNop(), plugin.PeriodDay, ref)
	if err != nil {
		t.Fatalf("生成本周期: %v", err)
	}
	if !report.PeriodEnd.Equal(dayEnd) {
		t.Errorf("报表元数据应保留原始半开右端点: %v", report.PeriodEnd)
	}
	if report.Content.TotalRequests != 1 || report.Content.TotalTokens != 20 {
		t.Errorf("end 时刻日志不应计入本周期: requests=%d tokens=%d",
			report.Content.TotalRequests, report.Content.TotalTokens)
	}
	next, err := GenerateComplianceReport(storage, zap.NewNop(), plugin.PeriodDay, dayEnd)
	if err != nil {
		t.Fatalf("生成下一周期: %v", err)
	}
	if next.Content.TotalRequests != 1 || next.Content.TotalTokens != 10 {
		t.Errorf("end 时刻日志应计入下一周期: requests=%d tokens=%d",
			next.Content.TotalRequests, next.Content.TotalTokens)
	}
}
