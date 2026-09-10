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
	"sync/atomic"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
)

// newUnreadableModelServer 构造含一行「密钥不可解密」模型的 AdminServer
func newUnreadableModelServer(t *testing.T) (*AdminServer, *oss.MemStorage) {
	t.Helper()
	st := oss.NewMemStorage()
	now := time.Now()
	if err := st.SaveModelConfig(&plugin.ModelConfig{
		ID: "m-bad", ModelName: "broken-model", Provider: "openai", ProviderModel: "gpt-4o",
		BaseURL: "https://x", APIKey: "", APIKeyUnreadable: true, Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveModelConfig: %v", err)
	}
	svr := NewAdminServer(st, nil, "oss", oss.NewRateLimiter(st, 100, 100000, "token_bucket"), nil)
	svr.DisableAuth()
	return svr, st
}

// TestListModelConfigsFlagsUnreadableKey 坏行须出现在列表中并带 key_unreadable 标记
func TestListModelConfigsFlagsUnreadableKey(t *testing.T) {
	svr, _ := newUnreadableModelServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/models?page=1&size=10", nil)
	svr.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			Items []map[string]interface{} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(resp.Data.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(resp.Data.Items))
	}
	if resp.Data.Items[0]["key_unreadable"] != true {
		t.Errorf("key_unreadable = %v, want true", resp.Data.Items[0]["key_unreadable"])
	}
}

// TestUpdateModelConfigRejectsEmptyKeyForUnreadableRow 坏行不填 key 必须被拒,
// 否则「留空保留原值」会把密文覆盖成空密钥
func TestUpdateModelConfigRejectsEmptyKeyForUnreadableRow(t *testing.T) {
	svr, st := newUnreadableModelServer(t)
	body := `{"name":"broken-model","provider":"openai","provider_model":"gpt-4o",` +
		`"base_url":"https://x","enabled":true}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/models/m-bad", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	svr.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
	got, err := st.GetModelConfigByID("m-bad")
	if err != nil {
		t.Fatalf("GetModelConfigByID: %v", err)
	}
	if !got.APIKeyUnreadable {
		t.Error("被拒的更新不得改动原行")
	}
}

// TestUpdateModelConfigAllowsRefillForUnreadableRow 坏行填了新 key 应可保存
func TestUpdateModelConfigAllowsRefillForUnreadableRow(t *testing.T) {
	svr, st := newUnreadableModelServer(t)
	body := `{"name":"broken-model","provider":"openai","provider_model":"gpt-4o",` +
		`"base_url":"https://x","api_key":"sk-refilled","enabled":true}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/models/m-bad", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	svr.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	got, err := st.GetModelConfigByID("m-bad")
	if err != nil {
		t.Fatalf("GetModelConfigByID: %v", err)
	}
	if got.APIKey != "sk-refilled" {
		t.Errorf("APIKey = %q, want sk-refilled", got.APIKey)
	}
}

// TestTestModelConfigSkipsRequestForUnreadableKey 坏行连通测试必须直接返回 ok=false,
// 不得带着空 key 发请求换回误导性的上游错误
func TestTestModelConfigSkipsRequestForUnreadableKey(t *testing.T) {
	var hits atomic.Int64
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer stub.Close()

	svr, st := newUnreadableModelServer(t)
	if err := st.SaveModelConfig(&plugin.ModelConfig{
		ID: "m-bad2", ModelName: "broken-2", Provider: "openai", ProviderModel: "gpt-4o",
		BaseURL: stub.URL, APIKey: "", APIKeyUnreadable: true, Enabled: true,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("SaveModelConfig: %v", err)
	}

	rec := httptest.NewRecorder()
	svr.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/models/m-bad2/test", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			Ok        bool   `json:"ok"`
			LatencyMS int64  `json:"latency_ms"`
			Error     string `json:"error"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp.Data.Ok || resp.Data.Error == "" || resp.Data.LatencyMS != 0 {
		t.Errorf("坏行连通测试响应 = %+v, want ok=false + 非空 error + latency_ms=0", resp.Data)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("坏行连通测试发出 %d 次上游请求, want 0", n)
	}
}
