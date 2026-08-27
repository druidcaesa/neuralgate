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
	"io"
	"net/http"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/gin-gonic/gin"
)

// LicenseManager 授权上传管理:校验+持久化上传的授权原文,并提供本机机器码。
// enterprise 装配注入;OSS 为 nil。pkg/admin 不依赖 enterprise 包(经本接口解耦)。
type LicenseManager interface {
	LocalMachineID() string
	// ApplyUpload 校验(验签+设备指纹+有效期)并持久化;成功返回解析后的授权,失败返回具体原因
	ApplyUpload(raw []byte) (*plugin.LicenseInfo, error)
}

// SetLicenseManager 注入授权上传管理(enterprise 装配层调用)
func (s *AdminServer) SetLicenseManager(m LicenseManager) { s.licenseMgr = m }

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
	MachineID     string     `json:"machine_id,omitempty"`     // 本机机器码（供页面复制送签；OSS 为空）
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
	if s.licenseMgr != nil {
		resp.MachineID = s.licenseMgr.LocalMachineID() // 未授权/降级态也回显,供取码送签
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

// uploadLicense POST /api/license：上传授权 JSON 原文，即时校验+持久化，功能重启生效。
// 全局域；不受授权门控（过期/换机后仍可上传修复）
func (s *AdminServer) uploadLicense(c *gin.Context) {
	if s.globalOnlyGuard(c) {
		return
	}
	if s.licenseMgr == nil {
		Error(c, http.StatusNotImplemented, http.StatusNotImplemented, "当前版本不支持授权上传")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	if err != nil || len(raw) == 0 {
		Error(c, http.StatusBadRequest, http.StatusBadRequest, "授权文件为空或不可读")
		return
	}
	info, err := s.licenseMgr.ApplyUpload(raw)
	if err != nil {
		Error(c, http.StatusUnprocessableEntity, http.StatusUnprocessableEntity, err.Error())
		return
	}
	OK(c, gin.H{
		"customer_name": info.CustomerName,
		"expires_at":    info.ExpiresAt,
		"features":      info.Features,
		"machine_bound": info.MachineID != "",
		"message":       "上传成功，重启后生效",
	})
}
