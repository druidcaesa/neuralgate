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
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

// CodeFeatureLocked 业务码：请求的功能未授权（企业版）。取值与沿用 HTTP status 值的错误码
// (400/403/409/500) 错开，供前端唯一识别以弹「升级企业版」提示而非普通错误。
const CodeFeatureLocked = 4030

// ctxBizCode 错误响应的业务码在 gin.Context 中的键,供访问日志中间件读取
const ctxBizCode = "ng_admin_biz_code"

// Response 统一响应格式
type Response struct {
	Code    int         `json:"code"`    // 0=成功,非0=错误码
	Message string      `json:"message"` // 成功或错误描述
	Data    interface{} `json:"data,omitempty"`
}

// OK 成功响应
func OK(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, Response{Code: 0, Message: "ok", Data: data})
}

// Error 错误响应(status 为 HTTP 状态码,code 为业务错误码)。
// 除写响应体外,同时把业务码存入 context、把 message 以公开错误类型记入 c.Errors,
// 供访问日志中间件统一读取
func Error(c *gin.Context, status, code int, message string) {
	if c != nil {
		c.Set(ctxBizCode, code)
		_ = c.Error(errors.New(message)).SetType(gin.ErrorTypePublic)
	}
	c.JSON(status, Response{Code: code, Message: message})
}

// ErrorCause 同 Error,额外把底层原因以私有错误类型记入 c.Errors。
// cause 只用于日志,不下发浏览器;cause 为 nil 时退化为 Error 的行为
func ErrorCause(c *gin.Context, status, code int, message string, cause error) {
	if c != nil {
		c.Set(ctxBizCode, code)
		_ = c.Error(errors.New(message)).SetType(gin.ErrorTypePublic)
		if cause != nil {
			_ = c.Error(cause).SetType(gin.ErrorTypePrivate)
		}
	}
	c.JSON(status, Response{Code: code, Message: message})
}
