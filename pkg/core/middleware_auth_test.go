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
	"strings"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
)

func hashKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func newTestStorage() *oss.MemStorage {
	s := oss.NewMemStorage()
	now := time.Now()
	_ = s.SaveAPIKey(&plugin.APIKey{
		ID: "k1", KeyHash: hashKey("ng-goodkey"), KeyPrefix: "ng-goodkey",
		Name: "test", Status: plugin.APIKeyStatusActive, Quota: -1,
		CreatedAt: now, UpdatedAt: now,
	})
	_ = s.SaveAPIKey(&plugin.APIKey{
		ID: "k2", KeyHash: hashKey("ng-disabled"), KeyPrefix: "ng-disabled",
		Name: "disabled", Status: plugin.APIKeyStatusDisabled,
		CreatedAt: now, UpdatedAt: now,
	})
	exp := now.Add(-time.Hour)
	_ = s.SaveAPIKey(&plugin.APIKey{
		ID: "k3", KeyHash: hashKey("ng-expired"), KeyPrefix: "ng-expired",
		Name: "expired", Status: plugin.APIKeyStatusActive, ExpiresAt: &exp,
		CreatedAt: now, UpdatedAt: now,
	})
	_ = s.SaveAPIKey(&plugin.APIKey{
		ID: "k4", KeyHash: hashKey("ng-quota"), KeyPrefix: "ng-quota",
		Name: "quota", Status: plugin.APIKeyStatusActive, Quota: 10, UsedQuota: 10,
		CreatedAt: now, UpdatedAt: now,
	})
	_ = s.SaveAPIKey(&plugin.APIKey{
		ID: "k5", KeyHash: hashKey("ng-statusexpired"), KeyPrefix: "ng-statusexpired",
		Name: "status-expired", Status: plugin.APIKeyStatusExpired, // 状态枚举直接置 expired(与 k3 的 ExpiresAt 检查互补)
		CreatedAt: now, UpdatedAt: now,
	})
	return s
}

func doAuthRequest(storage plugin.StoragePlugin, bearer string) *httptest.ResponseRecorder {
	mw := AuthMiddleware(storage)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc, ok := RequestContextFrom(r.Context())
		if !ok {
			http.Error(w, "no context", 500)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(rc.APIKeyID + "|" + rc.TenantID))
	}))
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestAuthValidKey(t *testing.T) {
	rec := doAuthRequest(newTestStorage(), "ng-goodkey")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.HasPrefix(rec.Body.String(), "k1") {
		t.Fatalf("body = %s; want prefix k1", rec.Body.String())
	}
}

func TestAuthMissingKey(t *testing.T) {
	rec := doAuthRequest(newTestStorage(), "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invalid_api_key") {
		t.Fatalf("body = %s; want invalid_api_key", rec.Body.String())
	}
}

func TestAuthInvalidKey(t *testing.T) {
	rec := doAuthRequest(newTestStorage(), "ng-unknown")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401", rec.Code)
	}
}

func TestAuthDisabledKey(t *testing.T) {
	rec := doAuthRequest(newTestStorage(), "ng-disabled")
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "api_key_disabled") {
		t.Fatalf("status=%d body=%s; want 401 api_key_disabled", rec.Code, rec.Body.String())
	}
}

func TestAuthExpiredKey(t *testing.T) {
	rec := doAuthRequest(newTestStorage(), "ng-expired")
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "api_key_expired") {
		t.Fatalf("status=%d body=%s; want 401 api_key_expired", rec.Code, rec.Body.String())
	}
}

func TestAuthStatusExpiredKey(t *testing.T) {
	// Status=expired 状态枚举校验(与 TestAuthExpiredKey 的 ExpiresAt 时间检查互补)
	rec := doAuthRequest(newTestStorage(), "ng-statusexpired")
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "api_key_expired") {
		t.Fatalf("status=%d body=%s; want 401 api_key_expired", rec.Code, rec.Body.String())
	}
}

func TestAuthQuotaExceeded(t *testing.T) {
	rec := doAuthRequest(newTestStorage(), "ng-quota")
	if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), "quota_exceeded") {
		t.Fatalf("status=%d body=%s; want 429 quota_exceeded", rec.Code, rec.Body.String())
	}
}
