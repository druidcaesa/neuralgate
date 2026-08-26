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

package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestParseRequestRoundTrip 合法请求解析后字段逐一保留
func TestParseRequestRoundTrip(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"search","arguments":{"q":"go"}}}`)
	req, err := ParseRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if req.JSONRPC != "2.0" || req.Method != MethodToolsCall {
		t.Fatalf("字段丢失: %+v", req)
	}
	if string(req.ID) != "7" {
		t.Errorf("ID 应原样保留, got %s", req.ID)
	}
	var params ToolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		t.Fatal(err)
	}
	if params.Name != "search" || !bytes.Contains(params.Arguments, []byte(`"go"`)) {
		t.Errorf("params 解析不符: %s %s", params.Name, params.Arguments)
	}
}

// TestParseRequestStringID 字符串型 ID 同样保留(MCP 客户端常用)
func TestParseRequestStringID(t *testing.T) {
	req, err := ParseRequest([]byte(`{"jsonrpc":"2.0","id":"abc-1","method":"ping"}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(req.ID) != `"abc-1"` {
		t.Errorf("字符串 ID 应含引号原样保留, got %s", req.ID)
	}
}

// TestParseRequestBadJSON 非法 JSON 归类 Parse 错误(-32700 语义)
func TestParseRequestBadJSON(t *testing.T) {
	if _, err := ParseRequest([]byte(`{nope`)); !errors.Is(err, ErrParse) {
		t.Errorf("应返回 ErrParse, got %v", err)
	}
}

// TestParseRequestMissingMethod 缺 method / 顶层数组归类 InvalidRequest(-32600 语义)
func TestParseRequestMissingMethod(t *testing.T) {
	if _, err := ParseRequest([]byte(`{"jsonrpc":"2.0","id":1}`)); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("缺 method 应 ErrInvalidRequest, got %v", err)
	}
	if _, err := ParseRequest([]byte(`[1,2]`)); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("顶层数组应 ErrInvalidRequest, got %v", err)
	}
}

// TestIsNotification ID 缺失或 null 均为通知
func TestIsNotification(t *testing.T) {
	notice, err := ParseRequest([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	if err != nil {
		t.Fatal(err)
	}
	withNull, err := ParseRequest([]byte(`{"jsonrpc":"2.0","id":null,"method":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	normal, err := ParseRequest([]byte(`{"jsonrpc":"2.0","id":1,"method":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !IsNotification(notice) || !IsNotification(withNull) {
		t.Error("缺 ID 或 null ID 都应是通知")
	}
	if IsNotification(normal) {
		t.Error("带 ID 的不是通知")
	}
}

// TestNewErrorResponse 错误响应序列化形状
func TestNewErrorResponse(t *testing.T) {
	resp := NewErrorResponse(json.RawMessage(`5`), CodeInvalidParams, "bad params")
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{`"jsonrpc":"2.0"`, `"id":5`, `"code":-32602`, `"message":"bad params"`} {
		if !strings.Contains(got, want) {
			t.Errorf("错误体缺少 %s: %s", want, got)
		}
	}
}

// TestWriteJSONRPCError HTTP 状态与 body 同时落位
func TestWriteJSONRPCError(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteJSONRPCError(rec, 404, json.RawMessage(`1`), CodeInvalidRequest, "unknown mcp server")
	if rec.Code != 404 {
		t.Errorf("状态码应为 404, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"code":-32600`) || !strings.Contains(body, "unknown mcp server") {
		t.Errorf("body 不符: %s", body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type 应为 application/json, got %s", ct)
	}
}
