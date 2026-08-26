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
	"sort"
	"strings"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"go.uber.org/zap"
)

// globalTenantKey 空租户日志在维度分布中的归并键
const globalTenantKey = "(global)"

// aggReportPageSize 生成报表时拉取审计日志的单页大小
const aggReportPageSize = 1000

// BuildRange 返回 refTime 所在周期区间 [start, end)：
// day=当日零点起 24h；week=所在周周一零点起 7 天；month=自然月；未知类型兜底 day
func BuildRange(periodType string, ref time.Time) (time.Time, time.Time) {
	loc := ref.Location()
	y, m, d := ref.Date()
	midnight := time.Date(y, m, d, 0, 0, 0, 0, loc)
	switch strings.ToLower(periodType) {
	case plugin.PeriodWeek:
		wd := int(ref.Weekday())
		if wd == 0 {
			wd = 7 // 周日按一周第 7 天计（周一为起点）
		}
		start := midnight.AddDate(0, 0, -(wd - 1))
		return start, start.AddDate(0, 0, 7)
	case plugin.PeriodMonth:
		start := time.Date(y, m, 1, 0, 0, 0, 0, loc)
		return start, start.AddDate(0, 1, 0)
	default: // day 与未知类型
		return midnight, midnight.AddDate(0, 0, 1)
	}
}

// AggregateReport 纯函数聚合：总量五项 + 模型/租户两维度分布。
// 维度行 Requests 降序、同数按 Key 字典序，保证输出确定性
func AggregateReport(logs []*plugin.AuditLog) *plugin.ReportContent {
	content := &plugin.ReportContent{}
	modelStat := map[string]*plugin.DimensionStat{}
	tenantStat := map[string]*plugin.DimensionStat{}

	bump := func(m map[string]*plugin.DimensionStat, key string, tokens int64) {
		st, ok := m[key]
		if !ok {
			st = &plugin.DimensionStat{Key: key}
			m[key] = st
		}
		st.Requests++
		st.Tokens += tokens
	}
	for _, log := range logs {
		content.TotalRequests++
		content.TotalTokens += int64(log.TotalTokens)
		switch {
		case log.ResponseStatus >= 500:
			content.Error5xx++
		case log.ResponseStatus >= 400:
			content.Error4xx++
		}
		if log.IsStream {
			content.StreamCount++
		}
		bump(modelStat, log.ModelName, int64(log.TotalTokens))
		tenantKey := log.TenantID
		if tenantKey == "" {
			tenantKey = globalTenantKey
		}
		bump(tenantStat, tenantKey, int64(log.TotalTokens))
	}
	content.ByModel = sortedStats(modelStat)
	content.ByTenant = sortedStats(tenantStat)
	return content
}

// sortedStats 维度行排序：Requests 降序，同数 Key 字典序
func sortedStats(m map[string]*plugin.DimensionStat) []plugin.DimensionStat {
	out := make([]plugin.DimensionStat, 0, len(m))
	for _, st := range m {
		out = append(out, *st)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Requests != out[j].Requests {
			return out[i].Requests > out[j].Requests
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// GenerateComplianceReport 对 refTime 所在周期聚合审计日志并 UPSERT 入库。
// 幂等：同期重复执行覆盖原记录（保留业务键）
func GenerateComplianceReport(storage plugin.StoragePlugin, logger *zap.Logger,
	periodType string, refTime time.Time) (*plugin.ComplianceReport, error) {
	normalized := normalizePeriod(periodType)
	start, end := BuildRange(normalized, refTime)
	// 方案：BuildRange 定义半开周期 [start, end)，但存储层 QueryAuditLogs 的时间过滤为闭区间
	// （mem 按 Before(start)/After(end) 排除、sql 用 >=/<=），若直接以 end 过滤，恰落在 end 时刻的
	// 审计日志会同时计入相邻两个周期的报表。调用侧把右端点换算为周期内最后一毫秒，
	// 使闭区间过滤等价于半开语义；PeriodEnd 与幂等业务键仍存原始 end，不受影响。
	endExclusive := end.Add(-time.Millisecond)
	filter := plugin.AuditLogFilter{StartTime: &start, EndTime: &endExclusive}
	var logs []*plugin.AuditLog
	for page := 1; ; page++ {
		batch, total, err := storage.QueryAuditLogs(filter, page, aggReportPageSize)
		if err != nil {
			return nil, err
		}
		logs = append(logs, batch...)
		if len(batch) == 0 || int64(page)*aggReportPageSize >= total {
			break
		}
	}
	report := &plugin.ComplianceReport{
		PeriodType:  normalized,
		PeriodStart: start,
		PeriodEnd:   end,
		GeneratedAt: time.Now(),
		Content:     AggregateReport(logs),
	}
	if err := storage.SaveComplianceReport(report); err != nil {
		return nil, err
	}
	logger.Info("合规报表已生成",
		zap.String("period", normalized),
		zap.Time("start", start),
		zap.Int64("requests", report.Content.TotalRequests))
	return report, nil
}

// normalizePeriod 周期类型归一化：未知值兜底 day（与 BuildRange 一致）
func normalizePeriod(periodType string) string {
	switch strings.ToLower(periodType) {
	case plugin.PeriodWeek:
		return plugin.PeriodWeek
	case plugin.PeriodMonth:
		return plugin.PeriodMonth
	default:
		return plugin.PeriodDay
	}
}
