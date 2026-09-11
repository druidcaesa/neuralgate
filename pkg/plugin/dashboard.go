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
	"math"
	"time"
)

// MaxAuditSamples 单次仪表盘聚合的采样行上限。达上限即置 truncated，
// 由前端如实提示，不静默失真
const MaxAuditSamples = 200000

// 仪表盘窗口标识
const (
	DashboardWindow24h = "24h"
	DashboardWindow7d  = "7d"
	DashboardWindow30d = "30d"
	// DashboardDefaultWindow 未指定 window 时的缺省窗口
	DashboardDefaultWindow = DashboardWindow7d
)

// AuditSample 仪表盘聚合用窄列采样行：只含聚合所需的最小字段，
// 不携带请求体/响应体等大字段
type AuditSample struct {
	CreatedAt      time.Time
	ResponseStatus int
	TotalTokens    int64
	DurationMS     int64
}

// DashboardSummary 首页指标卡
type DashboardSummary struct {
	Requests     int64   `json:"requests"`
	SuccessRate  float64 `json:"success_rate"`
	Tokens       int64   `json:"tokens"`
	AvgLatencyMS int64   `json:"avg_latency_ms"`
}

// TrendPoint 趋势图数据点
type TrendPoint struct {
	Date     string `json:"date"`
	Requests int64  `json:"requests"`
	Tokens   int64  `json:"tokens"`
}

// DashboardAlert 首页告警条目
type DashboardAlert struct {
	Level  string `json:"level"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Link   string `json:"link,omitempty"`
}

// DashboardData 首页聚合结果
type DashboardData struct {
	Summary   DashboardSummary `json:"summary"`
	Trend     []TrendPoint     `json:"trend"`
	Truncated bool             `json:"truncated"`
	Alerts    []DashboardAlert `json:"alerts"`
}

// DashboardRange 返回窗口对应的查询区间 [start, end) 与分桶规格。
// window 非法时返回错误。桶数、桶宽、首桶起点在此唯一确定——
// 调用方据此构造存储查询、聚合函数据此铺桶，避免「查了一段、画了另一段」。
// now 由调用方注入，本函数不读时钟，分桶断言才可确定性运行。
//
// 分桶按本地时区的自然边界（整点 / 自然日）对齐，取含当前桶在内的最近 N 个整桶，
// 而非 now-N 的滑动窗口；末桶与首桶因此天然是部分桶。
func DashboardRange(window string, now time.Time) (start, end time.Time, interval time.Duration, buckets int, err error) {
	y, m, d := now.Date()
	switch window {
	case DashboardWindow24h:
		end = now
		// 当前整点往前 23 小时起，共 24 个整点桶
		start = time.Date(y, m, d, now.Hour(), 0, 0, 0, now.Location()).Add(-23 * time.Hour)
		return start, end, time.Hour, 24, nil
	case DashboardWindow7d, DashboardWindow30d:
		days := 7
		if window == DashboardWindow30d {
			days = 30
		}
		end = now
		start = time.Date(y, m, d, 0, 0, 0, 0, now.Location()).AddDate(0, 0, -(days - 1))
		return start, end, 24 * time.Hour, days, nil
	default:
		return time.Time{}, time.Time{}, 0, 0, fmt.Errorf("unsupported dashboard window: %s", window)
	}
}

// bucketLabel 桶键：24h 窗口为 HH:00，日窗口为 YYYY-MM-DD（本地时区）
func bucketLabel(t time.Time, window string) string {
	if window == DashboardWindow24h {
		return t.Format("15:04")
	}
	return t.Format("2006-01-02")
}

// ComputeDashboard 把窄列采样聚合为首页数据。纯函数：无 IO、不读时钟——
// 分桶边界经 DashboardRange 派生，与调用方的查询区间同源。
// samples 为 [start, end) 内的采样，顺序无关；truncated 由存储层透传。
//
// 成功率为 status ∈ [200,400) 的行占比，status==0 计入失败（未记录响应不算成功）。
// 平均延迟为 duration_ms 的算术平均，长连接流式请求会显著拉高该值，非 SLA 指标。
func ComputeDashboard(samples []*AuditSample, window string, now time.Time, truncated bool) (*DashboardData, error) {
	start, end, interval, buckets, err := DashboardRange(window, now)
	if err != nil {
		return nil, err
	}

	trend := make([]TrendPoint, buckets)
	for i := range trend {
		trend[i] = TrendPoint{Date: bucketLabel(start.Add(time.Duration(i)*interval), window)}
	}
	data := &DashboardData{Trend: trend, Truncated: truncated, Alerts: []DashboardAlert{}}
	if len(samples) == 0 {
		return data, nil
	}

	var success, sumTokens, sumLatency int64
	for _, s := range samples {
		data.Summary.Requests++
		if s.ResponseStatus >= 200 && s.ResponseStatus < 400 {
			success++
		}
		sumTokens += s.TotalTokens
		sumLatency += s.DurationMS

		// 区间外的行不计入趋势（查询已限定区间，此处为防御性边界检查）
		if s.CreatedAt.Before(start) || !s.CreatedAt.Before(end) {
			continue
		}
		if idx := int(s.CreatedAt.Sub(start) / interval); idx >= 0 && idx < buckets {
			trend[idx].Requests++
			trend[idx].Tokens += s.TotalTokens
		}
	}

	n := int64(len(samples))
	data.Summary.Tokens = sumTokens
	// 平均延迟四舍五入为整数毫秒，与成功率同口径；采样值恒为非负，n/2 补偿即四舍五入。
	// n>0 由上方空样本提前返回保证，此处不会除零。
	data.Summary.AvgLatencyMS = (sumLatency + n/2) / n
	data.Summary.SuccessRate = math.Round(float64(success)/float64(len(samples))*1000) / 10
	return data, nil
}
