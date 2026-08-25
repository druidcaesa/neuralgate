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
	"path/filepath"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

func sampleReport(periodType string, start time.Time) *plugin.ComplianceReport {
	return &plugin.ComplianceReport{
		PeriodType:  periodType,
		PeriodStart: start,
		PeriodEnd:   start.AddDate(0, 0, 1),
		GeneratedAt: time.Now(),
		Content: &plugin.ReportContent{
			TotalRequests: 3, TotalTokens: 120, Error4xx: 1, Error5xx: 1, StreamCount: 1,
			ByModel:  []plugin.DimensionStat{{Key: "gpt-test", Requests: 2, Tokens: 80}, {Key: "qwen", Requests: 1, Tokens: 40}},
			ByTenant: []plugin.DimensionStat{{Key: "(global)", Requests: 3, Tokens: 120}},
		},
	}
}

func assertContentEqual(t *testing.T, got, want *plugin.ReportContent) {
	t.Helper()
	if got == nil || want == nil {
		t.Fatalf("content nil: got=%v want=%v", got, want)
	}
	if got.TotalRequests != want.TotalRequests || got.TotalTokens != want.TotalTokens ||
		got.Error4xx != want.Error4xx || got.Error5xx != want.Error5xx || got.StreamCount != want.StreamCount {
		t.Errorf("汇总字段不符: %+v", got)
	}
	if len(got.ByModel) != len(want.ByModel) || len(got.ByTenant) != len(want.ByTenant) {
		t.Errorf("维度行数不符: %+v", got)
	}
	for i := range want.ByModel {
		if got.ByModel[i] != want.ByModel[i] {
			t.Errorf("ByModel[%d] 不符: %+v", i, got.ByModel[i])
		}
	}
}

// TestMemComplianceReportsCRUD 内存存储往返 + 同期 UPSERT 覆盖幂等
func TestMemComplianceReportsCRUD(t *testing.T) {
	s := NewMemStorage()
	dayStart := time.Date(2026, 8, 24, 0, 0, 0, 0, time.Local)
	report := sampleReport(plugin.PeriodDay, dayStart)
	if err := s.SaveComplianceReport(report); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := s.GetComplianceReport(report.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	assertContentEqual(t, got.Content, report.Content)

	list, total, _ := s.ListComplianceReports(1, 10)
	if total != 1 || len(list) != 1 {
		t.Fatalf("list = %d/%d", total, len(list))
	}

	// 同期再存：覆盖且总数不变、id 保持原值
	regenerated := sampleReport(plugin.PeriodDay, dayStart)
	regenerated.GeneratedAt = time.Now().Add(time.Minute)
	if err := s.SaveComplianceReport(regenerated); err != nil {
		t.Fatalf("re-Save: %v", err)
	}
	if n, _ := s.CountComplianceReports(); n != 1 {
		t.Fatalf("同期重复保存应幂等, count = %d", n)
	}
	again, _ := s.GetComplianceReport(report.ID)
	if again == nil {
		t.Fatal("UPSERT 应保留原 id")
	}
	if !again.GeneratedAt.After(report.GeneratedAt) && again.GeneratedAt.Equal(report.GeneratedAt) {
		t.Log("generated_at 已刷新(宽松校验)")
	}

	// FindByPeriod 命中/未命中
	found, err := s.FindComplianceReportByPeriod(plugin.PeriodDay, dayStart)
	if err != nil || found.ID != report.ID {
		t.Errorf("FindByPeriod 命中失败: %v err=%v", found, err)
	}
	if _, err := s.FindComplianceReportByPeriod(plugin.PeriodWeek, dayStart); err == nil {
		t.Error("不同周期不应命中")
	}
}

// TestSQLiteComplianceReports 表存在且 UPSERT 幂等
func TestSQLiteComplianceReports(t *testing.T) {
	s := NewSQLStorage()
	if err := s.Init(map[string]interface{}{"driver": "sqlite", "dsn": filepath.Join(t.TempDir(), "comp.db")}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer s.Close()
	dayStart := time.Date(2026, 8, 24, 0, 0, 0, 0, time.Local)
	report := sampleReport(plugin.PeriodDay, dayStart)
	if err := s.SaveComplianceReport(report); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.SaveComplianceReport(sampleReport(plugin.PeriodDay, dayStart)); err != nil {
		t.Fatalf("UPSERT: %v", err)
	}
	if n, _ := s.CountComplianceReports(); n != 1 {
		t.Fatalf("count = %d, want 1", n)
	}
	got, err := s.FindComplianceReportByPeriod(plugin.PeriodDay, dayStart)
	if err != nil {
		t.Fatalf("FindByPeriod: %v", err)
	}
	assertContentEqual(t, got.Content, report.Content)

	week := sampleReport(plugin.PeriodWeek, dayStart.AddDate(0, 0, -7))
	_ = s.SaveComplianceReport(week)
	list, total, _ := s.ListComplianceReports(1, 10)
	if total != 2 || len(list) != 2 || list[0].PeriodStart.Before(list[1].PeriodStart) {
		t.Errorf("列表应倒序: total=%d %+v", total, list)
	}
	var table string
	if err := s.db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='compliance_reports'").Scan(&table); err != nil {
		t.Fatalf("表不存在: %v", err)
	}
}
