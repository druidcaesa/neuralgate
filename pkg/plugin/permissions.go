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

// 权限编码（PRD 十码）：管理面功能点的最小授权单元
const (
	PermAPIKeyRead  = "api_key:read"
	PermAPIKeyWrite = "api_key:write"
	PermModelRead   = "model:read"
	PermModelWrite  = "model:write"
	PermAuditRead   = "audit:read"
	PermAuditExport = "audit:export"
	PermTenantRead  = "tenant:read"
	PermTenantWrite = "tenant:write"
	PermRBACRead    = "rbac:read"
	PermRBACWrite   = "rbac:write"
	// 扩展码：PRD 未覆盖资源的自然延伸
	PermSystemRead     = "system:read"
	PermSystemWrite    = "system:write"
	PermRateLimitRead  = "rate_limit:read"
	PermRateLimitWrite = "rate_limit:write"
	PermPrivacyRead    = "privacy:read"
	PermPrivacyWrite   = "privacy:write"
)

// AllPermissions 全量权限清单：超管角色种子与前端勾选共用，顺序固定
var AllPermissions = []string{
	PermAPIKeyRead, PermAPIKeyWrite,
	PermModelRead, PermModelWrite,
	PermAuditRead, PermAuditExport,
	PermTenantRead, PermTenantWrite,
	PermRBACRead, PermRBACWrite,
	PermSystemRead, PermSystemWrite,
	PermRateLimitRead, PermRateLimitWrite,
	PermPrivacyRead, PermPrivacyWrite,
}

// SuperRoleName 超级管理员内置角色名（全局租户）
const SuperRoleName = "超级管理员"

// IsSuperRole 判定超管：全局角色且权限覆盖全集
func IsSuperRole(r *Role) bool {
	if r == nil || r.TenantID != "" {
		return false
	}
	granted := make(map[string]bool, len(r.Permissions))
	for _, p := range r.Permissions {
		granted[p] = true
	}
	for _, p := range AllPermissions {
		if !granted[p] {
			return false
		}
	}
	return true
}
