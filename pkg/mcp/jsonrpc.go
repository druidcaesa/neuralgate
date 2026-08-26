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

// Package mcp 提供 MCP(Streamable HTTP) 所需的窄协议层：JSON-RPC 2.0 消息
// 结构与 SSE 帧编解码。仅覆盖网关中继用到的协议面，不含完整 MCP SDK 抽象。
package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// 网关中继涉及的方法与通知名
const (
	MethodInitialize        = "initialize"
	NotificationInitialized = "notifications/initialized"
	MethodToolsList         = "tools/list"
	MethodToolsCall         = "tools/call"
	MethodPing              = "ping"
)

// JSON-RPC 2.0 标准错误码
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// 解析失败哨兵错误：调用方据此映射错误码与 HTTP 状态
var (
	ErrParse          = errors.New("mcp: parse error")
	ErrInvalidRequest = errors.New("mcp: invalid request")
)

// RPCError JSON-RPC 错误对象
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// RPCRequest 客户端→服务端请求；ID 用 RawMessage 兼容 string/number/null，
// 通知(无 ID 或 null)原样保留以便透传
type RPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// RPCResponse 服务端响应；Result 与 Error 二选一
type RPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// ToolCallParams tools/call 的参数形状
type ToolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// InitializeParams initialize 的参数形状(caller_agent 取 clientInfo.name)
type InitializeParams struct {
	ClientInfo struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"clientInfo"`
}

// InitializeResult initialize 的结果形状(serverInfo.name 供日志排障)
type InitializeResult struct {
	ServerInfo struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
}

// ParseRequest 解析单条 JSON-RPC 请求。非法 JSON 归类 ErrParse(-32700)；
// 顶层数组或缺失 method 归类 ErrInvalidRequest(-32600)。batch 帧不在支持面(YAGNI)
func ParseRequest(body []byte) (*RPCRequest, error) {
	var probe any
	if err := json.Unmarshal(body, &probe); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrParse, err)
	}
	if _, isObj := probe.(map[string]any); !isObj {
		return nil, fmt.Errorf("%w: 顶层必须是对象", ErrInvalidRequest)
	}
	var req RPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	if req.Method == "" {
		return nil, fmt.Errorf("%w: 缺少 method", ErrInvalidRequest)
	}
	return &req, nil
}

// IsNotification 无 ID 或 ID 为 null 的请求是通知，不期待响应
func IsNotification(r *RPCRequest) bool {
	return len(r.ID) == 0 || bytes.Equal(bytes.TrimSpace(r.ID), []byte("null"))
}

// NewErrorResponse 组装错误响应
func NewErrorResponse(id json.RawMessage, code int, msg string) *RPCResponse {
	return &RPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &RPCError{Code: code, Message: msg},
	}
}

// WriteJSONRPCError 以指定 HTTP 状态写出 JSON-RPC 错误体；
// 用于传输层故障(session 失效/上游不可达等)，与协议内 error 响应区分
func WriteJSONRPCError(w http.ResponseWriter, status int, id json.RawMessage, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body, _ := json.Marshal(NewErrorResponse(id, code, msg))
	_, _ = w.Write(body)
}
