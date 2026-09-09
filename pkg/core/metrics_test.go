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

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	dto "github.com/prometheus/client_model/go"
)

// gatherAll 拉取独立 registry 全部系列,规整为 "name{label=value,...}" → 值。
// 直方图取 _count(样本数),便于断言请求是否被记录。
func gatherAll(t *testing.T, m *Metrics) map[string]float64 {
	t.Helper()
	fams, err := m.reg.Gather()
	if err != nil {
		t.Fatalf("Gather 失败: %v", err)
	}
	out := map[string]float64{}
	for _, fam := range fams {
		for _, mt := range fam.Metric {
			var lbls []string
			for _, lp := range mt.Label {
				lbls = append(lbls, lp.GetName()+"="+lp.GetValue())
			}
			key := fam.GetName() + "{" + strings.Join(lbls, ",") + "}"
			switch fam.GetType() {
			case dto.MetricType_COUNTER:
				out[key] = mt.GetCounter().GetValue()
			case dto.MetricType_GAUGE:
				out[key] = mt.GetGauge().GetValue()
			case dto.MetricType_HISTOGRAM:
				out[key] = float64(mt.GetHistogram().GetSampleCount())
			}
		}
	}
	return out
}

// TestWrapOuterCounts 外层包裹:被中间件拒绝(未及模型)的请求也应计入总请求与 in-flight
func TestWrapOuterCounts(t *testing.T) {
	m := NewMetrics()
	h := m.WrapOuter(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests) // 429 → 4xx
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("响应应透传: %d", rec.Code)
	}
	got := gatherAll(t, m)
	want := map[string]float64{
		"ng_requests_total{status=4xx}": 1,
		"ng_http_in_flight{}":           0,
		"ng_requests_total{status=2xx}": 0,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("指标 %s 应为 %v,实际 %v", k, v, got[k])
		}
	}
	if got["ng_http_request_duration_seconds{}"] == 0 {
		t.Errorf("整体耗时直方图应记录样本: %v", got["ng_http_request_duration_seconds{}"])
	}
}

// TestObservabilityMiddlewareModelMetrics 内层中间件:仅写模型维度与 Token,不再碰总请求计数
func TestObservabilityMiddlewareModelMetrics(t *testing.T) {
	m := NewMetrics()
	h := ObservabilityMiddleware(m, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc, _ := RequestContextFrom(r.Context())
		rc.TotalTokens = 123
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	rc := &RequestContext{
		RequestID:   "rid-1",
		StartTime:   time.Now(),
		ModelConfig: &plugin.ModelConfig{ModelName: "gpt-4o", Provider: "openai"},
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req.WithContext(WithRequestContext(req.Context(), rc)))

	got := gatherAll(t, m)
	checks := map[string]float64{
		"ng_model_requests_total{model=gpt-4o,provider=openai,status_class=2xx}": 1,
		"ng_model_tokens_total{model=gpt-4o}":                                    123,
		"ng_tokens_total{}":                                                      123,
		// 总请求计数已被移除至外层,内层不得重复计
		"ng_requests_total{status=2xx}": 0,
	}
	for k, v := range checks {
		if got[k] != v {
			t.Errorf("指标 %s 应为 %v,实际 %v", k, v, got[k])
		}
	}
	if got["ng_model_request_duration_seconds{model=gpt-4o,provider=openai}"] == 0 {
		t.Error("模型耗时直方图应记录样本")
	}
}

// TestServeMetricsExposition /metrics 应答合法:200 且含核心指标名
func TestServeMetricsExposition(t *testing.T) {
	m := NewMetrics()
	m.WrapOuter(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated) // 2xx
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/models", nil))

	rec := httptest.NewRecorder()
	ServeMetrics(m, rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码应为 200: %d", rec.Code)
	}
	for _, name := range []string{"ng_requests_total", "ng_http_in_flight", "ng_tokens_total"} {
		if !strings.Contains(rec.Body.String(), name) {
			t.Errorf("exposition 应含 %s", name)
		}
	}
}

// TestBreakerGauge 快照写入 ng_upstream_state gauge:0 closed/1 open/2 half-open;消亡上游标签清理
func TestBreakerGauge(t *testing.T) {
	m := NewMetrics()
	m.SetBreakerGauge(map[string]int{"u1": 0, "u2": 1, "u3": 2})
	got := gatherAll(t, m)
	for k, v := range map[string]float64{
		"ng_upstream_state{upstream=u1}": 0,
		"ng_upstream_state{upstream=u2}": 1,
		"ng_upstream_state{upstream=u3}": 2,
	} {
		if got[k] != v {
			t.Errorf("指标 %s 应为 %v,实际 %v", k, v, got[k])
		}
	}
	// 快照不再含 u3(上游过期清扫后)→ 对应系列应被删除,防陈旧标签
	m.SetBreakerGauge(map[string]int{"u1": 0, "u2": 1})
	if _, ok := gatherAll(t, m)["ng_upstream_state{upstream=u3}"]; ok {
		t.Error("消亡上游 u3 的 gauge 应被清理")
	}
}
