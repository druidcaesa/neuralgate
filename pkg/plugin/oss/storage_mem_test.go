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
	"sync"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

func TestAPIKeyCRUD(t *testing.T) {
	s := NewMemStorage()
	if err := s.Init(nil); err != nil {
		t.Fatal(err)
	}
	key := &plugin.APIKey{
		ID:        "k1",
		KeyHash:   "hash1",
		KeyPrefix: "ng-test",
		TenantID:  "t1",
		Name:      "测试Key",
		Status:    plugin.APIKeyStatusActive,
		Quota:     1000,
		CreatedAt: time.Now(),
	}
	if err := s.SaveAPIKey(key); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetAPIKey("hash1")
	if err != nil {
		t.Fatalf("GetAPIKey() error = %v", err)
	}
	if got.ID != "k1" || got.TenantID != "t1" {
		t.Errorf("GetAPIKey() = %+v, want ID=k1 TenantID=t1", got)
	}
	if _, err := s.GetAPIKey("nope"); err != ErrNotFound {
		t.Errorf("GetAPIKey(missing) = %v, want ErrNotFound", err)
	}
	if err := s.IncrementAPIKeyUsage("k1", 500); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetAPIKey("hash1"); got.UsedQuota != 500 {
		t.Errorf("UsedQuota = %d, want 500", got.UsedQuota)
	}
	keys, total, err := s.ListAPIKeys("t1", 1, 10)
	if err != nil || total != 1 || len(keys) != 1 {
		t.Errorf("ListAPIKeys() = %d items/%d total, want 1/1", len(keys), total)
	}
	if err := s.DeleteAPIKey("k1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetAPIKey("hash1"); err != ErrNotFound {
		t.Errorf("GetAPIKey(after delete) = %v, want ErrNotFound", err)
	}
}

func TestModelConfigCRUD(t *testing.T) {
	s := NewMemStorage()
	cfg := &plugin.ModelConfig{
		ID:            "m1",
		ModelName:     "gpt-4",
		Provider:      "openai",
		ProviderModel: "gpt-4",
		BaseURL:       "https://api.openai.com/v1",
		Enabled:       true,
	}
	if err := s.SaveModelConfig(cfg); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetModelConfig("gpt-4")
	if err != nil || got.Provider != "openai" {
		t.Fatalf("GetModelConfig() = %+v, %v", got, err)
	}
	cfgs, total, err := s.ListModelConfigs(1, 10)
	if err != nil || total != 1 || len(cfgs) != 1 {
		t.Errorf("ListModelConfigs() = %d/%d, want 1/1", len(cfgs), total)
	}
	if err := s.DeleteModelConfig("m1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetModelConfig("gpt-4"); err != ErrNotFound {
		t.Errorf("GetModelConfig(after delete) = %v, want ErrNotFound", err)
	}
}

func TestAuditLogSaveAndQuery(t *testing.T) {
	s := NewMemStorage()
	now := time.Now()
	logs := []*plugin.AuditLog{
		{ID: "a1", RequestID: "r1", TenantID: "t1", ModelName: "gpt-4", ResponseStatus: 200, RequestBody: `{"model":"gpt-4"}`, CreatedAt: now},
		{ID: "a2", RequestID: "r2", TenantID: "t2", ModelName: "qwen-max", ResponseStatus: 429, RequestBody: `{"model":"qwen-max"}`, CreatedAt: now.Add(-time.Hour)},
	}
	if err := s.BatchSaveAuditLogs(logs); err != nil {
		t.Fatal(err)
	}
	got, total, err := s.QueryAuditLogs(plugin.AuditLogFilter{TenantID: "t1"}, 1, 10)
	if err != nil || total != 1 || len(got) != 1 || got[0].ID != "a1" {
		t.Errorf("QueryAuditLogs(tenant) = %d/%d %+v, want 1/1", len(got), total, err)
	}
	got, total, _ = s.QueryAuditLogs(plugin.AuditLogFilter{ModelName: "qwen-max"}, 1, 10)
	if total != 1 || got[0].ID != "a2" {
		t.Errorf("QueryAuditLogs(model) total = %d, want 1", total)
	}
	got, total, _ = s.QueryAuditLogs(plugin.AuditLogFilter{Keyword: "gpt-4"}, 1, 10)
	if total != 1 || got[0].ID != "a1" {
		t.Errorf("QueryAuditLogs(keyword) total = %d, want 1", total)
	}
	// 分页
	for i := 0; i < 5; i++ {
		_ = s.SaveAuditLog(&plugin.AuditLog{ID: "b" + string(rune('0'+i)), RequestID: "x" + string(rune('0'+i)), CreatedAt: now})
	}
	got, total, _ = s.QueryAuditLogs(plugin.AuditLogFilter{}, 2, 3)
	if total != 7 || len(got) != 3 {
		t.Errorf("QueryAuditLogs(page2) = %d/%d, want 3/7", len(got), total)
	}
}

func TestPingAndClose(t *testing.T) {
	s := NewMemStorage()
	if err := s.Ping(); err != nil {
		t.Errorf("Ping() error = %v", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func TestMemStorageIncrementAPIKeyUsage(t *testing.T) {
	s := NewMemStorage()
	key := &plugin.APIKey{ID: "k1", KeyHash: "h1", Name: "test"}
	if err := s.SaveAPIKey(key); err != nil {
		t.Fatal(err)
	}
	if err := s.IncrementAPIKeyUsage("k1", 100); err != nil {
		t.Fatalf("IncrementAPIKeyUsage(k1,100) error = %v", err)
	}
	if err := s.IncrementAPIKeyUsage("k1", 50); err != nil {
		t.Fatalf("IncrementAPIKeyUsage(k1,50) error = %v", err)
	}
	got, err := s.GetAPIKeyByID("k1")
	if err != nil || got.UsedQuota != 150 {
		t.Fatalf("UsedQuota = %d, %v; want 150", got.UsedQuota, err)
	}
	// 不存在 key → ErrNotFound
	if err := s.IncrementAPIKeyUsage("nope", 1); err != ErrNotFound {
		t.Fatalf("IncrementAPIKeyUsage(nope) err = %v; want ErrNotFound", err)
	}
}

func TestMemStorageIncrementAPIKeyUsageConcurrent(t *testing.T) {
	// N 个 goroutine 各累加 1,最终 UsedQuota == N(验证原子性,无丢量)
	s := NewMemStorage()
	key := &plugin.APIKey{ID: "k1", KeyHash: "h1", Name: "test"}
	if err := s.SaveAPIKey(key); err != nil {
		t.Fatal(err)
	}
	const n = 100
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.IncrementAPIKeyUsage("k1", 1); err != nil {
				t.Errorf("IncrementAPIKeyUsage: %v", err)
			}
		}()
	}
	wg.Wait()
	got, err := s.GetAPIKeyByID("k1")
	if err != nil || got.UsedQuota != n {
		t.Fatalf("UsedQuota = %d, %v; want %d", got.UsedQuota, err, n)
	}
}

func TestMemStorageGetAPIKeyByID(t *testing.T) {
	s := NewMemStorage()
	key := &plugin.APIKey{ID: "k1", KeyHash: "h1", Name: "test"}
	if err := s.SaveAPIKey(key); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetAPIKeyByID("k1")
	if err != nil || got.ID != "k1" {
		t.Fatalf("GetAPIKeyByID(k1) = %v, %v; want k1, nil", got, err)
	}
	if _, err := s.GetAPIKeyByID("nope"); err != ErrNotFound {
		t.Fatalf("GetAPIKeyByID(nope) err = %v; want ErrNotFound", err)
	}
}

func TestMemStorageGetModelConfigByID(t *testing.T) {
	s := NewMemStorage()
	cfg := &plugin.ModelConfig{ID: "m1", ModelName: "gpt-4"}
	if err := s.SaveModelConfig(cfg); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetModelConfigByID("m1")
	if err != nil || got.ModelName != "gpt-4" {
		t.Fatalf("GetModelConfigByID(m1) = %v, %v", got, err)
	}
	if _, err := s.GetModelConfigByID("nope"); err != ErrNotFound {
		t.Fatalf("GetModelConfigByID(nope) err = %v; want ErrNotFound", err)
	}
}

func TestMemStorageQueryAuditLogsByRequestID(t *testing.T) {
	s := NewMemStorage()
	l1 := &plugin.AuditLog{ID: "a1", RequestID: "r1", ModelName: "gpt-4"}
	if err := s.SaveAuditLog(l1); err != nil {
		t.Fatal(err)
	}
	logs, total, err := s.QueryAuditLogs(plugin.AuditLogFilter{RequestID: "r1"}, 1, 10)
	if err != nil || total != 1 || logs[0].ID != "a1" {
		t.Fatalf("QueryAuditLogs by requestID = %v,%d,%v", logs, total, err)
	}
}

// tamperAlert 构造指向指定审计日志的未处置告警
func tamperAlert(logID string) *plugin.TamperAlert {
	return &plugin.TamperAlert{AuditLogID: logID, Reason: "指纹不一致"}
}

func TestMemSaveListTamperAlerts(t *testing.T) {
	s := NewMemStorage()
	if err := s.SaveTamperAlerts([]*plugin.TamperAlert{tamperAlert("req-1")}); err != nil {
		t.Fatal(err)
	}
	// upsert：同 AuditLogID 再存更新而非新增
	if err := s.SaveTamperAlerts([]*plugin.TamperAlert{tamperAlert("req-1")}); err != nil {
		t.Fatal(err)
	}
	all, total, err := s.ListTamperAlerts(nil, 1, 10)
	if err != nil || total != 1 || len(all) != 1 {
		t.Fatalf("upsert 后应仍 1 条: total=%d err=%v", total, err)
	}
	if all[0].FirstSeenAt.After(all[0].LastCheckedAt) {
		t.Error("LastCheckedAt 应不早于 FirstSeenAt")
	}
	if all[0].ID == "" {
		t.Error("插入时应生成告警 ID")
	}
	// resolved=false 过滤应无结果（当前全部未处置）
	no := false
	unresolved, _, _ := s.ListTamperAlerts(&no, 1, 10)
	yes := true
	resolved, _, _ := s.ListTamperAlerts(&yes, 1, 10)
	if len(unresolved) != 1 || len(resolved) != 0 {
		t.Errorf("过滤不符: unresolved=%d resolved=%d", len(unresolved), len(resolved))
	}
}

func TestMemResolveTamperAlert(t *testing.T) {
	s := NewMemStorage()
	_ = s.SaveTamperAlerts([]*plugin.TamperAlert{tamperAlert("req-2")})
	got, _, _ := s.ListTamperAlerts(nil, 1, 10)
	if err := s.SetTamperAlertResolved(got[0].ID, true); err != nil {
		t.Fatal(err)
	}
	yes := true
	resolved, total, err := s.ListTamperAlerts(&yes, 1, 10)
	if err != nil || total != 1 || !resolved[0].Resolved {
		t.Fatalf("处置后应可查到 resolved=true: %d,%v", total, err)
	}
}

func TestMemDeleteAuditLogsBefore(t *testing.T) {
	s := NewMemStorage()
	old := time.Now().Add(-48 * time.Hour)
	_ = s.SaveAuditLog(&plugin.AuditLog{ID: "old", RequestID: "old", CreatedAt: old})
	_ = s.SaveAuditLog(&plugin.AuditLog{ID: "new", RequestID: "new", CreatedAt: time.Now()})
	n, err := s.DeleteAuditLogsBefore(time.Now().Add(-24 * time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("应删 1 条: n=%d err=%v", n, err)
	}
	logs, total, err := s.QueryAuditLogs(plugin.AuditLogFilter{RequestID: "old"}, 1, 10)
	if err != nil || total != 0 || len(logs) != 0 {
		t.Errorf("过期日志应已删除: %d,%v", total, err)
	}
	logs2, total2, _ := s.QueryAuditLogs(plugin.AuditLogFilter{RequestID: "new"}, 1, 10)
	if total2 != 1 || len(logs2) != 1 {
		t.Error("未到期日志应保留")
	}
}
