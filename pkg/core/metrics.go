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
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

// httpDurations 请求耗时直方图分桶(秒)：0.01s 起按 2 倍步进至约 82s,覆盖长流式尾部
var httpDurations = prometheus.ExponentialBuckets(0.01, 2, 14)

// Metrics 数据面 Prometheus 指标集(前缀 ng_),经官方 client_golang 采集。
//
// 捕获分两层,各司其职、互不重复计总:
//   - 外层(WrapOuter,main 在 rootHandler 里包裹 acceptor): 覆盖一切到达代理的请求——
//     鉴权/限流/隐私被拒的请求在链路中止,只有这层可见。记录 ng_requests_total、
//     ng_http_in_flight、ng_http_request_duration_seconds。
//   - 内层(ObservabilityMiddleware,pipeline.Use 挂链尾): 请求已匹配模型、能取到
//     RequestContext 终态,补模型维度系列。只写 ng_model_* 与 Token,不碰总计数。
type Metrics struct {
	reg     *prometheus.Registry
	handler http.Handler // 已装配 registry 的 promhttp 处理器(/metrics 复用)

	// 外层指标
	reqs     *prometheus.CounterVec // ng_requests_total{status}
	inflight prometheus.Gauge       // ng_http_in_flight
	durAll   prometheus.Histogram   // ng_http_request_duration_seconds

	// 内层指标
	modelReqs   *prometheus.CounterVec   // ng_model_requests_total{model,provider,status_class}
	modelDur    *prometheus.HistogramVec // ng_model_request_duration_seconds{model,provider}
	modelTokens *prometheus.CounterVec   // ng_model_tokens_total{model}
	tokensTotal prometheus.Counter       // ng_tokens_total

	upstreamState *prometheus.GaugeVec // ng_upstream_state{upstream}:0 closed/1 open/2 half-open
	upstreamIDs   map[string]struct{}  // 已登记上游集合(供清理消亡标签);唯一写入方是 SetBreakerGauge
}

// NewMetrics 创建指标集(每个实例独立 registry,互不冲突)
func NewMetrics() *Metrics {
	m := &Metrics{reg: prometheus.NewRegistry()}
	m.reqs = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ng_requests_total",
		Help: "数据面请求总数(按状态码族);覆盖所有到达代理的请求含鉴权/限流拒绝",
	}, []string{"status"})
	m.inflight = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "ng_http_in_flight",
		Help: "当前正在处理的请求数(进入外层包裹后到响应结束)",
	})
	m.durAll = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "ng_http_request_duration_seconds",
		Help:    "数据面请求整体耗时(含被中间件拒绝的请求)",
		Buckets: httpDurations,
	})
	m.modelReqs = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ng_model_requests_total",
		Help: "已匹配模型的请求总数",
	}, []string{"model", "provider", "status_class"})
	m.modelDur = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "ng_model_request_duration_seconds",
		Help:    "按模型统计的请求耗时",
		Buckets: httpDurations,
	}, []string{"model", "provider"})
	m.modelTokens = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ng_model_tokens_total",
		Help: "按模型统计的转发 Token 数",
	}, []string{"model"})
	m.tokensTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "ng_tokens_total",
		Help: "转发消耗 Token 总数",
	})
	m.upstreamState = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ng_upstream_state",
		Help: "上游熔断状态:0 closed/1 open/2 half-open(标签随请求惰性建立)",
	}, []string{"upstream"})
	m.upstreamIDs = make(map[string]struct{})
	m.reg.MustRegister(m.reqs, m.inflight, m.durAll, m.modelReqs, m.modelDur,
		m.modelTokens, m.tokensTotal, m.upstreamState) // 原列表基础上追加 m.upstreamState
	m.handler = promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
	return m
}

// SetBreakerGauge 按注册表快照刷新各上游熔断状态 gauge;对不再出现于新快照的上游
// (注册表空闲清扫后)删除标签防陈旧。仅由 /metrics 采集处理调用(单写入方),不触碰
// WrapOuter/Observability 的写路径,故无需加锁。
func (m *Metrics) SetBreakerGauge(snap map[string]int) {
	if m.upstreamState == nil {
		return
	}
	for id := range m.upstreamIDs {
		if _, ok := snap[id]; !ok {
			m.upstreamState.DeleteLabelValues(id)
			delete(m.upstreamIDs, id)
		}
	}
	for id, v := range snap {
		m.upstreamState.WithLabelValues(id).Set(float64(v))
		m.upstreamIDs[id] = struct{}{}
	}
}

// statusClass 将状态码归族:2xx/4xx/5xx/other
func statusClass(status int) string {
	switch {
	case status >= 200 && status < 300:
		return "2xx"
	case status >= 400 && status < 500:
		return "4xx"
	case status >= 500:
		return "5xx"
	default:
		return "other"
	}
}

// statusFamilyWriter 捕获下游写出的状态码(未显式 WriteHeader 视为 200)
type statusFamilyWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusFamilyWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusFamilyWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

// finalStatus 返回捕获到的状态码(默认 200)
func (w *statusFamilyWriter) finalStatus() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

// WrapOuter 外层包裹:进入即挂起 in-flight,响应结束记录总请求计数与整体耗时。
// 在 main 的 rootHandler 中只包裹 acceptor 分支(/metrics、/docs 不落入计数)。
func (m *Metrics) WrapOuter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.inflight.Inc()
		start := time.Now()
		sw := &statusFamilyWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		m.durAll.Observe(time.Since(start).Seconds())
		m.reqs.WithLabelValues(statusClass(sw.finalStatus())).Inc()
		m.inflight.Dec()
	})
}

// observeModel 记录模型维度指标与 Token(内层专用);无模型上下文时仅计总 Token
func (m *Metrics) observeModel(rc *RequestContext, status int, dur time.Duration) {
	if n := rc.TotalTokens; n > 0 {
		m.tokensTotal.Add(float64(n))
	}
	if rc.ModelConfig == nil || rc.ModelConfig.ModelName == "" {
		return
	}
	model := rc.ModelConfig.ModelName
	provider := rc.ModelConfig.Provider
	m.modelReqs.WithLabelValues(model, provider, statusClass(status)).Inc()
	m.modelDur.WithLabelValues(model, provider).Observe(dur.Seconds())
	if n := rc.TotalTokens; n > 0 {
		m.modelTokens.WithLabelValues(model).Add(float64(n))
	}
}

// ObservabilityMiddleware 可观测中间件:模型维度指标采集+访问日志。
// 经 pipeline.Use 挂载(固定链之后执行,可读取 rc 终态)。
// 只写 ng_model_* 与 Token;总请求计数/耗时由外层 WrapOuter 负责,避免重复计总。
func ObservabilityMiddleware(m *Metrics, logger *zap.Logger) Middleware {
	al := newAccessLogger(logger)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rc, ok := RequestContextFrom(r.Context())
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			start := time.Now()
			sw := &statusFamilyWriter{ResponseWriter: w}
			next.ServeHTTP(sw, r)
			m.observeModel(rc, sw.finalStatus(), time.Since(start))
			if al != nil {
				al.info(r.Method, r.URL.Path, sw.finalStatus(), rc)
			}
		})
	}
}

// ServeMetrics 以 Prometheus exposition 响应指标(main 在管道外挂载该端点)
func ServeMetrics(m *Metrics, w http.ResponseWriter, r *http.Request) {
	m.handler.ServeHTTP(w, r)
}
