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
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/mcp"
	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/google/uuid"
)

const (
	// MCPPathPrefix MCP 中继入口路径前缀（/v1/mcp/servers/{id}/mcp）
	MCPPathPrefix = "/v1/mcp/servers/"
	// mcpSessionTTL 会话空闲存活期：超时后客户端须重新 initialize
	mcpSessionTTL = 30 * time.Minute
	// mcpMaxBodyBytes 客户端请求体上限（超限拒绝，防止无界内存）
	mcpMaxBodyBytes = 1 << 20
	// mcpAuditCapBytes tool_arguments/tool_result 审计留存截断上限
	mcpAuditCapBytes = 1 << 20
)

// MCPRelay MCP Streamable HTTP 中继：会话校验、上游转发、响应原样回写、
// tools/call 旁路审计。通道本身 OSS+ 恒可用；hook 为 nil 时零审计开销
type MCPRelay struct {
	storage  plugin.StoragePlugin
	auditor  plugin.AuditPipeline // 可 nil；tools/call 同步提交常规数据面审计
	hook     plugin.MCPAuditHook  // 可 nil；enterprise 门控注入
	client   *http.Client
	sessions *sessionStore
	elapsed  func(start time.Time) int64 // 耗时计算（测试注入确定值）
}

// NewMCPRelay 创建中继；client 为 nil 时用 60s 超时的默认客户端
func NewMCPRelay(storage plugin.StoragePlugin, auditor plugin.AuditPipeline, hook plugin.MCPAuditHook, client *http.Client) *MCPRelay {
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	return &MCPRelay{
		storage:  storage,
		auditor:  auditor,
		hook:     hook,
		client:   client,
		sessions: newSessionStore(mcpSessionTTL),
		elapsed: func(start time.Time) int64 {
			return time.Since(start).Milliseconds()
		},
	}
}

// ServeHTTP 单入口分发：方法白名单 → 上游解析 → 帧解析 → 会话 → 转发
func (r *MCPRelay) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodPost, http.MethodDelete:
	default:
		mcp.WriteJSONRPCError(w, http.StatusMethodNotAllowed, nil, mcp.CodeInvalidRequest, "method not allowed")
		return
	}
	rc, ok := RequestContextFrom(req.Context())
	if !ok || rc == nil {
		// 直连中继（未过鉴权链，测试场景）：构造最小上下文保证审计字段可用
		rc = &RequestContext{RequestID: uuid.NewString(), StartTime: time.Now(), RequestPath: req.URL.Path}
	}
	serverID, okPath := parseMCPServerID(req.URL.Path)
	if !okPath {
		mcp.WriteJSONRPCError(w, http.StatusNotFound, nil, mcp.CodeInvalidRequest, "unknown mcp endpoint")
		return
	}
	srv, err := r.storage.GetMCPServer(serverID)
	if err != nil || !srv.Enabled {
		mcp.WriteJSONRPCError(w, http.StatusNotFound, nil, mcp.CodeInvalidRequest, "unknown mcp server")
		return
	}
	if req.Method == http.MethodDelete {
		r.handleDelete(w, req, srv)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, req.Body, mcpMaxBodyBytes))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			mcp.WriteJSONRPCError(w, http.StatusRequestEntityTooLarge, nil, mcp.CodeInvalidRequest, "request body too large")
		} else {
			mcp.WriteJSONRPCError(w, http.StatusBadRequest, nil, mcp.CodeInvalidRequest, "failed to read request body")
		}
		return
	}
	rpcReq, perr := mcp.ParseRequest(body)
	if perr != nil {
		code := mcp.CodeParseError
		if !errors.Is(perr, mcp.ErrParse) {
			code = mcp.CodeInvalidRequest
		}
		mcp.WriteJSONRPCError(w, http.StatusBadRequest, nil, code, perr.Error())
		return
	}

	var callParams mcp.ToolCallParams
	isToolCall := rpcReq.Method == mcp.MethodToolsCall && !mcp.IsNotification(rpcReq)
	if isToolCall {
		_ = json.Unmarshal(rpcReq.Params, &callParams) // 参数畸形按空处理，仍转发由上游裁决
	}

	var sess *mcpSession
	callerAgent := req.UserAgent()
	if rpcReq.Method == mcp.MethodInitialize {
		var initParams mcp.InitializeParams
		_ = json.Unmarshal(rpcReq.Params, &initParams)
		if initParams.ClientInfo.Name != "" {
			callerAgent = initParams.ClientInfo.Name
		}
	} else {
		s, ok := r.sessions.get(req.Header.Get("Mcp-Session-Id"), serverID, rc.APIKeyID)
		if !ok {
			mcp.WriteJSONRPCError(w, http.StatusNotFound, rpcReq.ID, mcp.CodeInvalidRequest, "session expired or unknown")
			return
		}
		sess = s
		callerAgent = sess.CallerAgent
	}

	if isToolCall && r.auditor != nil {
		r.auditor.Submit(&plugin.AuditEvent{
			RequestID: rc.RequestID,
			EventType: plugin.AuditEventRequestStart,
			Timestamp: rc.StartTime,
			Data: &plugin.AuditLog{
				ID: rc.RequestID, RequestID: rc.RequestID,
				RequestMethod: req.Method, RequestPath: req.URL.Path,
				ClientIP: rc.ClientIP, TenantID: rc.TenantID, APIKeyID: rc.APIKeyID,
				CreatedAt: rc.StartTime,
			},
		})
	}

	upResp, ferr := r.forward(req.Context(), srv, sess, body, rpcReq.ID, req.Header.Get("Accept"))
	if ferr != nil {
		mcp.WriteJSONRPCError(w, http.StatusBadGateway, rpcReq.ID, mcp.CodeInternalError, "upstream unavailable")
		if isToolCall {
			r.emitToolCall(rc, callerAgent, callParams, "", plugin.MCPStatusFailed, ferr.Error(), http.StatusBadGateway)
		}
		return
	}
	defer upResp.Body.Close()

	if ct := upResp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	if rpcReq.Method == mcp.MethodInitialize && upResp.StatusCode == http.StatusOK {
		gwSID := r.sessions.create(callerAgent, serverID, rc.APIKeyID)
		r.sessions.setUpstream(gwSID, upResp.Header.Get("Mcp-Session-Id"))
		w.Header().Set("Mcp-Session-Id", gwSID)
	}

	ct := upResp.Header.Get("Content-Type")
	if isToolCall && strings.Contains(ct, "text/event-stream") {
		r.relaySSE(w, upResp, rpcReq.ID, rc, callerAgent, callParams)
		return
	}

	// JSON 路径：旁路截断缓冲后原样写给客户端
	w.WriteHeader(upResp.StatusCode)
	tee := cappedBuffer{cap: mcpAuditCapBytes}
	_, copyErr := io.Copy(io.MultiWriter(w, &tee), upResp.Body)
	if isToolCall {
		final := parseFinalResponse(tee.buf.String(), rpcReq.ID)
		status, msg := auditStatusOf(final, upResp.StatusCode)
		if copyErr != nil {
			// 客户端中途断开：不再谎报 success
			status, msg = plugin.MCPStatusFailed, "client disconnected mid-response"
		}
		entry := r.buildToolCallEntry(rc, callerAgent, callParams,
			responseResultTextOf(final, tee.buf.String()), status, msg)
		entry.DurationMS = r.elapsed(rc.StartTime)
		if r.hook != nil {
			r.hook.OnToolCall(entry)
		}
		if r.auditor != nil {
			_ = r.auditor.Finalize(rc.RequestID, &plugin.AuditMeta{
				ResponseStatus: upResp.StatusCode,
				Duration:       entry.DurationMS,
			})
		}
	}
}

// handleDelete 终止网关会话并向上游代传 DELETE（忽略其响应内容），恒回 204
func (r *MCPRelay) handleDelete(w http.ResponseWriter, req *http.Request, srv *plugin.MCPServer) {
	sid := req.Header.Get("Mcp-Session-Id")
	sess, ok := r.sessions.get(sid, srv.ID, requestAPIKeyID(req))
	if ok {
		r.sessions.delete(sid)
		if dreq, err := http.NewRequestWithContext(req.Context(), http.MethodDelete, srv.Endpoint, nil); err == nil {
			mergeUpstreamHeaders(dreq, srv)
			if sess.UpstreamSessionID != "" {
				dreq.Header.Set("Mcp-Session-Id", sess.UpstreamSessionID)
			}
			if resp, err := r.client.Do(dreq); err == nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// forward 构造并执行上游请求：合并配置头与上游会话标识，透传 Accept（SSE 协商所需）
func (r *MCPRelay) forward(ctx context.Context, srv *plugin.MCPServer, sess *mcpSession,
	body []byte, id json.RawMessage, accept string) (*http.Response, error) {
	upReq, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	upReq.Header.Set("Content-Type", "application/json")
	if accept != "" {
		upReq.Header.Set("Accept", accept)
	}
	mergeUpstreamHeaders(upReq, srv)
	if sess != nil && sess.UpstreamSessionID != "" {
		upReq.Header.Set("Mcp-Session-Id", sess.UpstreamSessionID)
	}
	return r.client.Do(upReq)
}

// upstreamReservedHeaders 中继自身管理的协议头：管理员配置的
// srv.Headers 不得覆盖，防止会话/内容协商被配置项意外破坏
var upstreamReservedHeaders = map[string]bool{
	"Mcp-Session-Id": true,
	"Accept":         true,
	"Content-Type":   true,
	"Host":           true,
}

func mergeUpstreamHeaders(req *http.Request, srv *plugin.MCPServer) {
	for k, v := range srv.Headers {
		if upstreamReservedHeaders[k] || upstreamReservedHeaders[http.CanonicalHeaderKey(k)] {
			continue
		}
		req.Header.Set(k, v)
	}
}

// relaySSE 流式透传上游 SSE 帧给客户端，同时累积直至出现匹配 ID 的最终响应；
// 最终响应到达后继续透传剩余帧至 EOF（不阻塞客户端读流）
func (r *MCPRelay) relaySSE(w http.ResponseWriter, upResp *http.Response, targetID json.RawMessage,
	rc *RequestContext, callerAgent string, params mcp.ToolCallParams) {
	w.WriteHeader(upResp.StatusCode)
	reader := bufio.NewReader(upResp.Body)
	var final *mcp.RPCResponse
	for {
		payload, ok, err := mcp.NextSSEMessage(reader)
		if err != nil || !ok {
			break
		}
		if werr := mcp.WriteSSEMessage(w, payload); werr != nil {
			break // 客户端断开：不再累积审计（信息不足），交由数据面断连语义
		}
		if final == nil {
			var rr mcp.RPCResponse
			if jerr := json.Unmarshal(payload, &rr); jerr == nil &&
				strings.TrimSpace(string(rr.ID)) == strings.TrimSpace(string(targetID)) {
				cp := rr
				final = &cp
			}
		}
	}
	if final == nil {
		// 未拿到最终响应即结束(客户端断连/上游截断)：回收常规审计挂起项，
		// 避免 pending 泄漏至进程退出；信息不足不落工具调用审计(跳过 hook)
		if r.auditor != nil {
			_ = r.auditor.MarkDisconnect(rc.RequestID, "upstream stream ended without final response", nil)
		}
		return
	}
	entry := r.buildToolCallEntry(rc, callerAgent, params, responseResultText(final),
		responseStatusOf(final), "")
	entry.DurationMS = r.elapsed(rc.StartTime)
	if r.hook != nil {
		r.hook.OnToolCall(entry)
	}
	if r.auditor != nil {
		_ = r.auditor.Finalize(rc.RequestID, &plugin.AuditMeta{
			ResponseStatus: upResp.StatusCode,
			Duration:       entry.DurationMS,
		})
	}
}

// emitToolCall 传输层失败路径的审计出口（无最终响应可解析）
func (r *MCPRelay) emitToolCall(rc *RequestContext, callerAgent string, params mcp.ToolCallParams,
	rawResult string, status string, errMsg string, httpStatus int) {
	entry := r.buildToolCallEntry(rc, callerAgent, params, rawResult, status, errMsg)
	if r.hook != nil {
		r.hook.OnToolCall(entry)
	}
	if r.auditor != nil {
		_ = r.auditor.Finalize(rc.RequestID, &plugin.AuditMeta{
			ResponseStatus: httpStatus,
			Duration:       entry.DurationMS,
		})
	}
}

// emitToolCallFromResponse JSON 路径的审计出口
func (r *MCPRelay) emitToolCallFromResponse(rc *RequestContext, callerAgent string, params mcp.ToolCallParams,
	final *mcp.RPCResponse, rawBody string, httpStatus int) {
	status, msg := plugin.MCPStatusSuccess, ""
	resultText := rawBody
	if final != nil {
		status = responseStatusOf(final)
		msg = responseErrorMessage(final)
		resultText = responseResultText(final)
	} else if httpStatus >= 300 {
		status, msg = plugin.MCPStatusFailed, "upstream returned non-2xx"
	}
	entry := r.buildToolCallEntry(rc, callerAgent, params, resultText, status, msg)
	entry.DurationMS = r.elapsed(rc.StartTime)
	if r.hook != nil {
		r.hook.OnToolCall(entry)
	}
	if r.auditor != nil {
		_ = r.auditor.Finalize(rc.RequestID, &plugin.AuditMeta{
			ResponseStatus: httpStatus,
			Duration:       entry.DurationMS,
		})
	}
}

// buildToolCallEntry 组装 PRD 3.9 十三字段（大文本截断标注）
func (r *MCPRelay) buildToolCallEntry(rc *RequestContext, callerAgent string, params mcp.ToolCallParams,
	resultText, status, errMsg string) *plugin.MCPAuditLog {
	return &plugin.MCPAuditLog{
		RequestID:     rc.RequestID,
		TenantID:      rc.TenantID,
		APIKeyID:      rc.APIKeyID,
		ToolName:      params.Name,
		ToolArguments: string(params.Arguments),
		ToolResult:    resultText,
		CallerAgent:   callerAgent,
		Status:        status,
		ErrorMessage:  errMsg,
		ClientIP:      rc.ClientIP,
		CreatedAt:     time.Now(),
	}
}

// parseFinalResponse 从完整响应体提取匹配 ID 的 JSON-RPC 响应（JSON 路径专用）
func parseFinalResponse(body string, targetID json.RawMessage) *mcp.RPCResponse {
	var rr mcp.RPCResponse
	if err := json.Unmarshal([]byte(body), &rr); err != nil {
		return nil
	}
	if len(targetID) > 0 && strings.TrimSpace(string(rr.ID)) != strings.TrimSpace(string(targetID)) {
		return nil
	}
	return &rr
}

func responseStatusOf(final *mcp.RPCResponse) string {
	if final.Error != nil {
		return plugin.MCPStatusFailed
	}
	var result struct {
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(final.Result, &result); err == nil && result.IsError {
		return plugin.MCPStatusFailed
	}
	return plugin.MCPStatusSuccess
}

// responseErrorMessage 提取失败原因：JSON-RPC error 取 message；
// isError 结果取首个 content 文本；成功响应恒返回空串（error_message 仅失败时有值）
func responseErrorMessage(final *mcp.RPCResponse) string {
	if final.Error != nil {
		return final.Error.Message
	}
	var result struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(final.Result, &result); err == nil && result.IsError && len(result.Content) > 0 {
		return result.Content[0].Text
	}
	return ""
}

func responseResultText(final *mcp.RPCResponse) string {
	if final.Error != nil {
		b, _ := json.Marshal(final.Error)
		return string(b)
	}
	return string(final.Result)
}

func truncateForAudit(s string) string {
	if len(s) <= mcpAuditCapBytes {
		return s
	}
	return s[:mcpAuditCapBytes] + "[truncated]"
}

// parseMCPServerID 解析 /v1/mcp/servers/{id}/mcp 中的 {id}；
// 缺少 /mcp 后缀或含多余路径段视为非法端点(false)
func parseMCPServerID(path string) (string, bool) {
	rest := strings.TrimPrefix(path, MCPPathPrefix)
	if !strings.HasSuffix(rest, "/mcp") {
		return "", false
	}
	id := strings.TrimSuffix(rest, "/mcp")
	if id == "" || strings.Contains(id, "/") {
		return "", false
	}
	return id, true
}

func requestAPIKeyID(req *http.Request) string {
	if rc, ok := RequestContextFrom(req.Context()); ok && rc != nil {
		return rc.APIKeyID
	}
	return ""
}

// cappedBuffer 有上限的字节缓冲：写满后丢弃后续内容（只用于审计截断）
type cappedBuffer struct {
	buf bytes.Buffer
	cap int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if room := c.cap - c.buf.Len(); room > 0 {
		if room < len(p) {
			c.buf.Write(p[:room])
		} else {
			c.buf.Write(p)
		}
	}
	return len(p), nil // 恒报告全量写入，不影响 io.Copy 的透传计数
}

func (c *cappedBuffer) String() string { return c.buf.String() }

// auditStatusOf 由最终响应与 HTTP 状态推导审计状态与失败原因
func auditStatusOf(final *mcp.RPCResponse, httpStatus int) (string, string) {
	if final != nil {
		return responseStatusOf(final), responseErrorMessage(final)
	}
	if httpStatus >= 300 {
		return plugin.MCPStatusFailed, "upstream returned non-2xx"
	}
	return plugin.MCPStatusSuccess, ""
}

// responseResultTextOf 最终响应文本；无法定位最终响应时退回原始体
func responseResultTextOf(final *mcp.RPCResponse, rawBody string) string {
	if final != nil {
		return responseResultText(final)
	}
	return rawBody
}
