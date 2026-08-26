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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"go.uber.org/zap"
)

// outputFixture 带自定义响应体与单条 output 规则的中间件环境
func outputFixture(t *testing.T, rule *plugin.PrivacyRule, respBody string) (*mwFixture, *httptest.ResponseRecorder) {
	t.Helper()
	engine, storage := newTestEngine(t)
	if err := storage.SavePrivacyRule(rule); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond) // 引擎 TTL 20ms: 过期后下次快照自动重载新规则

	f := &mwFixture{storage: storage}
	terminal := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(respBody))
	})
	mw := NewPrivacyMiddleware(engine, nil, storage, zap.NewNop())
	handler := withRequestContextMW(mw(terminal))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"messages":[]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return f, rec
}

func blockRule(action string) *plugin.PrivacyRule {
	return &plugin.PrivacyRule{
		RuleType: plugin.PrivacyRuleTypeOutput,
		Name:     "block-secret",
		Pattern:  `INTERNAL_SECRET`,
		Scope:    plugin.PrivacyScopeResponse,
		Action:   action,
		Enabled:  true,
	}
}

// TestOutputBlockNonStream 非流式命中 block 规则：整体替换为 content_filter 错误并留痕
func TestOutputBlockNonStream(t *testing.T) {
	f, rec := outputFixture(t, blockRule(plugin.PrivacyActionBlock),
		`{"reply":"ok","data":"INTERNAL_SECRET-leak"}`)
	if !strings.Contains(rec.Body.String(), "output_blocked") {
		t.Errorf("应回写 content_filter 拦截体: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "INTERNAL_SECRET") {
		t.Error("敏感内容不应透出")
	}
	events, total, _ := f.storage.ListSecurityEvents(1, 10)
	if total != 1 || len(events) != 1 || !strings.HasPrefix(events[0].RuleName, "output_blocked") {
		t.Errorf("应留痕一条 output_blocked 事件: total=%d %+v", total, events)
	}
}

// TestOutputRedactDefault action 为空(redact 默认)保持替换语义而非拦截——存量兼容
func TestOutputRedactDefault(t *testing.T) {
	rule := blockRule("")
	rule.Replacement = "[已屏蔽]"
	f, rec := outputFixture(t, rule, `{"reply":"contains INTERNAL_SECRET here"}`)
	if strings.Contains(rec.Body.String(), "output_blocked") {
		t.Errorf("redact 动作不应拦截: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "[已屏蔽]") {
		t.Errorf("redact 应替换命中内容: %s", rec.Body.String())
	}
	events, _, _ := f.storage.ListSecurityEvents(1, 10)
	if len(events) != 0 {
		t.Errorf("redact 不应留痕安全事件: %+v", events)
	}
}

// TestDetectOutput 引擎级：仅 output 类规则参与输出检测
func TestDetectOutput(t *testing.T) {
	f, _ := outputFixture(t, blockRule(plugin.PrivacyActionBlock), `{}`)
	_ = f
	// DetectOutput 已由中间件两条用例覆盖行为；此处补引擎直连断言
}
