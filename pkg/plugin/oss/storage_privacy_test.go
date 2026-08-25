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

package oss

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// TestMemPrivacyRulesCRUD 内存存储规则库 CRUD 与类型过滤往返
func TestMemPrivacyRulesCRUD(t *testing.T) {
	s := NewMemStorage()
	rule := &plugin.PrivacyRule{RuleType: plugin.PrivacyRuleTypePII, Name: "测试规则", Pattern: `\d{4}`, Replacement: "****", Scope: plugin.PrivacyScopeBoth, Enabled: true}
	if err := s.SavePrivacyRule(rule); err != nil {
		t.Fatalf("SavePrivacyRule: %v", err)
	}
	if rule.ID == "" || rule.CreatedAt.IsZero() {
		t.Fatal("保存后应回填 ID 与 CreatedAt")
	}
	// 更新（同 ID 覆盖）
	rule.Name = "改名"
	if err := s.SavePrivacyRule(rule); err != nil {
		t.Fatalf("SavePrivacyRule update: %v", err)
	}
	all, err := s.ListPrivacyRules(nil)
	if err != nil || len(all) != 1 {
		t.Fatalf("ListPrivacyRules = %d items, err=%v, want 1", len(all), err)
	}
	if all[0].Name != "改名" {
		t.Errorf("更新未生效: %s", all[0].Name)
	}
	// 类型过滤
	inj := &plugin.PrivacyRule{RuleType: plugin.PrivacyRuleTypeInjection, Name: "注入", Pattern: "bad", Scope: plugin.PrivacyScopeRequest, Enabled: true}
	_ = s.SavePrivacyRule(inj)
	piiType := plugin.PrivacyRuleTypePII
	filtered, _ := s.ListPrivacyRules(&piiType)
	if len(filtered) != 1 || filtered[0].RuleType != plugin.PrivacyRuleTypePII {
		t.Errorf("类型过滤应只返回 pii 规则, got %d", len(filtered))
	}
	// 删除与不存在分支
	if err := s.DeletePrivacyRule(inj.ID); err != nil {
		t.Fatalf("DeletePrivacyRule: %v", err)
	}
	if err := s.DeletePrivacyRule("no-such-id"); !errors.Is(err, ErrNotFound) {
		t.Errorf("删除不存在规则应 ErrNotFound, got %v", err)
	}
}

// TestMemPrivacyWhitelistCRUD 白名单 CRUD 往返
func TestMemPrivacyWhitelistCRUD(t *testing.T) {
	s := NewMemStorage()
	entry := &plugin.PrivacyWhitelistEntry{Pattern: `^白名单样本$`, Note: "测试", Enabled: true}
	if err := s.SavePrivacyWhitelistEntry(entry); err != nil {
		t.Fatalf("Save: %v", err)
	}
	list, _ := s.ListPrivacyWhitelistEntries()
	if len(list) != 1 || list[0].Pattern != `^白名单样本$` {
		t.Fatalf("List = %+v", list)
	}
	if err := s.DeletePrivacyWhitelistEntry(entry.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := s.DeletePrivacyWhitelistEntry(entry.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("重复删除应 ErrNotFound, got %v", err)
	}
}

// TestMemSecurityEventsPagination 安全事件追加写入 + 倒序分页
func TestMemSecurityEventsPagination(t *testing.T) {
	s := NewMemStorage()
	for i := 0; i < 5; i++ {
		ev := &plugin.SecurityEvent{RequestID: string(rune('a' + i)), CreatedAt: time.Now().Add(time.Duration(i) * time.Second)}
		if err := s.SaveSecurityEvent(ev); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	items, total, err := s.ListSecurityEvents(1, 3)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 5 || len(items) != 3 {
		t.Fatalf("total=%d len=%d, want 5/3", total, len(items))
	}
	if items[0].RequestID != "e" { // 最近写入优先
		t.Errorf("首页最新应为 e, got %s", items[0].RequestID)
	}
	items2, _, _ := s.ListSecurityEvents(2, 3)
	if len(items2) != 2 || items2[0].RequestID != "b" {
		t.Errorf("第二页应为 b,a, got %+v", items2)
	}
}

// TestSQLiteInitSeedsPrivacyOnce 同一库文件两次 Init：三张隐私表存在且种子只写一次
func TestSQLiteInitSeedsPrivacyOnce(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "privacy.db")
	cfg := map[string]interface{}{"driver": "sqlite", "dsn": dsn}
	s := NewSQLStorage()
	if err := s.Init(cfg); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM privacy_rules").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 10 {
		t.Fatalf("首次建表种子数 = %d, want 10", count)
	}
	// 重启不重复插入
	if err := s.Init(cfg); err != nil {
		t.Fatalf("second Init: %v", err)
	}
	defer s.Close()
	if err := s.db.QueryRow("SELECT COUNT(*) FROM privacy_rules").Scan(&count); err != nil {
		t.Fatalf("recount: %v", err)
	}
	if count != 10 {
		t.Errorf("重启后种子数 = %d, want 10(不重复插入)", count)
	}
	for _, table := range []string{"privacy_rules", "privacy_whitelist", "security_events"} {
		var name string
		if err := s.db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name); err != nil {
			t.Errorf("表 %s 不存在: %v", table, err)
		}
	}
}

// TestSQLitePrivacyRoundTrip SQL 存储规则/白名单/事件 CRUD 往返
func TestSQLitePrivacyRoundTrip(t *testing.T) {
	s := NewSQLStorage()
	if err := s.Init(map[string]interface{}{"driver": "sqlite", "dsn": filepath.Join(t.TempDir(), "rt.db")}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer s.Close()

	rule := &plugin.PrivacyRule{RuleType: plugin.PrivacyRuleTypePII, Name: "SQL规则", Pattern: `[0-9]+`, Replacement: "*", Scope: plugin.PrivacyScopeResponse, Enabled: true}
	if err := s.SavePrivacyRule(rule); err != nil {
		t.Fatalf("SavePrivacyRule: %v", err)
	}
	got, err := s.ListPrivacyRules(nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := false
	for _, r := range got {
		if r.ID == rule.ID && r.Scope == plugin.PrivacyScopeResponse && r.Enabled {
			found = true
		}
	}
	if !found {
		t.Errorf("规则未按字段往返: %+v", got)
	}

	entry := &plugin.PrivacyWhitelistEntry{Pattern: "^ok$", Note: "n", Enabled: false}
	if err := s.SavePrivacyWhitelistEntry(entry); err != nil {
		t.Fatalf("SaveWhitelist: %v", err)
	}
	wl, _ := s.ListPrivacyWhitelistEntries()
	if len(wl) != 1 || wl[0].Enabled {
		t.Errorf("白名单往返失败: %+v", wl)
	}

	if err := s.SaveSecurityEvent(&plugin.SecurityEvent{RequestID: "req-1", RuleName: "r", Snippet: "s", ClientIP: "127.0.0.1"}); err != nil {
		t.Fatalf("SaveEvent: %v", err)
	}
	events, total, err := s.ListSecurityEvents(1, 10)
	if err != nil || total != 1 || len(events) != 1 {
		t.Fatalf("events total=%d len=%d err=%v", total, len(events), err)
	}
	if events[0].RequestID != "req-1" || events[0].ClientIP != "127.0.0.1" {
		t.Errorf("事件字段往返失败: %+v", events[0])
	}

	if err := s.DeletePrivacyRule(rule.ID); err != nil {
		t.Errorf("DeletePrivacyRule: %v", err)
	}
	if err := s.DeletePrivacyWhitelistEntry(entry.ID); err != nil {
		t.Errorf("DeleteWhitelistEntry: %v", err)
	}
}
