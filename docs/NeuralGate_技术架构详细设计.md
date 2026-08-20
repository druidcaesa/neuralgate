# NeuralGate AI大模型治理网关 — 技术架构详细设计文档

> **文档版本**: V1.0  
> **技术栈**: Go 1.22+  
> **适用对象**: 后端开发工程师、架构师  
> **文档目的**: 开发者拿到本文档后可直接进入编码阶段，无需二次设计

---

## 目录

- [1. 架构总览](#1-架构总览)
- [2. 分层设计详解](#2-分层设计详解)
- [3. 核心数据结构定义](#3-核心数据结构定义)
- [4. 核心接口定义](#4-核心接口定义)
- [5. SSE异步审计架构详解](#5-sse异步审计架构详解)
- [6. 插件机制详解](#6-插件机制详解)
- [7. 编译隔离机制](#7-编译隔离机制)
- [8. 模型适配器架构](#8-模型适配器架构)
- [9. 项目目录结构](#9-项目目录结构)
- [10. 启动流程](#10-启动流程)
- [11. 请求全链路流程](#11-请求全链路流程)
- [12. 双仓库Git管理方案](#12-双仓库git管理方案)
- [13. 开发落地顺序](#13-开发落地顺序)

---

## 1. 架构总览

### 1.1 双服务隔离架构

单进程内运行两个完全隔离的HTTP服务，杜绝框架冲突与流量交叉：

| 服务 | 端口 | 框架 | 职责 | 流量特征 |
|------|------|------|------|----------|
| 代理服务 | 8080 | 纯 net/http | LLM流量代理、SSE流式劫持、反向代理、中间件链路 | 高并发、长连接、流式 |
| 管理后台 | 8081 | Gin | CRUD接口、配置管理、日志查询、授权校验 | 低并发、短连接 |

**核心约束**：
- 代理服务**禁止引入Gin/Echo等框架**，仅使用 `net/http` 原生能力
- 管理后台**不处理任何业务LLM流量**，仅做后台管理
- 两个服务共享同一进程内的插件实例和配置

### 1.2 四层固定分层架构

```
┌─────────────────────────────────────────────────────────────┐
│                     接入层 (Acceptor)                        │
│   连接管理 · TLS终止 · IP黑白名单 · 协议解析 · 长连接超时适配     │
├─────────────────────────────────────────────────────────────┤
│                管道中间件层 (Pipeline)                        │
│   鉴权 → 限流 → 路由匹配 → 模型协议转换 → 前置钩子             │
├─────────────────────────────────────────────────────────────┤
│                 代理内核层 (Proxy Core)                      │
│   ReverseProxy · SSE分片劫持 · 流式重组 · 异常补偿 · 模型转发   │
├─────────────────────────────────────────────────────────────┤
│                插件扩展层 (Plugin Layer)                     │
│   存储插件 · 审计流水线 · 限流插件 · 日志导出 · 授权插件        │
└─────────────────────────────────────────────────────────────┘
```

### 1.3 设计原则

| 原则 | 说明 |
|------|------|
| 接口契约化 | 所有扩展点通过接口定义，内核零硬编码厂商逻辑 |
| 编译隔离 | 使用 BuildTag 条件编译，一套源码两套产物，禁止 go plugin (.so) |
| 异步非阻塞 | 审计链路通过内存环形队列 + 独立worker池，主流量零阻塞 |
| Open-Core | 开源内核 + 商业插件，enterprise目录私有不公开 |
| 单二进制 | 零外部依赖，支持Docker/裸机部署 |

---

## 2. 分层设计详解

### 2.1 接入层 (Acceptor)

**职责**：管理入站连接，完成协议解析与安全过滤

**核心组件**：

| 组件 | 功能 | 关键参数 |
|------|------|----------|
| ConnectionManager | 连接生命周期管理 | 最大连接数、空闲超时 |
| TLSHandler | TLS终止 | 证书路径、最低TLS版本 |
| IPFilter | IP黑白名单 | 规则列表、默认策略 |
| ProtocolParser | HTTP协议解析 | 支持HTTP/1.1 Keep-Alive |

**关键设计**：
- 设置 `ReadTimeout` / `WriteTimeout` / `IdleTimeout` 三个超时
- SSE流式请求特殊处理：检测到 `Accept: text/event-stream` 时动态取消 WriteTimeout
- 使用 `http.Server` 的 `ConnState` 回调追踪连接状态

### 2.2 管道中间件层 (Pipeline)

**职责**：按固定顺序执行预处理链路

**固定执行顺序**（不可调换）：

```
请求进入 → 鉴权(Auth) → 限流(RateLimit) → 路由匹配(RouteMatch) → 协议转换(Adapter) → 前置钩子(PreHook) → 进入代理内核
```

**中间件接口**：

```go
type Middleware func(next http.Handler) http.Handler
```

**各阶段职责**：

| 阶段 | 输入 | 输出 | 异常处理 |
|------|------|------|----------|
| 鉴权 | API Key (Header/Query) | 租户上下文 | 401 Unauthorized |
| 限流 | 租户ID + 请求路径 | 通过/拒绝 | 429 Too Many Requests |
| 路由匹配 | 请求路径 + Method | 目标模型配置 | 404 Not Found |
| 协议转换 | 原始请求体 | 统一内部格式 | 400 Bad Request |
| 前置钩子 | 完整请求上下文 | 修改后请求 | 钩子自定义 |

### 2.3 代理内核层 (Proxy Core)

**职责**：执行反向代理、SSE流式劫持、异常补偿

**核心组件**：

| 组件 | 功能 |
|------|------|
| ReverseProxy | 基于 `httputil.ReverseProxy` 封装，转发请求到上游模型API |
| SSEHijacker | 自定义 `ResponseWriter`，劫持SSE分片数据 |
| StreamReassembler | 分片重组，生成完整应答 |
| DisconnectHandler | 监听客户端断开，触发断连补全 |
| ErrorCompensator | 异常场景下的日志补偿逻辑 |

**SSE劫持原理**：
1. 包装 `http.ResponseWriter` 为自定义 `SSEResponseWriter`
2. 拦截 `Write()` 调用，将数据同时写入客户端和环形队列
3. 拦截 `Flush()` 调用，确保分片实时推送
4. 监听 `r.Context().Done()`，触发断连补全

### 2.4 插件扩展层 (Plugin Layer)

**职责**：提供存储、审计、限流、导出、授权等可插拔能力

**架构**：所有插件通过接口定义 + BuildTag工厂实现，详见[第6节](#6-插件机制详解)

---

## 3. 核心数据结构定义

### 3.1 请求上下文

```go
// RequestContext 贯穿整个请求生命周期的上下文
type RequestContext struct {
    RequestID    string            // 全局唯一请求ID
    TenantID     string            // 租户ID
    APIKeyID     string            // API Key ID
    ModelConfig  *ModelConfig      // 匹配到的模型配置
    Adapter      ModelAdapter      // 模型适配器实例
    StartTime    time.Time         // 请求开始时间
    ClientIP     string            // 客户端IP
    RequestMethod string           // HTTP方法
    RequestPath    string          // 请求路径
    RequestHeaders map[string]string // 请求头
    RequestBody  []byte           // 请求体
    ResponseStatus int             // 响应状态码
    ResponseBody  []byte           // 响应体（非流式）
    SSEChunks     []SSEChunk       // SSE分片列表（流式）
    EndTime      time.Time         // 请求结束时间
    PromptTokens  int              // Prompt Token数
    CompletionTokens int           // Completion Token数
    TotalTokens   int              // 总Token数
    Error        error             // 错误信息
    IsStream     bool              // 是否流式请求
    Disconnected  bool             // 客户端是否断开
}
```

### 3.2 模型配置

```go
// ModelConfig 模型路由配置
type ModelConfig struct {
    ID              string            // 配置ID
    ModelName       string            // 对外模型名称（如 "gpt-4"）
    Provider        string            // 供应商（openai/tongyi/zhipu/deepseek）
    ProviderModel   string            // 供应商实际模型名
    BaseURL         string            // 上游API地址
    APIKey          string            // 上游API Key
    Timeout         time.Duration     // 请求超时
    MaxRetries      int               // 最大重试次数
    RetryInterval   time.Duration     // 重试间隔
    Weight          int               // 负载均衡权重
    Enabled         bool              // 是否启用
    Tags            map[string]string // 扩展标签
}
```

### 3.3 API Key

```go
// APIKey 租户API Key
type APIKey struct {
    ID          string            // 主键ID
    KeyHash     string            // Key的SHA256哈希（不存明文）
    KeyPrefix   string            // Key前缀（用于展示，如 "ng-xxxx"）
    TenantID    string            // 所属租户ID
    TenantName  string            // 租户名称
    Name        string            // Key名称/备注
    Status      APIKeyStatus      // 状态：active/disabled/expired
    Quota       int64             // 额度（Token数，-1为无限）
    UsedQuota   int64             // 已用额度
    RateLimit   int               // 独立限流（请求/秒）
    AllowedModels []string        // 允许访问的模型列表
    ExpiresAt   *time.Time        // 过期时间
    CreatedAt   time.Time         // 创建时间
    UpdatedAt   time.Time         // 更新时间
    CreatedBy   string            // 创建人
}

type APIKeyStatus string

const (
    APIKeyStatusActive   APIKeyStatus = "active"
    APIKeyStatusDisabled APIKeyStatus = "disabled"
    APIKeyStatusExpired  APIKeyStatus = "expired"
)
```

### 3.4 审计日志

```go
// AuditLog 审计日志记录
type AuditLog struct {
    ID              string            // 主键ID
    RequestID       string            // 请求唯一ID
    TenantID        string            // 租户ID
    APIKeyID        string            // API Key ID
    ModelName       string            // 模型名称
    Provider        string            // 供应商
    RequestMethod   string            // HTTP方法
    RequestPath     string            // 请求路径
    RequestHeaders  map[string]string // 请求头（脱敏后）
    RequestBody     string            // 完整请求体（脱敏后）
    ResponseStatus  int               // 响应状态码
    ResponseBody    string            // 完整响应体（非流式）
    SSEChunks       []SSEChunk        // SSE分片列表（流式）
    PromptTokens    int               // Prompt Token数
    CompletionTokens int              // Completion Token数
    TotalTokens     int               // 总Token数
    Duration        int64             // 耗时（毫秒）
    ClientIP        string            // 客户端IP
    IsStream        bool              // 是否流式
    Disconnected    bool              // 是否断连
    DisconnectReason string           // 断连原因
    SHA256Fingerprint string         // 全量内容SHA256指纹
    CreatedAt       time.Time         // 记录时间
}

// SSEChunk SSE流式分片
type SSEChunk struct {
    Index     int       // 分片序号
    Data      string    // 分片原始数据
    Timestamp time.Time // 接收时间
    EventType string    // 事件类型（data/event/id/retry）
}
```

### 3.5 租户与权限（Enterprise版）

```go
// Tenant 租户
type Tenant struct {
    ID          string            // 租户ID
    Name        string            // 租户名称
    Code        string            // 租户编码
    Status      TenantStatus      // 状态
    Config      map[string]string // 租户配置
    CreatedAt   time.Time
    UpdatedAt   time.Time
}

// Role 角色（RBAC）
type Role struct {
    ID          string    // 角色ID
    TenantID    string    // 所属租户
    Name        string    // 角色名称
    Permissions []string  // 权限列表
    CreatedAt   time.Time
}

// User 管理后台用户
type User struct {
    ID          string    // 用户ID
    TenantID    string    // 所属租户
    Username    string    // 用户名
    PasswordHash string   // 密码哈希
    RoleID      string    // 角色ID
    Status      UserStatus // 状态
    LastLoginAt *time.Time // 最后登录时间
    CreatedAt   time.Time
}
```

### 3.6 限流配置

```go
// RateLimitConfig 限流配置
type RateLimitConfig struct {
    TenantID    string  // 租户ID
    ModelName   string  // 模型名称（空表示全模型）
    RequestsPerSec int  // 每秒请求数
    TokensPerMin   int64 // 每分钟Token数
    Strategy    string  // 策略：token_bucket/sliding_window
    Enabled     bool    // 是否启用
}
```

### 3.7 授权信息（Enterprise版）

```go
// LicenseInfo 商业授权信息
type LicenseInfo struct {
    LicenseKey    string    // 授权码
    ProductName   string    // 产品名称
    CustomerName  string    // 客户名称
    MaxNodes      int       // 最大节点数
    MaxTenants    int       // 最大租户数
    IssuedAt      time.Time // 签发时间
    ExpiresAt     time.Time // 过期时间
    Features      []string  // 授权功能列表
    Signature     string    // 签名
    IsOffline     bool      // 是否离线授权
}
```

---

## 4. 核心接口定义

### 4.1 插件工厂接口

```go
// PluginFactory 插件工厂统一入口
// 通过 BuildTag 条件编译，OSS和Enterprise各自注册实现
type PluginFactory interface {
    CreateStorage() StoragePlugin
    CreateAuditor() AuditPipeline
    CreateRateLimiter() RateLimitPlugin
    CreateExporter() LogExporter
    CreateLicenseValidator() LicenseValidator
}
```

### 4.2 存储插件接口

```go
// StoragePlugin 存储抽象接口
type StoragePlugin interface {
    // 初始化存储连接
    Init(config map[string]interface{}) error
    
    // API Key 管理
    GetAPIKey(keyHash string) (*APIKey, error)
    SaveAPIKey(key *APIKey) error
    UpdateAPIKeyQuota(keyID string, usedQuota int64) error
    ListAPIKeys(tenantID string, page, size int) ([]*APIKey, int64, error)
    DeleteAPIKey(keyID string) error
    
    // 模型配置管理
    GetModelConfig(modelName string) (*ModelConfig, error)
    ListModelConfigs(page, size int) ([]*ModelConfig, int64, error)
    SaveModelConfig(config *ModelConfig) error
    DeleteModelConfig(id string) error
    
    // 审计日志
    SaveAuditLog(log *AuditLog) error
    BatchSaveAuditLogs(logs []*AuditLog) error
    QueryAuditLogs(filter AuditLogFilter, page, size int) ([]*AuditLog, int64, error)
    
    // 健康检查
    Ping() error
    Close() error
}

// AuditLogFilter 审计日志查询过滤
type AuditLogFilter struct {
    TenantID    string
    APIKeyID    string
    ModelName   string
    StartTime   *time.Time
    EndTime     *time.Time
    Status      int      // 响应状态码过滤
    IsStream    *bool    // 是否流式
    Keyword     string   // 全文搜索关键词
}
```

### 4.3 审计流水线接口

```go
// AuditPipeline 审计流水线接口
type AuditPipeline interface {
    // 初始化审计管道
    Init(config AuditConfig) error
    
    // 提交审计事件（异步非阻塞）
    // 立即返回，不等待落库
    Submit(event *AuditEvent) error
    
    // 批量提交
    BatchSubmit(events []*AuditEvent) error
    
    // 提交流式分片
    SubmitSSEChunk(requestID string, chunk *SSEChunk) error
    
    // 标记请求结束，触发完整日志组装
    Finalize(requestID string, meta *AuditMeta) error
    
    // 标记客户端断连
    MarkDisconnect(requestID string, reason string) error
    
    // 关闭管道，flush剩余数据
    Shutdown() error
}

// AuditEvent 审计事件
type AuditEvent struct {
    RequestID   string
    EventType   AuditEventType
    Timestamp   time.Time
    Data        interface{}
}

type AuditEventType string

const (
    AuditEventRequestStart   AuditEventType = "request_start"
    AuditEventRequestComplete AuditEventType = "request_complete"
    AuditEventSSEChunk       AuditEventType = "sse_chunk"
    AuditEventDisconnect     AuditEventType = "disconnect"
    AuditEventError          AuditEventType = "error"
)

// AuditConfig 审计配置
type AuditConfig struct {
    QueueSize       int           // 环形队列大小
    WorkerCount     int           // worker数量
    BatchSize       int           // 批量写入大小
    FlushInterval   time.Duration // 刷新间隔
    EnableSHA256    bool          // 是否启用SHA256存证
    RetentionDays   int           // 日志保留天数
}

// AuditMeta 审计元信息
type AuditMeta struct {
    ResponseStatus   int
    PromptTokens     int
    CompletionTokens int
    TotalTokens      int
    Duration         int64
}
```

### 4.4 限流插件接口

```go
// RateLimitPlugin 限流抽象接口
type RateLimitPlugin interface {
    Init(config map[string]interface{}) error
    
    // 尝试获取令牌，返回是否允许及剩余配额
    Allow(tenantID string, model string, tokens int) (allowed bool, remaining int64, err error)
    
    // 获取当前限流状态
    Status(tenantID string, model string) (current int64, limit int64, resetAt time.Time)
    
    // 重置限流计数器
    Reset(tenantID string, model string) error
}
```

### 4.5 日志导出接口（Enterprise版）

```go
// LogExporter 日志外推接口
type LogExporter interface {
    Init(config map[string]interface{}) error
    
    // 推送审计日志到外部系统
    Export(log *AuditLog) error
    
    // 批量推送
    BatchExport(logs []*AuditLog) error
    
    // 测试连接
    TestConnection() error
    
    Close() error
}

// 支持的导出类型
type ExporterType string

const (
    ExporterTypeSIEM   ExporterType = "siem"
    ExporterTypeSyslog ExporterType = "syslog"
    ExporterTypeKafka  ExporterType = "kafka"
)
```

### 4.6 授权校验接口（Enterprise版）

```go
// LicenseValidator 授权校验接口
type LicenseValidator interface {
    // 加载授权文件
    LoadLicense(filePath string) (*LicenseInfo, error)
    
    // 验证授权有效性
    Validate(license *LicenseInfo) (bool, error)
    
    // 检查功能是否授权
    HasFeature(feature string) bool
    
    // 检查节点数限制
    CheckNodeLimit(currentNodes int) bool
    
    // 检查租户数限制
    CheckTenantLimit(currentTenants int) bool
    
    // 获取授权信息
    GetLicenseInfo() *LicenseInfo
}
```

### 4.7 模型适配器接口

```go
// ModelAdapter 模型适配器接口
type ModelAdapter interface {
    // 适配器名称
    Name() string
    
    // 将统一格式请求转换为供应商特定格式
    TransformRequest(req *UnifiedRequest) (*http.Request, error)
    
    // 将供应商响应转换为统一格式（非流式）
    TransformResponse(resp *http.Response) (*UnifiedResponse, error)
    
    // 处理流式响应（SSE分片转换）
    TransformStreamChunk(chunk []byte) (*UnifiedSSEChunk, error)
    
    // 解析Token用量
    ParseTokenUsage(resp *http.Response) (prompt, completion, total int)
    
    // 提取错误信息
    ParseError(resp *http.Response) (code int, message string)
}

// UnifiedRequest 统一请求格式 — 兼容 OpenAI Chat Completions API 全部参数
type UnifiedRequest struct {
    // ===== 核心参数 =====
    Model    string         `json:"model"`              // 模型名称（必填）
    Messages []Message      `json:"messages"`           // 消息列表（必填）

    // ===== 采样控制 =====
    Temperature      *float64 `json:"temperature,omitempty"`       // 0-2，默认1
    TopP             *float64 `json:"top_p,omitempty"`               // 0-1，默认1（核采样）
    FrequencyPenalty *float64 `json:"frequency_penalty,omitempty"`  // -2.0 到 2.0
    PresencePenalty  *float64 `json:"presence_penalty,omitempty"`    // -2.0 到 2.0
    LogitBias        map[string]int `json:"logit_bias,omitempty"`    // token_id -> bias(-100~100)
    Logprobs         *bool    `json:"logprobs,omitempty"`           // 是否返回logprobs
    TopLogprobs      *int     `json:"top_logprobs,omitempty"`       // 1-20，需logprobs=true

    // ===== 输出控制 =====
    MaxTokens          *int    `json:"max_tokens,omitempty"`          // 最大Token数（旧版）
    MaxCompletionTokens *int   `json:"max_completion_tokens,omitempty"` // 最大Token数（新版，reasoning模型）
    N                  *int    `json:"n,omitempty"`                   // 生成几个选项，默认1
    Stop               []string `json:"stop,omitempty"`                 // 停止词
    Seed               *int    `json:"seed,omitempty"`                // 随机种子（可复现）

    // ===== 结构化输出 =====
    ResponseFormat     *ResponseFormat `json:"response_format,omitempty"` // 结构化输出

    // ===== 流式控制 =====
    Stream        bool            `json:"stream,omitempty"`         // 是否流式
    StreamOptions *StreamOptions  `json:"stream_options,omitempty"` // 流式选项

    // ===== 工具调用 (Function Calling) =====
    Tools             []Tool      `json:"tools,omitempty"`           // 工具定义列表
    ToolChoice        interface{} `json:"tool_choice,omitempty"`     // "auto"/"none"/{type:function,...}
    ParallelToolCalls *bool       `json:"parallel_tool_calls,omitempty"` // 是否并行调用工具

    // ===== Reasoning模型控制 =====
    ReasoningEffort *string `json:"reasoning_effort,omitempty"` // "low"/"medium"/"high"
    Verbosity       *string `json:"verbosity,omitempty"`        // "low"/"medium"/"high"

    // ===== 元信息 =====
    User            string `json:"user,omitempty"`            // 终端用户标识
    ServiceTier     string `json:"service_tier,omitempty"`   // "default"/"flex"/"priority"
    PromptCacheKey  string `json:"prompt_cache_key,omitempty"` // 缓存命中键
    Store           *bool  `json:"store,omitempty"`            // 是否服务端留存

    // ===== 扩展参数（未识别参数透传） =====
    Extra           map[string]interface{} `json:"-"` // 未知参数原样保留，适配供应商私有参数
}

// Message 消息结构 — 支持多模态内容
type Message struct {
    Role       string      `json:"role"`              // system/developer/user/assistant/tool
    Content    interface{} `json:"content,omitempty"` // string 或 []ContentPart（多模态）
    Name       string      `json:"name,omitempty"`     // 可选的说话者名称
    ToolCallID string      `json:"tool_call_id,omitempty"` // tool角色的关联ID
    ToolCalls  []ToolCall  `json:"tool_calls,omitempty"`  // assistant角色的工具调用
    Refusal    string      `json:"refusal,omitempty"`     // 模型拒绝内容
}

// ContentPart 多模态内容片段
type ContentPart struct {
    Type     string         `json:"type"`     // "text"/"image_url"/"input_audio"
    Text     string         `json:"text,omitempty"`
    ImageURL *ImageURLPart  `json:"image_url,omitempty"`  // 图片
    InputAudio *AudioPart   `json:"input_audio,omitempty"` // 音频
}

type ImageURLPart struct {
    URL    string `json:"url"`              // http(s):// 或 data: URI
    Detail string `json:"detail,omitempty"` // "auto"/"low"/"high"
}

type AudioPart struct {
    Data   string `json:"data"`   // base64编码音频
    Format string `json:"format"`  // "wav"/"mp3"
}

// Tool 工具定义
type Tool struct {
    Type     string         `json:"type"`      // 目前仅 "function"
    Function ToolFunction   `json:"function"`
}

type ToolFunction struct {
    Name        string                 `json:"name"`
    Description string                 `json:"description,omitempty"`
    Parameters  map[string]interface{} `json:"parameters"` // JSON Schema
    Strict      *bool                  `json:"strict,omitempty"` // 严格模式
}

// ToolCall 工具调用
type ToolCall struct {
    ID       string            `json:"id"`
    Type     string            `json:"type"`     // "function"
    Function ToolCallFunction  `json:"function"`
}

type ToolCallFunction struct {
    Name      string `json:"name"`
    Arguments string `json:"arguments"` // JSON字符串
}

// ResponseFormat 结构化输出
type ResponseFormat struct {
    Type       string                 `json:"type"`        // "text"/"json_object"/"json_schema"
    JSONSchema *ResponseJSONSchema    `json:"json_schema,omitempty"`
}

type ResponseJSONSchema struct {
    Name        string                 `json:"name"`
    Schema      map[string]interface{} `json:"schema"`
    Strict      *bool                  `json:"strict,omitempty"`
}

// StreamOptions 流式选项
type StreamOptions struct {
    IncludeUsage bool `json:"include_usage"` // 最后一个分片是否包含Token用量
}

// UnifiedResponse 统一响应格式 — 兼容 OpenAI 响应
type UnifiedResponse struct {
    ID                string         `json:"id"`
    Object            string         `json:"object"`            // "chat.completion"
    Created           int64          `json:"created"`           // Unix时间戳
    Model             string         `json:"model"`
    Choices           []Choice       `json:"choices"`
    Usage             *TokenUsage    `json:"usage,omitempty"`
    SystemFingerprint string         `json:"system_fingerprint,omitempty"`
}

type Choice struct {
    Index        int      `json:"index"`
    Message      Message  `json:"message"`
    FinishReason string   `json:"finish_reason"` // "stop"/"length"/"tool_calls"/"content_filter"
    Logprobs     *LogprobsResult `json:"logprobs,omitempty"`
}

type TokenUsage struct {
    PromptTokens     int `json:"prompt_tokens"`
    CompletionTokens int `json:"completion_tokens"`
    TotalTokens      int `json:"total_tokens"`
}

type LogprobsResult struct {
    Content     []LogprobContent `json:"content,omitempty"`
    Refusal     []LogprobContent `json:"refusal,omitempty"`
}

type LogprobContent struct {
    Token       string             `json:"token"`
    Logprob     float64            `json:"logprob"`
    Bytes       []int              `json:"bytes,omitempty"`
    TopLogprobs []LogprobTokenAlt  `json:"top_logprobs,omitempty"`
}

type LogprobTokenAlt struct {
    Token   string  `json:"token"`
    Logprob float64 `json:"logprob"`
}

// UnifiedSSEChunk 统一SSE流式分片 — 兼容 OpenAI 流式格式
type UnifiedSSEChunk struct {
    ID                string       `json:"id"`
    Object            string       `json:"object"`           // "chat.completion.chunk"
    Created           int64        `json:"created"`
    Model             string       `json:"model"`
    Choices           []SSEChoice  `json:"choices"`
    Usage             *TokenUsage  `json:"usage,omitempty"` // stream_options.include_usage=true时
    SystemFingerprint string       `json:"system_fingerprint,omitempty"`
}

type SSEChoice struct {
    Index        int     `json:"index"`
    Delta        Message `json:"delta"`           // 增量内容
    FinishReason *string  `json:"finish_reason"` // nil=未结束, "stop"/"length"/"tool_calls"
}
```

---

## 5. SSE异步审计架构详解

### 5.1 整体数据流

```
客户端请求
    │
    ▼
┌──────────────┐     ┌───────────────────────────┐
│  代理内核层    │────▶│  SSEResponseWriter (劫持)  │
│  (Proxy Core) │     │  ┌─────────────────────┐  │
└──────────────┘     │  │ 1.写入客户端(同步)    │  │
                     │  │ 2.写入环形队列(异步)  │  │
                     │  └─────────────────────┘  │
                     └───────────┬───────────────┘
                                 │
                                 ▼
                    ┌────────────────────────┐
                    │   内存环形队列 (Ring Buffer) │
                    │   大小: 可配置(默认65536)      │
                    └───────────┬────────────┘
                                │
                    ┌───────────▼────────────┐
                    │  Worker池 (goroutine)    │
                    │  数量: 可配置(默认4)       │
                    │  批量消费 + 落库          │
                    └───────────┬────────────┘
                                │
                    ┌───────────▼────────────┐
                    │   存储插件 (Storage)      │
                    │   MySQL/SQLite/达梦/金仓  │
                    └────────────────────────┘
```

### 5.2 SSE分片劫持实现

**自定义ResponseWriter**：

```go
// SSEResponseWriter 劫持SSE流量
type SSEResponseWriter struct {
    http.ResponseWriter           // 嵌入原始Writer
    requestID    string
    auditor      AuditPipeline
    chunks       []SSEChunk
    mu           sync.Mutex
    startWrite   time.Time
    headerWritten bool
}
```

**劫持逻辑**：

| 方法 | 行为 |
|------|------|
| `Write(data []byte)` | 1. 写入原始Writer（推送给客户端） 2. 解析SSE分片 3. 投递到审计队列 |
| `WriteHeader(code int)` | 记录响应状态码，调用原始WriteHeader |
| `Flush()` | 调用原始Flush确保客户端实时收到数据 |
| `Hijack()` | 支持连接劫持（断连检测用） |

### 5.3 环形队列实现

```go
// RingBuffer 环形队列
type RingBuffer struct {
    buf       []*AuditEvent
    size      int
    head      int       // 写入位置
    tail      int       // 读取位置
    mu        sync.Mutex
    notFull   *sync.Cond
    notEmpty  *sync.Cond
    closed    bool
    overflowCount int64  // 溢出计数
}
```

**特性**：
- 固定大小内存预分配，无GC压力
- 队列满时阻塞写入方（短暂阻塞，worker消费后释放）
- 队列空时阻塞消费方
- 支持优雅关闭：`Shutdown()` 后不再接收新数据，flush剩余

### 5.4 断连补全逻辑

**触发条件**：

| 场景 | 检测方式 | 补全行为 |
|------|----------|----------|
| 客户端主动断开 | `r.Context().Done()` | 标记Disconnect，已收分片重组保存 |
| 客户端超时 | Write超时 | 同上 |
| 上游返回错误 | HTTP状态码非200 | 记录错误信息 |
| 上游连接断开 | 上游Response关闭 | 保存已收分片 + 错误标记 |

**补全流程**：
1. 监听 `request.Context().Done()` 信号
2. 收到断开信号后，调用 `auditor.MarkDisconnect(requestID, reason)`
3. Worker收到Disconnect事件后，将已收集的分片重组
4. 生成完整审计日志（标记 `Disconnected=true`），写入存储
5. 计算 `SHA256Fingerprint`（对已有内容）

### 5.5 SHA256防篡改机制（Enterprise版）

**存证流程**：
1. 请求完成后，将完整请求体 + 所有SSE分片 + 响应状态 + 元信息拼接
2. 计算SHA256哈希值
3. 哈希值与审计日志一同存储
4. 审计日志表设置独立权限：业务账号只有SELECT权限，无UPDATE/DELETE
5. 定期校验：后台任务扫描历史日志，重新计算哈希比对，发现不一致告警

### 5.6 数据库故障兜底

| 场景 | 兜底策略 |
|------|----------|
| 数据库连接断开 | 环形队列缓冲，等待重连 |
| 写入失败 | 重试3次，失败后写入本地文件降级存储 |
| 数据库慢查询 | 不阻塞主流量，worker独立处理 |
| 队列溢出 | 丢弃最旧的非关键事件，记录溢出计数，告警 |

---

## 6. 插件机制详解

### 6.1 设计原则

| 原则 | 说明 |
|------|------|
| 接口契约化 | 所有插件能力通过Go interface定义，开源公开 |
| 编译隔离 | BuildTag条件编译，不使用go plugin(.so) |
| 一套源码 | 同一仓库，OSS和Enterprise各自编译 |
| 工厂统一 | PluginFactory统一入口，上层无感知版本差异 |

### 6.2 BuildTag 工厂模式

**核心原则：Enterprise版包含OSS全部能力 + 商业增强能力。**

OSS目录下的实现文件**不设BuildTag**（两个版本都编译），Enterprise目录下的实现文件设 `//go:build enterprise`（仅Enterprise版编译）。工厂函数决定运行时创建哪些实现。

**文件**: `pkg/plugin/factory.go` — OSS工厂，仅注册OSS实现

```go
//go:build oss

package plugin

type ossFactory struct{}

func NewPluginFactory() PluginFactory {
    return &ossFactory{}
}

// 仅创建OSS实现（来自oss/目录）
func (f *ossFactory) CreateStorage() StoragePlugin {
    // 根据config.driver返回MySQL或SQLite实现
}
func (f *ossFactory) CreateAuditor() AuditPipeline {
    return NewSimpleAuditor() // 来自oss/audit_simple.go
}
func (f *ossFactory) CreateRateLimiter() RateLimitPlugin {
    return NewMemRateLimiter() // 来自oss/limit_mem.go
}
func (f *ossFactory) CreateExporter() LogExporter { return nil }
func (f *ossFactory) CreateLicenseValidator() LicenseValidator { return nil }
```

**文件**: `pkg/plugin/factory_enterprise.go` — Enterprise工厂，注册OSS实现 + Enterprise实现

```go
//go:build enterprise

package plugin

type enterpriseFactory struct{}

func NewPluginFactory() PluginFactory {
    return &enterpriseFactory{}
}

// 根据config.driver选择：MySQL/SQLite(OSS实现) 或 达梦/金仓(Enterprise实现)
func (f *enterpriseFactory) CreateStorage() StoragePlugin {
    switch config.Driver {
    case "mysql":   return NewMySQLStorage()      // 复用oss/实现
    case "sqlite":  return NewSQLiteStorage()     // 复用oss/实现
    case "dm":      return NewDMStorage()         // enterprise/实现
    case "kingbase": return NewKingbaseStorage()  // enterprise/实现
    }
}
// 流式审计（Enterprise增强，替代OSS简单审计）
func (f *enterpriseFactory) CreateAuditor() AuditPipeline {
    return NewStreamAuditor() // 来自enterprise/audit_stream.go
}
// Redis分布式限流（Enterprise增强，替代OSS内存限流）
// 也可降级使用OSS内存限流
func (f *enterpriseFactory) CreateRateLimiter() RateLimitPlugin {
    if redisAddr != "" {
        return NewRedisRateLimiter() // enterprise/实现
    }
    return NewMemRateLimiter() // 降级使用oss/实现
}
// Enterprise独有：日志外推
func (f *enterpriseFactory) CreateExporter() LogExporter {
    return NewSIEMExporter() // enterprise/实现
}
// Enterprise独有：授权校验
func (f *enterpriseFactory) CreateLicenseValidator() LicenseValidator {
    return NewLicenseValidator() // enterprise/实现
}
```

**关键说明**：

| 设计点 | 说明 |
|--------|------|
| oss/目录文件 | **无BuildTag**，两个版本都编译，Enterprise工厂可直接调用 |
| enterprise/目录文件 | `//go:build enterprise`，仅Enterprise版编译 |
| 工厂创建逻辑 | Enterprise工厂根据配置动态选择OSS或Enterprise实现 |
| 存储后端选择 | Enterprise版支持MySQL/SQLite/达梦/金仓，由config.driver决定 |
| 限流降级 | Enterprise版Redis不可用时自动降级为OSS内存限流 |

**编译命令**：

| 版本 | 编译命令 | 产物 | 包含的代码 |
|------|----------|------|------------|
| OSS | `go build -tags oss -o neuralgate ./cmd/gateway/` | `neuralgate` | oss/目录实现 + factory.go |
| Enterprise | `go build -tags enterprise -o neuralgate-enterprise ./cmd/gateway/` | `neuralgate-enterprise` | oss/目录实现 + enterprise/目录实现 + factory_enterprise.go |

### 6.3 插件实现清单

**共享实现**（oss/目录，无BuildTag，两个版本都编译）：

| 文件 | 接口 | 功能 | 说明 |
|------|------|------|------|
| storage_mysql.go | StoragePlugin | MySQL存储 | OSS和Enterprise都可用 |
| storage_sqlite.go | StoragePlugin | SQLite存储 | OSS和Enterprise都可用 |
| audit_simple.go | AuditPipeline | 同步元数据审计 | OSS版使用 |
| limit_mem.go | RateLimitPlugin | 内存令牌桶限流 | OSS版使用，Enterprise可降级使用 |
| ring_buffer.go | - | 环形队列基础实现 | Enterprise的audit_stream.go依赖此实现 |

**Enterprise专属实现**（enterprise/目录，`//go:build enterprise`）：

| 文件 | 接口 | 功能 | 说明 |
|------|------|------|------|
| storage_dm.go | StoragePlugin | 达梦数据库适配 | Enterprise专属，新增国产库选项 |
| storage_kingbase.go | StoragePlugin | 人大金仓适配 | Enterprise专属，新增国产库选项 |
| audit_stream.go | AuditPipeline | 环形队列+异步+SHA256 | Enterprise专属，增强审计（复用oss/ring_buffer.go） |
| limit_redis.go | RateLimitPlugin | Redis分布式限流 | Enterprise专属，增强限流 |
| export_siem.go | LogExporter | SIEM日志外推 | Enterprise专属 |
| export_syslog.go | LogExporter | Syslog外推 | Enterprise专属 |
| export_kafka.go | LogExporter | Kafka外推 | Enterprise专属 |
| security_pii.go | SecurityPlugin | PII动态脱敏 | Enterprise专属 |
| license.go | LicenseValidator | 授权校验 | Enterprise专属 |

**运行时能力矩阵**：

| 接口 | OSS版可用实现 | Enterprise版可用实现 | Enterprise版运行时选择逻辑 |
|------|--------------|---------------------|--------------------------|
| StoragePlugin | MySQL, SQLite | MySQL, SQLite, 达梦, 金仓 | 由config.driver决定 |
| AuditPipeline | SimpleAuditor | StreamAuditor, SimpleAuditor | Enterprise优先用StreamAuditor |
| RateLimitPlugin | MemRateLimiter | RedisRateLimiter, MemRateLimiter | Redis不可用时降级Mem |
| LogExporter | 无 | SIEM, Syslog, Kafka | 由config.export.type决定 |
| LicenseValidator | 无 | LicenseValidator | Enterprise版强制校验 |
| SecurityPlugin | 无 | PIIDetector | Enterprise版启用 |

---

## 7. 编译隔离机制

### 7.1 BuildTag 文件命名规范

```
pkg/plugin/
├── interface.go              // 接口定义（无BuildTag，全版本编译）
├── factory.go                // OSS工厂      (//go:build oss)  仅注册oss/实现
├── factory_enterprise.go     // Enterprise工厂 (//go:build enterprise)  注册oss/+enterprise/实现
├── oss/                      // 共享实现（全公开，无BuildTag，两版本都编译）
│   ├── storage_mysql.go      // 无BuildTag → OSS和Enterprise都编译
│   ├── storage_sqlite.go     // 无BuildTag → OSS和Enterprise都编译
│   ├── audit_simple.go       // 无BuildTag → OSS和Enterprise都编译
│   ├── limit_mem.go          // 无BuildTag → OSS和Enterprise都编译
│   └── ring_buffer.go        // 无BuildTag → OSS和Enterprise都编译（Enterprise审计依赖）
└── enterprise/                // Enterprise专属实现（私有不公开）
    ├── storage_dm.go          // //go:build enterprise → 仅Enterprise编译
    ├── storage_kingbase.go   // //go:build enterprise → 仅Enterprise编译
    ├── audit_stream.go       // //go:build enterprise → 仅Enterprise编译（复用oss/ring_buffer.go）
    ├── security_pii.go       // //go:build enterprise
    ├── export_siem.go        // //go:build enterprise
    ├── export_syslog.go      // //go:build enterprise
    ├── export_kafka.go       // //go:build enterprise
    ├── limit_redis.go        // //go:build enterprise
    └── license.go             // //go:build enterprise
```

**BuildTag 规则总结**：

| 文件位置 | BuildTag | OSS编译是否包含 | Enterprise编译是否包含 |
|----------|----------|----------------|----------------------|
| interface.go | 无 | ✅ 包含 | ✅ 包含 |
| factory.go | `//go:build oss` | ✅ 包含 | ❌ 不包含 |
| factory_enterprise.go | `//go:build enterprise` | ❌ 不包含 | ✅ 包含 |
| oss/*.go | 无 | ✅ 包含 | ✅ 包含 |
| enterprise/*.go | `//go:build enterprise` | ❌ 不包含 | ✅ 包含 |

**关键设计：为什么oss/目录不设BuildTag？**

Enterprise版必须包含OSS全部能力。如果oss/目录设了 `//go:build oss`，Enterprise编译时这些文件不会被编译，导致Enterprise版丢失MySQL/SQLite存储、基础限流等能力。因此oss/目录不设BuildTag，确保两个版本都能编译这些共享实现，由工厂函数在运行时决定使用哪个实现。

### 7.2 编译验证

| 检查项 | OSS编译 | Enterprise编译 |
|--------|---------|----------------|
| oss/目录代码 | ✅ 包含 | ✅ 包含 |
| enterprise/目录代码 | ❌ 不包含 | ✅ 包含 |
| 达梦/金仓驱动引用 | ❌ 不包含 | ✅ 包含 |
| SHA256审计逻辑 | ❌ 不包含 | ✅ 包含 |
| 授权校验逻辑 | ❌ 不包含 | ✅ 包含 |
| PII脱敏逻辑 | ❌ 不包含 | ✅ 包含 |
| MySQL/SQLite存储 | ✅ 可用 | ✅ 可用 |
| 内存限流实现 | ✅ 可用 | ✅ 可用（降级时） |
| 工厂函数 | factory.go (仅OSS实现) | factory_enterprise.go (OSS+Enterprise实现) |
| 二进制大小 | 较小 | 较大 |

### 7.3 运行时版本检测

```go
// VersionInfo 版本信息
type VersionInfo struct {
    Version      string // 版本号
    BuildTime    string // 编译时间
    GitCommit    string // Git提交
    Edition      string // 版本类型：oss/enterprise
    Features     []string // 可用功能列表
}
```

启动时打印版本信息，Enterprise版启动时校验授权文件，缺失则降级为OSS模式运行并告警。

---

## 8. 模型适配器架构

### 8.1 适配器注册机制

```go
// AdapterRegistry 模型适配器注册中心
type AdapterRegistry struct {
    adapters map[string]ModelAdapter // provider -> adapter
    mu       sync.RWMutex
}

func (r *AdapterRegistry) Register(adapter ModelAdapter) {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.adapters[adapter.Name()] = adapter
}

func (r *AdapterRegistry) Get(provider string) (ModelAdapter, error) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    adapter, ok := r.adapters[provider]
    if !ok {
        return nil, fmt.Errorf("adapter not found: %s", provider)
    }
    return adapter, nil
}
```

### 8.2 内置适配器

| 适配器 | 协议 | 流式支持 | 原生兼容 | 内置版本 |
|--------|------|----------|----------|----------|
| OpenAI Adapter | OpenAI API | SSE | ✅ 原样透传 | OSS |
| 通义千问 Adapter | DashScope API | SSE | ❌ 需转换 | OSS |
| 智谱GLM Adapter | Zhipu API | SSE | ❌ 需转换 | OSS |
| DeepSeek Adapter | OpenAI兼容 | SSE | ✅ 原样透传 | OSS |

> **原生兼容** = 上游协议与入口协议完全一致，适配器可直接透传请求体，无需序列化/反序列化。对于 OpenAI 兼容的上游（如 DeepSeek），网关只需替换 `model` 字段后原样转发，性能损耗最低。

### 8.3 ModelAdapter 接口增强：透传模式

```go
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
```

### 8.4 适配器工作流

```
客户端请求 (OpenAI协议)
    │
    ▼
路由匹配 → 确定目标 ModelConfig (provider=xxx)
    │
    ▼
从 AdapterRegistry 获取对应 Adapter
    │
    ▼
判断 adapter.SupportsNativeProxy()
    │
    ├─── true (OpenAI/DeepSeek等兼容上游) ──────┐
    │    仅替换 model 字段，rawBody 原样转发       │
    │    性能最优：零序列化损耗                    │
    │                                              │
    └─── false (通义/智谱等异构上游) ─────────────┐
         adapter.TransformRequest(unifiedReq, rawBody)
         将OpenAI格式转换为供应商格式
    │
    ▼
ReverseProxy 转发到上游
    │
    ▼
┌─ 非流式 ─────────────────────────────────────────┐
│ SupportsNativeProxy==true: 原样透传响应            │
│ SupportsNativeProxy==false: TransformResponse转换  │
│ → 写回客户端                                        │
└─────────────────────────────────────────────────────┘
┌─ 流式 ──────────────────────────────────────────────┐
│ SupportsNativeProxy==true: SSE分片原样透传           │
│ SupportsNativeProxy==false: TransformStreamChunk转换  │
│ → 写回客户端 + 同时投递到审计队列                      │
└─────────────────────────────────────────────────────┘
```

### 8.5 OpenAI API 端点兼容性规范

**设计原则**：NeuralGate 作为网关，入口协议必须与 OpenAI API 完全兼容。用户只需将 `OPENAI_BASE_URL` 指向网关地址，原有代码零改动即可使用。

**支持的端点清单**：

| 端点 | 方法 | 路径 | 处理方式 | 版本 |
|------|------|------|----------|------|
| Chat Completions | POST | `/v1/chat/completions` | 模型适配器转换/透传 | OSS+ |
| Completions (Legacy) | POST | `/v1/completions` | 透传到上游 | OSS+ |
| Embeddings | POST | `/v1/embeddings` | 透传到上游 | OSS+ |
| Models List | GET | `/v1/models` | 返回网关配置的可用模型列表 | OSS+ |
| Models Retrieve | GET | `/v1/models/{model}` | 返回单个模型信息 | OSS+ |
| Moderations | POST | `/v1/moderations` | 透传到上游 | OSS+ |
| Images Generations | POST | `/v1/images/generations` | 透传到上游 | OSS+ |
| Images Edits | POST | `/v1/images/edits` | 透传到上游 | OSS+ |
| Images Variations | POST | `/v1/images/variations` | 透传到上游 | OSS+ |
| Audio Speech (TTS) | POST | `/v1/audio/speech` | 透传到上游 | OSS+ |
| Audio Transcriptions | POST | `/v1/audio/transcriptions` | 透传到上游 | OSS+ |
| Audio Translations | POST | `/v1/audio/translations` | 透传到上游 | OSS+ |
| Files List | GET | `/v1/files` | 透传到上游 | OSS+ |
| Files Upload | POST | `/v1/files` | 透传到上游 | OSS+ |
| Files Retrieve | GET | `/v1/files/{file_id}` | 透传到上游 | OSS+ |
| Files Delete | DELETE | `/v1/files/{file_id}` | 透传到上游 | OSS+ |
| Files Content | GET | `/v1/files/{file_id}/content` | 透传到上游 | OSS+ |

**端点分类与处理策略**：

| 分类 | 端点 | 处理方式 | 说明 |
|------|------|----------|------|
| **核心代理端点** | `/v1/chat/completions`、`/v1/embeddings` | 适配器转换/透传 | 需要鉴权、限流、审计、Token计量 |
| **模型管理端点** | `/v1/models`、`/v1/models/{model}` | 网关本地响应 | 返回网关配置的可用模型，不转发上游 |
| **透传端点** | `/v1/completions`、`/v1/moderations`、`/v1/audio/*`、`/v1/images/*` | 原样透传 | 仅鉴权+限流，请求体原样转发，不解析 |
| **文件端点** | `/v1/files` | 透传到上游 | 文件操作直接转发，不本地存储 |

**`/v1/models` 响应格式**（网关本地生成）：

```json
{
  "object": "list",
  "data": [
    {
      "id": "gpt-4",
      "object": "model",
      "created": 1687835889,
      "owned_by": "neuralgate"
    },
    {
      "id": "qwen-max",
      "object": "model",
      "created": 1687835889,
      "owned_by": "neuralgate"
    }
  ]
}
```

### 8.6 OpenAI SSE 流式格式规范

**SSE 分片格式**（每个分片必须严格遵循 OpenAI 格式）：

```
data: {"id":"chatcmpl-xxx","object":"chat.completion.chunk","created":1699999999,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}

data: {"id":"chatcmpl-xxx","object":"chat.completion.chunk","created":1699999999,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}

data: {"id":"chatcmpl-xxx","object":"chat.completion.chunk","created":1699999999,"model":"gpt-4","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}

data: {"id":"chatcmpl-xxx","object":"chat.completion.chunk","created":1699999999,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}

data: [DONE]

```

**关键规范**：

| 规范项 | 说明 |
|--------|------|
| 每行格式 | `data: {JSON}\n\n`（两个换行结束一个事件） |
| 首个分片 | `delta` 包含 `role` 字段，`content` 为空字符串 |
| 内容分片 | `delta` 仅包含 `content` 增量文本 |
| 工具调用分片 | `delta` 包含 `tool_calls` 数组，增量式拼接 `arguments` |
| 结束分片 | `finish_reason` 非 null（"stop"/"length"/"tool_calls"），`delta` 为空对象 |
| Token用量 | 当 `stream_options.include_usage=true` 时，结束分片包含 `usage` 字段 |
| 终止标记 | 最后一个事件为 `data: [DONE]\n\n` |
| Content-Type | `text/event-stream` |
| Cache-Control | `no-cache` |
| Connection | `keep-alive` |

**SSE分片劫持注意事项**：
- 网关劫持SSE流时，**不能修改分片内容**（原生兼容模式下原样透传）
- 审计层只做旁路捕获（复制分片到审计队列），不影响客户端接收的数据
- 客户端断连时，网关需消费完上游剩余分片以保证审计完整性

### 8.7 OpenAI 错误响应格式规范

**所有错误响应必须遵循 OpenAI 格式**，确保 SDK 能正确解析：

```json
{
  "error": {
    "message": "Incorrect API key provided",
    "type": "invalid_request_error",
    "param": null,
    "code": "invalid_api_key"
  }
}
```

**错误类型与HTTP状态码映射**：

| HTTP状态码 | error.type | error.code | 触发场景 |
|------------|-----------|------------|----------|
| 401 | invalid_request_error | invalid_api_key | API Key无效 |
| 401 | invalid_request_error | api_key_disabled | API Key已禁用 |
| 401 | invalid_request_error | api_key_expired | API Key已过期 |
| 429 | rate_limit_exceeded | quota_exceeded | 额度用尽 |
| 429 | rate_limit_exceeded | rate_limit | 请求频率超限 |
| 404 | invalid_request_error | model_not_found | 模型未找到 |
| 403 | invalid_request_error | model_access_denied | 无权访问该模型 |
| 400 | invalid_request_error | bad_request | 请求格式错误 |
| 502 | api_error | upstream_error | 上游服务错误 |
| 504 | api_error | upstream_timeout | 上游服务超时 |
| 503 | api_error | service_unavailable | 服务不可用 |
| 500 | api_error | internal_error | 内部错误 |

**限流响应Header**（兼容 OpenAI SDK 限流处理）：

| Header | 说明 |
|--------|------|
| `X-RateLimit-Limit-Requests` | 每秒最大请求数 |
| `X-RateLimit-Limit-Tokens` | 每分钟最大Token数 |
| `X-RateLimit-Remaining-Requests` | 剩余请求数 |
| `X-RateLimit-Remaining-Tokens` | 剩余Token数 |
| `X-RateLimit-Reset-Requests` | 请求配额重置时间（如 "1s"） |
| `X-RateLimit-Reset-Tokens` | Token配额重置时间（如 "1m"） |
| `Retry-After` | 建议重试等待秒数 |

---

## 9. 项目目录结构

```
ai-gateway/
├── cmd/
│   └── gateway/
│       └── main.go                    # 程序入口，启动双服务
├── pkg/
│   ├── core/                           # 内核四层架构（全部开源）
│   │   ├── acceptor.go                 # 接入层：连接管理、TLS、IP过滤
│   │   ├── pipeline.go                 # 管道中间件层：中间件链
│   │   ├── middleware_auth.go          # 鉴权中间件
│   │   ├── middleware_limit.go         # 限流中间件
│   │   ├── middleware_route.go         # 路由匹配中间件
│   │   ├── proxy.go                    # 代理内核：ReverseProxy封装
│   │   ├── sse_writer.go               # SSE劫持ResponseWriter
│   │   ├── sse_reassembler.go          # SSE分片重组器
│   │   ├── disconnect_handler.go       # 断连检测与补全
│   │   └── context.go                  # RequestContext定义
│   ├── adapter/                        # 模型适配器层
│   │   ├── interface.go                # ModelAdapter接口定义
│   │   ├── registry.go                 # 适配器注册中心
│   │   ├── openai.go                   # OpenAI适配器
│   │   ├── tongyi.go                   # 通义千问适配器
│   │   ├── zhipu.go                    # 智谱GLM适配器
│   │   └── deepseek.go                 # DeepSeek适配器
│   ├── plugin/                         # 插件商业化隔离核心
│   │   ├── interface.go                # 全量接口定义（无BuildTag，全版本编译）
│   │   ├── factory.go                  # OSS工厂 (//go:build oss) 仅注册oss/实现
│   │   ├── factory_enterprise.go       # Enterprise工厂 (//go:build enterprise) 注册oss/+enterprise/
│   │   ├── oss/                        # 共享实现（无BuildTag，两版本都编译，全公开）
│   │   │   ├── storage_mysql.go        # MySQL存储（无BuildTag → 两版本都编译）
│   │   │   ├── storage_sqlite.go       # SQLite存储（无BuildTag → 两版本都编译）
│   │   │   ├── audit_simple.go         # 简单同步审计（无BuildTag → 两版本都编译）
│   │   │   ├── limit_mem.go            # 内存令牌桶限流（无BuildTag → 两版本都编译）
│   │   │   └── ring_buffer.go          # 环形队列基础（无BuildTag → Enterprise审计依赖此实现）
│   │   └── enterprise/                 # Enterprise专属实现（//go:build enterprise，不推GitHub）
│   │       ├── storage_dm.go           # 达梦数据库适配
│   │       ├── storage_kingbase.go     # 人大金仓适配
│   │       ├── audit_stream.go         # 生产级流式审计（复用oss/ring_buffer.go）
│   │       ├── security_pii.go         # PII动态脱敏
│   │       ├── export_siem.go          # SIEM日志外推
│   │       ├── export_syslog.go        # Syslog外推
│   │       ├── export_kafka.go         # Kafka外推
│   │       ├── limit_redis.go          # Redis分布式限流
│   │       └── license.go              # 授权校验
│   ├── admin/                          # Gin管理后台（开源）
│   │   ├── server.go                   # Gin服务初始化
│   │   ├── router.go                   # 路由注册
│   │   ├── middleware.go               # 后台中间件（CORS、Auth）
│   │   ├── api_key.go                  # API Key管理API
│   │   ├── model_config.go             # 模型配置管理API
│   │   ├── audit_api.go                # 审计日志查询API
│   │   ├── tenant.go                   # 租户管理API（Enterprise）
│   │   ├── rbac.go                     # 角色权限API（Enterprise）
│   │   ├── license_api.go              # 授权管理API（Enterprise）
│   │   └── system.go                   # 系统信息API
│   └── config/                         # 配置管理
│       └── config.go                   # 配置加载与解析
├── webui/                              # 前端静态资源
├── config.yaml                         # 默认配置文件
├── push-private.sh                     # 私有仓库推送脚本
├── push-github-oss.sh                  # GitHub OSS推送脚本
├── .gitignore
├── go.mod
└── go.sum
```

---

## 10. 启动流程

### 10.1 启动序列图

```
main()
  │
  ├─ 1. 解析命令行参数 (config路径、版本信息)
  │
  ├─ 2. 加载配置文件 config.yaml
  │     → 解析为 Config 结构体（仅系统级配置：端口、DB、审计、限流等）
  │     → 不包含模型配置，模型配置在数据库中
  │
  ├─ 3. 初始化插件工厂
  │     → NewPluginFactory() (BuildTag决定实现)
  │     → factory.CreateStorage() → 连接数据库
  │     → factory.CreateAuditor() → 初始化审计管道
  │     → factory.CreateRateLimiter() → 初始化限流器
  │     → factory.CreateExporter() → 初始化日志导出（Enterprise）
  │     → factory.CreateLicenseValidator() → 校验授权（Enterprise）
  │
  ├─ 4. 从数据库加载模型配置
  │     → storage.ListModelConfigs(全部已启用的模型)
  │     → 加载到内存路由表（ModelConfig → 路由匹配缓存）
  │     → 首次启动DB为空时，路由表为空，需管理员通过后台添加模型
  │     → 后续通过管理后台修改后触发路由表热更新
  │
  ├─ 5. 初始化模型适配器注册中心
  │     → Register(OpenAIAdapter{})
  │     → Register(TongyiAdapter{})
  │     → Register(ZhipuAdapter{})
  │     → Register(DeepSeekAdapter{})
  │
  ├─ 6. 初始化代理内核
  │     → NewPipeline(storage, rateLimiter, auditor)
  │     → NewProxyCore(pipeline, adapterRegistry)
  │     → NewAcceptor(proxyCore)
  │
  ├─ 7. 初始化管理后台
  │     → NewAdminServer(storage, auditor)
  │     → 注册路由（含模型配置CRUD、API Key管理、审计查询等）
  │
  ├─ 8. 启动双服务 (并发)
  │     → go proxyServer.ListenAndServe(":8080")   // 代理服务
  │     → go adminServer.ListenAndServe(":8081")   // 管理后台
  │
  ├─ 9. 信号监听 (优雅关闭)
  │     → 监听 SIGINT/SIGTERM
  │     → 收到信号 → Shutdown()
  │
  └─ 10. Shutdown
        → 代理服务优雅关闭 (等待活跃连接处理完)
        → 管理后台关闭
        → 审计管道Flush剩余数据
        → 存储连接关闭
        → 限流器关闭
        → 日志导出器关闭
```

### 10.2 模型配置生命周期

```
管理员通过管理后台添加模型配置
    │
    ▼
POST :8081/api/models → SaveModelConfig() → 写入数据库
    │
    ▼
触发路由表热更新（内存中的路由缓存刷新）
    │
    ▼
代理服务(:8080) 立即可以路由到新模型（无需重启）
    │
    ├── 编辑模型 → 更新数据库 → 刷新路由表 → 实时生效
    ├── 禁用模型 → 更新状态 → 从路由表移除 → 立即停止转发
    ├── 启用模型 → 更新状态 → 加入路由表 → 立即开始转发
    └── 删除模型 → 软删除 → 从路由表移除
```

> **关键设计**：模型配置全程通过管理后台管理，存储在数据库中，支持**热更新**——增删改模型配置后立即生效，无需重启网关进程。代理服务启动时从数据库加载全部已启用的模型到内存路由表，运行时监听管理后台的配置变更事件刷新路由表。

### 10.3 配置文件结构 (config.yaml)

```yaml
server:
  proxy_addr: ":8080"        # 代理服务监听地址
  admin_addr: ":8081"        # 管理后台监听地址
  read_timeout: 30s           # 读超时（非流式请求）
  write_timeout: 30s          # 写超时（非流式请求）
  idle_timeout: 120s         # 空闲超时
  max_header_bytes: 1048576  # 最大Header大小(1MB)

storage:
  # ===== 数据库驱动选择 =====
  # 可选值: mysql(默认) / sqlite / dm(Enterprise) / kingbase(Enterprise)
  # OSS版仅支持 mysql 和 sqlite；dm/kingbase 需要 Enterprise 编译版本
  driver: mysql

  # ----- MySQL (默认，OSS+Enterprise 均可用) -----
  dsn: "user:pass@tcp(host:3306)/neuralgate?charset=utf8mb4"

  # ----- SQLite (轻量部署，单机场景，OSS+Enterprise 均可用) -----
  # driver: sqlite
  # dsn: "/var/lib/neuralgate/neuralgate.db"

  # ----- 达梦数据库 (仅 Enterprise 编译生效，OSS版配置无效) -----
  # driver: dm
  # dsn: "dm://user:pass@host:5236/NEURALGATE"

  # ----- 人大金仓 (仅 Enterprise 编译生效，OSS版配置无效) -----
  # driver: kingbase
  # dsn: "kingbase://user:pass@host:54321/neuralgate"

  # ----- 连接池参数 (所有驱动通用) -----
  max_open_conns: 20          # 最大连接数
  max_idle_conns: 10          # 最大空闲连接

audit:
  queue_size: 65536          # 环形队列大小
  worker_count: 4            # worker数量
  batch_size: 100             # 批量写入大小
  flush_interval: 5s          # 刷新间隔
  enable_sha256: true         # SHA256存证（Enterprise）
  retention_days: 90          # 日志保留天数

rate_limit:
  strategy: token_bucket     # token_bucket/sliding_window
  default_rps: 10             # 默认每秒请求数
  default_tpm: 100000         # 默认每分钟Token数

export:                       # Enterprise only
  type: siem                  # siem/syslog/kafka
  endpoint: "https://siem.example.com/api"
  api_key: ""
  batch_size: 50
  flush_interval: 10s

license:                      # Enterprise only
  file_path: "/etc/neuralgate/license.lic"
  offline_mode: false

log:
  level: info                 # debug/info/warn/error
  format: json                 # json/console
  output: stdout              # stdout/file path
```

> **设计说明**：`../config.yaml` 仅包含**基础设施/系统级**配置（端口、数据库连接、审计参数、限流默认值等），**不包含模型配置**。模型配置完全通过管理后台页面（:8081）进行 CRUD 管理，存储在数据库中。启动时从数据库加载模型配置，运行时通过管理后台修改后实时生效（热更新）。首次部署时数据库为空，管理员通过管理后台添加首个模型配置。

---

## 11. 请求全链路流程

### 11.1 非流式请求流程

```
客户端
  │ POST /v1/chat/completions
  │ Header: Authorization: Bearer ng-xxxx
  │ Body: {"model":"gpt-4","messages":[...],"stream":false}
  │
  ▼
┌─ 接入层 ─────────────────────────────────────────────────┐
│ 1. 接受TCP连接                                            │
│ 2. TLS终止（如启用）                                      │
│ 3. IP黑名单检查 → 命中则拒绝                              │
│ 4. 解析HTTP请求                                          │
└──────────────────────────────┬─────────────────────────┘
                                │
┌─ 管道中间件层 ─────────────────▼─────────────────────────┐
│ 5. 鉴权: 从Header提取API Key → 查询Storage验证           │
│    → 失败: 401 Unauthorized                             │
│    → 成功: 写入RequestContext.TenantID                  │
│                                                          │
│ 6. 限流: 检查RateLimiter.Allow(tenantID, model)         │
│    → 超限: 429 Too Many Requests                        │
│    → 通过: 继续                                         │
│                                                          │
│ 7. 路由匹配: 根据 model 字段匹配ModelConfig              │
│    → 未匹配: 404 Not Found                              │
│    → 匹配: 写入RequestContext.ModelConfig               │
│                                                          │
│ 8. 协议转换: 解析请求体为UnifiedRequest                   │
│    → 解析失败: 400 Bad Request                          │
│    → 成功: 获取对应ModelAdapter                          │
│                                                          │
│ 9. 前置钩子: PII脱敏检查（Enterprise）                   │
│    → 命中敏感词: 脱敏后替换                              │
└──────────────────────────────┬─────────────────────────┘
                                │
┌─ 代理内核层 ─────────────────▼─────────────────────────┐
│ 10. adapter.TransformRequest(unifiedReq)                │
│     → 生成上游http.Request                              │
│                                                         │
│ 11. ReverseProxy转发到上游API                           │
│     → 超时/重试由ModelConfig控制                        │
│                                                         │
│ 12. 接收上游响应                                         │
│ 13. adapter.TransformResponse(resp)                     │
│     → 生成UnifiedResponse                              │
│ 14. 写回客户端                                           │
│ 15. adapter.ParseTokenUsage(resp)                      │
│     → 提取Token用量                                     │
└──────────────────────────────┬─────────────────────────┘
                                │
┌─ 审计层（异步） ──────────────▼─────────────────────────┐
│ 16. auditor.Submit(requestStart事件)                    │
│ 17. auditor.Finalize(requestID, meta)                   │
│     → Worker异步组装完整审计日志                          │
│     → 落库存储                                          │
│     → SHA256存证（Enterprise）                           │
│     → 日志外推（Enterprise）                             │
└─────────────────────────────────────────────────────────┘
```

### 11.2 流式请求流程（SSE）

```
客户端
  │ POST /v1/chat/completions
  │ Body: {"model":"gpt-4","messages":[...],"stream":true}
  │
  ▼
┌─ 前置流程 ────────────────────────────────────────────────┐
│ 步骤1-9 同非流式（鉴权→限流→路由→转换→钩子）                 │
│ 额外: 检测 stream=true → 动态取消WriteTimeout              │
└──────────────────────────────┬───────────────────────────┘
                                │
┌─ 代理内核层 ─────────────────▼───────────────────────────┐
│ 10. 设置SSE响应头                                         │
│     Content-Type: text/event-stream                       │
│     Cache-Control: no-cache                              │
│     Connection: keep-alive                                │
│                                                          │
│ 11. 包装ResponseWriter为SSEResponseWriter                 │
│     → 注入auditor引用                                     │
│                                                          │
│ 12. adapter.TransformRequest → 转发到上游                  │
│                                                          │
│ 13. 循环读取上游SSE流:                                     │
│     for {                                                 │
│       chunk = read from upstream                          │
│       sseWriter.Write(chunk) →                           │
│         → 写入客户端(同步)                                │
│         → 解析分片 → 投递到审计队列(异步)                   │
│       sseWriter.Flush() → 推送客户端                      │
│                                                          │
│       if chunk contains [DONE] → break                    │
│     }                                                     │
│                                                          │
│ 14. 流结束                                                 │
│     → 重组所有分片为完整应答                               │
│     → 计算SHA256（Enterprise）                            │
│     → 投递Finalize事件到审计队列                           │
└──────────────────────────────┬───────────────────────────┘
                                │
┌─ 断连检测（并发） ────────────▼──────────────────────────┐
│ 监听 r.Context().Done():                                  │
│   → 如果流未完成: 触发断连补全                              │
│   → auditor.MarkDisconnect(requestID, reason)             │
│   → Worker补全日志（标记Disconnected=true）                 │
└───────────────────────────────────────────────────────────┘
```

### 11.3 MCP协议流量流程

```
客户端
  │ POST /v1/mcp/tools/call
  │ Body: {"tool":"search","arguments":{...}}
  │
  ▼
┌─ 管道中间件层 ────────────────────────────────────────────┐
│ 鉴权 → 限流 → 路由匹配（匹配MCP工具配置）                    │
│ 协议转换: MCP协议解析                                       │
│ 前置钩子: 工具调用参数审计（Enterprise）                     │
└──────────────────────────────┬───────────────────────────┘
                                │
┌─ 代理内核层 ─────────────────▼───────────────────────────┐
│ 转发到MCP Server                                           │
│ 接收工具执行结果                                            │
│ 写回客户端                                                  │
└──────────────────────────────┬───────────────────────────┘
                                │
┌─ 审计层（Enterprise） ────────▼──────────────────────────┐
│ 记录: 工具名称、参数、返回结果、调用耗时、调用者             │
│ 全链路留存                                                  │
└──────────────────────────────────────────────────────────┘
```

---

## 12. 双仓库Git管理方案

### 12.1 远端配置

```bash
# 私有仓库（全量代码）
git remote add origin-private git@your-gitlab.com:team/ai-gateway.git

# GitHub开源仓库（过滤enterprise）
git remote add origin-github git@github.com:your-org/neuralgate.git
```

### 12.2 推送脚本

**push-private.sh** — 全量推送到私有仓库：

```bash
#!/bin/bash
# 推送全量代码到私有仓库（含enterprise目录）
# 用法: ./push-private.sh "本次提交说明"
# 示例: ./push-private.sh "feat: 新增达梦存储适配器"

COMMIT_MSG="$1"
if [ -z "$COMMIT_MSG" ]; then
  echo "用法: ./push-private.sh \"提交说明\""
  echo "示例: ./push-private.sh \"feat: 新增达梦存储适配器\""
  exit 1
fi

# 自动获取当前分支名
CURRENT_BRANCH=$(git branch --show-current)
if [ -z "$CURRENT_BRANCH" ]; then
  echo "错误: 无法获取当前分支名，请确保不在 detached HEAD 状态"
  exit 1
fi

TIMESTAMP=$(date +%Y%m%d_%H%M%S)
git add -A
git commit -m "[$TIMESTAMP] $COMMIT_MSG"
git push origin-private "$CURRENT_BRANCH"
echo "Private repo pushed to branch [$CURRENT_BRANCH]: [$TIMESTAMP] $COMMIT_MSG"
```

**push-github-oss.sh** — 过滤enterprise后推送到GitHub：

```bash
#!/bin/bash
# 推送开源代码到GitHub（自动过滤enterprise目录）
# 用法: ./push-github-oss.sh "本次提交说明"
# 示例: ./push-github-oss.sh "fix: 修复SSE流式分片丢失问题"

COMMIT_MSG="$1"
if [ -z "$COMMIT_MSG" ]; then
  echo "用法: ./push-github-oss.sh \"提交说明\""
  echo "示例: ./push-github-oss.sh \"fix: 修复SSE流式分片丢失问题\""
  exit 1
fi

# 自动获取当前分支名，推送到同名远程分支
CURRENT_BRANCH=$(git branch --show-current)
if [ -z "$CURRENT_BRANCH" ]; then
  echo "错误: 无法获取当前分支名，请确保不在 detached HEAD 状态"
  exit 1
fi

TIMESTAMP=$(date +%Y%m%d_%H%M%S)
TEMP_BRANCH="oss-release-$TIMESTAMP"
git checkout -b "$TEMP_BRANCH"

# 使用 sparse-checkout 排除 enterprise 目录
git sparse-checkout init --cone
git sparse-checkout set pkg/core pkg/adapter pkg/plugin/interface.go pkg/plugin/factory.go pkg/plugin/oss pkg/admin pkg/config cmd webui config.yaml go.mod go.sum

# 提交过滤后的代码
git rm -r --cached pkg/plugin/enterprise 2>/dev/null || true
git commit -m "[$TIMESTAMP] $COMMIT_MSG"

# 推送到GitHub同名分支
git push origin-github "$TEMP_BRANCH":"$CURRENT_BRANCH" --force

# 清理临时分支
git sparse-checkout disable
git checkout "$CURRENT_BRANCH"
git branch -D "$TEMP_BRANCH"

echo "GitHub OSS repo pushed to branch [$CURRENT_BRANCH]: [$TIMESTAMP] $COMMIT_MSG (Enterprise code excluded)"
```

### 12.3 .gitignore 关键条目

```gitignore
# 商业授权文件
*.lic
license/

# 编译产物
/neuralgate
/neuralgate-enterprise

# 本地配置（含密钥）
config.local.yaml
```

### 12.4 开发工作流

```
本地完整源码开发
    │
    ├── 日常开发（含enterprise代码）
    │
    ├── 功能完成 → 执行 push-private.sh
    │   → 全量推送到私有仓库（版本备份）
    │
    └── 准备开源发布 → 执行 push-github-oss.sh
        → 自动过滤enterprise目录
        → 仅推送开源代码到GitHub
```

---

## 13. 开发落地顺序

| 阶段 | 内容 | 预估工时 | 依赖 |
|------|------|----------|------|
| Phase 1 | 初始化项目：go mod init、目录结构、git双远端配置 | 0.5天 | 无 |
| Phase 2 | 全部插件接口定义：`interface.go`、工厂BuildTag逻辑 | 1天 | Phase 1 |
| Phase 3 | OSS插件实现：MySQL/SQLite存储、简单审计、内存限流 | 3天 | Phase 2 |
| Phase 4 | 内核实现：Pipeline中间件链、Proxy代理、SSE劫持基础能力 | 4天 | Phase 3 |
| Phase 5 | 模型适配器：OpenAI、通义、智谱、DeepSeek | 2天 | Phase 4 |
| Phase 6 | Gin管理后台：API Key管理、模型配置、审计查询、系统信息 | 3天 | Phase 4 |
| Phase 7 | Enterprise插件：流式审计、SHA256、达梦/金仓、PII脱敏、SIEM导出、授权校验 | 5天 | Phase 4 |
| Phase 8 | 双编译脚本调试、版本隔离验证 | 1天 | Phase 3+7 |
| Phase 9 | 集成测试、性能压测、Docker化 | 2天 | Phase 8 |
| Phase 10 | GitHub开源发布、文档完善 | 1天 | Phase 9 |

**总计**: 约22.5人天

---

## 附录A: 关键Go依赖

| 依赖 | 用途 | 版本要求 |
|------|------|----------|
| github.com/gin-gonic/gin | 管理后台框架 | v1.9+ |
| github.com/go-sql-driver/mysql | MySQL驱动 | v1.7+ |
| modernc.org/sqlite | 纯Go SQLite驱动 | v1.28+ |
| gopkg.in/yaml.v3 | 配置解析 | v3.0+ |
| github.com/google/uuid | UUID生成 | v1.6+ |
| go.uber.org/zap | 日志库 | v1.27+ |
| github.com/redis/go-redis/v9 | Redis客户端(Enterprise) | v9.5+ |

**Enterprise额外依赖**（仅 `enterprise` BuildTag编译时引入）：
- 达梦数据库Go驱动
- 人大金仓Go驱动
- Kafka Go客户端

---

## 附录B: 错误码定义（遵循OpenAI错误格式）

所有错误响应格式：`{"error":{"message":"...","type":"...","param":null,"code":"..."}}`

| HTTP状态 | error.type | error.code | 含义 |
|----------|-----------|------------|------|
| 401 | invalid_request_error | invalid_api_key | API Key无效 |
| 401 | invalid_request_error | api_key_disabled | API Key已禁用 |
| 401 | invalid_request_error | api_key_expired | API Key已过期 |
| 429 | rate_limit_exceeded | quota_exceeded | API Key额度已用尽 |
| 429 | rate_limit_exceeded | rate_limit | 请求频率超限 |
| 429 | rate_limit_exceeded | token_limit | Token用量超限 |
| 404 | invalid_request_error | model_not_found | 模型未找到 |
| 403 | invalid_request_error | model_access_denied | 无权访问该模型 |
| 400 | invalid_request_error | bad_request | 请求格式错误 |
| 400 | invalid_request_error | unsupported_model | 不支持的模型 |
| 502 | api_error | upstream_error | 上游服务错误 |
| 504 | api_error | upstream_timeout | 上游服务超时 |
| 503 | api_error | service_unavailable | 上游服务不可用 |
| 500 | api_error | internal_error | 内部错误 |
| 503 | api_error | audit_queue_overflow | 审计队列溢出（Enterprise） |
| 403 | invalid_request_error | license_invalid | 授权校验失败（Enterprise） |
