// 统一响应包装
export interface ApiResponse<T> {
  code: number
  message: string
  data: T
}

// ===== 模型配置(列表 item 小写)=====
export interface ModelItem {
  id: string
  name: string
  provider: string
  provider_model: string
  base_url: string
  timeout: number
  max_retries: number
  retry_interval: number
  weight: number
  enabled: boolean
  tags: Record<string, string>
  created_at: string
  _upstreams?: UpstreamItem[]
}

export interface ModelCreateRequest {
  name: string
  provider: string
  provider_model: string
  base_url: string
  api_key: string
  timeout?: number
  max_retries?: number
  retry_interval?: number
  weight?: number
  enabled?: boolean
  tags?: Record<string, string>
}

// ===== 上游 =====
export interface UpstreamItem {
  id: string
  base_url: string
  weight: number
  enabled: boolean
  created_at: string
}

export interface UpstreamRequest {
  base_url: string
  api_key: string
  weight?: number
  enabled?: boolean
}

// ===== API Key(列表 item 小写)=====
export interface ApiKeyItem {
  id: string
  key_prefix: string
  name: string
  status: string
  quota: number
  used_quota: number
  rate_limit: number
  allowed_models: string[]
  expires_at: string | null
  created_at: string
}

export interface ApiKeyCreateRequest {
  name: string
  tenant_id?: string
  quota?: number // 缺省 -1(无限)
  rate_limit?: number
  allowed_models?: string[]
  expires_at?: string | null
}

// 创建响应含明文 key(仅一次)
export interface ApiKeyCreateResult {
  id: string
  key: string
  key_hash: string
  key_prefix: string
  name: string
  quota: number
  rate_limit: number
  allowed_models: string[]
  expires_at: string | null
}

// ===== 审计日志(列表直接返回 plugin.AuditLog → 大写字段)=====
export interface AuditLogItem {
  ID: string
  RequestID: string
  TenantID: string
  APIKeyID: string
  ModelName: string
  Provider: string
  RequestMethod: string
  RequestPath: string
  RequestHeaders: Record<string, string>
  RequestBody: string
  ResponseStatus: number
  ResponseBody: string
  SSEChunks: SSEChunkItem[]
  PromptTokens: number
  CompletionTokens: number
  TotalTokens: number
  Duration: number
  ClientIP: string
  IsStream: boolean
  Disconnected: boolean
  DisconnectReason: string
  SHA256Fingerprint: string
  CreatedAt: string
}

export interface SSEChunkItem {
  Index: number
  Data: string
  Timestamp: string
  EventType: string
}

// 审计详情(小写 gin.H)
export interface AuditDetail {
  id: string
  request_id: string
  tenant_id: string
  model_name: string
  provider: string
  request_body: string
  response_body: string
  response_status: number
  sse_chunks: SSEChunkItem[]
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  duration_ms: number
  is_stream: boolean
  disconnected: boolean
  disconnect_reason: string
  created_at: string
  reassembled?: string
}

export interface AuditQueryParams {
  page?: number
  size?: number
  tenant_id?: string
  api_key_id?: string
  model_name?: string
  request_id?: string
  response_status?: number
  keyword?: string
  start_time?: string
  end_time?: string
  is_stream?: string
}

// ===== 限流配置(直接返回 plugin.RateLimitConfig → 大写)=====
export interface RateLimitItem {
  ID: string
  TenantID: string
  ModelName: string
  RequestsPerSec: number
  TokensPerMin: number
  Strategy: string
  Enabled: boolean
  CreatedAt: string
  UpdatedAt: string
}

export interface RateLimitRequest {
  tenant_id?: string
  model_name?: string
  requests_per_sec: number
  tokens_per_min: number
  strategy: string
  enabled?: boolean
}

// ===== 系统信息 =====
export interface SystemInfo {
  version: string
  build_time: string
  git_commit: string
  edition: string
  uptime: string
  db_status: string
  audit_queue_status: { status: string }
  rate_limiter_status: { status: string }
  license?: {
    status: string
    customer?: string
    expires_at?: string
    features_count?: number
  }
  tamper?: {
    unresolved_count: number
  }
}

// ===== 篡改告警（GET /api/tamper-alerts） =====
export interface TamperAlertItem {
  id: string
  audit_log_id: string
  reason: string
  resolved: boolean
  first_seen_at: string
  last_checked_at: string
}

export interface TamperAlertQueryParams {
  page?: number
  size?: number
  resolved?: string   // 'true'/'false'，不传=全部
}

// ===== 授权信息（GET /api/license，脱敏后） =====
export interface LicenseDetail {
  status: string            // valid/expired/invalid/missing/oss
  message?: string          // 降级原因说明
  edition: string           // 运行版本（降级后为 oss）
  license_key?: string      // 授权码（前 8 位 + ****）
  product_name?: string
  customer_name?: string
  max_nodes?: number
  max_tenants?: number
  issued_at?: string
  expires_at?: string
  features?: string[]
  is_offline?: boolean
  signed: boolean           // 是否携带签名（不回显签名全文）
  days_remaining?: number   // 剩余天数（仅有效时）
}

// ===== 分页列表 =====
export interface Paged<T> {
  items: T[]
  total: number
  page: number
  size: number
}

// ===== 隐私合规(E4) =====
export interface PrivacyRuleItem {
  id?: string
  rule_type: 'pii' | 'injection' | 'output'
  name: string
  pattern: string
  replacement: string
  scope: 'request' | 'response' | 'both'
  action?: 'redact' | 'block'
  enabled: boolean
  created_at?: string
  updated_at?: string
}

export interface PrivacyWhitelistItem {
  id?: string
  pattern: string
  note: string
  enabled: boolean
  created_at?: string
}

export interface SecurityEventItem {
  id: string
  request_id: string
  rule_name: string
  snippet: string
  client_ip: string
  model_name: string
  created_at: string
}

// ===== RBAC 权限体系(E5) =====
export interface TenantItem {
  id?: string
  name: string
  code: string
  status: 'active' | 'disabled'
  config?: Record<string, string>
  created_at?: string
}

export interface RoleItem {
  id?: string
  name: string
  tenant_id: string
  permissions: string[]
  created_at?: string
}

export interface AdminUserItem {
  id?: string
  username: string
  tenant_id: string
  role_id: string
  status: 'active' | 'disabled'
  created_at?: string
}

export interface OperationLogItem {
  id: string
  user_id: string
  username: string
  method: string
  path: string
  target_id: string
  status_code: number
  client_ip: string
  created_at: string
}

// ===== 合规报表(E6) =====
export interface DimensionStat {
  key: string // 模型名/租户ID；空串归 "(global)"
  requests: number
  tokens: number
}

export interface ReportContent {
  total_requests: number
  total_tokens: number
  error_4xx: number
  error_5xx: number
  stream_count: number
  by_model: DimensionStat[]
  by_tenant: DimensionStat[]
}

export interface ReportItem {
  id: string
  period_type: 'day' | 'week' | 'month'
  period_start: string // RFC3339
  period_end: string   // RFC3339
  generated_at: string // RFC3339
  content?: ReportContent | null
}

// ===== E7 MCP 智能体审计 =====

// MCP 上游服务器配置（Streamable HTTP 端点）
export interface MCPServerItem {
  id: string
  name: string
  endpoint: string
  headers?: Record<string, string> | null
  enabled: boolean
  created_at: string
  updated_at: string
}

// 工具调用审计记录（PRD 3.9 十三字段）
export interface MCPAuditLogItem {
  id: string
  request_id: string
  tenant_id: string
  api_key_id: string
  tool_name: string
  tool_arguments: string
  tool_result: string
  caller_agent: string
  duration_ms: number
  status: 'success' | 'failed'
  error_message: string
  client_ip: string
  created_at: string
}
