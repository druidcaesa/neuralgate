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

package docsui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestServeHitPaths 命中路径写单页:200 + text/html + 标题/端点标记;未命中返回 false
func TestServeHitPaths(t *testing.T) {
	markers := []string{
		"NeuralGate 接口调用说明", "两种协议怎么选", "模型名两入口共用同一套",
		"OpenAI 兼容", "/v1/chat/completions", "Authorization: Bearer",
		"Anthropic Messages", "/v1/messages", "x-api-key", "anthropic-version",
		"message_stop", "my-model",
	}
	for _, p := range []string{"/docs", "/docs/", "/docs/index.html"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, p, nil)
		if !Serve(rec, req) {
			t.Fatalf("Serve(%s) = false; want true", p)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("Serve(%s) status = %d; want 200", p, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Fatalf("Serve(%s) Content-Type = %q; want text/html", p, ct)
		}
		body := rec.Body.String()
		for _, m := range markers {
			if !strings.Contains(body, m) {
				t.Fatalf("Serve(%s) body 缺少标记 %q", p, m)
			}
		}
	}
}

// TestServeMiss 未命中路径返回 false(交调用方继续处理)
func TestServeMiss(t *testing.T) {
	for _, p := range []string{"/docs/x.js", "/v1/chat/completions", "/metrics", "/"} {
		rec := httptest.NewRecorder()
		if Serve(rec, httptest.NewRequest(http.MethodGet, p, nil)) {
			t.Fatalf("Serve(%s) = true; want false", p)
		}
	}
}
