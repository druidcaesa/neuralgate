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

import "testing"

func TestClassifyOperation(t *testing.T) {
	cases := []struct {
		method, path, wantModule, wantAction string
	}{
		// 模板路径(写时)与具体路径(回填读时)应归一为同一结果
		{"PUT", "/api/auth/password", OpModuleAccount, "修改密码"},

		{"POST", "/api/api-keys", OpModuleAPIKey, "创建"},
		{"POST", "/api/api-keys/batch-create", OpModuleAPIKey, "批量创建"},
		{"POST", "/api/api-keys/batch-delete", OpModuleAPIKey, "批量删除"},
		{"PATCH", "/api/api-keys/:id", OpModuleAPIKey, OpActionStatusToggle},
		{"PATCH", "/api/api-keys/key-01", OpModuleAPIKey, OpActionStatusToggle},
		{"DELETE", "/api/api-keys/:id", OpModuleAPIKey, "删除"},
		{"DELETE", "/api/api-keys/key-01", OpModuleAPIKey, "删除"},

		{"POST", "/api/models", OpModuleModel, "创建"},
		{"PUT", "/api/models/:id", OpModuleModel, "编辑"},
		{"PUT", "/api/models/model-ab", OpModuleModel, "编辑"},
		{"DELETE", "/api/models/model-ab", OpModuleModel, "删除"},
		{"POST", "/api/models/model-ab/test", OpModuleModel, "连通测试"},
		{"POST", "/api/models/model-ab/upstreams", OpModuleModel, "添加上游"},
		{"PUT", "/api/upstreams/u-1", OpModuleModel, "编辑上游"},
		{"DELETE", "/api/upstreams/u-1", OpModuleModel, "删除上游"},

		{"POST", "/api/license", OpModuleLicense, "上传授权"},
		{"PATCH", "/api/tamper-alerts/ta-1", OpModuleTamper, "标记处置"},
		{"POST", "/api/compliance-reports/generate", OpModuleCompliance, "生成报表"},

		{"POST", "/api/mcp-servers", OpModuleMCP, "创建"},
		{"PUT", "/api/mcp-servers/mcp-1", OpModuleMCP, "编辑"},
		{"DELETE", "/api/mcp-servers/mcp-1", OpModuleMCP, "删除"},

		{"POST", "/api/rate-limits", OpModuleRateLimit, "创建"},
		{"PUT", "/api/rate-limits/rl-1", OpModuleRateLimit, "编辑"},
		{"DELETE", "/api/rate-limits/rl-1", OpModuleRateLimit, "删除"},

		{"POST", "/api/privacy-rules", OpModulePrivacy, "创建"},
		{"PUT", "/api/privacy-rules/pr-1", OpModulePrivacy, "编辑"},
		{"DELETE", "/api/privacy-rules/pr-1", OpModulePrivacy, "删除"},
		{"POST", "/api/privacy-whitelist", OpModulePrivacy, "添加白名单"},
		{"DELETE", "/api/privacy-whitelist/pw-1", OpModulePrivacy, "删除白名单"},

		{"POST", "/api/tenants", OpModuleTenant, "创建"},
		{"PUT", "/api/tenants/te-1", OpModuleTenant, "编辑"},
		{"DELETE", "/api/tenants/te-1", OpModuleTenant, "删除"},

		{"POST", "/api/roles", OpModuleRole, "创建"},
		{"PUT", "/api/roles/ro-1", OpModuleRole, "编辑"},
		{"DELETE", "/api/roles/ro-1", OpModuleRole, "删除"},

		{"POST", "/api/admin-users", OpModuleAccount, "创建"},
		{"PUT", "/api/admin-users/ad-1", OpModuleAccount, "编辑"},
		{"DELETE", "/api/admin-users/ad-1", OpModuleAccount, "删除"},

		// 未命中路由按 method 兜底,模块归系统管理
		{"POST", "/api/feature-x/do", OpModuleSystem, "创建"},
		{"PATCH", "/api/feature-x/do", OpModuleSystem, "编辑"},
		{"DELETE", "/api/feature-x/x", OpModuleSystem, "删除"},
		// 读方法不在审计范围,命中兜底返回原方法名
		{"GET", "/api/models", OpModuleSystem, "GET"},
	}
	for _, c := range cases {
		gotModule, gotAction := ClassifyOperation(c.method, c.path)
		if gotModule != c.wantModule || gotAction != c.wantAction {
			t.Errorf("ClassifyOperation(%s, %s) = (%q, %q), want (%q, %q)",
				c.method, c.path, gotModule, gotAction, c.wantModule, c.wantAction)
		}
	}
}

func TestToggleActionForStatus(t *testing.T) {
	cases := []struct {
		status, want string
	}{
		{"active", "启用"},
		{"disabled", "禁用"},
		{"", OpActionStatusToggle},
		{"pending", OpActionStatusToggle},
	}
	for _, c := range cases {
		if got := ToggleActionForStatus(c.status); got != c.want {
			t.Errorf("ToggleActionForStatus(%q) = %q, want %q", c.status, got, c.want)
		}
	}
}
