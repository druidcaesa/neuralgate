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
	"os"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
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

// TestMySQLCreateTables_SkipWithoutDB MySQL 需要真实环境;无 MySQL 时跳过。
// dsn 可用环境变量 NG_MYSQL_DSN 覆盖(如 NG_MYSQL_DSN="root:root@tcp(127.0.0.1:3306)/neuralgate?charset=utf8mb4")
func TestMySQLCreateTables_SkipWithoutDB(t *testing.T) {
	dsn := os.Getenv("NG_MYSQL_DSN")
	if dsn == "" {
		dsn = "root:root@tcp(127.0.0.1:3306)/neuralgate?charset=utf8mb4"
	}
	db, err := sql.Open("mysql", dsn)
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

	// 原子累加额度(生产路径)
	if err := s.IncrementAPIKeyUsage("k1", 100); err != nil {
		t.Fatalf("IncrementAPIKeyUsage: %v", err)
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

// TestSQLStorageQueryAuditLogsTimeBoundary 时间过滤端点契约：StartTime 与 EndTime 均为
// 闭区间（两端点皆命中、端点外不命中）；半开周期调用方须自行换算右端点
// （参见 enterprise.GenerateComplianceReport 的 end-1ms 换算）
func TestSQLStorageQueryAuditLogsTimeBoundary(t *testing.T) {
	s := newTestSQLStorage(t)
	base := time.Date(2026, 8, 25, 12, 0, 0, 0, time.Local) // 整秒，避开毫秒截断歧义
	stamps := []struct {
		id string
		at time.Time
	}{
		{"before", base.Add(-time.Hour)},       // StartTime 之前：不命中
		{"at-start", base},                     // 恰在 StartTime：闭区间必须命中
		{"interior", base.Add(time.Hour)},      // 区间内部：命中
		{"at-end", base.Add(2 * time.Hour)},    // 恰在 EndTime：闭区间必须命中
		{"after-end", base.Add(3 * time.Hour)}, // EndTime 之后：不命中
	}
	for _, st := range stamps {
		if err := s.SaveAuditLog(&plugin.AuditLog{
			ID: st.id, RequestID: st.id, ModelName: "gpt-boundary", CreatedAt: st.at,
		}); err != nil {
			t.Fatalf("SaveAuditLog %s: %v", st.id, err)
		}
	}
	end := base.Add(2 * time.Hour)
	logs, total, err := s.QueryAuditLogs(plugin.AuditLogFilter{
		ModelName: "gpt-boundary", StartTime: &base, EndTime: &end}, 1, 10)
	if err != nil {
		t.Fatalf("QueryAuditLogs: %v", err)
	}
	if total != 3 {
		t.Fatalf("total = %d, want 3(at-start+interior+at-end)", total)
	}
	got := map[string]bool{}
	for _, l := range logs {
		got[l.ID] = true
	}
	for _, id := range []string{"at-start", "interior", "at-end"} {
		if !got[id] {
			t.Errorf("%s 未命中: 端点应含于闭区间", id)
		}
	}
	if got["before"] || got["after-end"] {
		t.Errorf("区间外日志不应命中: %v", got)
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

func TestSQLStorageRateLimitConfigCRUD(t *testing.T) {
	s := newTestSQLStorage(t)
	now := time.Now()
	cfg := &plugin.RateLimitConfig{
		ID: "rl1", TenantID: "t1", ModelName: "gpt-4",
		RequestsPerSec: 20, TokensPerMin: 50000, Strategy: "token_bucket",
		Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.SaveRateLimitConfig(cfg); err != nil {
		t.Fatalf("SaveRateLimitConfig: %v", err)
	}
	got, err := s.GetRateLimitConfig("t1", "gpt-4")
	if err != nil || got.RequestsPerSec != 20 || got.Strategy != "token_bucket" {
		t.Fatalf("GetRateLimitConfig = %v, %v", got, err)
	}
	// 不存在返回 ErrNotFound
	if _, err := s.GetRateLimitConfig("t1", "nope"); err != ErrNotFound {
		t.Fatalf("GetRateLimitConfig(nope) err = %v; want ErrNotFound", err)
	}
	if _, total, err := s.ListRateLimitConfigs(nil, 1, 10); err != nil || total != 1 {
		t.Fatalf("ListRateLimitConfigs total = %d, err %v", total, err)
	}
	if err := s.DeleteRateLimitConfig("rl1"); err != nil {
		t.Fatalf("DeleteRateLimitConfig: %v", err)
	}
	if _, err := s.GetRateLimitConfig("t1", "gpt-4"); err != ErrNotFound {
		t.Fatalf("after delete err = %v; want ErrNotFound", err)
	}
}

func TestSQLStorageUpstreamCRUD(t *testing.T) {
	s := newTestSQLStorage(t)
	now := time.Now()
	up := &plugin.Upstream{
		ID: "u1", ModelConfigID: "m1", BaseURL: "https://up1",
		APIKey: "sk-up-secret", Weight: 3, Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.SaveUpstream(up); err != nil {
		t.Fatalf("SaveUpstream: %v", err)
	}
	// api_key 加密后读回还原
	got, err := s.GetUpstreamByID("u1")
	if err != nil || got.APIKey != "sk-up-secret" || got.Weight != 3 {
		t.Fatalf("GetUpstreamByID = %v, %v", got, err)
	}
	ups, err := s.ListUpstreams("m1")
	if err != nil || len(ups) != 1 || ups[0].BaseURL != "https://up1" {
		t.Fatalf("ListUpstreams = %v, %v", ups, err)
	}
	// 另一模型的上游不返回
	if ups2, _ := s.ListUpstreams("m2"); len(ups2) != 0 {
		t.Fatalf("ListUpstreams(m2) len = %d; want 0", len(ups2))
	}
	if err := s.DeleteUpstream("u1"); err != nil {
		t.Fatalf("DeleteUpstream: %v", err)
	}
	if _, err := s.GetUpstreamByID("u1"); err != ErrNotFound {
		t.Fatalf("after delete err = %v; want ErrNotFound", err)
	}
}
