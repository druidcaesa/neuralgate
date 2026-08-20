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
	"database/sql"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	_ "github.com/go-sql-driver/mysql"
	_ "modernc.org/sqlite"
)

// TestSQLiteCreateTables SQLite 三表建表后均应存在
func TestSQLiteCreateTables(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := sqliteCreateTables(db); err != nil {
		t.Fatalf("sqliteCreateTables: %v", err)
	}
	for _, table := range []string{"api_keys", "model_configs", "audit_logs"} {
		var name string
		if err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name); err != nil {
			t.Fatalf("table %s missing: %v", table, err)
		}
	}
}

// TestMySQLCreateTables_SkipWithoutDB MySQL 需要真实环境;本地无 MySQL 时跳过
func TestMySQLCreateTables_SkipWithoutDB(t *testing.T) {
	db, err := sql.Open("mysql", "root:pass@tcp(127.0.0.1:3306)/neuralgate?charset=utf8mb4")
	if err != nil {
		t.Skipf("mysql driver unavailable: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Skipf("mysql not reachable: %v", err)
	}
	defer db.Close()
	if err := mysqlCreateTables(db); err != nil {
		t.Fatalf("mysqlCreateTables: %v", err)
	}
}

// newTestSQLStorage 创建基于内存 SQLite 的 SQLStorage(建表)
func newTestSQLStorage(t *testing.T) *SQLStorage {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := sqliteCreateTables(db); err != nil {
		t.Fatal(err)
	}
	return &SQLStorage{db: db, encryptKey: "test-key"}
}

func TestSQLStorageAPIKeyCRUD(t *testing.T) {
	s := newTestSQLStorage(t)
	now := time.Now()
	key := &plugin.APIKey{
		ID: "k1", KeyHash: "hash-1", KeyPrefix: "ng-abcdef12",
		TenantID: "t1", Name: "测试Key", Status: plugin.APIKeyStatusActive,
		Quota: -1, RateLimit: 10, AllowedModels: []string{"gpt-4"},
		ExpiresAt: nil, CreatedAt: now, UpdatedAt: now, CreatedBy: "admin",
	}
	if err := s.SaveAPIKey(key); err != nil {
		t.Fatalf("SaveAPIKey: %v", err)
	}

	// 按哈希查
	got, err := s.GetAPIKey("hash-1")
	if err != nil || got.Name != "测试Key" || got.TenantID != "t1" {
		t.Fatalf("GetAPIKey = %v, %v", got, err)
	}
	if len(got.AllowedModels) != 1 || got.AllowedModels[0] != "gpt-4" {
		t.Fatalf("AllowedModels mismatch: %v", got.AllowedModels)
	}

	// 按 ID 查
	byID, err := s.GetAPIKeyByID("k1")
	if err != nil || byID.KeyHash != "hash-1" {
		t.Fatalf("GetAPIKeyByID = %v, %v", byID, err)
	}

	// 更新额度
	if err := s.UpdateAPIKeyQuota("k1", 100); err != nil {
		t.Fatalf("UpdateAPIKeyQuota: %v", err)
	}
	got, _ = s.GetAPIKey("hash-1")
	if got.UsedQuota != 100 {
		t.Fatalf("UsedQuota = %d; want 100", got.UsedQuota)
	}

	// 列表(含租户过滤)
	if _, total, err := s.ListAPIKeys("t1", 1, 10); err != nil || total != 1 {
		t.Fatalf("ListAPIKeys = total %d, err %v", total, err)
	}

	// 软删除后查不到
	if err := s.DeleteAPIKey("k1"); err != nil {
		t.Fatalf("DeleteAPIKey: %v", err)
	}
	if _, err := s.GetAPIKey("hash-1"); err != ErrNotFound {
		t.Fatalf("GetAPIKey after delete err = %v; want ErrNotFound", err)
	}
	if _, total, _ := s.ListAPIKeys("", 1, 10); total != 0 {
		t.Fatalf("ListAPIKeys after delete total = %d; want 0", total)
	}
}

func TestSQLStorageIncrementAPIKeyUsage(t *testing.T) {
	s := newTestSQLStorage(t)
	now := time.Now()
	key := &plugin.APIKey{
		ID: "k1", KeyHash: "hash-1", KeyPrefix: "ng-abcdef12",
		TenantID: "t1", Name: "测试Key", Status: plugin.APIKeyStatusActive,
		Quota: -1, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.SaveAPIKey(key); err != nil {
		t.Fatalf("SaveAPIKey: %v", err)
	}
	if err := s.IncrementAPIKeyUsage("k1", 100); err != nil {
		t.Fatalf("IncrementAPIKeyUsage(k1,100): %v", err)
	}
	if err := s.IncrementAPIKeyUsage("k1", 50); err != nil {
		t.Fatalf("IncrementAPIKeyUsage(k1,50): %v", err)
	}
	got, err := s.GetAPIKeyByID("k1")
	if err != nil || got.UsedQuota != 150 {
		t.Fatalf("UsedQuota = %d, %v; want 150", got.UsedQuota, err)
	}
	// 不存在 key → ErrNotFound
	if err := s.IncrementAPIKeyUsage("nope", 1); err != ErrNotFound {
		t.Fatalf("IncrementAPIKeyUsage(nope) err = %v; want ErrNotFound", err)
	}
	// 软删除后同样查不到
	if err := s.DeleteAPIKey("k1"); err != nil {
		t.Fatal(err)
	}
	if err := s.IncrementAPIKeyUsage("k1", 1); err != ErrNotFound {
		t.Fatalf("IncrementAPIKeyUsage(deleted) err = %v; want ErrNotFound", err)
	}
}

func TestSQLStorageModelConfigCRUD(t *testing.T) {
	s := newTestSQLStorage(t)
	now := time.Now()
	cfg := &plugin.ModelConfig{
		ID: "m1", ModelName: "gpt-4", Provider: "openai", ProviderModel: "gpt-4o",
		BaseURL: "https://api.openai.com", APIKey: "sk-upstream-secret",
		Timeout: 60, MaxRetries: 2, RetryInterval: 3, Weight: 1, Enabled: true,
		Tags: map[string]string{"env": "prod"}, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.SaveModelConfig(cfg); err != nil {
		t.Fatalf("SaveModelConfig: %v", err)
	}

	// 读回后 api_key 已解密还原
	got, err := s.GetModelConfig("gpt-4")
	if err != nil || got.APIKey != "sk-upstream-secret" {
		t.Fatalf("GetModelConfig = %v, %v", got, err)
	}
	if got.Tags["env"] != "prod" {
		t.Fatalf("Tags mismatch: %v", got.Tags)
	}
	// 按 ID 查
	byID, err := s.GetModelConfigByID("m1")
	if err != nil || byID.ModelName != "gpt-4" {
		t.Fatalf("GetModelConfigByID = %v, %v", byID, err)
	}
	// 列表
	if _, total, err := s.ListModelConfigs(1, 10); err != nil || total != 1 {
		t.Fatalf("ListModelConfigs total = %d, err %v", total, err)
	}
	// 删除
	if err := s.DeleteModelConfig("m1"); err != nil {
		t.Fatalf("DeleteModelConfig: %v", err)
	}
	if _, err := s.GetModelConfig("gpt-4"); err != ErrNotFound {
		t.Fatalf("GetModelConfig after delete err = %v; want ErrNotFound", err)
	}
}

func TestSQLStorageAuditLogCRUD(t *testing.T) {
	s := newTestSQLStorage(t)
	now := time.Now()
	log := &plugin.AuditLog{
		ID: "a1", RequestID: "r1", TenantID: "t1", APIKeyID: "k1",
		ModelName: "gpt-4", Provider: "openai", RequestMethod: "POST",
		RequestPath: "/v1/chat/completions", RequestHeaders: map[string]string{"Content-Type": "application/json"},
		RequestBody: `{"model":"gpt-4"}`, ResponseStatus: 200, ResponseBody: `{"choices":[]}`,
		SSEChunks:    []plugin.SSEChunk{{Index: 0, Data: `{"choices":[]}`, Timestamp: now}},
		PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, Duration: 120,
		ClientIP: "127.0.0.1", IsStream: true, CreatedAt: now,
	}
	if err := s.SaveAuditLog(log); err != nil {
		t.Fatalf("SaveAuditLog: %v", err)
	}
	// 批量
	if err := s.BatchSaveAuditLogs([]*plugin.AuditLog{
		{ID: "a2", RequestID: "r2", ModelName: "qwen", CreatedAt: now},
	}); err != nil {
		t.Fatalf("BatchSaveAuditLogs: %v", err)
	}

	// 按 RequestID 精查
	logs, total, err := s.QueryAuditLogs(plugin.AuditLogFilter{RequestID: "r1"}, 1, 10)
	if err != nil || total != 1 {
		t.Fatalf("Query by requestID = %d, %v", total, err)
	}
	if logs[0].SSEChunks[0].Data != `{"choices":[]}` {
		t.Fatalf("SSEChunks roundtrip mismatch: %+v", logs[0].SSEChunks)
	}
	if logs[0].RequestHeaders["Content-Type"] != "application/json" {
		t.Fatalf("headers roundtrip mismatch: %v", logs[0].RequestHeaders)
	}

	// 组合过滤: 租户 + 模型 + 状态 + 流式 + 关键词
	f := plugin.AuditLogFilter{TenantID: "t1", ModelName: "gpt-4", Status: 200, IsStream: boolPtr(true), Keyword: "choices"}
	if _, total, err := s.QueryAuditLogs(f, 1, 10); err != nil || total != 1 {
		t.Fatalf("Query combined = %d, %v", total, err)
	}
	// 时间过滤
	start := now.Add(-time.Hour)
	end := now.Add(time.Hour)
	f2 := plugin.AuditLogFilter{StartTime: &start, EndTime: &end}
	if _, total, err := s.QueryAuditLogs(f2, 1, 10); err != nil || total != 2 {
		t.Fatalf("Query time range = %d, %v", total, err)
	}
}

func boolPtr(b bool) *bool { return &b }

// TestSQLStorageModelConfigDecryptFail 密钥不匹配时读取应返回错误,而非返回密文
func TestSQLStorageModelConfigDecryptFail(t *testing.T) {
	// 保存时用 key A
	s := newTestSQLStorage(t)
	now := time.Now()
	_ = s.SaveModelConfig(&plugin.ModelConfig{
		ID: "m1", ModelName: "gpt-4", Provider: "openai", ProviderModel: "gpt-4o",
		BaseURL: "https://x", APIKey: "sk-secret", Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	})
	// 读取时用 key B(密钥不匹配 → 解密失败 → 返回错误)
	other := &SQLStorage{db: s.db, encryptKey: "other-key"}
	if _, err := other.GetModelConfig("gpt-4"); err == nil {
		t.Fatal("decrypt with wrong key must return error")
	}
}

func TestSQLStoragePingClose(t *testing.T) {
	s := newTestSQLStorage(t)
	if err := s.Ping(); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestSQLStorageAPIKeyReSaveAfterDelete 软删除后同 ID 重新保存应恢复(deleted 列随 UPSERT 重置为 0)
func TestSQLStorageAPIKeyReSaveAfterDelete(t *testing.T) {
	s := newTestSQLStorage(t)
	now := time.Now()
	key := &plugin.APIKey{
		ID: "k1", KeyHash: "hash-1", KeyPrefix: "ng-abcdef12",
		TenantID: "t1", Name: "恢复测试", Status: plugin.APIKeyStatusActive,
		Quota: -1, RateLimit: 10, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.SaveAPIKey(key); err != nil {
		t.Fatalf("SaveAPIKey: %v", err)
	}
	if err := s.DeleteAPIKey("k1"); err != nil {
		t.Fatalf("DeleteAPIKey: %v", err)
	}
	if _, err := s.GetAPIKey("hash-1"); err != ErrNotFound {
		t.Fatalf("GetAPIKey after delete err = %v; want ErrNotFound", err)
	}
	// 同 ID 重新保存(UPSERT),deleted 应恢复为 0
	if err := s.SaveAPIKey(key); err != nil {
		t.Fatalf("SaveAPIKey after delete: %v", err)
	}
	got, err := s.GetAPIKey("hash-1")
	if err != nil {
		t.Fatalf("GetAPIKey after re-save = %v, %v; key not restored", got, err)
	}
	if got.Name != "恢复测试" {
		t.Fatalf("re-saved key name = %q; want 恢复测试", got.Name)
	}
}
