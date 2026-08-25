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

package core

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
)

func newTenantGateFixture(t *testing.T) (*TenantGate, *oss.MemStorage) {
	t.Helper()
	storage := oss.NewMemStorage()
	return NewTenantGate(storage, 20*time.Millisecond), storage
}

// TestTenantGateEmptyTenantsAllow 未使用租户体系(空表)时恒放行(OSS 零变化)
func TestTenantGateEmptyTenantsAllow(t *testing.T) {
	gate, _ := newTenantGateFixture(t)
	if !gate.Allowed("any-tenant") || !gate.Allowed("") {
		t.Error("空表应恒放行")
	}
}

// TestTenantGateDisableEnable 禁用租户 ≤TTL 拒绝；恢复 ≤TTL 放行
func TestTenantGateDisableEnable(t *testing.T) {
	gate, storage := newTenantGateFixture(t)
	tenant := &plugin.Tenant{Name: "A", Code: "ta", Status: plugin.TenantStatusActive}
	if err := storage.SaveTenant(tenant); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond) // 越过 TTL 建立缓存
	if !gate.Allowed(tenant.ID) {
		t.Fatal("启用租户应放行")
	}

	tenant.Status = plugin.TenantStatusDisabled
	_ = storage.SaveTenant(tenant)
	time.Sleep(30 * time.Millisecond)
	if gate.Allowed(tenant.ID) {
		t.Error("禁用后 ≤TTL 内应拒绝")
	}

	tenant.Status = plugin.TenantStatusActive
	_ = storage.SaveTenant(tenant)
	time.Sleep(30 * time.Millisecond)
	if !gate.Allowed(tenant.ID) {
		t.Error("恢复后 ≤TTL 内应放行")
	}
}

// TestTenantGateConcurrent 并发读安全（配合 -race）
func TestTenantGateConcurrent(t *testing.T) {
	gate, storage := newTenantGateFixture(t)
	_ = storage.SaveTenant(&plugin.Tenant{Name: "B", Code: "tb", Status: plugin.TenantStatusDisabled})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = gate.Allowed("t1")
				_ = gate.Allowed("")
			}
		}()
	}
	wg.Wait()
}

// TestAuthMiddlewareTenantDisabled 数据面联动：禁用租户的 Key 请求返回 401
func TestAuthMiddlewareTenantDisabled(t *testing.T) {
	storage := oss.NewMemStorage()
	tenant := &plugin.Tenant{Name: "停用", Code: "off", Status: plugin.TenantStatusDisabled}
	if err := storage.SaveTenant(tenant); err != nil {
		t.Fatal(err)
	}
	rawKey := "ng-test-key-0001"
	sum := sha256.Sum256([]byte(rawKey))
	if err := storage.SaveAPIKey(&plugin.APIKey{
		ID: "k1", KeyHash: hex.EncodeToString(sum[:]), TenantID: tenant.ID,
		Status: plugin.APIKeyStatusActive, Quota: -1,
	}); err != nil {
		t.Fatal(err)
	}

	handler := AuthMiddleware(storage)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer "+rawKey)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("禁用租户的 Key 应 401, got %d %s", rec.Code, rec.Body.String())
	}

	// 无租户体系的存储：Key 正常放行（现状零变化）
	plainStorage := oss.NewMemStorage()
	sum2 := sha256.Sum256([]byte(rawKey))
	if err := plainStorage.SaveAPIKey(&plugin.APIKey{
		ID: "k2", KeyHash: hex.EncodeToString(sum2[:]), Status: plugin.APIKeyStatusActive, Quota: -1,
	}); err != nil {
		t.Fatal(err)
	}
	okHandler := AuthMiddleware(plainStorage)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req2 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req2.Header.Set("Authorization", "Bearer "+rawKey)
	rec2 := httptest.NewRecorder()
	okHandler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Errorf("无租户体系应放行, got %d", rec2.Code)
	}
}
