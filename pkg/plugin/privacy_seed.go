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

// DefaultPrivacyRules 内置规则种子：privacy_rules 空表初始化时批量插入（oss 存储建表流程调用）。
// PII 四条 scope=both 双向脱敏；注入六条恒 request、replacement 为空（命中即拦截）
func DefaultPrivacyRules() []*PrivacyRule {
	return []*PrivacyRule{
		{RuleType: PrivacyRuleTypePII, Name: "手机号脱敏", Pattern: `1[3-9]\d{9}`, Replacement: "1**********", Scope: PrivacyScopeBoth, Enabled: true},
		{RuleType: PrivacyRuleTypePII, Name: "身份证脱敏", Pattern: `\d{17}[\dXx]`, Replacement: "******************", Scope: PrivacyScopeBoth, Enabled: true},
		{RuleType: PrivacyRuleTypePII, Name: "银行卡脱敏", Pattern: `\d{16,19}`, Replacement: "****-****-****-****", Scope: PrivacyScopeBoth, Enabled: true},
		{RuleType: PrivacyRuleTypePII, Name: "邮箱脱敏", Pattern: `[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`, Replacement: "***@***.***", Scope: PrivacyScopeBoth, Enabled: true},
		{RuleType: PrivacyRuleTypeInjection, Name: "忽略指令(中)", Pattern: `忽略(以上|之前|上面)(的)?(所有)?(指令|提示|设定)`, Scope: PrivacyScopeRequest, Enabled: true},
		{RuleType: PrivacyRuleTypeInjection, Name: "忽略指令(英)", Pattern: `(?i)ignore\s+(all\s+)?(previous|prior|above)\s+(instructions|prompts|rules)`, Scope: PrivacyScopeRequest, Enabled: true},
		{RuleType: PrivacyRuleTypeInjection, Name: "系统提示窃取", Pattern: `(?i)(reveal|show|print)\s+(your\s+)?(system\s+)?(prompt|instructions)`, Scope: PrivacyScopeRequest, Enabled: true},
		{RuleType: PrivacyRuleTypeInjection, Name: "角色扮演越狱", Pattern: `(?i)(pretend|act)\s+(to\s+be|as)\s+(a\s+)?(DAN|jailbreak)`, Scope: PrivacyScopeRequest, Enabled: true},
		{RuleType: PrivacyRuleTypeInjection, Name: "开发者模式", Pattern: `(?i)developer\s+mode`, Scope: PrivacyScopeRequest, Enabled: true},
		{RuleType: PrivacyRuleTypeInjection, Name: "越权指令(中)", Pattern: `(你现在是|扮演).*(不受(任何)?限制|没有(任何)?规则)`, Scope: PrivacyScopeRequest, Enabled: true},
	}
}
