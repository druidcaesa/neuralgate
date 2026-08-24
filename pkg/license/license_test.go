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

package license

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// baseInfo 构造一份用于测试的基础授权信息
func baseInfo() plugin.LicenseInfo {
	return plugin.LicenseInfo{
		LicenseKey:   "NG-ENT-20260824-ab12",
		ProductName:  "NeuralGate Enterprise",
		CustomerName: "示例科技有限公司",
		MaxNodes:     3,
		MaxTenants:   50,
		IssuedAt:     time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC),
		ExpiresAt:    time.Date(2027, 8, 24, 0, 0, 0, 0, time.UTC),
		Features:     []string{FeatureAuditStream, FeatureRBAC},
		IsOffline:    true,
	}
}

func TestCanonicalPayloadDeterministic(t *testing.T) {
	info := baseInfo()
	a := CanonicalPayload(&info)
	b := CanonicalPayload(&info)
	if !bytes.Equal(a, b) {
		t.Fatal("同一授权信息两次序列化结果不一致")
	}
}

func TestCanonicalPayloadFieldSensitive(t *testing.T) {
	base := baseInfo()
	cases := map[string]func(*plugin.LicenseInfo){
		"customer_name": func(i *plugin.LicenseInfo) { i.CustomerName = "另一家客户" },
		"max_nodes":     func(i *plugin.LicenseInfo) { i.MaxNodes = 99 },
		"feature_order": func(i *plugin.LicenseInfo) { i.Features = []string{FeatureRBAC, FeatureAuditStream} },
		"is_offline":    func(i *plugin.LicenseInfo) { i.IsOffline = false },
	}
	for name, mutate := range cases {
		mutated := baseInfo()
		mutate(&mutated)
		if bytes.Equal(CanonicalPayload(&base), CanonicalPayload(&mutated)) {
			t.Errorf("%s 变化未影响签名载荷", name)
		}
	}
}

func TestCanonicalPayloadTimeNormalized(t *testing.T) {
	utc := baseInfo()
	east8 := baseInfo()
	// 同一时刻的东八区表示（2026-08-24T00:00:00Z == 2026-08-24T08:00:00+08:00）
	east8.IssuedAt = time.Date(2026, 8, 24, 8, 0, 0, 0, time.FixedZone("CST", 8*3600))
	if !bytes.Equal(CanonicalPayload(&utc), CanonicalPayload(&east8)) {
		t.Fatal("同一时刻不同时区应产生相同载荷")
	}
}

func TestCanonicalPayloadDelimiterSafe(t *testing.T) {
	info := baseInfo()
	info.CustomerName = "客户A\n客户B"
	payload := string(CanonicalPayload(&info))
	want := fmt.Sprintf("%d:%s;", len(info.CustomerName), info.CustomerName)
	if !strings.Contains(payload, want) {
		t.Fatalf("含换行的字段未被长度前缀完整保留: %q", payload)
	}
}

func TestLicenseInfoJSONTags(t *testing.T) {
	data, err := json.Marshal(baseInfo())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(data)
	for _, key := range []string{
		`"license_key"`, `"product_name"`, `"customer_name"`, `"max_nodes"`,
		`"max_tenants"`, `"issued_at"`, `"expires_at"`, `"features"`,
		`"signature"`, `"is_offline"`,
	} {
		if !strings.Contains(got, key) {
			t.Errorf("JSON 输出缺少字段 %s", key)
		}
	}
}
