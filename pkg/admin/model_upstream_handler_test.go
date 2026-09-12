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
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
)

// upstreamModelsResp 拉取接口的响应形状,供断言解包
type upstreamModelsResp struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
	} `json:"data"`
}

// postUpstreamModels 向 handler 发一次请求,返回响应码与解包后的 body
func postUpstreamModels(t *testing.T, svr *AdminServer, body string) (int, upstreamModelsResp) {
	t.Helper()
	var parsed upstreamModelsResp
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/models/upstream-models", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	svr.Router().ServeHTTP(rec, req)
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v, body=%s", err, rec.Body.String())
	}
	return rec.Code, parsed
}

// newUpstreamModelsServer 构造空存储的 AdminServer(免认证)
func newUpstreamModelsServer(t *testing.T) (*AdminServer, *oss.MemStorage) {
	t.Helper()
	st := oss.NewMemStorage()
	svr := NewAdminServer(st, nil, "oss", oss.NewRateLimiter(st, 100, 100000, "token_bucket"), nil)
	svr.DisableAuth()
	return svr, st
}

// TestUpstreamModelsRouteNotShadowed 路由须命中本 handler,
// 不被 /models/:id/test 之类的参数路由吞掉
func TestUpstreamModelsRouteNotShadowed(t *testing.T) {
	srv := stubUpstream(t, http.StatusOK, `{"data":[{"id":"m1"}]}`)
	svr, _ := newUpstreamModelsServer(t)

	status, resp := postUpstreamModels(t, svr, `{"base_url":"`+srv.URL+`","api_key":"sk-x"}`)

	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%+v", status, resp)
	}
	if len(resp.Data.Models) != 1 || resp.Data.Models[0].ID != "m1" {
		t.Errorf("models = %+v, want [m1]", resp.Data.Models)
	}
}

// TestUpstreamModelsPlaintextKeyWins 请求体带明文密钥时直接使用,不需要 model_id
func TestUpstreamModelsPlaintextKeyWins(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	t.Cleanup(srv.Close)
	svr, _ := newUpstreamModelsServer(t)

	_, _ = postUpstreamModels(t, svr, `{"base_url":"`+srv.URL+`","api_key":"sk-plain"}`)

	if gotAuth != "Bearer sk-plain" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer sk-plain")
	}
}

// TestUpstreamModelsFallsBackToStoredKey 无明文密钥时取库中该模型的密钥
func TestUpstreamModelsFallsBackToStoredKey(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	t.Cleanup(srv.Close)

	svr, st := newUpstreamModelsServer(t)
	if err := st.SaveModelConfig(&plugin.ModelConfig{
		ID: "m-stored", ModelName: "stored-model", Provider: "openai", ProviderModel: "gpt-4o",
		BaseURL: srv.URL, APIKey: "sk-stored", Enabled: true,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("SaveModelConfig: %v", err)
	}

	_, _ = postUpstreamModels(t, svr, `{"base_url":"`+srv.URL+`","model_id":"m-stored"}`)

	if gotAuth != "Bearer sk-stored" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer sk-stored")
	}
}

// TestUpstreamModelsGatewayErrors 网关侧校验各自返回独立业务码,且不出网
func TestUpstreamModelsGatewayErrors(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	t.Cleanup(srv.Close)

	cases := []struct {
		name     string
		body     string
		wantCode int
		wantHTTP int
	}{
		{"缺 base_url", `{"api_key":"sk-x"}`, CodeUpstreamModelsMissingBaseURL, http.StatusBadRequest},
		{"协议非法", `{"base_url":"ftp://x","api_key":"sk-x"}`, CodeUpstreamModelsBadScheme, http.StatusBadRequest},
		{"既无密钥也无 model_id", `{"base_url":"` + srv.URL + `"}`, CodeUpstreamModelsMissingAPIKey, http.StatusBadRequest},
		{"model_id 不存在", `{"base_url":"` + srv.URL + `","model_id":"nope"}`, CodeUpstreamModelsModelNotFound, http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svr, _ := newUpstreamModelsServer(t)
			status, resp := postUpstreamModels(t, svr, tc.body)

			if status != tc.wantHTTP {
				t.Errorf("status = %d, want %d", status, tc.wantHTTP)
			}
			if resp.Code != tc.wantCode {
				t.Errorf("code = %d, want %d (msg=%q)", resp.Code, tc.wantCode, resp.Message)
			}
		})
	}
	if hits != 0 {
		t.Errorf("网关侧校验失败仍发出了 %d 次上游请求, want 0", hits)
	}
}

// TestUpstreamModelsUnreadableKeyRejected 密钥不可解密的模型直接拒绝,不出网
func TestUpstreamModelsUnreadableKeyRejected(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	t.Cleanup(srv.Close)

	svr, st := newUpstreamModelsServer(t)
	if err := st.SaveModelConfig(&plugin.ModelConfig{
		ID: "m-bad", ModelName: "broken", Provider: "openai", ProviderModel: "gpt-4o",
		BaseURL: srv.URL, APIKey: "", APIKeyUnreadable: true, Enabled: true,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("SaveModelConfig: %v", err)
	}

	status, resp := postUpstreamModels(t, svr, `{"base_url":"`+srv.URL+`","model_id":"m-bad"}`)

	if status != http.StatusBadRequest || resp.Code != CodeUpstreamModelsKeyUnreadable {
		t.Errorf("status=%d code=%d, want 400/%d", status, resp.Code, CodeUpstreamModelsKeyUnreadable)
	}
	if hits != 0 {
		t.Errorf("坏行仍发出了 %d 次上游请求, want 0", hits)
	}
}

// TestUpstreamModelsUpstreamErrorPassesThrough 上游错误按原因归类下发,不折叠
func TestUpstreamModelsUpstreamErrorPassesThrough(t *testing.T) {
	srv := stubUpstream(t, http.StatusForbidden, `{"error":{"message":"no perm"}}`)
	svr, _ := newUpstreamModelsServer(t)

	status, resp := postUpstreamModels(t, svr, `{"base_url":"`+srv.URL+`","api_key":"sk-x"}`)

	if status != http.StatusBadRequest || resp.Code != CodeUpstreamModelsForbidden {
		t.Errorf("status=%d code=%d, want 400/%d", status, resp.Code, CodeUpstreamModelsForbidden)
	}
	if strings.Contains(resp.Message, "no perm") {
		t.Errorf("不得回显上游响应体原文: %q", resp.Message)
	}
}
