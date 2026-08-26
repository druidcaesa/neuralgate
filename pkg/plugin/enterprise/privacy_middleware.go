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

//go:build enterprise

package enterprise

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/core"
	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"go.uber.org/zap"
)

// maxInspectBody 单请求体检查上限（与路由中间件一致 1MB）
const maxInspectBody = 1 << 20

// securitySnippetMax 安全事件正文截断长度（字符）
const securitySnippetMax = 256

// NewPrivacyMiddleware 隐私防护中间件（挂固定链之后、代理内核之前）：
// 白名单豁免 → 注入命中 403+安全事件留痕+审计短路落库 → 请求侧脱敏转发；
// 响应侧包装对写出内容做替换（非流式整包，流式逐分片）
func NewPrivacyMiddleware(engine *PrivacyEngine, auditor plugin.AuditPipeline,
	storage plugin.StoragePlugin, logger *zap.Logger) core.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rc, ok := core.RequestContextFrom(r.Context())
			if !ok || !inspectable(r) {
				next.ServeHTTP(w, r)
				return
			}
			body, overflow := readBodyForInspect(r)
			if overflow {
				next.ServeHTTP(w, r) // 超限不检查不截断，原样放行
				return
			}

			if engine.Whitelisted(body) {
				next.ServeHTTP(w, r)
				return
			}
			if hit := engine.DetectInjection(body); hit != nil {
				blockInjected(w, rc, hit, body, storage, auditor, logger)
				return
			}

			sanitized, changed := engine.Sanitize(body, plugin.PrivacyScopeRequest)
			if changed {
				body = sanitized
				r.Body = io.NopCloser(bytes.NewReader(body))
				r.ContentLength = int64(len(body))
				r.Header.Set("Content-Length", strconv.Itoa(len(body)))
				rc.RequestBody = body // 转发与审计统一使用脱敏后文本
			}
			next.ServeHTTP(&sanitizeResponseWriter{
				ResponseWriter: w, engine: engine,
				rc: rc, storage: storage, logger: logger,
			}, r)
		})
	}
}

// blockInjected 注入命中收尾：安全事件入库 + 短路路径自行补审计(status=403) + 403 响应。
// 审计保留原始请求体以便追溯攻击载荷
func blockInjected(w http.ResponseWriter, rc *core.RequestContext, hit *plugin.PrivacyRule,
	body []byte, storage plugin.StoragePlugin, auditor plugin.AuditPipeline, logger *zap.Logger) {
	event := &plugin.SecurityEvent{
		RequestID: rc.RequestID,
		RuleName:  hit.Name,
		Snippet:   truncateRunes(string(body), securitySnippetMax),
		ClientIP:  rc.ClientIP,
		CreatedAt: time.Now(),
	}
	if rc.ModelConfig != nil {
		event.ModelName = rc.ModelConfig.ModelName
	}
	if err := storage.SaveSecurityEvent(event); err != nil {
		logger.Warn("安全事件留痕失败", zap.String("request_id", rc.RequestID), zap.Error(err))
	}
	if auditor != nil {
		modelName, provider := "", ""
		if rc.ModelConfig != nil {
			modelName, provider = rc.ModelConfig.ModelName, rc.ModelConfig.Provider
		}
		if err := auditor.Submit(&plugin.AuditEvent{
			RequestID: rc.RequestID,
			EventType: plugin.AuditEventRequestStart,
			Timestamp: rc.StartTime,
			Data: &plugin.AuditLog{
				ID: rc.RequestID, RequestID: rc.RequestID,
				TenantID: rc.TenantID, APIKeyID: rc.APIKeyID,
				ModelName: modelName, Provider: provider,
				RequestMethod: rc.RequestMethod, RequestPath: rc.RequestPath,
				RequestHeaders: rc.RequestHeaders, RequestBody: string(body),
				ClientIP: rc.ClientIP, CreatedAt: rc.StartTime,
			},
		}); err != nil {
			logger.Warn("审计事件投递失败", zap.String("request_id", rc.RequestID), zap.Error(err))
		}
		if err := auditor.Finalize(rc.RequestID, &plugin.AuditMeta{
			ResponseStatus: http.StatusForbidden,
			Duration:       time.Since(rc.StartTime).Milliseconds(),
		}); err != nil {
			logger.Warn("审计落库失败", zap.String("request_id", rc.RequestID), zap.Error(err))
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": "请求被安全策略拦截",
			"type":    "prompt_injection_blocked",
			"param":   nil,
			"code":    "prompt_injection_blocked",
		},
	})
}

// inspectable 判定是否需要内容检查：仅 JSON 文本端点
// （audio/images/files 为二进制/multipart，整读会损坏转发）
func inspectable(r *http.Request) bool {
	if r.Method == http.MethodGet {
		return false
	}
	return strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json")
}

// readBodyForInspect 读出待检查请求体并恢复 r.Body；
// 超上限时把已读前缀接回原流返回 overflow=true（调用方原样放行）
func readBodyForInspect(r *http.Request) (body []byte, overflow bool) {
	buf, _ := io.ReadAll(io.LimitReader(r.Body, maxInspectBody+1))
	if len(buf) > maxInspectBody {
		r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(buf), r.Body))
		return nil, true
	}
	r.Body = io.NopCloser(bytes.NewReader(buf))
	return buf, false
}

// truncateRunes 按字符截断（中文按 1 字符计，避免按字节截出半个 UTF-8）
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// sanitizeResponseWriter 响应侧包装：写出前按 response scope 规则替换。
// 流式 SSE 逐分片扫描（单分片内完整匹配，跨分片漏检为已知局限）；
// 替换导致长度变化时删除 Content-Length，交由 net/http 分块编码兜底。
// output 类 block 规则命中：未写过字节则回写 content_filter 错误体(200)，
// 已写过则丢弃剩余分片（流式已透传内容不可撤回，尽力而为）——均记一次安全事件
type sanitizeResponseWriter struct {
	http.ResponseWriter
	engine  *PrivacyEngine
	rc      *core.RequestContext
	storage plugin.StoragePlugin
	logger  *zap.Logger

	blocked    bool // 已进入拦截态：后续 Write 一律吞掉
	hasWritten bool
	eventSaved bool
}

func (w *sanitizeResponseWriter) Write(b []byte) (int, error) {
	if !w.blocked {
		if hit := w.engine.DetectOutput(b); hit != nil &&
			strings.EqualFold(hit.Action, plugin.PrivacyActionBlock) {
			w.enterBlocked(hit)
			return len(b), nil
		}
	} else {
		return len(b), nil
	}
	out, changed := w.engine.Sanitize(b, plugin.PrivacyScopeResponse)
	if changed {
		w.Header().Del("Content-Length")
		b = out
	}
	n, err := w.ResponseWriter.Write(b)
	if n > 0 || err == nil {
		w.hasWritten = true
	}
	return n, err
}

// enterBlocked 进入拦截态：安全事件留痕一次；尚无任何输出时回写错误体
func (w *sanitizeResponseWriter) enterBlocked(hit *plugin.PrivacyRule) {
	w.blocked = true
	if !w.eventSaved {
		w.eventSaved = true
		event := &plugin.SecurityEvent{
			RequestID: w.rc.RequestID,
			RuleName:  "output_blocked:" + hit.Name,
			ClientIP:  w.rc.ClientIP,
			CreatedAt: time.Now(),
		}
		if w.rc.ModelConfig != nil {
			event.ModelName = w.rc.ModelConfig.ModelName
		}
		if err := w.storage.SaveSecurityEvent(event); err != nil && w.logger != nil {
			w.logger.Warn("输出风控安全事件留痕失败",
				zap.String("request_id", w.rc.RequestID), zap.Error(err))
		}
	}
	if !w.hasWritten {
		w.Header().Del("Content-Length")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w.ResponseWriter).Encode(map[string]any{
			"error": map[string]any{
				"message": "响应内容被安全策略拦截",
				"type":    "content_filter",
				"param":   nil,
				"code":    "output_blocked",
			},
		})
		w.hasWritten = true
	}
}

// Flush 透传 Flusher（流式响应依赖）
func (w *sanitizeResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap 支持 http.ResponseController 穿透取底层 Writer（写截止时间等）
func (w *sanitizeResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
