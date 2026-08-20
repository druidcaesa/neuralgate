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

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
	"go.uber.org/zap"
)

func TestHealthz(t *testing.T) {
	s := NewAdminServer(oss.NewMemStorage(), zap.NewNop(), "oss")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/healthz", nil)
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestAPIPing(t *testing.T) {
	s := NewAdminServer(oss.NewMemStorage(), zap.NewNop(), "oss")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/ping", nil)
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestCORSMiddleware(t *testing.T) {
	s := NewAdminServer(oss.NewMemStorage(), zap.NewNop(), "oss")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/healthz", nil)
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("OPTIONS status = %d, want 204", rec.Code)
	}
}

func TestAdminAPIKeyCRUD(t *testing.T) {
	s := oss.NewMemStorage()
	router := NewAdminServer(s, nil, "oss").Router()

	// 创建
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/api-keys",
		strings.NewReader(`{"name":"测试Key","quota":-1,"rate_limit":10}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create status = %d; body=%s", w.Code, w.Body.String())
	}
	var created struct {
		Data struct {
			ID      string `json:"id"`
			Key     string `json:"key"`
			KeyHash string `json:"key_hash"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created.Data.Key, "ng-") {
		t.Fatalf("key = %q; want ng- prefix", created.Data.Key)
	}
	// 密文明文在响应中,哈希已入库
	if _, err := s.GetAPIKey(created.Data.KeyHash); err != nil {
		t.Fatalf("key hash not stored: %v", err)
	}

	// 列表
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/api-keys?page=1&size=10", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), created.Data.ID) {
		t.Fatalf("list status=%d body=%s", w.Code, w.Body.String())
	}
	// 列表不泄露明文与哈希,Key 脱敏
	listBody := w.Body.String()
	if strings.Contains(listBody, created.Data.Key) {
		t.Fatalf("list leaks plaintext key: %s", listBody)
	}
	if strings.Contains(listBody, created.Data.KeyHash) {
		t.Fatalf("list leaks key hash: %s", listBody)
	}
	if !strings.Contains(listBody, "****") {
		t.Fatalf("list key not masked: %s", listBody)
	}

	// 禁用
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPatch, "/api/api-keys/"+created.Data.ID,
		strings.NewReader(`{"status":"disabled"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("disable status = %d", w.Code)
	}
	key, _ := s.GetAPIKeyByID(created.Data.ID)
	if key.Status != plugin.APIKeyStatusDisabled {
		t.Fatalf("status = %s; want disabled", key.Status)
	}

	// 删除(软删除)
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/api/api-keys/"+created.Data.ID, nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete status = %d", w.Code)
	}
	if _, err := s.GetAPIKeyByID(created.Data.ID); err == nil {
		t.Fatal("key should be soft-deleted")
	}
}

func TestAdminModelConfigCRUD(t *testing.T) {
	s := oss.NewMemStorage()
	router := NewAdminServer(s, nil, "oss").Router()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/models",
		strings.NewReader(`{"name":"gpt-4","provider":"openai","provider_model":"gpt-4o","base_url":"https://api.openai.com","api_key":"sk-test"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create status = %d; body=%s", w.Code, w.Body.String())
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	// 名称唯一冲突
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/models",
		strings.NewReader(`{"name":"gpt-4","provider":"openai","provider_model":"x","base_url":"https://x","api_key":"y"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate name status = %d; want 409", w.Code)
	}

	// 列表
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/models", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "gpt-4") {
		t.Fatalf("list status=%d body=%s", w.Code, w.Body.String())
	}
	// 列表不回显上游 api_key
	if strings.Contains(w.Body.String(), "sk-test") {
		t.Fatalf("list leaks api_key: %s", w.Body.String())
	}
}

func TestAdminModelConfigRenameConflict(t *testing.T) {
	s := oss.NewMemStorage()
	router := NewAdminServer(s, nil, "oss").Router()
	postModel := func(name string) (string, string) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/models",
			strings.NewReader(`{"name":"`+name+`","provider":"openai","provider_model":"x","base_url":"https://x","api_key":"y"}`))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("create %s status = %d; body=%s", name, w.Code, w.Body.String())
		}
		var created struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &created)
		return created.Data.ID, w.Body.String()
	}

	// 创建 A、B
	_, _ = postModel("model-a")
	idB, _ := postModel("model-b")

	// PUT B 改名为 A 的名称 → 409,且不覆盖 A
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/models/"+idB,
		strings.NewReader(`{"name":"model-a","provider":"openai","provider_model":"x","base_url":"https://x","api_key":"y"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("rename conflict status = %d; want 409", w.Code)
	}
	// B 应保持原名(未被覆盖)
	cfg, err := s.GetModelConfigByID(idB)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ModelName != "model-b" {
		t.Fatalf("B renamed to %q; want model-b", cfg.ModelName)
	}
}

func TestAdminAuditExportAll(t *testing.T) {
	s := oss.NewMemStorage()
	now := time.Now()
	for i := 0; i < 120; i++ {
		_ = s.SaveAuditLog(&plugin.AuditLog{
			ID: fmt.Sprintf("id-%03d", i), RequestID: fmt.Sprintf("req-%03d", i),
			ModelName: "gpt-4", ResponseStatus: 200, TotalTokens: 15,
			CreatedAt: now.Add(time.Duration(i) * time.Second),
		})
	}
	router := NewAdminServer(s, nil, "oss").Router()

	// JSON 导出应拉全量(>100 条不受单页上限截断)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/audit-logs/export?format=json", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("export status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			Items []*plugin.AuditLog `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data.Items) != 120 {
		t.Fatalf("export items = %d; want 120(翻页全量)", len(resp.Data.Items))
	}
}

func TestAdminAuditQuery(t *testing.T) {
	s := oss.NewMemStorage()
	now := time.Now()
	_ = s.SaveAuditLog(&plugin.AuditLog{
		ID: "a1", RequestID: "r1", ModelName: "gpt-4", ResponseStatus: 200,
		TotalTokens: 15, CreatedAt: now,
	})
	router := NewAdminServer(s, nil, "oss").Router()

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/audit-logs?model_name=gpt-4&page=1&size=10", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "r1") {
		t.Fatalf("query status=%d body=%s", w.Code, w.Body.String())
	}

	// 详情(按 RequestID 查询,路径参数即请求 ID)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/audit-logs/r1", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "gpt-4") {
		t.Fatalf("detail status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestAdminSystem(t *testing.T) {
	s := oss.NewMemStorage()
	router := NewAdminServer(s, nil, "oss").Router()
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/system", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "version") {
		t.Fatalf("body = %s; want version field", w.Body.String())
	}
}
