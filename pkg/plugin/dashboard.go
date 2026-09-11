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
	"sort"
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
// 不含请求体/响应体/请求头等大字段
type AuditSample struct {
	CreatedAt        time.Time
	ResponseStatus   int
	ModelName        string
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	DurationMS       int64
	IsStream         bool
}

// DashboardSummary 首页指标卡。
// Failed 由服务端唯一计算（与 SuccessRate 同出处），前端不得由状态分桶求和反推
type DashboardSummary struct {
	Requests     int64   `json:"requests"`
	SuccessRate  float64 `json:"success_rate"`
	Failed       int64   `json:"failed"`
	Tokens       int64   `json:"tokens"`
	AvgLatencyMS int64   `json:"avg_latency_ms"`
}

// TrendPoint 趋势图数据点
type TrendPoint struct {
	Date     string `json:"date"`
	Requests int64  `json:"requests"`
	Tokens   int64  `json:"tokens"`
}

// LatencyBucket 延迟直方图的一档
type LatencyBucket struct {
	Label string `json:"label"`
	Count int64  `json:"count"`
}

// DashboardLatency 延迟分位与分布
type DashboardLatency struct {
	P50MS   int64           `json:"p50_ms"`
	P95MS   int64           `json:"p95_ms"`
	P99MS   int64           `json:"p99_ms"`
	Buckets []LatencyBucket `json:"buckets"`
}

// StatusBucket 状态码分布的一档
type StatusBucket struct {
	Class string `json:"class"`
	Count int64  `json:"count"`
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
	Latency   DashboardLatency `json:"latency"`
	Status    []StatusBucket   `json:"status"`
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
	data := &DashboardData{
		Trend:     trend,
		Truncated: truncated,
		Alerts:    []DashboardAlert{},
		// 各面板恒返回骨架（分档全零、切片非 nil），前端无需判空即可绘图
		Latency: computeLatency(samples),
		Status:  computeStatus(samples),
	}
	if len(samples) == 0 {
		return data, nil
	}

	var success, sumTokens, sumLatency int64
	for _, s := range samples {
		data.Summary.Requests++
		if isSuccess(s.ResponseStatus) {
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
	data.Summary.Failed = n - success
	// 平均延迟四舍五入为整数毫秒，与成功率同口径；采样值恒为非负，n/2 补偿即四舍五入。
	// n>0 由上方空样本提前返回保证，此处不会除零。
	data.Summary.AvgLatencyMS = (sumLatency + n/2) / n
	data.Summary.SuccessRate = math.Round(float64(success)/float64(len(samples))*1000) / 10
	return data, nil
}

// latencyBucketBounds 延迟分档上界（毫秒，左闭右开），与 latencyBucketLabels 一一对应；
// 超出最后一个上界的归入末档
var latencyBucketBounds = []int64{100, 300, 1000, 3000, 10000}

// latencyBucketLabels 档位文案，即下发前端的 label 精确取值
var latencyBucketLabels = []string{"<100ms", "100–300ms", "0.3–1s", "1–3s", "3–10s", "≥10s"}

// latencyBucketIndex 返回耗时所属档位下标：依次与上界比较，
// 未落入任何上界者归入末档
func latencyBucketIndex(ms int64) int {
	for i, ub := range latencyBucketBounds {
		if ms < ub {
			return i
		}
	}
	return len(latencyBucketBounds)
}

// percentile 最近秩法分位数：索引 = ceil(k*n/100) - 1。
// 用整数运算避免浮点误差：(k*n+99)/100 即向上取整。
// 前置条件：sorted 已升序、非空，k ∈ [1,100]——此时索引必落在 [0, n-1]
func percentile(sorted []int64, k int) int64 {
	return sorted[(k*len(sorted)+99)/100-1]
}

// computeLatency 延迟分位与分布。空样本返回全零分位与六档零计数骨架，
// 前端无需判空即可绘图
func computeLatency(samples []*AuditSample) DashboardLatency {
	out := DashboardLatency{Buckets: make([]LatencyBucket, len(latencyBucketLabels))}
	for i, l := range latencyBucketLabels {
		out.Buckets[i].Label = l
	}
	if len(samples) == 0 {
		return out
	}

	// 排序在副本上进行，不得改动入参顺序
	durations := make([]int64, 0, len(samples))
	for _, s := range samples {
		durations = append(durations, s.DurationMS)
		out.Buckets[latencyBucketIndex(s.DurationMS)].Count++
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })

	out.P50MS = percentile(durations, 50)
	out.P95MS = percentile(durations, 95)
	out.P99MS = percentile(durations, 99)
	return out
}

// statusClasses 状态分布档位与固定输出顺序。
// other 收纳 status 为 0（未产生响应）及 [200,600) 之外的异常取值，
// 使分类全量覆盖，sum(分桶) == requests 恒成立
var statusClasses = []string{"2xx", "3xx", "4xx", "5xx", "other"}

// isSuccess 成功判定：status ∈ [200,400)
func isSuccess(status int) bool { return status >= 200 && status < 400 }

// statusClass 把响应状态归入分布档位
func statusClass(status int) string {
	switch status / 100 {
	case 2:
		return "2xx"
	case 3:
		return "3xx"
	case 4:
		return "4xx"
	case 5:
		return "5xx"
	default:
		return "other"
	}
}

// computeStatus 状态分布：恒定返回 statusClasses 顺序的五档（含零值）
func computeStatus(samples []*AuditSample) []StatusBucket {
	counts := make(map[string]int64, len(statusClasses))
	for _, s := range samples {
		counts[statusClass(s.ResponseStatus)]++
	}
	out := make([]StatusBucket, len(statusClasses))
	for i, c := range statusClasses {
		out[i] = StatusBucket{Class: c, Count: counts[c]}
	}
	return out
}
