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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/druidcaesa/neuralgate/pkg/license"
	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func newFeatRateLimiter() plugin.RateLimitPlugin {
	return oss.NewRateLimiter(oss.NewMemStorage(), 100, 100000, "token_bucket")
}

// TestHasFeatureThreeStates 授权有效放行声明功能；OSS 与过期一律空集
func TestHasFeatureThreeStates(t *testing.T) {
	// OSS：license 为 nil → 无任何 feature
	oss1 := NewAdminServer(oss.NewMemStorage(), zap.NewNop(), "oss", newFeatRateLimiter(), nil)
	if oss1.hasFeature(license.FeatureRBAC) {
		t.Error("OSS 不应放行任何 feature")
	}

	// enterprise + valid：仅放行声明的 feature
	ovValid := &LicenseOverview{Status: "valid", Info: &plugin.LicenseInfo{Features: []string{license.FeatureRBAC}}}
	ent := NewAdminServer(oss.NewMemStorage(), zap.NewNop(), "enterprise", newFeatRateLimiter(), ovValid)
	if !ent.hasFeature(license.FeatureRBAC) {
		t.Error("enterprise+valid 应放行已声明的 rbac")
	}
	if ent.hasFeature(license.FeatureCompliance) {
		t.Error("未声明的 compliance 不应放行")
	}
	if len(ent.featureList()) != 1 {
		t.Errorf("featureList 应含 1 项, got %v", ent.featureList())
	}

	// enterprise + expired：即便 Info 带 features 也全部关闭
	ovExpired := &LicenseOverview{Status: "expired", Info: &plugin.LicenseInfo{Features: []string{license.FeatureRBAC}}}
	exp := NewAdminServer(oss.NewMemStorage(), zap.NewNop(), "enterprise", newFeatRateLimiter(), ovExpired)
	if exp.hasFeature(license.FeatureRBAC) {
		t.Error("过期授权不应放行 feature")
	}
	if len(exp.featureList()) != 0 {
		t.Error("过期授权 featureList 应为空")
	}
}

// TestRequireFeatureGate 授权含该功能则放行；缺失则 403 + CodeFeatureLocked
func TestRequireFeatureGate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ov := &LicenseOverview{Status: "valid", Info: &plugin.LicenseInfo{Features: []string{license.FeatureRBAC}}}
	s := NewAdminServer(oss.NewMemStorage(), zap.NewNop(), "enterprise", newFeatRateLimiter(), ov)

	// 放行：授权含 rbac
	pass := gin.New()
	pass.GET("/t", s.RequireFeature(license.FeatureRBAC), func(c *gin.Context) { OK(c, gin.H{}) })
	recPass := httptest.NewRecorder()
	pass.ServeHTTP(recPass, httptest.NewRequest(http.MethodGet, "/t", nil))
	if recPass.Code != http.StatusOK {
		t.Fatalf("授权含 rbac 应放行, got %d %s", recPass.Code, recPass.Body.String())
	}

	// 拦截：授权不含 compliance
	block := gin.New()
	block.GET("/t", s.RequireFeature(license.FeatureCompliance), func(c *gin.Context) { OK(c, gin.H{}) })
	recBlock := httptest.NewRecorder()
	block.ServeHTTP(recBlock, httptest.NewRequest(http.MethodGet, "/t", nil))
	if recBlock.Code != http.StatusForbidden || !strings.Contains(recBlock.Body.String(), "企业版授权") {
		t.Fatalf("缺 compliance 应 403 企业版, got %d %s", recBlock.Code, recBlock.Body.String())
	}
	var resp Response
	if err := json.Unmarshal(recBlock.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Code != CodeFeatureLocked {
		t.Errorf("业务码应为 %d, got %d", CodeFeatureLocked, resp.Code)
	}
}
