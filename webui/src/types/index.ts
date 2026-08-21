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
}

// ===== 分页列表 =====
export interface Paged<T> {
  items: T[]
  total: number
  page: number
  size: number
}
