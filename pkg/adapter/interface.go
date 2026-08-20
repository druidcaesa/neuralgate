// Package adapter 定义模型适配器契约：ModelAdapter 接口与统一请求/响应数据结构。
//
// 来源：NeuralGate_技术架构详细设计.md 第 4.7 节（数据结构）与第 8.3 节（接口增强版）。
// 统一格式兼容 OpenAI Chat Completions API，网关以该包作为适配器层与核心层的唯一契约边界。
package adapter

import "net/http"

// ModelAdapter 模型适配器接口
type ModelAdapter interface {
	// 适配器名称
	Name() string

	// 是否支持原生透传（上游协议与入口协议一致）
	// 返回true时，网关跳过TransformRequest，仅替换model字段后原样转发
	SupportsNativeProxy() bool

	// 将统一格式请求转换为供应商特定格式
	// SupportsNativeProxy()==true时此方法不会被调用
	TransformRequest(req *UnifiedRequest, rawBody []byte) (*http.Request, error)

	// 将供应商响应转换为统一格式（非流式）
	TransformResponse(resp *http.Response) (*UnifiedResponse, error)

	// 处理流式响应（SSE分片转换）
	// SupportsNativeProxy()==true时可原样透传
	TransformStreamChunk(chunk []byte) (*UnifiedSSEChunk, error)

	// 解析Token用量（非流式）
	ParseTokenUsage(resp *http.Response) (prompt, completion, total int)

	// 解析流式最后一个分片的Token用量
	ParseStreamUsage(chunk []byte) (prompt, completion, total int)

	// 提取错误信息
	ParseError(resp *http.Response) (code int, message string)
}

// UnifiedRequest 统一请求格式 — 兼容 OpenAI Chat Completions API 全部参数
type UnifiedRequest struct {
	// ===== 核心参数 =====
	Model    string    `json:"model"`    // 模型名称（必填）
	Messages []Message `json:"messages"` // 消息列表（必填）

	// ===== 采样控制 =====
	Temperature      *float64       `json:"temperature,omitempty"`       // 0-2，默认1
	TopP             *float64       `json:"top_p,omitempty"`             // 0-1，默认1（核采样）
	FrequencyPenalty *float64       `json:"frequency_penalty,omitempty"` // -2.0 到 2.0
	PresencePenalty  *float64       `json:"presence_penalty,omitempty"`  // -2.0 到 2.0
	LogitBias        map[string]int `json:"logit_bias,omitempty"`        // token_id -> bias(-100~100)
	Logprobs         *bool          `json:"logprobs,omitempty"`          // 是否返回logprobs
	TopLogprobs      *int           `json:"top_logprobs,omitempty"`      // 1-20，需logprobs=true

	// ===== 输出控制 =====
	MaxTokens           *int     `json:"max_tokens,omitempty"`            // 最大Token数（旧版）
	MaxCompletionTokens *int     `json:"max_completion_tokens,omitempty"` // 最大Token数（新版，reasoning模型）
	N                   *int     `json:"n,omitempty"`                     // 生成几个选项，默认1
	Stop                []string `json:"stop,omitempty"`                  // 停止词
	Seed                *int     `json:"seed,omitempty"`                  // 随机种子（可复现）

	// ===== 结构化输出 =====
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"` // 结构化输出

	// ===== 流式控制 =====
	Stream        bool           `json:"stream,omitempty"`         // 是否流式
	StreamOptions *StreamOptions `json:"stream_options,omitempty"` // 流式选项

	// ===== 工具调用 (Function Calling) =====
	Tools             []Tool      `json:"tools,omitempty"`               // 工具定义列表
	ToolChoice        interface{} `json:"tool_choice,omitempty"`         // "auto"/"none"/{type:function,...}
	ParallelToolCalls *bool       `json:"parallel_tool_calls,omitempty"` // 是否并行调用工具

	// ===== Reasoning模型控制 =====
	ReasoningEffort *string `json:"reasoning_effort,omitempty"` // "low"/"medium"/"high"
	Verbosity       *string `json:"verbosity,omitempty"`        // "low"/"medium"/"high"

	// ===== 元信息 =====
	User           string `json:"user,omitempty"`             // 终端用户标识
	ServiceTier    string `json:"service_tier,omitempty"`     // "default"/"flex"/"priority"
	PromptCacheKey string `json:"prompt_cache_key,omitempty"` // 缓存命中键
	Store          *bool  `json:"store,omitempty"`            // 是否服务端留存

	// ===== 扩展参数（未识别参数透传） =====
	Extra map[string]interface{} `json:"-"` // 未知参数原样保留，适配供应商私有参数
}

// Message 消息结构 — 支持多模态内容
type Message struct {
	Role       string      `json:"role"`                   // system/developer/user/assistant/tool
	Content    interface{} `json:"content,omitempty"`      // string 或 []ContentPart（多模态）
	Name       string      `json:"name,omitempty"`         // 可选的说话者名称
	ToolCallID string      `json:"tool_call_id,omitempty"` // tool角色的关联ID
	ToolCalls  []ToolCall  `json:"tool_calls,omitempty"`   // assistant角色的工具调用
	Refusal    string      `json:"refusal,omitempty"`      // 模型拒绝内容
}

// ContentPart 多模态内容片段
type ContentPart struct {
	Type       string        `json:"type"` // "text"/"image_url"/"input_audio"
	Text       string        `json:"text,omitempty"`
	ImageURL   *ImageURLPart `json:"image_url,omitempty"`   // 图片
	InputAudio *AudioPart    `json:"input_audio,omitempty"` // 音频
}

type ImageURLPart struct {
	URL    string `json:"url"`              // http(s):// 或 data: URI
	Detail string `json:"detail,omitempty"` // "auto"/"low"/"high"
}

type AudioPart struct {
	Data   string `json:"data"`   // base64编码音频
	Format string `json:"format"` // "wav"/"mp3"
}

// Tool 工具定义
type Tool struct {
	Type     string       `json:"type"` // 目前仅 "function"
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters"`       // JSON Schema
	Strict      *bool                  `json:"strict,omitempty"` // 严格模式
}

// ToolCall 工具调用
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"` // "function"
	Function ToolCallFunction `json:"function"`
}

type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON字符串
}

// ResponseFormat 结构化输出
type ResponseFormat struct {
	Type       string              `json:"type"` // "text"/"json_object"/"json_schema"
	JSONSchema *ResponseJSONSchema `json:"json_schema,omitempty"`
}

type ResponseJSONSchema struct {
	Name   string                 `json:"name"`
	Schema map[string]interface{} `json:"schema"`
	Strict *bool                  `json:"strict,omitempty"`
}

// StreamOptions 流式选项
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage"` // 最后一个分片是否包含Token用量
}

// UnifiedResponse 统一响应格式 — 兼容 OpenAI 响应
type UnifiedResponse struct {
	ID                string      `json:"id"`
	Object            string      `json:"object"`  // "chat.completion"
	Created           int64       `json:"created"` // Unix时间戳
	Model             string      `json:"model"`
	Choices           []Choice    `json:"choices"`
	Usage             *TokenUsage `json:"usage,omitempty"`
	SystemFingerprint string      `json:"system_fingerprint,omitempty"`
}

type Choice struct {
	Index        int             `json:"index"`
	Message      Message         `json:"message"`
	FinishReason string          `json:"finish_reason"` // "stop"/"length"/"tool_calls"/"content_filter"
	Logprobs     *LogprobsResult `json:"logprobs,omitempty"`
}

type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type LogprobsResult struct {
	Content []LogprobContent `json:"content,omitempty"`
	Refusal []LogprobContent `json:"refusal,omitempty"`
}

type LogprobContent struct {
	Token       string            `json:"token"`
	Logprob     float64           `json:"logprob"`
	Bytes       []int             `json:"bytes,omitempty"`
	TopLogprobs []LogprobTokenAlt `json:"top_logprobs,omitempty"`
}

type LogprobTokenAlt struct {
	Token   string  `json:"token"`
	Logprob float64 `json:"logprob"`
}

// UnifiedSSEChunk 统一SSE流式分片 — 兼容 OpenAI 流式格式
type UnifiedSSEChunk struct {
	ID                string      `json:"id"`
	Object            string      `json:"object"` // "chat.completion.chunk"
	Created           int64       `json:"created"`
	Model             string      `json:"model"`
	Choices           []SSEChoice `json:"choices"`
	Usage             *TokenUsage `json:"usage,omitempty"` // stream_options.include_usage=true时
	SystemFingerprint string      `json:"system_fingerprint,omitempty"`
}

type SSEChoice struct {
	Index        int     `json:"index"`
	Delta        Message `json:"delta"`         // 增量内容
	FinishReason *string `json:"finish_reason"` // nil=未结束, "stop"/"length"/"tool_calls"
}
