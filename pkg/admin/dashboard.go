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
	"fmt"
	"net/http"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/gin-gonic/gin"
)

// dashboard GET /api/dashboard?window=24h|7d|30d：首页概览聚合数据。
// 查询区间与分桶规格均出自 plugin.DashboardRange，二者同源；
// 存储失败整页报错，绝不返回半截数字——宁可无数据，不可有错数据
func (s *AdminServer) dashboard(c *gin.Context) {
	window := c.DefaultQuery("window", plugin.DashboardDefaultWindow)
	now := time.Now()
	start, end, _, _, err := plugin.DashboardRange(window, now)
	if err != nil {
		Error(c, http.StatusBadRequest, 400, "window 仅支持 24h|7d|30d")
		return
	}
	samples, truncated, err := s.storage.AuditSamples(start, end, plugin.MaxAuditSamples)
	if err != nil {
		ErrorCause(c, http.StatusInternalServerError, 500, "failed to load dashboard samples", err)
		return
	}
	data, err := plugin.ComputeDashboard(samples, window, now, truncated)
	if err != nil {
		ErrorCause(c, http.StatusInternalServerError, 500, "failed to compute dashboard", err)
		return
	}
	data.Alerts = s.dashboardAlerts()
	OK(c, data)
}

// dashboardAlerts 首页告警：授权降级与未处置篡改告警。
// 只取授权 Status/Message，不含 LicenseInfo 的授权码/客户名等业务字段。
// 未知授权状态按 warning 兜底——将来新增状态时首页默认可见，不悄悄隐瞒一次降级
func (s *AdminServer) dashboardAlerts() []plugin.DashboardAlert {
	alerts := []plugin.DashboardAlert{}
	ov := s.licenseOverview()
	switch ov.Status {
	case "valid", "oss":
		// 正常态：有效授权与开源版均无授权告警
	case "expired", "invalid", "missing":
		detail := ov.Message
		if detail == "" {
			detail = "当前授权不可用，已降级为开源模式运行"
		}
		alerts = append(alerts, plugin.DashboardAlert{
			Level:  "warning",
			Title:  "授权异常，已降级运行",
			Detail: detail,
		})
	default:
		alerts = append(alerts, plugin.DashboardAlert{
			Level:  "warning",
			Title:  "授权状态未知",
			Detail: fmt.Sprintf("当前授权状态为 %s，请核实授权文件", ov.Status),
		})
	}
	if n := s.unresolvedTamperCount(); n > 0 {
		alerts = append(alerts, plugin.DashboardAlert{
			Level:  "error",
			Title:  "检测到审计篡改告警",
			Detail: fmt.Sprintf("%d 条未处理，请立即核实处置", n),
			Link:   "/tamper-alerts",
		})
	}
	return alerts
}
