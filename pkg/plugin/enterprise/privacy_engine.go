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

//go:build enterprise

package enterprise

import (
	"bytes"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"go.uber.org/zap"
)

// compiledRule 编译后的隐私规则
type compiledRule struct {
	source *plugin.PrivacyRule
	re     *regexp.Regexp
}

// PrivacyEngine 隐私防护引擎：编译规则缓存 + TTL 自动重载。
// 规则经管理后台 CRUD 后 ≤TTL 生效（免 admin↔engine 回调耦合）；
// 禁用条目与非法正则加载时跳过并告警；正则执行异常降级放行（可用性优先）
type PrivacyEngine struct {
	storage plugin.StoragePlugin
	ttl     time.Duration
	logger  *zap.Logger

	mu             sync.RWMutex
	loadedAt       time.Time
	piiRules       []*compiledRule
	injectionRules []*compiledRule
	outputRules    []*compiledRule
	whitelist      []*compiledRule
}

// NewPrivacyEngine 创建引擎；ttl 为规则重载周期（生产 30s，测试注入短值）
func NewPrivacyEngine(storage plugin.StoragePlugin, ttl time.Duration, logger *zap.Logger) *PrivacyEngine {
	return &PrivacyEngine{storage: storage, ttl: ttl, logger: logger}
}

// snapshot 返回当前编译规则；从未加载或缓存超过 TTL 时先重载再取
func (e *PrivacyEngine) snapshot() (pii, inj, output, wl []*compiledRule) {
	e.mu.RLock()
	expired := e.loadedAt.IsZero() || time.Since(e.loadedAt) > e.ttl
	pii, inj, output, wl = e.piiRules, e.injectionRules, e.outputRules, e.whitelist
	e.mu.RUnlock()
	if !expired {
		return pii, inj, output, wl
	}
	e.reload()
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.piiRules, e.injectionRules, e.outputRules, e.whitelist
}

// reload 从存储重载全部规则并整体换入缓存。
// 加载失败沿用旧缓存、仅刷新时间戳（避免失败后每次请求都打库）
func (e *PrivacyEngine) reload() {
	var pii, inj, output []*compiledRule
	rules, err := e.storage.ListPrivacyRules(nil)
	if err != nil {
		e.logger.Warn("隐私规则加载失败，沿用上次缓存", zap.Error(err))
	} else {
		for _, r := range rules {
			if !r.Enabled {
				continue
			}
			re, cErr := regexp.Compile(r.Pattern)
			if cErr != nil {
				e.logger.Warn("隐私规则正则非法，已跳过", zap.String("rule", r.Name), zap.Error(cErr))
				continue
			}
			cr := &compiledRule{source: r, re: re}
			switch r.RuleType {
			case plugin.PrivacyRuleTypePII:
				pii = append(pii, cr)
			case plugin.PrivacyRuleTypeInjection:
				inj = append(inj, cr)
			case plugin.PrivacyRuleTypeOutput:
				output = append(output, cr)
			}
		}
	}
	var wl []*compiledRule
	entries, wErr := e.storage.ListPrivacyWhitelistEntries()
	if wErr != nil {
		e.logger.Warn("隐私白名单加载失败，沿用上次缓存", zap.Error(wErr))
	} else {
		for _, entry := range entries {
			if !entry.Enabled {
				continue
			}
			re, cErr := regexp.Compile(entry.Pattern)
			if cErr != nil {
				e.logger.Warn("白名单正则非法，已跳过", zap.String("note", entry.Note), zap.Error(cErr))
				continue
			}
			wl = append(wl, &compiledRule{source: &plugin.PrivacyRule{Name: "whitelist:" + entry.Note}, re: re})
		}
	}
	e.mu.Lock()
	if err == nil {
		e.piiRules, e.injectionRules, e.outputRules = pii, inj, output
	}
	if wErr == nil {
		e.whitelist = wl
	}
	e.loadedAt = time.Now()
	e.mu.Unlock()
}

// Whitelisted 内容命中任一启用白名单正则返回 true（调用方据此整体跳过脱敏与注入检测）
func (e *PrivacyEngine) Whitelisted(body []byte) bool {
	_, _, _, wl := e.snapshot()
	for _, cr := range wl {
		if safeMatch(cr, body) {
			return true
		}
	}
	return false
}

// Sanitize 按 scope 过滤 PII 规则做字面替换（replacement 不解释 $1 等分组引用），
// 返回结果文本与是否变更；单条规则异常保留当前文本继续（降级放行）
func (e *PrivacyEngine) Sanitize(body []byte, scope string) ([]byte, bool) {
	pii, _, output, _ := e.snapshot()
	result := body
	for _, cr := range pii {
		if s := cr.source.Scope; s != plugin.PrivacyScopeBoth && s != scope {
			continue
		}
		if !safeMatch(cr, result) {
			continue
		}
		result = safeReplace(cr, result)
	}
	// 输出风控 redact：output 类规则仅作用响应侧，按 Replacement 替换命中内容；
	// block 动作不在此处理（由中间件拦截），空替换串跳过
	if scope == plugin.PrivacyScopeResponse {
		for _, cr := range output {
			if strings.EqualFold(cr.source.Action, plugin.PrivacyActionBlock) || cr.source.Replacement == "" {
				continue
			}
			if !safeMatch(cr, result) {
				continue
			}
			result = safeReplace(cr, result)
		}
	}
	return result, !bytes.Equal(body, result)
}

// DetectInjection 返回命中的注入检测规则（nil=未命中）
func (e *PrivacyEngine) DetectInjection(body []byte) *plugin.PrivacyRule {
	_, inj, _, _ := e.snapshot()
	for _, cr := range inj {
		if safeMatch(cr, body) {
			return cr.source
		}
	}
	return nil
}

// DetectOutput 输出内容风控：命中任一启用的 output 类规则即返回该规则
// （调用方按 Action 决定 redact/block）；nil=未命中
func (e *PrivacyEngine) DetectOutput(body []byte) *plugin.PrivacyRule {
	_, _, output, _ := e.snapshot()
	for _, cr := range output {
		if safeMatch(cr, body) {
			return cr.source
		}
	}
	return nil
}

// safeMatch 正则执行兜底：panic 一律视为未命中
func safeMatch(cr *compiledRule, b []byte) (hit bool) {
	defer func() {
		if recover() != nil {
			hit = false
		}
	}()
	return cr.re.Match(b)
}

// safeReplace 正则替换兜底：panic 返回原文（降级放行）
func safeReplace(cr *compiledRule, b []byte) (out []byte) {
	defer func() {
		if recover() != nil {
			out = b
		}
	}()
	return cr.re.ReplaceAllLiteral(b, []byte(cr.source.Replacement))
}
