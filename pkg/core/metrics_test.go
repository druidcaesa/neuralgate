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

package core

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestMetricsRenderSnapshot 状态码族计数与 Token 计数的渲染快照
func TestMetricsRenderSnapshot(t *testing.T) {
	m := NewMetrics()
	m.observeRequest(200)
	m.observeRequest(200)
	m.observeRequest(404)
	m.observeRequest(502)
	m.observeTokens(120)
	got := m.Render()
	for _, want := range []string{
		`ng_requests_total{status="2xx"} 2`,
		`ng_requests_total{status="4xx"} 1`,
		`ng_requests_total{status="5xx"} 1`,
		`ng_tokens_total 120`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("渲染缺 %s\n实际:\n%s", want, got)
		}
	}
}

// TestObservabilityMiddlewareCounts 中间件采集状态与 Token；/metrics 由外层伺服不在此测
func TestObservabilityMiddlewareCounts(t *testing.T) {
	m := NewMetrics()
	pipelineLike := ObservabilityMiddleware(m, nil)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			rc, _ := RequestContextFrom(r.Context())
			rc.TotalTokens = 77
			w.WriteHeader(http.StatusTeapot) // 418 → 4xx 族
		}))
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	rc := &RequestContext{RequestID: "rid-1", StartTime: time.Now()}
	rec := httptest.NewRecorder()
	pipelineLike.ServeHTTP(rec, req.WithContext(WithRequestContext(req.Context(), rc)))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("下游响应应透传: %d", rec.Code)
	}
	if !strings.Contains(m.Render(), `ng_requests_total{status="4xx"} 1`) ||
		!strings.Contains(m.Render(), `ng_tokens_total 77`) {
		t.Errorf("指标未采集:\n%s", m.Render())
	}
}
