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

package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/license"
	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
	"go.uber.org/zap"
)

// newTestRateLimiter 构造测试限流器
func newTestRateLimiter() plugin.RateLimitPlugin {
	return oss.NewRateLimiter(oss.NewMemStorage(), 100, 100000, "token_bucket")
}

// fetchLicenseData 请求 /api/license 并解包统一响应的 data 字段
func fetchLicenseData(t *testing.T, s *AdminServer) (map[string]interface{}, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/license", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	return resp.Data, rec.Body.String()
}

func TestLicenseAPIOnOSS(t *testing.T) {
	s := NewAdminServer(oss.NewMemStorage(), zap.NewNop(), "oss", newTestRateLimiter(), nil)
	data, _ := fetchLicenseData(t, s)
	if data["status"] != "oss" {
		t.Errorf("status = %v, want oss", data["status"])
	}
	if msg, _ := data["message"].(string); !strings.Contains(msg, "开源") {
		t.Errorf("message = %q, 应包含开源说明", msg)
	}
	if data["signed"] != false {
		t.Errorf("signed = %v, want false", data["signed"])
	}
}

func TestLicenseAPIValid(t *testing.T) {
	ov := &LicenseOverview{
		Status: "valid",
		Info: &plugin.LicenseInfo{
			LicenseKey:   "NG-ENT-20260824-ab12",
			ProductName:  "NeuralGate Enterprise",
			CustomerName: "示例科技有限公司",
			MaxNodes:     3,
			MaxTenants:   50,
			IssuedAt:     time.Now().Add(-24 * time.Hour),
			ExpiresAt:    time.Now().Add(48 * time.Hour),
			Features:     []string{license.FeatureRBAC},
			Signature:    "c2ln",
			IsOffline:    true,
		},
	}
	s := NewAdminServer(oss.NewMemStorage(), zap.NewNop(), "enterprise", newTestRateLimiter(), ov)
	data, raw := fetchLicenseData(t, s)

	if data["status"] != "valid" {
		t.Errorf("status = %v, want valid", data["status"])
	}
	// 脱敏：保留前 8 位 + ****
	if key, _ := data["license_key"].(string); key != "NG-ENT-2****" {
		t.Errorf("license_key = %q, want NG-ENT-2****", key)
	}
	// 签名全文不得回显
	if strings.Contains(raw, "c2lg") || strings.Contains(raw, `"c2ln"`) {
		t.Error("响应不得回显签名字段")
	}
	days, ok := data["days_remaining"].(float64)
	if !ok || days < 1 || days > 3 {
		t.Errorf("days_remaining = %v, 应为 1~3", data["days_remaining"])
	}
	if data["customer_name"] != "示例科技有限公司" {
		t.Errorf("customer_name = %v", data["customer_name"])
	}
	feats, _ := data["features"].([]interface{})
	if len(feats) != 1 {
		t.Errorf("features = %v", data["features"])
	}
}

func TestLicenseAPIExpired(t *testing.T) {
	ov := &LicenseOverview{
		Status:  "expired",
		Message: "授权已过期",
		Info: &plugin.LicenseInfo{
			LicenseKey:   "NG-ENT-20250101-ff99",
			CustomerName: "过期客户",
			ExpiresAt:    time.Now().Add(-72 * time.Hour),
		},
	}
	s := NewAdminServer(oss.NewMemStorage(), zap.NewNop(), "enterprise", newTestRateLimiter(), ov)
	data, _ := fetchLicenseData(t, s)
	if data["status"] != "expired" {
		t.Errorf("status = %v, want expired", data["status"])
	}
	if data["message"] != "授权已过期" {
		t.Errorf("message = %v", data["message"])
	}
	// 过期授权不展示剩余天数，但业务字段尽力展示
	if _, exists := data["days_remaining"]; exists {
		t.Error("过期授权不应有 days_remaining")
	}
	if data["customer_name"] != "过期客户" {
		t.Errorf("过期授权业务字段应尽力展示, got %v", data["customer_name"])
	}
}

// fetchSystemData 请求 /api/system 并解包 data
func fetchSystemData(t *testing.T, s *AdminServer) map[string]interface{} {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/system", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	return resp.Data
}

func TestSystemInfoIncludesLicenseOverview(t *testing.T) {
	ov := &LicenseOverview{
		Status: "valid",
		Info: &plugin.LicenseInfo{
			CustomerName: "示例科技有限公司",
			ExpiresAt:    time.Date(2027, 8, 24, 0, 0, 0, 0, time.UTC),
			Features:     []string{"rbac", "audit_stream"},
		},
	}
	s := NewAdminServer(oss.NewMemStorage(), zap.NewNop(), "enterprise", newTestRateLimiter(), ov)
	data := fetchSystemData(t, s)

	lic, ok := data["license"].(map[string]interface{})
	if !ok {
		t.Fatalf("/api/system 缺少 license 概览: %s", fmt.Sprint(data))
	}
	if lic["status"] != "valid" {
		t.Errorf("license.status = %v", lic["status"])
	}
	if lic["customer"] != "示例科技有限公司" {
		t.Errorf("license.customer = %v", lic["customer"])
	}
	if count, _ := lic["features_count"].(float64); count != 2 {
		t.Errorf("license.features_count = %v", lic["features_count"])
	}
	if data["edition"] != "enterprise" {
		t.Errorf("edition = %v, want enterprise(降级时由调用方传入 oss)", data["edition"])
	}
}

func TestSystemInfoWithoutLicense(t *testing.T) {
	s := NewAdminServer(oss.NewMemStorage(), zap.NewNop(), "oss", newTestRateLimiter(), nil)
	data := fetchSystemData(t, s)
	lic, ok := data["license"].(map[string]interface{})
	if !ok {
		t.Fatal("/api/system 缺少 license 概览")
	}
	if lic["status"] != "oss" {
		t.Errorf("license.status = %v, want oss", lic["status"])
	}
}
