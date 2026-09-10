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
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/adapter"
	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"go.uber.org/zap"
)

// 写截止策略：server 层不设统一写超时（无法兼顾长流式），
// 非流式按 上游超时+余量 设定，流式按分片间空闲滚动设定
const (
	minResponseWriteDeadline = 30 * time.Second // 非流式写截止下限
	responseWriteGrace       = 10 * time.Second // 非流式在上游超时外追加的余量
	streamIdleWriteDeadline  = 60 * time.Second // 流式相邻分片间允许的最大空闲
)

// 入口协议(写入 rc.Entry,由 proxyHandler 按路径判定)
const (
	EntryOpenAI    = "openai"
	EntryAnthropic = "anthropic"
)

// 上游协议(2×2 矩阵分支用)
const (
	protoAnthropic = "anthropic" // 上游为 Anthropic Messages
	protoOpenAI    = "openai"    // 原生透传族(openai/deepseek)
	protoOther     = "other"     // 其余需转换适配器(qwen/zhipu,仅 OpenAI 入口支持)
)

// upstreamProtocol 判定适配器承载的上游协议
func upstreamProtocol(a adapter.ModelAdapter) string {
	if adapter.IsAnthropicProtocol(a) {
		return protoAnthropic
	}
	if a.SupportsNativeProxy() {
		return protoOpenAI
	}
	return protoOther
}

// upstreamPath 上游端点:anthropic 上游一律 /v1/messages;
// anthropic 入口转 openai 上游用 /v1/chat/completions;其余沿用入口路径
func upstreamPath(r *http.Request, entry string, adpt adapter.ModelAdapter) string {
	if adapter.IsAnthropicProtocol(adpt) {
		return "/v1/messages"
	}
	if entry == EntryAnthropic {
		return "/v1/chat/completions"
	}
	return r.URL.Path
}

// ProxyCore 代理内核层：端点分类 → 本地响应或核心代理转发
type ProxyCore struct {
	pipeline *Pipeline
	registry *adapter.AdapterRegistry
	logger   *zap.Logger // 审计链路失败等静默路径的记录器（默认 Nop）
	breaker  *BreakerRegistry
}

// NewProxyCore 创建代理内核
func NewProxyCore(pipeline *Pipeline, registry *adapter.AdapterRegistry) *ProxyCore {
	return &ProxyCore{pipeline: pipeline, registry: registry, logger: zap.NewNop()}
}

// WithLogger 注入日志器（生产装配调用；nil 忽略）
func (p *ProxyCore) WithLogger(l *zap.Logger) *ProxyCore {
	if l != nil {
		p.logger = l
	}
	return p
}

// WithBreaker 注入上游熔断注册表;nil=不启用(零行为变化)。须在服务启动前调用
func (p *ProxyCore) WithBreaker(b *BreakerRegistry) *ProxyCore {
	p.breaker = b
	return p
}

// Handler 返回经管道包装的代理入口
func (p *ProxyCore) Handler() http.Handler {
	return p.pipeline.Build(http.HandlerFunc(p.proxyHandler))
}

// proxyHandler 代理处理入口：端点分类
func (p *ProxyCore) proxyHandler(w http.ResponseWriter, r *http.Request) {
	// 健康检查
	if r.URL.Path == "/healthz" {
		writeHealthz(w)
		return
	}
	rc, ok := RequestContextFrom(r.Context())
	if !ok {
		writeEntryError(w, r, http.StatusInternalServerError, "api_error", "internal_error", "internal error")
		return
	}

	switch {
	case r.URL.Path == "/v1/models":
		p.handleModelsList(w, rc)
	case strings.HasPrefix(r.URL.Path, "/v1/models/"):
		p.handleModelDetail(w, r, rc)
	case r.URL.Path == "/v1/chat/completions" || r.URL.Path == "/v1/messages" || r.URL.Path == "/v1/embeddings":
		p.handleProxy(w, r, rc)
	default:
		// 透传端点:按 PRD 8.5 精确路由 + 方法校验
		methods, ok := matchPassthrough(r.URL.Path)
		if !ok {
			writeOpenAIError(w, http.StatusNotFound, "invalid_request_error", "model_not_found", "unknown endpoint: "+r.URL.Path)
			return
		}
		if !methodAllowed(methods, r.Method) {
			writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method_not_allowed", "method "+r.Method+" not allowed for "+r.URL.Path)
			return
		}
		p.handlePassThrough(w, r, rc)
	}
}

// handleModelsList GET /v1/models：返回启用模型列表（本地响应）
func (p *ProxyCore) handleModelsList(w http.ResponseWriter, rc *RequestContext) {
	// 翻页拉全量(存储层单页上限 100)
	var models []*plugin.ModelConfig
	page := 1
	for {
		pageModels, total, err := p.pipeline.storage.ListModelConfigs(page, 100)
		if err != nil {
			writeOpenAIError(w, http.StatusInternalServerError, "api_error", "internal_error", "failed to list models")
			return
		}
		models = append(models, pageModels...)
		if page*100 >= int(total) {
			break
		}
		page++
	}
	type modelItem struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		OwnedBy string `json:"owned_by"`
	}
	data := make([]modelItem, 0, len(models))
	for _, m := range models {
		if !m.Enabled {
			continue
		}
		data = append(data, modelItem{
			ID:      m.ModelName,
			Object:  "model",
			Created: m.CreatedAt.Unix(),
			OwnedBy: "neuralgate",
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"object": "list", "data": data})
}

// handleModelDetail GET /v1/models/{model}
func (p *ProxyCore) handleModelDetail(w http.ResponseWriter, r *http.Request, rc *RequestContext) {
	name := strings.TrimPrefix(r.URL.Path, "/v1/models/")
	config, err := p.pipeline.storage.GetModelConfig(name)
	if err != nil || !config.Enabled {
		writeOpenAIError(w, http.StatusNotFound, "invalid_request_error", "model_not_found", "model not found: "+name)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"id": config.ModelName, "object": "model",
		"created": config.CreatedAt.Unix(), "owned_by": "neuralgate",
	})
}

// handleProxy 核心代理：chat/completions、messages 与 embeddings(2×2:入口 × 上游协议)。
// 流程: 判定(入口,上游协议) → 原生透传/正反向转换 → 超时重试转发 → 非流式写回/流式劫持 + 审计
func (p *ProxyCore) handleProxy(w http.ResponseWriter, r *http.Request, rc *RequestContext) {
	if rc.ModelConfig == nil || rc.Adapter == nil {
		writeEntryError(w, r, http.StatusInternalServerError, "api_error", "internal_error", "routing context missing")
		return
	}
	cfg := rc.ModelConfig
	adpt := rc.Adapter

	// 入口协议:OpenAI 入口(/v1/chat/completions、/v1/embeddings)与 Anthropic 入口(/v1/messages)
	rc.Entry = EntryOpenAI
	if r.URL.Path == "/v1/messages" {
		rc.Entry = EntryAnthropic
	}
	entry := rc.Entry
	upProto := upstreamProtocol(adpt)

	// Anthropic 入口不支持 qwen/zhipu 等非 OpenAI 兼容上游(缺反转换能力,明确 400)
	if entry == EntryAnthropic && upProto == protoOther {
		rc.ResponseStatus = http.StatusBadRequest
		rc.EndTime = time.Now()
		p.finalizeAudit(rc, 0, 0, 0)
		writeEntryError(w, r, http.StatusBadRequest, "invalid_request_error", "entry_not_supported",
			"entry protocol Anthropic only supports OpenAI-compatible or Anthropic upstreams")
		return
	}

	// 负载均衡:熔断感知选路(覆盖默认 base_url/api_key,局部副本)
	sel, allBlocked := pickHealthy(rc.Upstreams, p.breaker)
	if allBlocked {
		rc.ResponseStatus = http.StatusServiceUnavailable
		rc.EndTime = time.Now()
		p.finalizeAudit(rc, 0, 0, 0)
		writeEntryError(w, r, http.StatusServiceUnavailable, "api_error", "upstream_unavailable",
			"all upstreams are temporarily unavailable")
		return
	}
	if sel != nil {
		cfgCopy := *cfg
		cfgCopy.BaseURL = sel.BaseURL
		cfgCopy.APIKey = sel.APIKey
		cfg = &cfgCopy
	}

	// 0. 审计:请求开始(携带基础元数据)
	rc.IsStream = isStreamRequest(r)
	if p.pipeline.auditor != nil {
		if err := p.pipeline.auditor.Submit(&plugin.AuditEvent{
			RequestID: rc.RequestID,
			EventType: plugin.AuditEventRequestStart,
			Timestamp: rc.StartTime,
			Data: &plugin.AuditLog{
				ID: rc.RequestID, RequestID: rc.RequestID,
				TenantID: rc.TenantID, APIKeyID: rc.APIKeyID,
				KeyMask:   rc.KeyMask,
				ModelName: cfg.ModelName, Provider: cfg.Provider,
				RequestMethod: rc.RequestMethod, RequestPath: rc.RequestPath,
				RequestHeaders: rc.RequestHeaders, RequestBody: string(rc.RequestBody),
				ClientIP: rc.ClientIP, IsStream: rc.IsStream,
				CreatedAt: rc.StartTime,
			},
		}); err != nil {
			p.logger.Warn("审计事件投递失败", zap.String("request_id", rc.RequestID), zap.Error(err))
		}
	}

	// 1. 构造上游请求(路径与鉴权随上游协议分路)
	upstreamURL := strings.TrimRight(cfg.BaseURL, "/") + upstreamPath(r, entry, adpt)
	var outbound *http.Request
	var err error
	switch {
	case upProto == protoAnthropic && entry == EntryAnthropic:
		// anthropic 入口 × anthropic 上游:原生透传(model 替换,anthropic 鉴权/路径)
		outbound, err = p.buildNativeRequest(r, upstreamURL, cfg, adpt)
	case upProto == protoAnthropic:
		// openai 入口 × anthropic 上游:openai body → 统一 → anthropic body(+默认 max_tokens)
		outbound, err = p.buildAnthropicForwardRequest(r, upstreamURL, cfg, adpt)
	case entry == EntryAnthropic:
		// anthropic 入口 × openai 兼容上游:反向转换(默认 max_tokens + include_usage)
		outbound, err = p.buildReverseRequest(r, upstreamURL, cfg, adpt)
	case adpt.SupportsNativeProxy():
		// openai 入口 × openai 原生透传族(现路径)
		outbound, err = p.buildNativeRequest(r, upstreamURL, cfg, adpt)
	default:
		// openai 入口 × qwen/zhipu 等转换适配器(现路径,无默认注入)
		outbound, err = p.buildConvertedRequest(r, upstreamURL, cfg, adpt)
	}
	if err != nil {
		p.releaseBreaker(sel) // 请求构造失败:释放已占试探槽,与 recordBreaker 二选一配对
		rc.ResponseStatus = http.StatusBadRequest
		rc.EndTime = time.Now()
		p.finalizeAudit(rc, 0, 0, 0)
		writeEntryError(w, r, http.StatusBadRequest, "invalid_request_error", "bad_request", err.Error())
		return
	}

	// 2. 转发(重试)
	resp, err := p.doWithRetry(outbound, cfg)
	p.recordBreaker(sel, err, respStatus(resp, err))
	if err != nil {
		rc.ResponseStatus = http.StatusGatewayTimeout
		rc.EndTime = time.Now()
		p.finalizeAudit(rc, 0, 0, 0)
		writeEntryError(w, r, http.StatusGatewayTimeout, "api_error", "upstream_timeout", "upstream timeout or unreachable: "+err.Error())
		return
	}
	defer resp.Body.Close()

	// 3. 上游错误(4xx/5xx) → 502 透传错误信息
	if resp.StatusCode >= 400 {
		code, msg := adpt.ParseError(resp)
		if code == 0 {
			code = resp.StatusCode
			msg = "upstream returned " + http.StatusText(resp.StatusCode)
		}
		rc.ResponseStatus = resp.StatusCode
		rc.EndTime = time.Now()
		p.finalizeAudit(rc, 0, 0, 0)
		writeEntryError(w, r, http.StatusBadGateway, "api_error", "upstream_error", msg)
		return
	}

	// 3.5 流式响应:劫持 SSE(按入口/上游协议分三种模式)
	if resp.StatusCode == http.StatusOK && isStreamRequest(r) {
		p.handleStreaming(w, r, rc, resp, cfg, adpt)
		return
	}

	// 4. 非流式响应
	// Token 用量:adapter 内部读取并恢复 body,须在 io.ReadAll 之前调用
	prompt, completion, total := adpt.ParseTokenUsage(resp)
	rc.PromptTokens, rc.CompletionTokens, rc.TotalTokens = prompt, completion, total

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		rc.ResponseStatus = http.StatusBadGateway
		rc.EndTime = time.Now()
		p.finalizeAudit(rc, 0, 0, 0)
		writeEntryError(w, r, http.StatusBadGateway, "api_error", "upstream_error", "failed to read upstream response")
		return
	}

	// 跨协议时把上游响应体转成客户端入口形状;同协议原样透传(现行为)
	writeBody := body
	switch {
	case upProto == protoAnthropic && entry == EntryOpenAI:
		// anthropic Message → openai chat.completion
		var ur *adapter.UnifiedResponse
		if ur, err = adapter.AnthropicParseResponse(body); err != nil {
			rc.ResponseStatus = http.StatusBadGateway
			rc.EndTime = time.Now()
			p.finalizeAudit(rc, 0, 0, 0)
			writeEntryError(w, r, http.StatusBadGateway, "api_error", "upstream_error", "failed to convert upstream response")
			return
		}
		writeBody, err = json.Marshal(ur)
	case upProto != protoAnthropic && entry == EntryAnthropic:
		// openai 兼容响应 → unified(OpenAI 形)→ anthropic Message
		var ur adapter.UnifiedResponse
		if err = json.Unmarshal(body, &ur); err != nil {
			rc.ResponseStatus = http.StatusBadGateway
			rc.EndTime = time.Now()
			p.finalizeAudit(rc, 0, 0, 0)
			writeEntryError(w, r, http.StatusBadGateway, "api_error", "upstream_error", "failed to convert upstream response")
			return
		}
		writeBody, err = adapter.AnthropicEncodeResponse(&ur, cfg.ProviderModel)
	}
	if err != nil {
		rc.ResponseStatus = http.StatusBadGateway
		rc.EndTime = time.Now()
		p.finalizeAudit(rc, 0, 0, 0)
		writeEntryError(w, r, http.StatusBadGateway, "api_error", "upstream_error", "failed to convert upstream response")
		return
	}
	rc.ResponseBody = writeBody
	rc.ResponseStatus = resp.StatusCode
	p.updateQuota(rc)
	p.recordTokens(rc)

	// 写回客户端(透传上游响应头)；先按请求设定写截止，防止慢客户端长期占住连接
	p.setNonStreamWriteDeadline(w, cfg)
	copyResponseHeaders(w.Header(), resp.Header)
	ct := resp.Header.Get("Content-Type")
	if !bytes.Equal(writeBody, body) { // 转换过形状:上游 Content-Type 未必适配,统一按 JSON
		ct = "application/json"
	}
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(writeBody)
	rc.EndTime = time.Now()
	p.finalizeAudit(rc, prompt, completion, total)
}

// setNonStreamWriteDeadline 非流式响应写截止 = max(下限, 上游超时+余量)。
// ResponseController 不支持时(如测试 Recorder)静默忽略
func (p *ProxyCore) setNonStreamWriteDeadline(w http.ResponseWriter, cfg *plugin.ModelConfig) {
	deadline := minResponseWriteDeadline
	if cfg != nil && cfg.Timeout+responseWriteGrace > deadline {
		deadline = cfg.Timeout + responseWriteGrace
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(deadline))
}

// isStreamRequest 判断流式请求(请求体 stream=true 或 Accept: text/event-stream)
func isStreamRequest(r *http.Request) bool {
	if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		return true
	}
	rc, ok := RequestContextFrom(r.Context())
	if ok && rc.IsStream {
		return true
	}
	var body struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(rcBody(r), &body)
	return body.Stream
}

// streamMode 流式转发模式(2×2 的流侧语义)
type streamMode int

const (
	streamPassthrough streamMode = iota // 原样转发上游 SSE 行(现路径 + anthropic 原生透传)
	streamDecode                        // anthropic 上游 SSE → openai 客户端 chunks(openai 入口 × anthropic)
	streamEncode                        // openai 上游 chunks → anthropic 事件(anthropic 入口 × openai)
)

// streamModeFor 按(入口,上游协议)选流式转发模式
func streamModeFor(rc *RequestContext, adpt adapter.ModelAdapter) streamMode {
	if adapter.IsAnthropicProtocol(adpt) {
		if rc.Entry == EntryAnthropic {
			return streamPassthrough // anthropic ↔ anthropic 原样
		}
		return streamDecode
	}
	if rc.Entry == EntryAnthropic {
		return streamEncode
	}
	return streamPassthrough
}

// streamRequestsUsage 客户端是否请求 OpenAI 流式用量尾块(stream_options.include_usage)
func streamRequestsUsage(r *http.Request) bool {
	var body struct {
		StreamOptions struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
	}
	_ = json.Unmarshal(rcBody(r), &body)
	return body.StreamOptions.IncludeUsage
}

// observeStreamUsage 流式 Token 用量:openai 系从 usage 尾块整取(一次性);
// anthropic 系按事件累计上报(observer,message_start→prompt、message_delta→completion,均累计值)
func (p *ProxyCore) observeStreamUsage(rc *RequestContext, adpt adapter.ModelAdapter, payload []byte) {
	if prompt, completion, total := adpt.ParseStreamUsage(payload); total > 0 {
		rc.PromptTokens, rc.CompletionTokens, rc.TotalTokens = prompt, completion, total
		return
	}
	if obs, ok := adpt.(adapter.StreamUsageObserver); ok {
		if prompt, completion, ok := obs.ObserveUsage(payload); ok {
			if prompt > 0 {
				rc.PromptTokens = prompt
			}
			if completion > 0 {
				rc.CompletionTokens = completion
			}
			rc.TotalTokens = rc.PromptTokens + rc.CompletionTokens
		}
	}
}

// handleStreaming 流式转发:按模式劫持分片写客户端 + 投递审计 + Finalize
func (p *ProxyCore) handleStreaming(w http.ResponseWriter, r *http.Request, rc *RequestContext, upstreamResp *http.Response, cfg *plugin.ModelConfig, adpt adapter.ModelAdapter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// 解除继承的整请求写截止：流式时长不受限，
	// 由分片间滚动写截止(空闲上限)与客户端断开共同约束
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})

	done := make(chan struct{})
	defer close(done)
	disconnect := NewDisconnectHandler(p.pipeline.auditor).WithLogger(p.logger)
	go disconnect.Watch(r.Context(), rc.RequestID, done, rc)

	scanner := bufio.NewScanner(upstreamResp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	rc.ResponseStatus = http.StatusOK

	mode := streamModeFor(rc, adpt)

	// 捕获一条写给客户端的 data 负载(审计=客户端侧线,与现透传口径一致)
	captureChunk := func(payload []byte) {
		chunk := plugin.SSEChunk{
			Index:     len(rc.SSEChunks),
			Data:      string(payload),
			Timestamp: time.Now(),
			EventType: "data",
		}
		rc.SSEChunks = append(rc.SSEChunks, chunk)
		if p.pipeline.auditor != nil {
			if err := p.pipeline.auditor.SubmitSSEChunk(rc.RequestID, &chunk); err != nil {
				p.logger.Warn("审计分片投递失败", zap.String("request_id", rc.RequestID), zap.Error(err))
			}
		}
	}
	// 写一条 data: 帧并捕获(转换流模式用);返回错误表示客户端断开
	writeFrame := func(payload []byte) error {
		if _, err := w.Write([]byte("data: ")); err != nil {
			return err
		}
		if _, err := w.Write(payload); err != nil {
			return err
		}
		if _, err := w.Write([]byte("\n\n")); err != nil {
			return err
		}
		captureChunk(payload)
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(streamIdleWriteDeadline))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		return nil
	}
	// 透传模式:逐行原样写(保留空行/event 行),滚动写截止随每行滚动
	writeLine := func(line string) error {
		if _, err := w.Write([]byte(line + "\n")); err != nil {
			return err
		}
		if f, ok := w.(http.Flusher); ok {
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(streamIdleWriteDeadline))
			f.Flush()
		}
		return nil
	}
	// 解析上游一行 data: 负载(非 data 行返回 nil)
	dataPayload := func(line string) []byte {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "data:") {
			return nil
		}
		payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
		if payload == "" {
			return nil
		}
		return []byte(payload)
	}

	clientGone := false // 客户端断连:不再补写收尾帧
	switch mode {
	case streamPassthrough:
		for scanner.Scan() {
			line := scanner.Text()
			if err := writeLine(line); err != nil {
				clientGone = true
				break // 客户端断开
			}
			if payload := dataPayload(line); payload != nil {
				captureChunk(payload)
				p.observeStreamUsage(rc, adpt, payload)
			}
		}

	case streamDecode:
		// 上游 anthropic 事件逐条解成 openai chunk;anthropic 流无 [DONE],
		// 由本模式在结束时补 usage 尾块(可选)与 [DONE]。
		dec := adapter.NewAnthropicStreamDecoder()
		var streamID, streamModel string
		for scanner.Scan() {
			payload := dataPayload(scanner.Text())
			if payload == nil {
				continue
			}
			var ev struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(payload, &ev)
			if chunks, err := dec.Decode(payload); err == nil {
				for i := range chunks {
					if chunks[i].ID != "" {
						streamID = chunks[i].ID
					}
					if chunks[i].Model != "" {
						streamModel = chunks[i].Model
					}
					b, _ := json.Marshal(chunks[i])
					if err := writeFrame(b); err != nil {
						clientGone = true
						break
					}
				}
			}
			p.observeStreamUsage(rc, adpt, payload)
			if clientGone || ev.Type == "message_stop" { // 收尾帧在循环后统一补
				break
			}
		}
		if !clientGone {
			if streamRequestsUsage(r) {
				tail := adapter.UnifiedSSEChunk{
					ID: streamID, Object: "chat.completion.chunk", Model: streamModel,
					Choices: []adapter.SSEChoice{},
					Usage: &adapter.TokenUsage{
						PromptTokens: rc.PromptTokens, CompletionTokens: rc.CompletionTokens, TotalTokens: rc.TotalTokens,
					},
				}
				b, _ := json.Marshal(tail)
				if err := writeFrame(b); err != nil {
					clientGone = true
				}
			}
			if !clientGone {
				if err := writeFrame([]byte("[DONE]")); err != nil {
					clientGone = true
				}
			}
		}

	case streamEncode:
		// 上游 openai chunks 逐条编码成 anthropic 事件;上游 [DONE] 触发 writer 收尾(message_stop)。
		// id 由请求 ID 派生,保证确定性(anthropic 消息 id 无格式约束)
		writer := adapter.NewAnthropicSSEWriter("msg_"+strings.ReplaceAll(rc.RequestID, "-", ""), cfg.ProviderModel)
		for scanner.Scan() {
			payload := dataPayload(scanner.Text())
			if payload == nil {
				continue
			}
			if string(payload) == "[DONE]" {
				if events, err := writer.Finish(); err == nil {
					for i := range events {
						if err := writeFrame(events[i]); err != nil {
							clientGone = true
							break
						}
					}
				}
				break
			}
			if events, err := writer.Process(payload); err == nil {
				for i := range events {
					if err := writeFrame(events[i]); err != nil {
						clientGone = true
						break
					}
				}
			}
			p.observeStreamUsage(rc, adpt, payload)
			if clientGone {
				break
			}
		}
	}

	// 上游读取错误或单行超长(ErrTooLong):先标记断连落库(Disconnected=true),再 Finalize 防重复。
	// 客户端断连会取消 r.Context() 从而中断上游读,此路径归因为 client_disconnected
	// (与 Watch goroutine 一致),避免二者竞态下真实断连被误标为 upstream read error
	if err := scanner.Err(); err != nil && p.pipeline.auditor != nil {
		reason := "upstream read error: " + err.Error()
		if r.Context().Err() != nil {
			reason = "client_disconnected"
		}
		if mErr := p.pipeline.auditor.MarkDisconnect(rc.RequestID, reason, &plugin.AuditMeta{
			ResponseStatus:   rc.ResponseStatus,
			PromptTokens:     rc.PromptTokens,
			CompletionTokens: rc.CompletionTokens,
			TotalTokens:      rc.TotalTokens,
			Duration:         time.Since(rc.StartTime).Milliseconds(),
		}); mErr != nil {
			p.logger.Warn("审计断连标记失败", zap.String("request_id", rc.RequestID), zap.Error(mErr))
		}
	}
	rc.EndTime = time.Now()
	p.updateQuota(rc)
	p.recordTokens(rc)
	p.finalizeAudit(rc, rc.PromptTokens, rc.CompletionTokens, rc.TotalTokens)
}

// buildNativeRequest 原生透传:仅替换 model 字段,raw body 原样转发
// (map 序列化会改变字段顺序,上游不敏感,可接受);鉴权随适配器(anthropic 用 x-api-key)
func (p *ProxyCore) buildNativeRequest(r *http.Request, upstreamURL string, cfg *plugin.ModelConfig, adpt adapter.ModelAdapter) (*http.Request, error) {
	raw := make([]byte, len(rcBody(r)))
	copy(raw, rcBody(r))
	// 替换 model 字段
	var bodyMap map[string]interface{}
	if err := json.Unmarshal(raw, &bodyMap); err != nil {
		return nil, err
	}
	bodyMap["model"] = cfg.ProviderModel
	newBody, err := json.Marshal(bodyMap)
	if err != nil {
		return nil, err
	}
	return p.newUpstreamRequest(r, upstreamURL, cfg, newBody, adpt)
}

// buildConvertedRequest 非原生适配器:TransformRequest 转换(qwen/zhipu,openai 入口现路径)
func (p *ProxyCore) buildConvertedRequest(r *http.Request, upstreamURL string, cfg *plugin.ModelConfig, adpt adapter.ModelAdapter) (*http.Request, error) {
	var unified adapter.UnifiedRequest
	if err := json.Unmarshal(rcBody(r), &unified); err != nil {
		return nil, err
	}
	outbound, err := adpt.TransformRequest(&unified, rcBody(r))
	if err != nil {
		return nil, err
	}
	parsed, err := url.Parse(upstreamURL)
	if err != nil {
		return nil, err
	}
	outbound.URL = parsed // 覆盖为上游地址
	return p.attachUpstreamAuth(outbound, cfg, adpt)
}

// buildAnthropicForwardRequest openai 入口 × anthropic 上游:openai body → 统一 → anthropic body。
// model 替换为 ProviderModel;客户端未带 max_tokens 时按模型默认注入(anthropic 必填)
func (p *ProxyCore) buildAnthropicForwardRequest(r *http.Request, upstreamURL string, cfg *plugin.ModelConfig, adpt adapter.ModelAdapter) (*http.Request, error) {
	var unified adapter.UnifiedRequest
	if err := json.Unmarshal(rcBody(r), &unified); err != nil {
		return nil, err
	}
	unified.Model = cfg.ProviderModel
	if unified.MaxTokens == nil && unified.MaxCompletionTokens == nil && cfg.MaxTokens > 0 {
		mt := cfg.MaxTokens
		unified.MaxTokens = &mt
	}
	outbound, err := adpt.TransformRequest(&unified, rcBody(r))
	if err != nil {
		return nil, err
	}
	parsed, err := url.Parse(upstreamURL)
	if err != nil {
		return nil, err
	}
	outbound.URL = parsed // 覆盖为上游地址
	return p.attachUpstreamAuth(outbound, cfg, adpt)
}

// buildReverseRequest anthropic 入口 × openai 兼容上游:Anthropic Messages body → 统一 → openai body。
// model 替换为 ProviderModel;默认 max_tokens 按需注入;流式置 stream_options.include_usage
// 以便取回上游用量(供 anthropic 流 message_delta 与审计)
func (p *ProxyCore) buildReverseRequest(r *http.Request, upstreamURL string, cfg *plugin.ModelConfig, adpt adapter.ModelAdapter) (*http.Request, error) {
	unified, err := adapter.ParseAnthropicRequest(rcBody(r))
	if err != nil {
		return nil, err
	}
	unified.Model = cfg.ProviderModel
	if unified.MaxTokens == nil && unified.MaxCompletionTokens == nil && cfg.MaxTokens > 0 {
		mt := cfg.MaxTokens
		unified.MaxTokens = &mt
	}
	if unified.Stream {
		unified.StreamOptions = &adapter.StreamOptions{IncludeUsage: true}
	}
	body, err := json.Marshal(unified)
	if err != nil {
		return nil, err
	}
	return p.newUpstreamRequest(r, upstreamURL, cfg, body, adpt)
}

// newUpstreamRequest 组装上游请求(URL/方法/头/上游Key)
func (p *ProxyCore) newUpstreamRequest(r *http.Request, upstreamURL string, cfg *plugin.ModelConfig, body []byte, adpt adapter.ModelAdapter) (*http.Request, error) {
	outbound, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	return p.attachUpstreamAuth(outbound, cfg, adpt)
}

// attachUpstreamAuth 设置上游鉴权与 Content-Type。
// anthropic 上游:去 Authorization,x-api-key + anthropic-version;其余现 Bearer
func (p *ProxyCore) attachUpstreamAuth(req *http.Request, cfg *plugin.ModelConfig, adpt adapter.ModelAdapter) (*http.Request, error) {
	req.Header.Set("Content-Type", "application/json")
	if adapter.IsAnthropicProtocol(adpt) {
		req.Header.Del("Authorization")
		req.Header.Set("x-api-key", cfg.APIKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		return req, nil
	}
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	return req, nil
}

// rcBody 从 RequestContext 取请求体(路由中间件已缓存)
func rcBody(r *http.Request) []byte {
	if rc, ok := RequestContextFrom(r.Context()); ok {
		return rc.RequestBody
	}
	return nil
}

// sharedUpstreamTransport 上游连接池单例：复用空闲连接，杜绝每请求新建握手。
// 各请求的超时差异由 per-request Client.Timeout 承担，传输层共享
var sharedUpstreamTransport = &http.Transport{
	MaxIdleConns:        200,
	MaxIdleConnsPerHost: 50,
	IdleConnTimeout:     90 * time.Second,
	TLSHandshakeTimeout: 10 * time.Second,
}

// doWithRetry 转发并重试。收敛后的重试语义：
//   - 连接类失败(未收到响应)：任何方法都可重试——请求未触达上游无副作用
//   - 已收到响应的 5xx：仅幂等方法(GET/HEAD)自动重试；POST 等
//     非幂等方法直接返回该响应（防重复副作用）
//
// 5xx 重试耗尽后返回最后一次响应(含 body),由调用方透传错误(502)
func (p *ProxyCore) doWithRetry(req *http.Request, cfg *plugin.ModelConfig) (*http.Response, error) {
	timeout := time.Duration(cfg.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	client := &http.Client{Timeout: timeout, Transport: sharedUpstreamTransport}
	bodyBytes, _ := io.ReadAll(req.Body) // 预先读出,供每次重试
	var lastResp *http.Response
	var lastErr error
	attempts := cfg.MaxRetries + 1
	if attempts < 1 {
		attempts = 1
	}
	for i := 0; i < attempts; i++ {
		if i > 0 {
			time.Sleep(time.Duration(cfg.RetryInterval) * time.Second)
		}
		attempt := req.Clone(req.Context())
		attempt.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		resp, err := client.Do(attempt)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode >= 500 {
			if req.Method != http.MethodGet && req.Method != http.MethodHead {
				// 非幂等方法不自动重试 5xx(防重复副作用),直接返回由调用方透传
				return resp, nil
			}
			lastErr = fmt.Errorf("upstream status %d", resp.StatusCode)
			// 保留最后一次 5xx 响应(含 body 供错误解析),释放之前的
			if lastResp != nil {
				_, _ = io.Copy(io.Discard, io.LimitReader(lastResp.Body, 1<<20))
				lastResp.Body.Close()
			}
			lastResp = resp
			continue
		}
		// 成功:释放重试过程中留下的 5xx 响应
		if lastResp != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(lastResp.Body, 1<<20))
			lastResp.Body.Close()
		}
		return resp, nil
	}
	if lastResp != nil {
		return lastResp, nil // 5xx 重试耗尽,交由调用方透传错误信息
	}
	if lastErr == nil {
		lastErr = errors.New("upstream request failed")
	}
	return nil, lastErr
}

// updateQuota 原子回补 API Key 已用额度(quota>=0 时)
// quota 条件判断用 Get(低频配置,窗口可接受),used_quota 累加用原子方法避免并发丢量
func (p *ProxyCore) updateQuota(rc *RequestContext) {
	if rc.APIKeyID == "" {
		return
	}
	if key, err := p.pipeline.storage.GetAPIKeyByID(rc.APIKeyID); err == nil && key.Quota >= 0 {
		_ = p.pipeline.storage.IncrementAPIKeyUsage(key.ID, int64(rc.TotalTokens))
	}
}

// finalizeAudit 审计 Finalize
func (p *ProxyCore) finalizeAudit(rc *RequestContext, prompt, completion, total int) {
	if p.pipeline.auditor == nil {
		return
	}
	if err := p.pipeline.auditor.Finalize(rc.RequestID, &plugin.AuditMeta{
		ResponseStatus:   rc.ResponseStatus,
		ResponseBody:     string(rc.ResponseBody),
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      total,
		Duration:         rc.EndTime.Sub(rc.StartTime).Milliseconds(),
	}); err != nil {
		p.logger.Error("审计日志落库失败", zap.String("request_id", rc.RequestID), zap.Error(err))
	}
}

// recordTokens 请求完成后回补限流器 TPM 计数(model 维度)
func (p *ProxyCore) recordTokens(rc *RequestContext) {
	if p.pipeline.rateLimiter == nil || rc.TotalTokens <= 0 {
		return
	}
	model := ""
	if rc.ModelConfig != nil {
		model = rc.ModelConfig.ModelName
	}
	_ = p.pipeline.rateLimiter.RecordTokens(rc.TenantID, model, rc.TotalTokens)
}

// handlePassThrough 透传端点:原样转发(不解析 body,仅替换上游 Key)
func (p *ProxyCore) handlePassThrough(w http.ResponseWriter, r *http.Request, rc *RequestContext) {
	cfg := rc.ModelConfig
	if cfg == nil {
		// GET 透传端点(如 /v1/files/:id)无 model 字段,取首个启用模型配置作为上游
		configs, _, err := p.pipeline.storage.ListModelConfigs(1, 100)
		if err == nil {
			for _, c := range configs {
				if c.Enabled && !c.APIKeyUnreadable {
					cfg = c
					break
				}
			}
		}
	}
	if cfg == nil {
		writeOpenAIError(w, http.StatusNotFound, "invalid_request_error", "model_not_found", "no enabled model configured")
		return
	}
	// 负载均衡:透传端点也支持多上游(选中则覆盖 base_url/api_key,局部副本)
	sel, allBlocked := pickHealthy(rc.Upstreams, p.breaker)
	if allBlocked {
		rc.ResponseStatus = http.StatusServiceUnavailable
		rc.EndTime = time.Now()
		p.finalizeAudit(rc, 0, 0, 0)
		writeOpenAIError(w, http.StatusServiceUnavailable, "api_error", "upstream_unavailable",
			"all upstreams are temporarily unavailable")
		return
	}
	if sel != nil {
		cfgCopy := *cfg
		cfgCopy.BaseURL = sel.BaseURL
		cfgCopy.APIKey = sel.APIKey
		cfg = &cfgCopy
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		p.releaseBreaker(sel) // 读体失败中止:释放已占试探槽,与 recordBreaker 二选一配对
		rc.ResponseStatus = http.StatusBadRequest
		rc.EndTime = time.Now()
		p.finalizeAudit(rc, 0, 0, 0)
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "bad_request", "failed to read body")
		return
	}
	// 0. 审计:请求开始(携带基础元数据;透传不解析 body 外的语义,model/provider 取自路由配置)
	if p.pipeline.auditor != nil {
		if err := p.pipeline.auditor.Submit(&plugin.AuditEvent{
			RequestID: rc.RequestID,
			EventType: plugin.AuditEventRequestStart,
			Timestamp: rc.StartTime,
			Data: &plugin.AuditLog{
				ID: rc.RequestID, RequestID: rc.RequestID,
				TenantID: rc.TenantID, APIKeyID: rc.APIKeyID,
				KeyMask:   rc.KeyMask,
				ModelName: cfg.ModelName, Provider: cfg.Provider,
				RequestMethod: rc.RequestMethod, RequestPath: rc.RequestPath,
				RequestHeaders: rc.RequestHeaders, RequestBody: string(body),
				ClientIP: rc.ClientIP, CreatedAt: rc.StartTime,
			},
		}); err != nil {
			p.logger.Warn("审计事件投递失败", zap.String("request_id", rc.RequestID), zap.Error(err))
		}
	}
	upstreamURL := strings.TrimRight(cfg.BaseURL, "/") + r.URL.Path
	outbound, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL, bytes.NewReader(body))
	if err != nil {
		p.releaseBreaker(sel) // 构造上游请求失败中止:释放已占试探槽,与 recordBreaker 二选一配对
		rc.ResponseStatus = http.StatusInternalServerError
		rc.EndTime = time.Now()
		p.finalizeAudit(rc, 0, 0, 0)
		writeOpenAIError(w, http.StatusInternalServerError, "api_error", "internal_error", err.Error())
		return
	}
	// 复制请求头(Content-Type 等),替换鉴权
	for k, vv := range r.Header {
		if k == "Authorization" {
			continue
		}
		for _, v := range vv {
			outbound.Header.Add(k, v)
		}
	}
	outbound.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	resp, err := p.doWithRetry(outbound, cfg)
	p.recordBreaker(sel, err, respStatus(resp, err))
	if err != nil {
		rc.ResponseStatus = http.StatusGatewayTimeout
		rc.EndTime = time.Now()
		p.finalizeAudit(rc, 0, 0, 0)
		writeOpenAIError(w, http.StatusGatewayTimeout, "api_error", "upstream_timeout", "upstream timeout: "+err.Error())
		return
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		rc.ResponseStatus = http.StatusBadGateway
		rc.EndTime = time.Now()
		p.finalizeAudit(rc, 0, 0, 0)
		writeOpenAIError(w, http.StatusBadGateway, "api_error", "upstream_error", "failed to read upstream response")
		return
	}
	rc.ResponseStatus = resp.StatusCode
	copyResponseHeaders(w.Header(), resp.Header)
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(respBody)
	rc.EndTime = time.Now()
	p.finalizeAudit(rc, 0, 0, 0)
}

// copyResponseHeaders 复制上游响应头到客户端
func copyResponseHeaders(dst, src http.Header) {
	for k, vv := range src {
		if k == "Content-Length" {
			continue
		}
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

// writeHealthz 健康检查响应体
func writeHealthz(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// openAIErrorBody OpenAI 错误响应体
type openAIErrorBody struct {
	Error openAIError `json:"error"`
}

type openAIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Param   any    `json:"param"`
	Code    string `json:"code"`
}

// writeOpenAIError 按 OpenAI 错误格式写响应
func writeOpenAIError(w http.ResponseWriter, status int, etype, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(openAIErrorBody{
		Error: openAIError{Message: message, Type: etype, Param: nil, Code: code},
	})
}

// anthropicErrorBody Anthropic 错误响应体
type anthropicErrorBody struct {
	Type  string       `json:"type"`
	Error anthropicErr `json:"error"`
}

type anthropicErr struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// writeAnthropicError 按 Anthropic 错误格式写响应
func writeAnthropicError(w http.ResponseWriter, status int, etype, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(anthropicErrorBody{
		Type:  "error",
		Error: anthropicErr{Type: etype, Message: message},
	})
}

// writeEntryError 按入口协议分形写错误:anthropic 入口写 Anthropic 形,其余写 OpenAI 形。
// r 为 nil(共享中间件无 rc 且无请求可用)时按 OpenAI 形兜底
func writeEntryError(w http.ResponseWriter, r *http.Request, status int, etype, code, message string) {
	if r != nil && r.URL.Path == "/v1/messages" {
		writeAnthropicError(w, status, etype, message)
		return
	}
	writeOpenAIError(w, status, etype, code, message)
}

// selectUpstream 按 weight 加权随机选一个 enabled 上游
// 空或全禁用返回 nil(调用方回退 ModelConfig 默认上游);权重和为 0 时等概率
func selectUpstream(ups []plugin.Upstream) *plugin.Upstream {
	enabled := make([]plugin.Upstream, 0, len(ups))
	total := 0
	for _, u := range ups {
		if u.Enabled {
			enabled = append(enabled, u)
			w := u.Weight
			if w < 0 {
				w = 0
			}
			total += w
		}
	}
	if len(enabled) == 0 {
		return nil
	}
	if len(enabled) == 1 {
		return &enabled[0]
	}
	if total <= 0 {
		// 权重全 0:等概率
		return &enabled[rand.Intn(len(enabled))]
	}
	r := rand.Intn(total)
	for i := range enabled {
		w := enabled[i].Weight
		if w < 0 {
			w = 0
		}
		if r < w {
			return &enabled[i]
		}
		r -= w
	}
	return &enabled[len(enabled)-1] // 兜底(浮点/边界)
}

// pickHealthy 熔断感知选路:用只读的 Selectable(不占试探槽)剔除 open 上游,再对加权随机的
// 选中者执行 Allow 占用槽位。reg 为 nil(特性关闭)时语义=原 selectUpstream。密钥不可解密
// (APIKey 为空)的上游一律不参与选路,全部如此时返回 (nil, false) 让调用方回退模型自身密钥;
// 存在可解密 enabled 上游但全被熔断/选中者的试探槽被并发占满 → (nil, true) 供调用方快速 503
func pickHealthy(ups []plugin.Upstream, reg *BreakerRegistry) (*plugin.Upstream, bool) {
	// 先统计是否存在可参与选路的候选(任一 enabled 且密钥可解密)
	hasEnabled := false
	for _, u := range ups {
		if u.Enabled && !u.APIKeyUnreadable {
			hasEnabled = true
			break
		}
	}
	if !hasEnabled {
		return nil, false // 无候选: 调用方回退默认上游(现行为)
	}
	if reg == nil {
		usable := make([]plugin.Upstream, 0, len(ups))
		for _, u := range ups {
			if u.Enabled && !u.APIKeyUnreadable {
				usable = append(usable, u)
			}
		}
		return selectUpstream(usable), false
	}
	available := make([]plugin.Upstream, 0, len(ups))
	for _, u := range ups {
		if u.Enabled && !u.APIKeyUnreadable && reg.Selectable(u.ID) {
			available = append(available, u)
		}
	}
	// select-then-Allow:先加权随机选中,再占用槽位;选中者刚被 open 或 half-open
	// 试探槽被并发占满时将其移出候选重选,循环至多 len(available) 轮
	for len(available) > 0 {
		sel := selectUpstream(available)
		if reg.Allow(sel.ID) {
			return sel, false
		}
		id := sel.ID
		next := make([]plugin.Upstream, 0, len(available)-1)
		for _, u := range available {
			if u.ID != id {
				next = append(next, u)
			}
		}
		available = next
	}
	return nil, true
}

// respStatus 取响应状态码;错误时为 0(供熔断失败判定)
func respStatus(resp *http.Response, err error) int {
	if err != nil || resp == nil {
		return 0
	}
	return resp.StatusCode
}

// recordBreaker 回写熔断结果:连通且状态<500 记成功,否则失败。sel 为 nil 且未走上游
// (blocked 分支已提前 return;此处仅 sel!=nil 时调用)为空操作
func (p *ProxyCore) recordBreaker(sel *plugin.Upstream, err error, status int) {
	if p.breaker == nil || sel == nil {
		return
	}
	ok := err == nil && status > 0 && status < 500
	p.breaker.Record(sel.ID, ok)
}

// releaseBreaker 释放已选上游未成行的试探槽:选路(Allow 占槽)后、转发(recordBreaker)前
// 中止的路径调用,与 recordBreaker 二选一配对,防止 half-open 槽位被泄漏。breaker/sel 为空即空操作
func (p *ProxyCore) releaseBreaker(sel *plugin.Upstream) {
	if p.breaker == nil || sel == nil {
		return
	}
	p.breaker.Release(sel.ID)
}

// passthroughEndpoints 透传端点 → 允许的 HTTP 方法集合(PRD 8.5)
var passthroughEndpoints = map[string][]string{
	"/v1/completions":          {http.MethodPost},
	"/v1/moderations":          {http.MethodPost},
	"/v1/images/generations":   {http.MethodPost},
	"/v1/images/edits":         {http.MethodPost},
	"/v1/images/variations":    {http.MethodPost},
	"/v1/audio/speech":         {http.MethodPost},
	"/v1/audio/transcriptions": {http.MethodPost},
	"/v1/audio/translations":   {http.MethodPost},
	"/v1/files":                {http.MethodGet, http.MethodPost},
}

// matchPassthrough 判断路径是否为透传端点,返回允许方法与是否命中
// 处理带路径参数的 files 子路径(/v1/files/{id}、/v1/files/{id}/content)
func matchPassthrough(path string) (methods []string, ok bool) {
	path = strings.TrimRight(path, "/")
	if m, exist := passthroughEndpoints[path]; exist {
		return m, true
	}
	if strings.HasPrefix(path, "/v1/files/") {
		if strings.HasSuffix(path, "/content") {
			return []string{http.MethodGet}, true
		}
		return []string{http.MethodGet, http.MethodDelete}, true
	}
	return nil, false
}

func methodAllowed(methods []string, m string) bool {
	for _, x := range methods {
		if x == m {
			return true
		}
	}
	return false
}
