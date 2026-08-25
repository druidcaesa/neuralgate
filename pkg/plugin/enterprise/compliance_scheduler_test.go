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

// TestDueReportsDay 到达 00:05 后应补昨日日报
func TestDueReportsDay(t *testing.T) {
	now := time.Date(2026, 8, 25, 0, 6, 0, 0, time.Local) // 周二凌晨
	due := dueReports(now)
	if len(due) == 0 {
		t.Fatal("00:06 应有到期项")
	}
	found := false
	for _, d := range due {
		if d[0] == plugin.PeriodDay && d[1] == "2026-08-24" {
			found = true
		}
	}
	if !found {
		t.Errorf("应含昨日日报 ref, got %v", due)
	}
}

// TestDueReportsTooEarly 未到触发时刻返回空
func TestDueReportsTooEarly(t *testing.T) {
	if due := dueReports(time.Date(2026, 8, 25, 0, 4, 0, 0, time.Local)); len(due) != 0 {
		t.Errorf("00:04 不应有到期项: %v", due)
	}
}

// TestDueReportsWeekAndMonth 周一含周报；每月1日含月报且周二不含周报
func TestDueReportsWeekAndMonth(t *testing.T) {
	monday := time.Date(2026, 8, 24, 0, 11, 0, 0, time.Local) // 周一
	mondayHasWeek := false
	for _, d := range dueReports(monday) {
		if d[0] == plugin.PeriodWeek && d[1] == "2026-08-17" { // 上周一
			mondayHasWeek = true
		}
	}
	if !mondayHasWeek {
		t.Error("周一 00:11 应含上周周报")
	}

	firstTue := time.Date(2026, 9, 1, 0, 16, 0, 0, time.Local) // 周二兼月初
	var hasMonth, hasWeek bool
	for _, d := range dueReports(firstTue) {
		switch d[0] {
		case plugin.PeriodMonth:
			hasMonth = d[1] == "2026-08-01"
		case plugin.PeriodWeek:
			hasWeek = true
		}
	}
	if !hasMonth {
		t.Error("月初 00:16 应含上月月报(ref=8月1日)")
	}
	if hasWeek {
		t.Error("周二不应含周报")
	}
}

// TestCatchUpMissing 跨期审计回扫：day/week/month 全部补齐且重复执行不再新增
func TestCatchUpMissing(t *testing.T) {
	storage := oss.NewMemStorage()
	base := time.Now().AddDate(0, 0, -3)
	for i := 0; i < 3; i++ {
		if err := storage.SaveAuditLog(&plugin.AuditLog{
			RequestID: string(rune('a' + i)), ModelName: "m", TenantID: "t",
			ResponseStatus: 200, TotalTokens: 10,
			CreatedAt: base.AddDate(0, 0, i).Add(2 * time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}
	s := NewReportScheduler(storage, zap.NewNop())
	s.catchUpMissing()

	dayCount, weekCount, monthCount := 0, 0, 0
	_, total, err := storage.ListComplianceReports(1, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range mustAllReports(t, storage) {
		switch r.PeriodType {
		case plugin.PeriodDay:
			dayCount++
		case plugin.PeriodWeek:
			weekCount++
		case plugin.PeriodMonth:
			monthCount++
		}
	}
	if total < int64(dayCount+weekCount+monthCount) || dayCount != 3 {
		t.Fatalf("回扫后报表不符: total=%d day=%d week=%d month=%d", total, dayCount, weekCount, monthCount)
	}
	before, _ := storage.CountComplianceReports()
	s.catchUpMissing() // 幂等：已存在的不重建
	after, _ := storage.CountComplianceReports()
	if before != after {
		t.Errorf("重复回扫不应新增: %d → %d", before, after)
	}
}

// mustAllReports 翻页拉全量报表便于逐条断言
func mustAllReports(t *testing.T, storage plugin.StoragePlugin) []*plugin.ComplianceReport {
	t.Helper()
	var all []*plugin.ComplianceReport
	for page := 1; ; page++ {
		batch, total, err := storage.ListComplianceReports(page, 100)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, batch...)
		if int64(len(all)) >= total || len(batch) == 0 {
			return all
		}
	}
}

// TestSchedulerStopIdempotent Start 后连续 Stop 不 panic 且干净退出（配合 -race）
func TestSchedulerStopIdempotent(t *testing.T) {
	s := NewReportScheduler(oss.NewMemStorage(), zap.NewNop())
	s.Start()
	done := s.doneCh
	s.Stop()
	s.Stop() // 二次调用必须安全
	select {
	case <-done:
	default:
		t.Error("Stop 返回后 doneCh 应已关闭")
	}
}
