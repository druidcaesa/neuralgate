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

// Package license 授权签名载荷的规范化序列化与验签：网关侧校验与 licensegen 签发工具共用，
// 保证签发与验签使用同一份字节表示。
package license

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// 授权功能常量：license 的 features 字段使用的功能名，企业功能模块据此判断门控
const (
	FeatureAuditStream = "audit_stream" // 全量审计日志外推
	FeatureTamperProof = "tamper_proof" // 审计防篡改存证
	FeaturePrivacy     = "privacy"      // 数据隐私合规
	FeatureRBAC        = "rbac"         // 角色权限控制
	FeatureCompliance  = "compliance"   // 等保合规模块
	FeatureMCPAudit    = "mcp_audit"    // MCP 调用审计
	// FeatureDistributedRateLimit 分布式限流(Redis 集中计数,多实例共享配额)
	FeatureDistributedRateLimit = "distributed_ratelimit"
	FeatureDomesticDB           = "domestic_db" // 信创数据库存储
)

// CanonicalPayload 将授权信息中除签名外的全部字段做确定性序列化，作为签名载荷。
// 每个字段以 "长度:内容;" 形式拼接，杜绝字段值含分隔符导致的歧义；
// 时间统一转 UTC RFC3339，Features 按原序合并为逗号分隔串。
// 签发与验签必须使用同一函数保证字节一致。
func CanonicalPayload(info *plugin.LicenseInfo) []byte {
	fields := []string{
		info.LicenseKey,
		info.ProductName,
		info.CustomerName,
		strconv.Itoa(info.MaxNodes),
		strconv.Itoa(info.MaxTenants),
		info.IssuedAt.UTC().Format(time.RFC3339),
		info.ExpiresAt.UTC().Format(time.RFC3339),
		strings.Join(info.Features, ","),
		strconv.FormatBool(info.IsOffline),
	}
	var buf bytes.Buffer
	for _, f := range fields {
		fmt.Fprintf(&buf, "%d:%s;", len(f), f)
	}
	return buf.Bytes()
}
