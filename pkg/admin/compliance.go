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

package admin

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/gin-gonic/gin"
)

// SetReportGenerator 注入合规报表手动补生成器。pkg/admin 不依赖 enterprise 包，
// 由企业版装配层传入适配 GenerateComplianceReport 的薄闭包；未注入时手动生成返回 503。
// 入参 start 为已归一的周期起点（本包自行计算），返回已入库的报表
func (s *AdminServer) SetReportGenerator(fn func(periodType string, start time.Time) (*plugin.ComplianceReport, error)) {
	s.reportGenerator = fn
}

// listComplianceReports GET /api/compliance-reports?page=&size=:分页查询，period_start 倒序
func (s *AdminServer) listComplianceReports(c *gin.Context) {
	if s.globalOnlyGuard(c) {
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	reports, total, err := s.storage.ListComplianceReports(page, size)
	if err != nil {
		Error(c, http.StatusInternalServerError, 500, err.Error())
		return
	}
	OK(c, gin.H{"items": reports, "total": total, "page": page, "size": size})
}

// getComplianceReport GET /api/compliance-reports/:id?format=json|csv:
// json 缺省返回统一 JSON；csv 以附件形式输出 complianceToCSV 文本；其余格式 400
func (s *AdminServer) getComplianceReport(c *gin.Context) {
	if s.globalOnlyGuard(c) {
		return
	}
	report, err := s.storage.GetComplianceReport(c.Param("id"))
	if err != nil || report == nil {
		Error(c, http.StatusNotFound, 404, "compliance report not found")
		return
	}
	switch c.DefaultQuery("format", "json") {
	case "", "json":
		OK(c, report)
	case "csv":
		c.Header("Content-Type", "text/csv")
		c.Header("Content-Disposition", "attachment; filename=compliance-report-"+report.ID+".csv")
		_, _ = c.Writer.WriteString(complianceToCSV(report))
	default:
		Error(c, http.StatusBadRequest, 400, "不支持的导出格式（仅支持 json|csv）")
	}
}

type generateReportRequest struct {
	PeriodType string `json:"period_type" binding:"required"` // day|week|month
	Start      string `json:"start"`                          // 2006-01-02；空取当前周期起点
}

// generateComplianceReport POST /api/compliance-reports/generate:手动补生成指定周期报表。
// 周期起点在本包内归一（不引入 enterprise.BuildRange），生成器由 enterprise 装配注入；
// 存储按 period_type+period_start 幂等 UPSERT，同期重复生成覆盖原记录
func (s *AdminServer) generateComplianceReport(c *gin.Context) {
	if s.globalOnlyGuard(c) {
		return
	}
	if s.reportGenerator == nil {
		Error(c, http.StatusServiceUnavailable, http.StatusServiceUnavailable, "合规生成不可用")
		return
	}
	var req generateReportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, http.StatusBadRequest, 400, "period_type 必填（day|week|month）")
		return
	}
	periodType := strings.ToLower(req.PeriodType)
	switch periodType {
	case plugin.PeriodDay, plugin.PeriodWeek, plugin.PeriodMonth:
	default:
		Error(c, http.StatusBadRequest, 400, "period_type 仅支持 day|week|month")
		return
	}
	start := currentPeriodStart(periodType, time.Now())
	if req.Start != "" {
		parsed, err := time.ParseInLocation("2006-01-02", req.Start, time.Local)
		if err != nil {
			Error(c, http.StatusBadRequest, 400, "start 格式须为 2006-01-02")
			return
		}
		start = parsed
	}
	report, err := s.reportGenerator(periodType, start)
	if err != nil {
		Error(c, http.StatusInternalServerError, 500, err.Error())
		return
	}
	OK(c, report)
}

// currentPeriodStart 计算 now 所在周期起点：day=当日零点、week=本周一零点、
// month=本月一日零点；未知类型兜底 day（与 enterprise BuildRange 同规则）
func currentPeriodStart(periodType string, now time.Time) time.Time {
	loc := now.Location()
	y, m, d := now.Date()
	midnight := time.Date(y, m, d, 0, 0, 0, 0, loc)
	switch periodType {
	case plugin.PeriodWeek:
		wd := int(now.Weekday())
		if wd == 0 {
			wd = 7 // 周日按一周第 7 天计（周一为起点）
		}
		return midnight.AddDate(0, 0, -(wd - 1))
	case plugin.PeriodMonth:
		return time.Date(y, m, 1, 0, 0, 0, 0, loc)
	default:
		return midnight
	}
}

// complianceToCSV 报表导出文本（纯函数）：表头 section,key,requests,tokens；
// summary 五行数值放 requests 列 tokens 留空；model_*/tenant_* 维度行双列数值。
// 经 csv 编码转义模型名/租户键中的逗号等特殊字符
func complianceToCSV(r *plugin.ComplianceReport) string {
	var b strings.Builder
	cw := csv.NewWriter(&b)
	_ = cw.Write([]string{"section", "key", "requests", "tokens"})
	content := r.Content
	if content == nil {
		content = &plugin.ReportContent{}
	}
	for _, row := range [][3]string{
		{"summary", "total_requests", strconv.FormatInt(content.TotalRequests, 10)},
		{"summary", "total_tokens", strconv.FormatInt(content.TotalTokens, 10)},
		{"summary", "error_4xx", strconv.FormatInt(content.Error4xx, 10)},
		{"summary", "error_5xx", strconv.FormatInt(content.Error5xx, 10)},
		{"summary", "stream_count", strconv.FormatInt(content.StreamCount, 10)},
	} {
		_ = cw.Write([]string{row[0], row[1], row[2], ""})
	}
	for _, st := range content.ByModel {
		_ = cw.Write([]string{"model", "model_" + st.Key,
			strconv.FormatInt(st.Requests, 10), strconv.FormatInt(st.Tokens, 10)})
	}
	for _, st := range content.ByTenant {
		_ = cw.Write([]string{"tenant", "tenant_" + st.Key,
			strconv.FormatInt(st.Requests, 10), strconv.FormatInt(st.Tokens, 10)})
	}
	cw.Flush()
	return b.String()
}
