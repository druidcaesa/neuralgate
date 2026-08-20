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
	_ "modernc.org/sqlite"
)

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
