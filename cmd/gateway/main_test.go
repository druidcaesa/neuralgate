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

package main

import (
	"testing"

	"github.com/druidcaesa/neuralgate/pkg/core"
	"github.com/druidcaesa/neuralgate/pkg/license"
)

// featureGate 测试用固定清单门控
type featureGate map[string]bool

func (f featureGate) HasFeature(feature string) bool { return f[feature] }

func TestShouldStartExport(t *testing.T) {
	if ok, reason := shouldStartExport(core.NopGate(), false); ok || reason == "" {
		t.Errorf("未启用应不启动且给出原因: ok=%v reason=%q", ok, reason)
	}
	ok, reason := shouldStartExport(core.NopGate(), true)
	if ok || reason != "授权未包含 audit_stream 功能" {
		t.Errorf("NopGate 应因缺 feature 不启动: ok=%v reason=%q", ok, reason)
	}
	licensed := featureGate{license.FeatureAuditStream: true}
	if ok, _ := shouldStartExport(licensed, true); !ok {
		t.Error("启用且授权含 audit_stream 应启动")
	}
	other := featureGate{license.FeatureRBAC: true}
	if ok, _ := shouldStartExport(other, true); ok {
		t.Error("仅含其他功能不应启动")
	}
}

func TestShouldStartTamper(t *testing.T) {
	if ok, reason := shouldStartTamper(core.NopGate(), false); ok || reason == "" {
		t.Errorf("未启用应不启动且给出原因: ok=%v reason=%q", ok, reason)
	}
	ok, reason := shouldStartTamper(core.NopGate(), true)
	if ok || reason != "授权未包含 tamper_proof 功能" {
		t.Errorf("NopGate 应因缺 feature 不启动: ok=%v reason=%q", ok, reason)
	}
	licensed := featureGate{license.FeatureTamperProof: true}
	if ok, _ := shouldStartTamper(licensed, true); !ok {
		t.Error("启用且授权含 tamper_proof 应启动")
	}
}

func TestShouldStartPrivacy(t *testing.T) {
	if ok, reason := shouldStartPrivacy(core.NopGate(), false); ok || reason == "" {
		t.Errorf("未启用应不启动且给出原因: ok=%v reason=%q", ok, reason)
	}
	ok, reason := shouldStartPrivacy(core.NopGate(), true)
	if ok || reason != "授权未包含 privacy 功能" {
		t.Errorf("NopGate 应因缺 feature 不启动: ok=%v reason=%q", ok, reason)
	}
	licensed := featureGate{license.FeaturePrivacy: true}
	if ok, _ := shouldStartPrivacy(licensed, true); !ok {
		t.Error("启用且授权含 privacy 应启动")
	}
	other := featureGate{license.FeatureRBAC: true}
	if ok, _ := shouldStartPrivacy(other, true); ok {
		t.Error("仅含其他功能不应启动")
	}
}

func TestShouldStartRBAC(t *testing.T) {
	if ok, reason := shouldStartRBAC(core.NopGate(), false); ok || reason == "" {
		t.Errorf("未启用应不启动且给出原因: ok=%v reason=%q", ok, reason)
	}
	ok, reason := shouldStartRBAC(core.NopGate(), true)
	if ok || reason != "授权未包含 rbac 功能" {
		t.Errorf("NopGate 应因缺 feature 不启动: ok=%v reason=%q", ok, reason)
	}
	licensed := featureGate{license.FeatureRBAC: true}
	if ok, _ := shouldStartRBAC(licensed, true); !ok {
		t.Error("启用且授权含 rbac 应启动")
	}
	other := featureGate{license.FeaturePrivacy: true}
	if ok, _ := shouldStartRBAC(other, true); ok {
		t.Error("仅含其他功能不应启动")
	}
}

func TestShouldStartCompliance(t *testing.T) {
	if ok, reason := shouldStartCompliance(core.NopGate(), false); ok || reason == "" {
		t.Errorf("未启用应不启动且给出原因: ok=%v reason=%q", ok, reason)
	}
	ok, reason := shouldStartCompliance(core.NopGate(), true)
	if ok || reason != "授权未包含 compliance 功能" {
		t.Errorf("NopGate 应因缺 feature 不启动: ok=%v reason=%q", ok, reason)
	}
	licensed := featureGate{license.FeatureCompliance: true}
	if ok, _ := shouldStartCompliance(licensed, true); !ok {
		t.Error("启用且授权含 compliance 应启动")
	}
	other := featureGate{license.FeaturePrivacy: true}
	if ok, _ := shouldStartCompliance(other, true); ok {
		t.Error("仅含其他功能不应启动")
	}
}

func TestShouldStartMCPAudit(t *testing.T) {
	if ok, reason := shouldStartMCPAudit(core.NopGate(), false); ok || reason == "" {
		t.Errorf("未启用应不启动且给出原因: ok=%v reason=%q", ok, reason)
	}
	ok, reason := shouldStartMCPAudit(core.NopGate(), true)
	if ok || reason != "授权未包含 mcp_audit 功能" {
		t.Errorf("NopGate 应因缺 feature 不启动: ok=%v reason=%q", ok, reason)
	}
	licensed := featureGate{license.FeatureMCPAudit: true}
	if ok, _ := shouldStartMCPAudit(licensed, true); !ok {
		t.Error("启用且授权含 mcp_audit 应启动")
	}
	other := featureGate{license.FeaturePrivacy: true}
	if ok, _ := shouldStartMCPAudit(other, true); ok {
		t.Error("仅含其他功能不应启动")
	}
}

func TestShouldStartDistributedRateLimit(t *testing.T) {
	if ok, reason := shouldStartDistributedRateLimit(core.NopGate(), false); ok || reason == "" {
		t.Errorf("未启用应不启动且给出原因: ok=%v reason=%q", ok, reason)
	}
	ok, reason := shouldStartDistributedRateLimit(core.NopGate(), true)
	if ok || reason != "授权未包含 distributed_ratelimit 功能" {
		t.Errorf("NopGate 应因缺 feature 不启动: ok=%v reason=%q", ok, reason)
	}
	licensed := featureGate{license.FeatureDistributedRateLimit: true}
	if ok, _ := shouldStartDistributedRateLimit(licensed, true); !ok {
		t.Error("启用且授权含 distributed_ratelimit 应启动")
	}
	other := featureGate{license.FeaturePrivacy: true}
	if ok, _ := shouldStartDistributedRateLimit(other, true); ok {
		t.Error("仅含其他功能不应启动")
	}
}
