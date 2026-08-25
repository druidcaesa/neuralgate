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

// Package plugin 插件契约层：存储、审计、限流、导出、授权等插件接口与核心数据结构定义。
// RequestContext 定义在 pkg/core/context.go。
package plugin

import "time"

// ModelConfig 模型路由配置
type ModelConfig struct {
	ID            string            // 配置ID
	ModelName     string            // 对外模型名称（如 "gpt-4"）
	Provider      string            // 供应商（openai/qwen/zhipu/deepseek/自定义）
	ProviderModel string            // 供应商实际模型名
	BaseURL       string            // 上游API地址
	APIKey        string            // 上游API Key
	Timeout       time.Duration     // 请求超时
	MaxRetries    int               // 最大重试次数
	RetryInterval time.Duration     // 重试间隔
	Weight        int               // 负载均衡权重
	Enabled       bool              // 是否启用
	Tags          map[string]string // 扩展标签
	CreatedAt     time.Time         // 创建时间
	UpdatedAt     time.Time         // 更新时间
}

// Upstream 模型的上游端点（负载均衡：一个 ModelConfig 可挂多个 Upstream）
type Upstream struct {
	ID            string    // 上游ID
	ModelConfigID string    // 所属模型配置ID
	BaseURL       string    // 上游API地址
	APIKey        string    // 上游API Key（加密存储）
	Weight        int       // 加权轮询权重
	Enabled       bool      // 是否启用
	CreatedAt     time.Time // 创建时间
	UpdatedAt     time.Time // 更新时间
}

// APIKey 租户API Key
type APIKey struct {
	ID            string       // 主键ID
	KeyHash       string       // Key的SHA256哈希（不存明文）
	KeyPrefix     string       // Key前缀（用于展示，如 "ng-xxxx"）
	TenantID      string       // 所属租户ID
	TenantName    string       // 租户名称
	Name          string       // Key名称/备注
	Status        APIKeyStatus // 状态：active/disabled/expired
	Quota         int64        // 额度（Token数，-1为无限）
	UsedQuota     int64        // 已用额度
	RateLimit     int          // 独立限流（请求/秒）
	AllowedModels []string     // 允许访问的模型列表
	ExpiresAt     *time.Time   // 过期时间
	CreatedAt     time.Time    // 创建时间
	UpdatedAt     time.Time    // 更新时间
	CreatedBy     string       // 创建人
}

type APIKeyStatus string

const (
	APIKeyStatusActive   APIKeyStatus = "active"
	APIKeyStatusDisabled APIKeyStatus = "disabled"
	APIKeyStatusExpired  APIKeyStatus = "expired"
)

// AdminUser 管理后台账号（登录凭证存数据库，密码只存 bcrypt 哈希）
type AdminUser struct {
	ID           string          // 主键ID
	Username     string          // 用户名（唯一）
	PasswordHash string          // bcrypt 哈希
	Status       AdminUserStatus // 状态：active/disabled
	CreatedAt    time.Time       // 创建时间
	UpdatedAt    time.Time       // 更新时间
	LastLoginAt  *time.Time      // 最近登录时间
}

type AdminUserStatus string

const (
	AdminUserStatusActive   AdminUserStatus = "active"
	AdminUserStatusDisabled AdminUserStatus = "disabled"
)

// AuditLog 审计日志记录
type AuditLog struct {
	ID                string            // 主键ID
	RequestID         string            // 请求唯一ID
	TenantID          string            // 租户ID
	APIKeyID          string            // API Key ID
	ModelName         string            // 模型名称
	Provider          string            // 供应商
	RequestMethod     string            // HTTP方法
	RequestPath       string            // 请求路径
	RequestHeaders    map[string]string // 请求头（脱敏后）
	RequestBody       string            // 完整请求体（脱敏后）
	ResponseStatus    int               // 响应状态码
	ResponseBody      string            // 完整响应体（非流式）
	SSEChunks         []SSEChunk        // SSE分片列表（流式）
	PromptTokens      int               // Prompt Token数
	CompletionTokens  int               // Completion Token数
	TotalTokens       int               // 总Token数
	Duration          int64             // 耗时（毫秒）
	ClientIP          string            // 客户端IP
	IsStream          bool              // 是否流式
	Disconnected      bool              // 是否断连
	DisconnectReason  string            // 断连原因
	SHA256Fingerprint string            // 全量内容SHA256指纹
	CreatedAt         time.Time         // 记录时间
}

// SSEChunk SSE流式分片
type SSEChunk struct {
	Index     int       // 分片序号
	Data      string    // 分片原始数据
	Timestamp time.Time // 接收时间
	EventType string    // 事件类型（data/event/id/retry）
}

// Tenant 租户
type Tenant struct {
	ID        string            // 租户ID
	Name      string            // 租户名称
	Code      string            // 租户编码
	Status    TenantStatus      // 状态
	Config    map[string]string // 租户配置
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Role 角色（RBAC）
type Role struct {
	ID          string   // 角色ID
	TenantID    string   // 所属租户
	Name        string   // 角色名称
	Permissions []string // 权限列表
	CreatedAt   time.Time
}

// User 管理后台用户
type User struct {
	ID           string     // 用户ID
	TenantID     string     // 所属租户
	Username     string     // 用户名
	PasswordHash string     // 密码哈希
	RoleID       string     // 角色ID
	Status       UserStatus // 状态
	LastLoginAt  *time.Time // 最后登录时间
	CreatedAt    time.Time
}

// RateLimitConfig 限流配置
type RateLimitConfig struct {
	ID             string    // 配置ID
	TenantID       string    // 租户ID（空表示全局）
	ModelName      string    // 模型名称（空表示全模型）
	RequestsPerSec int       // 每秒请求数
	TokensPerMin   int64     // 每分钟Token数
	Strategy       string    // 策略：token_bucket/sliding_window
	Enabled        bool      // 是否启用
	CreatedAt      time.Time // 创建时间
	UpdatedAt      time.Time // 更新时间
}

// LicenseInfo 商业授权信息（license.json 的结构）
type LicenseInfo struct {
	LicenseKey   string    `json:"license_key"`   // 授权码
	ProductName  string    `json:"product_name"`  // 产品名称
	CustomerName string    `json:"customer_name"` // 客户名称
	MaxNodes     int       `json:"max_nodes"`     // 最大节点数
	MaxTenants   int       `json:"max_tenants"`   // 最大租户数
	IssuedAt     time.Time `json:"issued_at"`     // 签发时间
	ExpiresAt    time.Time `json:"expires_at"`    // 过期时间
	Features     []string  `json:"features"`      // 授权功能列表
	Signature    string    `json:"signature"`     // 签名
	IsOffline    bool      `json:"is_offline"`    // 是否离线授权
}

// PluginFactory 插件工厂统一入口
// 通过 BuildTag 条件编译，OSS和Enterprise各自注册实现
type PluginFactory interface {
	CreateStorage() StoragePlugin
	CreateAuditor() AuditPipeline
	CreateRateLimiter() RateLimitPlugin
	CreateExporter() LogExporter
	CreateLicenseValidator() LicenseValidator
}

// StoragePlugin 存储抽象接口
type StoragePlugin interface {
	// 初始化存储连接
	Init(config map[string]interface{}) error

	// API Key 管理
	GetAPIKey(keyHash string) (*APIKey, error)
	GetAPIKeyByID(id string) (*APIKey, error) // 按主键查询(管理后台用)
	SaveAPIKey(key *APIKey) error
	IncrementAPIKeyUsage(keyID string, delta int64) error // 原子累加已用额度(并发安全)
	ListAPIKeys(tenantID string, page, size int) ([]*APIKey, int64, error)
	DeleteAPIKey(keyID string) error

	// 管理后台账号
	CountAdminUsers() (int64, error)
	GetAdminUserByUsername(username string) (*AdminUser, error)
	GetAdminUserByID(id string) (*AdminUser, error)
	SaveAdminUser(user *AdminUser) error // UPSERT：按主键插入或全量更新

	// 模型配置管理
	GetModelConfig(modelName string) (*ModelConfig, error)
	GetModelConfigByID(id string) (*ModelConfig, error) // 按主键查询(管理后台用)
	ListModelConfigs(page, size int) ([]*ModelConfig, int64, error)
	SaveModelConfig(config *ModelConfig) error
	DeleteModelConfig(id string) error

	// 审计日志
	SaveAuditLog(log *AuditLog) error
	BatchSaveAuditLogs(logs []*AuditLog) error
	QueryAuditLogs(filter AuditLogFilter, page, size int) ([]*AuditLog, int64, error)

	// 留存清理：删除 cutoff 之前的审计日志，返回删除条数
	DeleteAuditLogsBefore(cutoff time.Time) (int64, error)

	// 篡改告警：同一 AuditLogID 存在未处置告警则更新检查时间，否则插入
	SaveTamperAlerts(alerts []*TamperAlert) error

	// 篡改告警查询：resolved nil=全部
	ListTamperAlerts(resolved *bool, page, size int) ([]*TamperAlert, int64, error)

	// 标记篡改告警处置状态
	SetTamperAlertResolved(id string, resolved bool) error

	// 限流配置管理
	GetRateLimitConfig(tenantID, modelName string) (*RateLimitConfig, error)
	SaveRateLimitConfig(cfg *RateLimitConfig) error
	ListRateLimitConfigs(page, size int) ([]*RateLimitConfig, int64, error)
	DeleteRateLimitConfig(id string) error

	// 上游管理（负载均衡）
	ListUpstreams(modelConfigID string) ([]*Upstream, error)
	GetUpstreamByID(id string) (*Upstream, error)
	SaveUpstream(up *Upstream) error
	DeleteUpstream(id string) error

	// 健康检查
	Ping() error
	Close() error
}

// AuditLogFilter 审计日志查询过滤
type AuditLogFilter struct {
	TenantID  string
	APIKeyID  string
	ModelName string
	RequestID string // 按请求 ID 精查(审计详情)
	StartTime *time.Time
	EndTime   *time.Time
	Status    int    // 响应状态码过滤
	IsStream  *bool  // 是否流式
	Keyword   string // 全文搜索关键词
}

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

	// 标记客户端断连(meta 携带断连时已收集的 tokens/duration)
	MarkDisconnect(requestID string, reason string, meta *AuditMeta) error

	// 关闭管道，flush剩余数据
	Shutdown() error
}

// AuditEvent 审计事件
type AuditEvent struct {
	RequestID string
	EventType AuditEventType
	Timestamp time.Time
	Data      interface{}
}

type AuditEventType string

const (
	AuditEventRequestStart    AuditEventType = "request_start"
	AuditEventRequestComplete AuditEventType = "request_complete"
	AuditEventSSEChunk        AuditEventType = "sse_chunk"
	AuditEventDisconnect      AuditEventType = "disconnect"
	AuditEventError           AuditEventType = "error"
)

// AuditConfig 审计配置
type AuditConfig struct {
	QueueSize     int           // 环形队列大小
	WorkerCount   int           // worker数量
	BatchSize     int           // 批量写入大小
	FlushInterval time.Duration // 刷新间隔
	EnableSHA256  bool          // 是否启用SHA256存证
	RetentionDays int           // 日志保留天数
}

// AuditMeta 审计元信息
type AuditMeta struct {
	ResponseStatus   int
	ResponseBody     string // 非流式响应体(流式留空,分片已单独留存)
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	Duration         int64
}

// RateLimitPlugin 限流抽象接口
type RateLimitPlugin interface {
	Init(config map[string]interface{}) error

	// 尝试获取令牌，返回是否允许及剩余配额
	Allow(tenantID string, model string, tokens int) (allowed bool, remaining int64, err error)

	// 获取当前限流状态
	Status(tenantID string, model string) (current int64, limit int64, resetAt time.Time)

	// 重置限流计数器
	Reset(tenantID string, model string) error

	// RecordTokens 记录已消耗 token（TPM 事后回补，请求完成后调用）
	RecordTokens(tenantID string, model string, tokens int) error

	// ReloadConfig 从存储重载限流配置（管理后台写操作后触发）
	ReloadConfig() error
}

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

// TamperAlert 篡改告警
type TamperAlert struct {
	ID            string    `json:"id"`              // 主键
	AuditLogID    string    `json:"audit_log_id"`    // 被篡改日志 ID
	Reason        string    `json:"reason"`          // 不一致描述
	Resolved      bool      `json:"resolved"`        // 是否已处置
	FirstSeenAt   time.Time `json:"first_seen_at"`   // 首次发现
	LastCheckedAt time.Time `json:"last_checked_at"` // 最近一次确认仍不一致
}

// FingerprintFunc 审计指纹计算函数（enterprise 提供，OSS 为 nil 不计算）
type FingerprintFunc func(log *AuditLog) string

// FingerprintHook 可选能力：审计器支持注入指纹计算
type FingerprintHook interface {
	SetFingerprintFunc(fn FingerprintFunc)
}

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

// TenantStatus 租户状态
type TenantStatus string

const (
	TenantStatusActive   TenantStatus = "active"
	TenantStatusDisabled TenantStatus = "disabled"
)

// UserStatus 后台用户状态
type UserStatus string

const (
	UserStatusActive   UserStatus = "active"
	UserStatusDisabled UserStatus = "disabled"
)
