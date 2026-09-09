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
	"net/http"
	"strings"
)

// 管理面操作日志按「功能模块 + 操作类型」分类的单一映射源。
// module/action 存中文文案并原样回显,入库与展示一致;路径参数段(:id/:uid)
// 用占位符,同时兼容 gin 模板路径(写时)与具体路径(历史回填读时)。

// 模块展示名(与 WebUI 菜单口径一致)
const (
	OpModuleAccount    = "账号管理"
	OpModuleAPIKey     = "API Key"
	OpModuleModel      = "模型配置"
	OpModuleRateLimit  = "限流配置"
	OpModulePrivacy    = "隐私合规"
	OpModuleMCP        = "MCP 服务"
	OpModuleCompliance = "合规报表"
	OpModuleTamper     = "篡改告警"
	OpModuleLicense    = "授权管理"
	OpModuleTenant     = "租户管理"
	OpModuleRole       = "角色管理"
	OpModuleSystem     = "系统管理" // 未命中路由的兜底模块
)

// OpActionStatusToggle 状态切换类操作的兜底动作文案;写入时若请求体带 status,
// 由 ToggleActionForStatus 细化为「启用/禁用」。
const OpActionStatusToggle = "启停"

// adminOpLogRule 一条(方法,路径模板) → (模块, 操作) 分类规则
type adminOpLogRule struct {
	method string
	path   string // 形如 /api/models/:id,冒号段视为通配
	module string
	action string
}

// adminOpLogRules 管理面全部写操作路由的分类规则;与 pkg/admin/router.go 注册一致。
var adminOpLogRules = []adminOpLogRule{
	{http.MethodPut, "/api/auth/password", OpModuleAccount, "修改密码"},

	{http.MethodPost, "/api/api-keys", OpModuleAPIKey, "创建"},
	{http.MethodPost, "/api/api-keys/batch-create", OpModuleAPIKey, "批量创建"},
	{http.MethodPost, "/api/api-keys/batch-delete", OpModuleAPIKey, "批量删除"},
	{http.MethodPatch, "/api/api-keys/:id", OpModuleAPIKey, OpActionStatusToggle},
	{http.MethodDelete, "/api/api-keys/:id", OpModuleAPIKey, "删除"},

	{http.MethodPost, "/api/models", OpModuleModel, "创建"},
	{http.MethodPut, "/api/models/:id", OpModuleModel, "编辑"},
	{http.MethodDelete, "/api/models/:id", OpModuleModel, "删除"},
	{http.MethodPost, "/api/models/:id/test", OpModuleModel, "连通测试"},
	{http.MethodPost, "/api/models/:id/upstreams", OpModuleModel, "添加上游"},
	{http.MethodPut, "/api/upstreams/:uid", OpModuleModel, "编辑上游"},
	{http.MethodDelete, "/api/upstreams/:uid", OpModuleModel, "删除上游"},

	{http.MethodPost, "/api/license", OpModuleLicense, "上传授权"},
	{http.MethodPatch, "/api/tamper-alerts/:id", OpModuleTamper, "标记处置"},
	{http.MethodPost, "/api/compliance-reports/generate", OpModuleCompliance, "生成报表"},

	{http.MethodPost, "/api/mcp-servers", OpModuleMCP, "创建"},
	{http.MethodPut, "/api/mcp-servers/:id", OpModuleMCP, "编辑"},
	{http.MethodDelete, "/api/mcp-servers/:id", OpModuleMCP, "删除"},

	{http.MethodPost, "/api/rate-limits", OpModuleRateLimit, "创建"},
	{http.MethodPut, "/api/rate-limits/:id", OpModuleRateLimit, "编辑"},
	{http.MethodDelete, "/api/rate-limits/:id", OpModuleRateLimit, "删除"},

	{http.MethodPost, "/api/privacy-rules", OpModulePrivacy, "创建"},
	{http.MethodPut, "/api/privacy-rules/:id", OpModulePrivacy, "编辑"},
	{http.MethodDelete, "/api/privacy-rules/:id", OpModulePrivacy, "删除"},
	{http.MethodPost, "/api/privacy-whitelist", OpModulePrivacy, "添加白名单"},
	{http.MethodDelete, "/api/privacy-whitelist/:id", OpModulePrivacy, "删除白名单"},

	{http.MethodPost, "/api/tenants", OpModuleTenant, "创建"},
	{http.MethodPut, "/api/tenants/:id", OpModuleTenant, "编辑"},
	{http.MethodDelete, "/api/tenants/:id", OpModuleTenant, "删除"},

	{http.MethodPost, "/api/roles", OpModuleRole, "创建"},
	{http.MethodPut, "/api/roles/:id", OpModuleRole, "编辑"},
	{http.MethodDelete, "/api/roles/:id", OpModuleRole, "删除"},

	{http.MethodPost, "/api/admin-users", OpModuleAccount, "创建"},
	{http.MethodPut, "/api/admin-users/:id", OpModuleAccount, "编辑"},
	{http.MethodDelete, "/api/admin-users/:id", OpModuleAccount, "删除"},
}

// ClassifyOperation 把管理面写操作归为(功能模块, 操作类型)中文文案。
// path 接受 gin 模板(参数段为 :id/:uid)或带真实值的具体路径(参数段归一后匹配);
// 未命中规则时按 method 粗分到 OpModuleSystem,保证未来新增路由仍可打标。
func ClassifyOperation(method, path string) (module, action string) {
	for _, r := range adminOpLogRules {
		if r.method == method && opPathMatch(r.path, path) {
			return r.module, r.action
		}
	}
	switch method {
	case http.MethodPost:
		return OpModuleSystem, "创建"
	case http.MethodPut, http.MethodPatch:
		return OpModuleSystem, "编辑"
	case http.MethodDelete:
		return OpModuleSystem, "删除"
	default:
		return OpModuleSystem, method
	}
}

// ToggleActionForStatus 状态切换请求的细化动作:active→启用,disabled→禁用,其余兜底。
func ToggleActionForStatus(status string) string {
	switch status {
	case "active":
		return "启用"
	case "disabled":
		return "禁用"
	default:
		return OpActionStatusToggle
	}
}

// opPathMatch 段级匹配:规则中冒号开头的段为通配(接受任意非空段),其余须字面相等。
func opPathMatch(rulePath, path string) bool {
	rs := pathSegs(rulePath)
	ps := pathSegs(path)
	if len(rs) != len(ps) {
		return false
	}
	for i, seg := range rs {
		if strings.HasPrefix(seg, ":") {
			if ps[i] == "" {
				return false
			}
			continue
		}
		if seg != ps[i] {
			return false
		}
	}
	return true
}

// pathSegs 按 / 拆分路径,丢弃首尾空段。
func pathSegs(p string) []string {
	raw := strings.Split(p, "/")
	segs := raw[:0]
	for _, s := range raw {
		if s != "" {
			segs = append(segs, s)
		}
	}
	return segs
}
