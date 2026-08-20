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
	if err := s.UpdateAPIKeyQuota("k1", 500); err != nil {
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
