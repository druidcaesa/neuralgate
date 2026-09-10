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

package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestErrorRecordsBizCodeAndMessage 错误响应除写响应体外,须把业务码存入 context、
// 把 message 以 gin 公开错误类型塞入 c.Errors,供访问日志中间件读取
func TestErrorRecordsBizCodeAndMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	Error(c, http.StatusForbidden, CodeFeatureLocked, "该功能需要企业版授权")

	if rec.Code != http.StatusForbidden {
		t.Fatalf("状态码应 %d, got %d", http.StatusForbidden, rec.Code)
	}
	var resp Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Code != CodeFeatureLocked || resp.Message != "该功能需要企业版授权" {
		t.Errorf("响应体不符: code=%d message=%s", resp.Code, resp.Message)
	}

	v, ok := c.Get(ctxBizCode)
	if !ok {
		t.Fatal("业务码应存入 context")
	}
	if code, ok := v.(int); !ok || code != CodeFeatureLocked {
		t.Errorf("context 业务码应 %d, got %v", CodeFeatureLocked, v)
	}

	if len(c.Errors) != 1 {
		t.Fatalf("应记录 1 条 gin 错误, got %d", len(c.Errors))
	}
	if !c.Errors[0].IsType(gin.ErrorTypePublic) {
		t.Error("message 应以公开错误类型记录")
	}
	if c.Errors[0].Err.Error() != "该功能需要企业版授权" {
		t.Errorf("gin 错误内容不符: %s", c.Errors[0].Err.Error())
	}
}

// TestErrorCauseKeepsCauseOutOfResponse 底层原因只进 c.Errors(供日志),不得下发浏览器
func TestErrorCauseKeepsCauseOutOfResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	cause := errors.New("sql: database is locked")
	ErrorCause(c, http.StatusInternalServerError, 500, "failed to list mcp servers", cause)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("状态码应 500, got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "database is locked") {
		t.Errorf("底层原因不得下发浏览器: %s", rec.Body.String())
	}
	var resp Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Message != "failed to list mcp servers" {
		t.Errorf("响应体应为通用文案, got %s", resp.Message)
	}

	if len(c.Errors) != 2 {
		t.Fatalf("应记录 2 条 gin 错误(公开文案 + 私有原因), got %d", len(c.Errors))
	}
	if !c.Errors[0].IsType(gin.ErrorTypePublic) {
		t.Error("第 1 条应为公开文案")
	}
	if !c.Errors[1].IsType(gin.ErrorTypePrivate) {
		t.Error("第 2 条应为私有原因")
	}
	if c.Errors[1].Err != cause {
		t.Error("私有错误应即传入的 cause 本身")
	}
}

// TestErrorCauseToleratesNilCause cause 为 nil 时只记录文案,不得 panic(c.Error(nil) 会 panic)
func TestErrorCauseToleratesNilCause(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	ErrorCause(c, http.StatusInternalServerError, 500, "failed to do thing", nil)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("状态码应 500, got %d", rec.Code)
	}
	if len(c.Errors) != 1 {
		t.Errorf("nil cause 时只应记录 1 条, got %d", len(c.Errors))
	}
}
