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

// TestUpstreamModelsPlaintextKeyWins 明文密钥与库中密钥同时可选时,明文优先。
// 本用例同时传 api_key 与 model_id,钉的是「先看明文、再看 model_id」这一**顺序**,
// 不是「明文可用」——若精简成只传 api_key,顺序被写反也照样通过
func TestUpstreamModelsPlaintextKeyWins(t *testing.T) {
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

	_, _ = postUpstreamModels(t, svr,
		`{"base_url":"`+srv.URL+`","model_id":"m-stored","api_key":"sk-plain"}`)

	if gotAuth != "Bearer sk-plain" {
		t.Errorf("Authorization = %q, want %q (明文密钥须盖过库中密钥)", gotAuth, "Bearer sk-plain")
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

// TestUpstreamModelsStoredKeyEgressesToOwnBaseURL 密钥与地址同源:
// 回落库中密钥时,出网地址必须是该模型自己的 base_url,而非调用方传入的地址。
// 否则调用方可用任意已存模型换出该模型的密钥,并把 Bearer 发往自己指定的主机
func TestUpstreamModelsStoredKeyEgressesToOwnBaseURL(t *testing.T) {
	var attackerHits, ownHits int
	var ownAuth string
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attackerHits++
		_, _ = w.Write([]byte(`{"data":[{"id":"from-attacker"}]}`))
	}))
	t.Cleanup(attacker.Close)
	own := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ownHits++
		ownAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":[{"id":"from-own"}]}`))
	}))
	t.Cleanup(own.Close)

	svr, st := newUpstreamModelsServer(t)
	if err := st.SaveModelConfig(&plugin.ModelConfig{
		ID: "m-stored", ModelName: "stored-model", Provider: "openai", ProviderModel: "gpt-4o",
		BaseURL: own.URL, APIKey: "sk-stored", Enabled: true,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("SaveModelConfig: %v", err)
	}

	status, resp := postUpstreamModels(t, svr, `{"base_url":"`+attacker.URL+`","model_id":"m-stored"}`)

	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%+v", status, resp)
	}
	if ownHits != 1 {
		t.Errorf("模型自身地址收到 %d 次请求, want 1", ownHits)
	}
	if attackerHits != 0 {
		t.Errorf("调用方传入的地址收到 %d 次请求, want 0(库中密钥不得发往调用方指定主机)", attackerHits)
	}
	if ownAuth != "Bearer sk-stored" {
		t.Errorf("Authorization = %q, want %q", ownAuth, "Bearer sk-stored")
	}
	if len(resp.Data.Models) != 1 || resp.Data.Models[0].ID != "from-own" {
		t.Errorf("models = %+v, want [from-own](清单须来自模型自身地址)", resp.Data.Models)
	}
}

// TestUpstreamModelsEmptyStoredKeyRejected 库中密钥为空串时一并拦下:
// createModelConfig 不校验 api_key,故存在 APIKey == "" 且未标记不可解密的行,
// 放行会以 "Bearer " 出网换回误导性的 4611
func TestUpstreamModelsEmptyStoredKeyRejected(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	t.Cleanup(srv.Close)

	svr, st := newUpstreamModelsServer(t)
	if err := st.SaveModelConfig(&plugin.ModelConfig{
		ID: "m-empty", ModelName: "empty-key", Provider: "openai", ProviderModel: "gpt-4o",
		BaseURL: srv.URL, APIKey: "", Enabled: true,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("SaveModelConfig: %v", err)
	}

	status, resp := postUpstreamModels(t, svr, `{"base_url":"`+srv.URL+`","model_id":"m-empty"}`)

	if status != http.StatusBadRequest || resp.Code != CodeUpstreamModelsKeyUnreadable {
		t.Errorf("status=%d code=%d, want 400/%d (msg=%q)",
			status, resp.Code, CodeUpstreamModelsKeyUnreadable, resp.Message)
	}
	if hits != 0 {
		t.Errorf("空密钥行仍发出了 %d 次上游请求, want 0", hits)
	}
}

// TestUpstreamModelsPlaintextKeyUsesCallerBaseURL 明文密钥路径仍用调用方地址:
// 同源约束只针对库中密钥,新建态(表单现填密钥+地址)必须照旧
func TestUpstreamModelsPlaintextKeyUsesCallerBaseURL(t *testing.T) {
	var ownHits, callerHits int
	own := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ownHits++
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	t.Cleanup(own.Close)
	caller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callerHits++
		_, _ = w.Write([]byte(`{"data":[{"id":"from-caller"}]}`))
	}))
	t.Cleanup(caller.Close)

	svr, st := newUpstreamModelsServer(t)
	if err := st.SaveModelConfig(&plugin.ModelConfig{
		ID: "m-stored", ModelName: "stored-model", Provider: "openai", ProviderModel: "gpt-4o",
		BaseURL: own.URL, APIKey: "sk-stored", Enabled: true,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("SaveModelConfig: %v", err)
	}

	status, resp := postUpstreamModels(t, svr,
		`{"base_url":"`+caller.URL+`","model_id":"m-stored","api_key":"sk-plain"}`)

	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%+v", status, resp)
	}
	if callerHits != 1 || ownHits != 0 {
		t.Errorf("caller=%d own=%d, want caller=1 own=0(明文密钥须走调用方地址)", callerHits, ownHits)
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

// TestUpstreamModelsStoredBaseURLBadSchemeRejected 库中地址协议非法时同样归 4605:
// 显式白名单必须也跑在 cfg.BaseURL 上。只靠传输层兜底会把「地址写错」报成
// 4614「无法连接上游」,而本接口的价值正是精确归因,归类错等于白归。
// 调用方地址此处是合法字面量且不会被拨号(库中密钥路径不走它),故断言只针对归类
func TestUpstreamModelsStoredBaseURLBadSchemeRejected(t *testing.T) {
	svr, st := newUpstreamModelsServer(t)
	if err := st.SaveModelConfig(&plugin.ModelConfig{
		ID: "m-ftp", ModelName: "ftp-row", Provider: "openai", ProviderModel: "gpt-4o",
		BaseURL: "ftp://example.invalid", APIKey: "sk-stored", Enabled: true,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("SaveModelConfig: %v", err)
	}

	status, resp := postUpstreamModels(t, svr,
		`{"base_url":"https://example.invalid","model_id":"m-ftp"}`)

	if status != http.StatusBadRequest || resp.Code != CodeUpstreamModelsBadScheme {
		t.Errorf("status=%d code=%d, want 400/%d (msg=%q)",
			status, resp.Code, CodeUpstreamModelsBadScheme, resp.Message)
	}
}
