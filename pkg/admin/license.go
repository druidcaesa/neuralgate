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
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/gin-gonic/gin"
)

// LicenseOverview 授权概要：启动时校验一次的结果快照，供后台展示。
// Status 取值 valid/expired/invalid/missing/oss；Info 在授权缺失时为 nil，
// 过期或验签失败时仍尽量携带已加载字段。
type LicenseOverview struct {
	Status  string              // 授权状态
	Message string              // 降级原因说明（有效或 OSS 时为空）
	Info    *plugin.LicenseInfo // 授权业务字段
}

// licenseResponse GET /api/license 响应体（脱敏后）
type licenseResponse struct {
	Status        string     `json:"status"`                   // 授权状态
	Message       string     `json:"message,omitempty"`        // 降级原因
	Edition       string     `json:"edition"`                  // 运行版本（降级后为 oss）
	LicenseKey    string     `json:"license_key,omitempty"`    // 授权码（前 8 位 + ****）
	ProductName   string     `json:"product_name,omitempty"`   // 产品名称
	CustomerName  string     `json:"customer_name,omitempty"`  // 客户名称
	MaxNodes      int        `json:"max_nodes,omitempty"`      // 最大节点数
	MaxTenants    int        `json:"max_tenants,omitempty"`    // 最大租户数
	IssuedAt      *time.Time `json:"issued_at,omitempty"`      // 签发时间
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`     // 过期时间
	Features      []string   `json:"features,omitempty"`       // 授权功能列表
	IsOffline     bool       `json:"is_offline,omitempty"`     // 是否离线授权
	Signed        bool       `json:"signed"`                   // 是否携带签名（不回显签名全文）
	DaysRemaining *int       `json:"days_remaining,omitempty"` // 剩余天数（仅有效时）
}

// licenseInfo GET /api/license：授权状态与脱敏后的业务字段
func (s *AdminServer) licenseInfo(c *gin.Context) {
	OK(c, s.buildLicenseResponse())
}

// licenseOverview 返回授权概要；未注入时按 OSS 未授权处理
func (s *AdminServer) licenseOverview() *LicenseOverview {
	if s.license == nil {
		return &LicenseOverview{Status: "oss", Message: "开源版本，无授权信息"}
	}
	return s.license
}

// buildLicenseResponse 由授权概要构造展示响应（剩余天数在请求时实时计算）
func (s *AdminServer) buildLicenseResponse() licenseResponse {
	ov := s.licenseOverview()
	resp := licenseResponse{
		Status:  ov.Status,
		Message: ov.Message,
		Edition: s.edition,
		Signed:  ov.Info != nil && ov.Info.Signature != "",
	}
	if ov.Info == nil {
		return resp
	}
	resp.LicenseKey = maskLicenseKey(ov.Info.LicenseKey)
	resp.ProductName = ov.Info.ProductName
	resp.CustomerName = ov.Info.CustomerName
	resp.MaxNodes = ov.Info.MaxNodes
	resp.MaxTenants = ov.Info.MaxTenants
	resp.IssuedAt = &ov.Info.IssuedAt
	resp.ExpiresAt = &ov.Info.ExpiresAt
	resp.Features = ov.Info.Features
	resp.IsOffline = ov.Info.IsOffline
	if ov.Status == "valid" {
		days := int(time.Until(ov.Info.ExpiresAt).Hours() / 24)
		resp.DaysRemaining = &days
	}
	return resp
}

// maskLicenseKey 授权码脱敏：保留前 8 位，其余以 **** 替代
func maskLicenseKey(key string) string {
	if len(key) <= 8 {
		return key + "****"
	}
	return key[:8] + "****"
}
