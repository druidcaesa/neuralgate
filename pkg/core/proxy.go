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
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/adapter"
	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// ProxyCore 代理内核层：端点分类 → 本地响应或核心代理转发
type ProxyCore struct {
	pipeline *Pipeline
	registry *adapter.AdapterRegistry
}

// NewProxyCore 创建代理内核
func NewProxyCore(pipeline *Pipeline, registry *adapter.AdapterRegistry) *ProxyCore {
	return &ProxyCore{pipeline: pipeline, registry: registry}
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
		writeOpenAIError(w, http.StatusInternalServerError, "api_error", "internal_error", "internal error")
		return
	}

	switch {
	case r.URL.Path == "/v1/models":
		p.handleModelsList(w, rc)
	case strings.HasPrefix(r.URL.Path, "/v1/models/"):
		p.handleModelDetail(w, r, rc)
	case r.URL.Path == "/v1/chat/completions" || r.URL.Path == "/v1/embeddings":
		p.handleProxy(w, r, rc)
	default:
		// 透传端点（completions/moderations/images/audio/files 等）
		p.handlePassThrough(w, r, rc)
	}
}

// handleModelsList GET /v1/models：返回启用模型列表（本地响应）
func (p *ProxyCore) handleModelsList(w http.ResponseWriter, rc *RequestContext) {
	models, _, err := p.pipeline.storage.ListModelConfigs(1, 1000)
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "api_error", "internal_error", "failed to list models")
		return
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

// handleProxy 核心代理：chat/completions 与 embeddings
// 流程: 原生透传(替换model) 或 适配器转换 → 超时重试转发 → 非流式写回+审计
func (p *ProxyCore) handleProxy(w http.ResponseWriter, r *http.Request, rc *RequestContext) {
	if rc.ModelConfig == nil || rc.Adapter == nil {
		writeOpenAIError(w, http.StatusInternalServerError, "api_error", "internal_error", "routing context missing")
		return
	}
	cfg := rc.ModelConfig
	adpt := rc.Adapter

	// 0. 审计:请求开始(携带基础元数据)
	rc.IsStream = isStreamRequest(r)
	if p.pipeline.auditor != nil {
		_ = p.pipeline.auditor.Submit(&plugin.AuditEvent{
			RequestID: rc.RequestID,
			EventType: plugin.AuditEventRequestStart,
			Timestamp: rc.StartTime,
			Data: &plugin.AuditLog{
				ID: rc.RequestID, RequestID: rc.RequestID,
				TenantID: rc.TenantID, APIKeyID: rc.APIKeyID,
				ModelName: cfg.ModelName, Provider: cfg.Provider,
				RequestMethod: rc.RequestMethod, RequestPath: rc.RequestPath,
				RequestHeaders: rc.RequestHeaders, RequestBody: string(rc.RequestBody),
				ClientIP: rc.ClientIP, IsStream: rc.IsStream,
				CreatedAt: rc.StartTime,
			},
		})
	}

	// 1. 构造上游请求
	upstreamURL := strings.TrimRight(cfg.BaseURL, "/") + r.URL.Path
	var outbound *http.Request
	var err error
	if adpt.SupportsNativeProxy() {
		outbound, err = p.buildNativeRequest(r, upstreamURL, cfg)
	} else {
		outbound, err = p.buildConvertedRequest(r, upstreamURL, cfg, adpt)
	}
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "bad_request", err.Error())
		return
	}

	// 2. 转发(重试)
	resp, err := p.doWithRetry(outbound, cfg)
	if err != nil {
		writeOpenAIError(w, http.StatusGatewayTimeout, "api_error", "upstream_timeout", "upstream timeout or unreachable: "+err.Error())
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
		writeOpenAIError(w, http.StatusBadGateway, "api_error", "upstream_error", msg)
		return
	}

	// 4. 非流式响应
	// Token 用量:adapter 内部读取并恢复 body,须在 io.ReadAll 之前调用
	prompt, completion, total := adpt.ParseTokenUsage(resp)
	rc.PromptTokens, rc.CompletionTokens, rc.TotalTokens = prompt, completion, total

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "api_error", "upstream_error", "failed to read upstream response")
		return
	}
	rc.ResponseBody = body
	rc.ResponseStatus = resp.StatusCode
	p.updateQuota(rc)

	// 写回客户端(透传上游响应头)
	copyResponseHeaders(w.Header(), resp.Header)
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
	rc.EndTime = time.Now()
	p.finalizeAudit(rc, prompt, completion, total)
}

// isStreamRequest 判断流式请求(请求体 stream=true 或 rc 已标记流式)
func isStreamRequest(r *http.Request) bool {
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

// buildNativeRequest 原生透传:仅替换 model 字段,raw body 原样转发
// (map 序列化会改变字段顺序,上游不敏感,可接受)
func (p *ProxyCore) buildNativeRequest(r *http.Request, upstreamURL string, cfg *plugin.ModelConfig) (*http.Request, error) {
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
	return p.newUpstreamRequest(r, upstreamURL, cfg, newBody)
}

// buildConvertedRequest 非原生适配器:TransformRequest 转换
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
	return p.attachUpstreamAuth(outbound, cfg)
}

// newUpstreamRequest 组装上游请求(URL/方法/头/上游Key)
func (p *ProxyCore) newUpstreamRequest(r *http.Request, upstreamURL string, cfg *plugin.ModelConfig, body []byte) (*http.Request, error) {
	outbound, err := http.NewRequest(r.Method, upstreamURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	return p.attachUpstreamAuth(outbound, cfg)
}

// attachUpstreamAuth 设置上游鉴权与 Content-Type
func (p *ProxyCore) attachUpstreamAuth(req *http.Request, cfg *plugin.ModelConfig) (*http.Request, error) {
	req.Header.Set("Content-Type", "application/json")
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

// doWithRetry 转发并重试(连接错误/5xx 重试 MaxRetries 次,4xx 不重试)
// 5xx 重试耗尽后返回最后一次响应(含 body),由调用方透传错误(502)
func (p *ProxyCore) doWithRetry(req *http.Request, cfg *plugin.ModelConfig) (*http.Response, error) {
	timeout := time.Duration(cfg.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	client := &http.Client{Timeout: timeout}
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

// updateQuota 回补 API Key 已用额度
func (p *ProxyCore) updateQuota(rc *RequestContext) {
	if rc.APIKeyID == "" {
		return
	}
	if key, err := p.pipeline.storage.GetAPIKeyByID(rc.APIKeyID); err == nil && key.Quota >= 0 {
		_ = p.pipeline.storage.UpdateAPIKeyQuota(key.ID, key.UsedQuota+int64(rc.TotalTokens))
	}
}

// finalizeAudit 审计 Finalize
func (p *ProxyCore) finalizeAudit(rc *RequestContext, prompt, completion, total int) {
	if p.pipeline.auditor == nil {
		return
	}
	_ = p.pipeline.auditor.Finalize(rc.RequestID, &plugin.AuditMeta{
		ResponseStatus:   rc.ResponseStatus,
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      total,
		Duration:         rc.EndTime.Sub(rc.StartTime).Milliseconds(),
	})
}

// handlePassThrough 透传端点:原样转发(不解析 body,仅替换上游 Key)
func (p *ProxyCore) handlePassThrough(w http.ResponseWriter, r *http.Request, rc *RequestContext) {
	if rc.ModelConfig == nil {
		writeOpenAIError(w, http.StatusNotFound, "invalid_request_error", "model_not_found", "model not found")
		return
	}
	cfg := rc.ModelConfig
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "bad_request", "failed to read body")
		return
	}
	upstreamURL := strings.TrimRight(cfg.BaseURL, "/") + r.URL.Path
	outbound, err := http.NewRequest(r.Method, upstreamURL, bytes.NewReader(body))
	if err != nil {
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
	if err != nil {
		writeOpenAIError(w, http.StatusGatewayTimeout, "api_error", "upstream_timeout", "upstream timeout: "+err.Error())
		return
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "api_error", "upstream_error", "failed to read upstream response")
		return
	}
	rc.ResponseStatus = resp.StatusCode
	copyResponseHeaders(w.Header(), resp.Header)
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(respBody)
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
