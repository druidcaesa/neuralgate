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
	"context"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/adapter"
	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// RequestContext 贯穿整个请求生命周期的上下文（照设计文档 3.1）
type RequestContext struct {
	RequestID        string               // 全局唯一请求ID
	TenantID         string               // 租户ID
	APIKeyID         string               // API Key ID
	ModelConfig      *plugin.ModelConfig  // 匹配到的模型配置
	Adapter          adapter.ModelAdapter // 模型适配器实例
	StartTime        time.Time            // 请求开始时间
	ClientIP         string               // 客户端IP
	RequestMethod    string               // HTTP方法
	RequestPath      string               // 请求路径
	RequestHeaders   map[string]string    // 请求头
	RequestBody      []byte               // 请求体
	ResponseStatus   int                  // 响应状态码
	ResponseBody     []byte               // 响应体（非流式）
	SSEChunks        []plugin.SSEChunk    // SSE分片列表（流式）
	EndTime          time.Time            // 请求结束时间
	PromptTokens     int                  // Prompt Token数
	CompletionTokens int                  // Completion Token数
	TotalTokens      int                  // 总Token数
	Error            error                // 错误信息
	IsStream         bool                 // 是否流式请求
	Disconnected     bool                 // 客户端是否断开
}

// requestContextKey 中间件链传递 RequestContext 的 context key
type requestContextKey struct{}

// WithRequestContext 将 RequestContext 写入 context
func WithRequestContext(ctx context.Context, rc *RequestContext) context.Context {
	return context.WithValue(ctx, requestContextKey{}, rc)
}

// RequestContextFrom 从 context 取出 RequestContext
func RequestContextFrom(ctx context.Context) (*RequestContext, bool) {
	rc, ok := ctx.Value(requestContextKey{}).(*RequestContext)
	return rc, ok
}
