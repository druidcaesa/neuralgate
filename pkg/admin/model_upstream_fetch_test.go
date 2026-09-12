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
	"net/http"
	"net/http/httptest"
	"testing"
)

// stubUpstream 起一个假上游,固定返回给定状态码与响应体
func stubUpstream(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestFetchUpstreamModelsSuccess 正常路径:只取 data[].id 字段,按字典序返回
func TestFetchUpstreamModelsSuccess(t *testing.T) {
	srv := stubUpstream(t, http.StatusOK,
		`{"object":"list","data":[{"id":"b-model","owned_by":"x"},{"id":"a-model"}]}`)

	models, status, code, msg := fetchUpstreamModels(srv.URL, "sk-test")

	if status != 0 || code != 0 || msg != "" {
		t.Fatalf("成功路径不应带错误: status=%d code=%d msg=%q", status, code, msg)
	}
	if len(models) != 2 || models[0].ID != "a-model" || models[1].ID != "b-model" {
		t.Errorf("models = %+v, want 字典序 [a-model b-model]", models)
	}
}

// TestFetchUpstreamModelsSkipsEmptyID 元素缺 id 时跳过该条,其余照常返回
func TestFetchUpstreamModelsSkipsEmptyID(t *testing.T) {
	srv := stubUpstream(t, http.StatusOK,
		`{"data":[{"id":"keep-me"},{"id":""},{"owned_by":"no-id"}]}`)

	models, status, code, _ := fetchUpstreamModels(srv.URL, "sk-test")

	if status != 0 || code != 0 {
		t.Fatalf("不应报错: status=%d code=%d", status, code)
	}
	if len(models) != 1 || models[0].ID != "keep-me" {
		t.Errorf("models = %+v, want 仅 [keep-me]", models)
	}
}

// TestFetchUpstreamModelsEmptyDataIsOk 空数组不是错误——账号可能确实无可用模型
func TestFetchUpstreamModelsEmptyDataIsOk(t *testing.T) {
	srv := stubUpstream(t, http.StatusOK, `{"data":[]}`)

	models, status, code, _ := fetchUpstreamModels(srv.URL, "sk-test")

	if status != 0 || code != 0 {
		t.Fatalf("空数组不应报错: status=%d code=%d", status, code)
	}
	if len(models) != 0 {
		t.Errorf("models = %+v, want 空", models)
	}
}

// TestFetchUpstreamModelsBadResponse 非 JSON / data 缺失 / data 为 null / data 非数组
// 四种形态统一归为 upstream_bad_response
func TestFetchUpstreamModelsBadResponse(t *testing.T) {
	cases := map[string]string{
		"非 JSON":      `not json at all`,
		"data 缺失":     `{"object":"list"}`,
		"data 为 null": `{"data":null}`,
		"data 非数组":    `{"data":{"id":"x"}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			srv := stubUpstream(t, http.StatusOK, body)

			_, status, code, msg := fetchUpstreamModels(srv.URL, "sk-test")

			if code != CodeUpstreamModelsBadResponse {
				t.Errorf("code = %d, want %d (msg=%q)", code, CodeUpstreamModelsBadResponse, msg)
			}
			if status != http.StatusBadGateway {
				t.Errorf("status = %d, want %d (msg=%q)", status, http.StatusBadGateway, msg)
			}
		})
	}
}

// TestFetchUpstreamModelsMapsUpstreamStatus 上游状态码按原因归类,不折叠成单一码
func TestFetchUpstreamModelsMapsUpstreamStatus(t *testing.T) {
	cases := []struct {
		status     int
		wantCode   int
		wantStatus int
		body       string // 空则用通用错误体
	}{
		{http.StatusUnauthorized, CodeUpstreamModelsAuthFailed, http.StatusBadRequest, ""},
		{http.StatusForbidden, CodeUpstreamModelsForbidden, http.StatusBadRequest, ""},
		{http.StatusNotFound, CodeUpstreamModelsUpstreamNotFound, http.StatusBadRequest, ""},
		{http.StatusInternalServerError, CodeUpstreamModelsUpstreamError, http.StatusBadGateway, ""},
		{http.StatusTooManyRequests, CodeUpstreamModelsUpstreamError, http.StatusBadGateway, ""},
		// 300 不在 Go client 自动跟随之列(只跟 301/302/303/307/308),会原样返回,
		// 故须自己判失败:响应体即使是一份合法清单,也不得当成 200 收下
		{http.StatusMultipleChoices, CodeUpstreamModelsUpstreamError, http.StatusBadGateway,
			`{"data":[{"id":"x"}]}`},
	}
	for _, tc := range cases {
		body := tc.body
		if body == "" {
			body = `{"error":{"message":"nope"}}`
		}
		srv := stubUpstream(t, tc.status, body)

		_, status, code, msg := fetchUpstreamModels(srv.URL, "sk-test")

		if code != tc.wantCode {
			t.Errorf("上游 %d → code = %d, want %d (msg=%q)", tc.status, code, tc.wantCode, msg)
		}
		if status != tc.wantStatus {
			t.Errorf("上游 %d → status = %d, want %d (msg=%q)", tc.status, status, tc.wantStatus, msg)
		}
	}
}

// TestFetchUpstreamModelsUnreachable 连不上时归为 upstream_unreachable
func TestFetchUpstreamModelsUnreachable(t *testing.T) {
	srv := stubUpstream(t, http.StatusOK, `{"data":[]}`)
	srv.Close() // 关闭后地址不可达

	_, status, code, msg := fetchUpstreamModels(srv.URL, "sk-test")

	if code != CodeUpstreamModelsUnreachable {
		t.Errorf("code = %d, want %d (msg=%q)", code, CodeUpstreamModelsUnreachable, msg)
	}
	if status != http.StatusBadGateway {
		t.Errorf("status = %d, want %d (msg=%q)", status, http.StatusBadGateway, msg)
	}
}

// TestFetchUpstreamModelsSendsBearer 出网请求须带 Authorization 且打 /v1/models
func TestFetchUpstreamModelsSendsBearer(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	t.Cleanup(srv.Close)

	_, _, _, _ = fetchUpstreamModels(srv.URL, "sk-secret")

	if gotAuth != "Bearer sk-secret" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer sk-secret")
	}
	if gotPath != "/v1/models" {
		t.Errorf("path = %q, want /v1/models", gotPath)
	}
}
