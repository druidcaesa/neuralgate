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
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"

	"go.uber.org/zap"
)

// Metrics 数据面自拼 Prometheus 文本指标：零第三方依赖，暴露面收敛为
// 请求计数(按状态码族)与 Token 用量两类核心指标
type Metrics struct {
	total2xx atomic.Int64
	total4xx atomic.Int64
	total5xx atomic.Int64
	totalOth atomic.Int64
	tokens   atomic.Int64
}

// NewMetrics 创建指标集
func NewMetrics() *Metrics { return &Metrics{} }

// observeRequest 按响应状态码归类计数
func (m *Metrics) observeRequest(status int) {
	switch {
	case status >= 200 && status < 300:
		m.total2xx.Add(1)
	case status >= 400 && status < 500:
		m.total4xx.Add(1)
	case status >= 500:
		m.total5xx.Add(1)
	default:
		m.totalOth.Add(1)
	}
}

// observeTokens 累计转发消耗 Token（负值防御）
func (m *Metrics) observeTokens(n int) {
	if n > 0 {
		m.tokens.Add(int64(n))
	}
}

// Render 输出 Prometheus 文本格式（ exposition format v0.0.4）
func (m *Metrics) Render() string {
	var b strings.Builder
	b.WriteString("# HELP ng_requests_total 数据面请求总数(按状态码族)\n# TYPE ng_requests_total counter\n")
	fmt.Fprintf(&b, "ng_requests_total{status=\"2xx\"} %d\n", m.total2xx.Load())
	fmt.Fprintf(&b, "ng_requests_total{status=\"4xx\"} %d\n", m.total4xx.Load())
	fmt.Fprintf(&b, "ng_requests_total{status=\"5xx\"} %d\n", m.total5xx.Load())
	if v := m.totalOth.Load(); v > 0 {
		fmt.Fprintf(&b, "ng_requests_total{status=\"other\"} %d\n", v)
	}
	b.WriteString("# HELP ng_tokens_total 转发消耗 Token 总数\n# TYPE ng_tokens_total counter\n")
	fmt.Fprintf(&b, "ng_tokens_total %d\n", m.tokens.Load())
	return b.String()
}

// statusFamilyWriter 捕获下游写出的状态码
type statusFamilyWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusFamilyWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// ObservabilityMiddleware 可观测中间件：每请求指标采集+访问日志。
// 经 pipeline.Use 挂载（固定链之后执行，可读取 rc 终态）。
// /metrics 端点由 main 在管道外层伺服（避免被路由中间件 404）
func ObservabilityMiddleware(m *Metrics, logger *zap.Logger) Middleware {
	al := newAccessLogger(logger)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sw := &statusFamilyWriter{ResponseWriter: w, status: 200}
			next.ServeHTTP(sw, r)
			rc, _ := RequestContextFrom(r.Context())
			m.observeRequest(sw.status)
			if rc != nil {
				m.observeTokens(rc.TotalTokens)
				if al != nil {
					al.info(r.Method, r.URL.Path, sw.status, rc)
				}
			}
		})
	}
}

// ServeMetrics 以 Prometheus 文本格式响应指标（main 在管道外挂载该端点）
func ServeMetrics(m *Metrics, w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write([]byte(m.Render()))
}
