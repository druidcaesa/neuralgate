# OSS 端到端打通 — 设计文档

> **日期**: 2026-08-20
> **目标**: 按《NeuralGate_技术架构详细设计.md》Phase 3/4/6 完成 OSS 版核心实现,使 OSS 版可端到端部署使用
> **范围**: 存储层(MySQL/SQLite) + 中间件真实逻辑 + 代理内核 + SSE 审计接线 + 适配器实现 + 管理后台 CRUD
> **版本**: V1.0

---

## 1. 背景与现状

### 1.1 现状(已实现)

| 模块 | 状态 |
|------|------|
| 项目骨架、双仓库脚本 | ✅ 完成 |
| 插件接口 `pkg/plugin/interface.go`、BuildTag 工厂 | ✅ 完成 |
| 内存存储 `storage_mem.go`(完整 CRUD) | ✅ 完成 |
| 简单审计 `audit_simple.go`、内存限流 `limit_mem.go`、环形队列 `ring_buffer.go` | ✅ 完成 |
| 适配器接口 + 注册中心 + 4 个适配器骨架 | ⚠️ 转换方法全部 `not implemented` |
| Pipeline 骨架(鉴权/限流/路由中间件) | ⚠️ 三个中间件全部占位放行 |
| ProxyCore | ⚠️ 占位,所有 `/v1/*` 返回 503 |
| SSEResponseWriter / Reassembler / DisconnectHandler | ⚠️ 骨架未接线 |
| 管理后台 AdminServer | ⚠️ 仅 /healthz + /api/ping |
| 配置 `config.go` | ✅ 完整 |

### 1.2 目标状态(本次迭代完成)

OSS 版端到端可用:管理后台配置模型与 API Key → 客户端通过网关调用 `/v1/chat/completions`(非流式+流式)→ 审计日志可查。

### 1.3 非目标(本次不做)

- Enterprise 插件(audit_stream/达梦/金仓/Redis限流/SIEM/授权/PII) — 下一迭代
- 通义/智谱转换的"生产级完备性" — 本次实现基础转换,单测用 mock 验证
- 后台登录/RBAC(OSS 无多租户,后台直接开放) — Enterprise 迭代
- 模型配置热更新事件机制 — 本次每次请求直接查存储(见 3.2)

---

## 2. 存储层(MySQL + SQLite)

### 2.1 文件结构

```
pkg/plugin/oss/
├── storage_sql.go       # 新增:共享 SQL 实现(通用 CRUD,基于 database/sql)
├── storage_mysql.go     # 新增:MySQL 专用(Open + 建表),driver="mysql"
├── storage_sqlite.go    # 新增:SQLite 专用(Open + 建表),driver="sqlite"
├── storage_mem.go       # 保留(内存实现)
└── crypto.go            # 新增:AES-GCM 加解密(上游 API Key 加密存储)
```

两个驱动均使用 `?` 占位符、`LIMIT/OFFSET` 分页,CRUD 逻辑共享于 `storage_sql.go`;MySQL/SQLite 文件只负责连接打开与建表方言。

### 2.2 新增依赖

| 依赖 | 用途 |
|------|------|
| `github.com/go-sql-driver/mysql` | MySQL 驱动 |
| `modernc.org/sqlite` | 纯 Go SQLite 驱动(无 CGO) |

### 2.3 表结构

**api_keys**(对齐 `plugin.APIKey`):

```sql
CREATE TABLE api_keys (
  id            TEXT PRIMARY KEY,
  key_hash      TEXT NOT NULL UNIQUE,        -- SHA256,鉴权 O(1) 查询
  key_prefix    TEXT NOT NULL,               -- 展示用,如 ng-xxxx
  tenant_id     TEXT NOT NULL DEFAULT '',
  name          TEXT NOT NULL,
  status        TEXT NOT NULL DEFAULT 'active',  -- active/disabled/expired
  quota         INTEGER NOT NULL DEFAULT -1,     -- -1 为无限
  used_quota    INTEGER NOT NULL DEFAULT 0,
  rate_limit    INTEGER NOT NULL DEFAULT 10,
  allowed_models TEXT NOT NULL DEFAULT '[]',     -- JSON 数组
  expires_at    INTEGER,                          -- unix 毫秒,NULL=永不过期
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL,
  created_by    TEXT NOT NULL DEFAULT '',
  deleted       INTEGER NOT NULL DEFAULT 0        -- 软删除标记
);
```

**model_configs**(对齐 `plugin.ModelConfig`):

```sql
CREATE TABLE model_configs (
  id             TEXT PRIMARY KEY,
  model_name     TEXT NOT NULL UNIQUE,        -- 对外模型名称
  provider       TEXT NOT NULL,               -- openai/tongyi/zhipu/deepseek
  provider_model TEXT NOT NULL,
  base_url       TEXT NOT NULL,
  api_key        TEXT NOT NULL,               -- AES-GCM 加密存储
  encrypted      INTEGER NOT NULL DEFAULT 1,  -- 加密标记(1=已加密)
  timeout        INTEGER NOT NULL DEFAULT 60,
  max_retries    INTEGER NOT NULL DEFAULT 2,
  retry_interval INTEGER NOT NULL DEFAULT 3,
  weight         INTEGER NOT NULL DEFAULT 1,
  enabled        INTEGER NOT NULL DEFAULT 1,
  tags           TEXT NOT NULL DEFAULT '{}',  -- JSON map
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL
);
```

**audit_logs**(对齐 `plugin.AuditLog`):

```sql
CREATE TABLE audit_logs (
  id                TEXT PRIMARY KEY,
  request_id        TEXT NOT NULL,
  tenant_id         TEXT NOT NULL DEFAULT '',
  api_key_id        TEXT NOT NULL DEFAULT '',
  model_name        TEXT NOT NULL DEFAULT '',
  provider          TEXT NOT NULL DEFAULT '',
  request_method    TEXT NOT NULL DEFAULT '',
  request_path      TEXT NOT NULL DEFAULT '',
  request_headers   TEXT NOT NULL DEFAULT '{}',   -- JSON map
  request_body      TEXT NOT NULL DEFAULT '',
  response_status   INTEGER NOT NULL DEFAULT 0,
  response_body     TEXT NOT NULL DEFAULT '',
  sse_chunks        TEXT NOT NULL DEFAULT '[]',   -- JSON 数组
  prompt_tokens     INTEGER NOT NULL DEFAULT 0,
  completion_tokens INTEGER NOT NULL DEFAULT 0,
  total_tokens      INTEGER NOT NULL DEFAULT 0,
  duration_ms       INTEGER NOT NULL DEFAULT 0,
  client_ip         TEXT NOT NULL DEFAULT '',
  is_stream         INTEGER NOT NULL DEFAULT 0,
  disconnected      INTEGER NOT NULL DEFAULT 0,
  disconnect_reason TEXT NOT NULL DEFAULT '',
  sha256_fingerprint TEXT NOT NULL DEFAULT '',    -- 预留(Enterprise 填充)
  created_at        INTEGER NOT NULL
);
CREATE INDEX idx_audit_created ON audit_logs(created_at);
CREATE INDEX idx_audit_tenant  ON audit_logs(tenant_id);
CREATE INDEX idx_audit_model   ON audit_logs(model_name);
```

**设计要点**:

| 要点 | 说明 |
|------|------|
| 时间存储 | 统一 `int64 unix 毫秒`(MySQL BIGINT / SQLite INTEGER),时间比较/排序/过滤在 SQL 层直接正确;API 响应时转换 |
| JSON 字段 | allowed_models/tags/request_headers/sse_chunks 存 JSON 文本 |
| 软删除 | api_keys.deleted 标记,DeleteAPIKey 置 1;列表与鉴权查询过滤 `deleted=0` |
| 上游 Key 加密 | AES-GCM,密钥从 `config.yaml` 新增 `storage.encrypt_key` 读取;`encrypted` 标记兼容旧数据 |

### 2.4 工厂分发

`pkg/plugin/oss/factory.go` 的 `CreateStorage` 改为按 `driver` 选择(Init 时接收 `driver` + `dsn` + `encrypt_key`):

```go
// storage.Init({"driver":"mem"} | {"driver":"mysql","dsn":...,"encrypt_key":...} | {"driver":"sqlite",...})
```

启动流程 `cmd/gateway/main.go` 传 `cfg.Storage` 全部字段给 Init。

### 2.5 配置默认值

`config.yaml` 默认 `driver: sqlite`(开箱即用零外部依赖),MySQL 保留注释示例。`storage.encrypt_key` 新增字段,默认值固定 32 字节字符串(文档可公开,生产需修改)。

---

## 3. 中间件真实逻辑(pkg/core)

替换三个占位中间件。失败响应一律 OpenAI 错误格式 `{error:{message,type,code}}`。

### 3.1 AuthMiddleware(鉴权)

1. 提取 `Authorization: Bearer {key}`
2. 无 Key → 401 `invalid_api_key`
3. SHA256(Key) → `storage.GetAPIKey(keyHash)` → 不存在/deleted → 401 `invalid_api_key`
4. status=disabled → 401 `api_key_disabled`;expired(expires_at 已过)→ 401 `api_key_expired`
5. quota != -1 且 used_quota >= quota → 429 `quota_exceeded`
6. 写入 `rc.APIKeyID`、`rc.TenantID`

### 3.2 RouteMatchMiddleware(路由匹配)

1. 读取请求体(上限 1MB)→ 解析 `model` 字段 → 缓存到 `rc.RequestBody` → 恢复 `r.Body`
2. `storage.GetModelConfig(model)` → 不存在/!enabled → 404 `model_not_found`
3. Key 的 `allowed_models` 非空且不含该模型 → 403 `model_access_denied`
4. 注册中心获取适配器 → 写 `rc.ModelConfig`、`rc.Adapter`

> **热更新说明**:本次不做内存路由缓存,每次请求查存储(存储层有索引,量级满足);Enterprise 迭代再引入缓存+失效机制。

### 3.3 RateLimitMiddleware(限流)

1. `rateLimiter.Allow(tenantID, model, 0)`(token 用量由代理内核 Finalize 后回补)
2. 通过 → 设置 `X-RateLimit-*` 响应 Header(从 Status 获取)
3. 超限 → 429 `rate_limit` + `Retry-After`
4. 限流器异常 → **降级放行** + 记录错误日志

---

## 4. 代理内核 ProxyCore(pkg/core/proxy.go)

替换 503 占位,按文档 8.5 端点分类。

### 4.1 端点分类

| 端点 | 处理方式 |
|------|----------|
| `GET /healthz` | 本地 200 |
| `GET /v1/models`、`GET /v1/models/{model}` | **本地响应**:存储列出启用模型,OpenAI `{object:"list"}` 格式,不转发上游 |
| `POST /v1/chat/completions`、`POST /v1/embeddings` | **核心代理**:完整链路 |
| `/v1/completions`、`/v1/moderations`、`/v1/images/*`、`/v1/audio/*`、`/v1/files*` | **透传**:鉴权+限流后原样转发上游 |

### 4.2 核心代理转发流程(chat/completions)

```
① 原生透传(openai/deepseek,SupportsNativeProxy()==true):
   rc.RequestBody 解析为 map → 替换 model=ProviderModel → 重新序列化 → 构造上游请求
② 非原生(tongyi/zhipu):
   adapter.TransformRequest(unifiedReq, rawBody) → 上游请求
③ 超时/重试: ModelConfig.Timeout 控制超时;连接错误/上游 5xx 自动重试 MaxRetries 次(间隔 RetryInterval),4xx 不重试(直接透传错误)
④ 响应:
   非流式 → 原样写回客户端(同时 ParseTokenUsage 提取用量 → Finalize 审计)
   流式 → 包装 SSEResponseWriter → 循环读上游分片 → 写客户端 + 投递审计
⑤ 错误: 上游返回非 2xx → 502 upstream_error + ParseError 提取 message
```

### 4.3 限流 Header 与用量回补

非流式:ParseTokenUsage 后 `UpdateAPIKeyQuota`(used_quota 累加);流式:末尾分片 ParseStreamUsage 后同样回补。

---

## 5. SSE 审计接线(OSS 基础版)

| 组件 | 改动 |
|------|------|
| `SSEResponseWriter.Write` | 补全:按 `\n\n` 切分 SSE 事件 → 解析 `data:` 行 → 构建 `plugin.SSEChunk`(Index/Data/Timestamp)→ `auditor.SubmitSSEChunk(requestID, chunk)`;`[DONE]` 或流结束时触发 `Finalize` |
| `StreamReassembler.Reassemble` | 实现:分片 data 拼接重组(Enterprise 完整留存此函数同样复用) |
| `DisconnectHandler.Watch` | 流式请求启动时接线:监听 `r.Context().Done()` → `MarkDisconnect(requestID, reason)` |
| 审计事件 | 请求开始 `Submit(AuditEventRequestStart)`;结束 `Finalize(requestID, meta)` |

> **范围说明**:OSS 仅做元数据 + Token 用量审计(PRD 3.4 的分片留存/重组/SHA256 是 Enterprise);但分片捕获代码路径本次走通,Enterprise `audit_stream.go` 直接复用接线。

---

## 6. 适配器实现(pkg/adapter)

| 适配器 | 本次实现 |
|--------|----------|
| OpenAI/DeepSeek(原生透传) | `ParseTokenUsage`/`ParseStreamUsage`/`ParseError` 真实解析(响应体/分片 JSON) |
| Tongyi(DashScope) | TransformRequest(OpenAI→DashScope 格式)、TransformResponse、ParseError;单测 mock 验证 |
| Zhipu(GLM) | TransformRequest(OpenAI→GLM 格式)、TransformResponse、ParseError;单测 mock 验证 |

---

## 7. 管理后台 CRUD(pkg/admin)

| 文件 | 路由 | 功能 |
|------|------|------|
| `api_key.go` | `POST /api/api-keys` | 创建:生成 `ng-` 前缀随机 Key,存 SHA256,**明文仅返回一次** |
| | `GET /api/api-keys` | 分页列表,Key 脱敏(`ng-xxxx****`),含 quota/status/expires_at |
| | `PATCH /api/api-keys/:id` | 禁用/启用 |
| | `DELETE /api/api-keys/:id` | 软删除(deleted=1) |
| `model_config.go` | `POST/GET/PUT/DELETE /api/models` | 模型配置 CRUD,字段校验按 PRD 3.1(name 唯一、URL 合法、timeout 1-300、max_retries 0-5 等) |
| | `POST /api/models/:id/test` | 测试连接:轻量请求到上游,返回延迟/错误 |
| `audit_api.go` | `GET /api/audit-logs` | 分页查询(tenant/model/状态/时间/流式/关键词过滤) |
| | `GET /api/audit-logs/:id` | 详情(含 SSE 分片、重组文本) |
| | `GET /api/audit-logs/export` | CSV/JSON 导出 |
| `system.go` | `GET /api/system` | 版本/edition/uptime/db 状态/审计队列状态/限流器状态 |
| `response.go` | - | 统一响应格式(全后台接口) |

> 管理后台 OSS 版无鉴权(内网部署假设),Enterprise 迭代补登录+RBAC。

---

## 8. 测试与验证策略

| 层级 | 方式 | 覆盖 |
|------|------|------|
| 存储单测 | SQLite 临时文件 DB + MemStorage 对照 | 全部 CRUD + 过滤 + 软删除 + 加密解密 |
| 中间件单测 | httptest + MemStorage | 401/429/403/404 各分支 |
| 代理内核测试 | httptest mock 上游(OpenAI 格式) | 路由替换、非流式透传、SSE 分片捕获、Token 解析、超时重试、错误透传 |
| 适配器测试 | mock 响应 JSON | 通义/智谱转换、用量解析 |
| Admin 测试 | httptest | 各 CRUD 分支、Key 明文仅一次 |
| 端到端 | 完整启动 + mock 上游 | 建模型→建 Key→调用→查审计 全链路 |

MySQL 无真实环境:共享 SQL 逻辑由 SQLite 测试覆盖,MySQL 连接测试 `t.Skip` 待环境。

---

## 9. 涉及文件清单

```
新增:
  pkg/plugin/oss/storage_sql.go     # 共享 SQL CRUD
  pkg/plugin/oss/storage_mysql.go   # MySQL 连接+建表
  pkg/plugin/oss/storage_sqlite.go  # SQLite 连接+建表
  pkg/plugin/oss/crypto.go          # AES-GCM 加解密
  pkg/admin/api_key.go / model_config.go / audit_api.go / system.go / response.go
修改:
  pkg/plugin/oss/factory.go         # CreateStorage 按 driver 分发
  pkg/core/middleware_auth.go       # 真实鉴权
  pkg/core/middleware_route.go      # 真实路由匹配
  pkg/core/middleware_limit.go      # 真实限流
  pkg/core/proxy.go                 # 真实代理内核
  pkg/core/sse_writer.go            # 分片捕获
  pkg/core/sse_reassembler.go       # 重组实现
  pkg/core/disconnect_handler.go    # 接线(或代理内核内直接调用)
  pkg/adapter/openai.go / deepseek.go / tongyi.go / zhipu.go
  pkg/admin/router.go               # 注册 CRUD 路由
  pkg/config/config.go              # storage.encrypt_key 字段
  cmd/gateway/main.go               # 传 dsn/encrypt_key
  config.yaml / go.mod
```

---

## 10. 决策记录

| # | 决策 | 理由 |
|---|------|------|
| 1 | 默认 driver 为 sqlite | 开箱即用零外部依赖;MySQL 注释示例保留 |
| 2 | 上游 API Key AES-GCM 加密,密钥在 config.yaml | 满足 PRD 3.1 加密存储要求,标准库实现 |
| 3 | Key 软删除(deleted 标记) | PRD 3.2 要求保留历史审计 |
| 4 | 时间统一 unix 毫秒 int64 | MySQL/SQLite 天然兼容,SQL 层比较正确 |
| 5 | OSS 审计仅元数据+用量,分片路径走通 | PRD 3.4 分片留存为 Enterprise;代码路径可复用 |
| 6 | 后台无鉴权(OSS) | Enterprise 迭代做 RBAC |
| 7 | 无内存路由缓存,每次查存储 | 存储索引满足量级;缓存+失效在 Enterprise 迭代 |
