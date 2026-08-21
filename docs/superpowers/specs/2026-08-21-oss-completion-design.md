# OSS 补全至 PRD 完整度 — 设计文档

> **日期**: 2026-08-21
> **目标**: 补齐 OSS 版对照 PRD 标记 `OSS+` 但当前占位或缺失的功能:限流增强、限流管理、负载均衡、透传端点精确路由、IP 黑白名单、TLS
> **前置**: OSS 端到端已完成(112 tests,MySQL 真库验证),核心代理链路可用
> **版本**: V1.0

---

## 1. 背景与现状

### 1.1 已完成(不在本次范围)

鉴权、路由、代理转发(非流式+SSE 流式)、审计落库、API Key/模型/审计 CRUD、超时重试、Token 计量、额度扣减、MySQL/SQLite/mem 存储。

### 1.2 本次补齐的缺口(对照 PRD OSS+)

| # | 功能 | PRD 位置 | 现状 |
|---|------|----------|------|
| 1 | Token 用量限流(TPM) | 3.3 | ❌ 只有 RPS 固定窗口,tokens 参数未用 |
| 2 | 限流策略 token_bucket/sliding_window | 3.3 | ❌ 仅固定窗口,strategy 未实现 |
| 3 | 限流管理(租户级/模型级配置 CRUD) | 3.3 | ❌ 硬编码 defaultRPS,无 Admin API |
| 4 | 负载均衡(同模型多上游加权轮询) | 3.1 | ❌ model_name 唯一,weight 字段未用 |
| 5 | 透传端点精确路由 | 8.5 | ⚠️ default 兜底,无方法校验 |
| 6 | IP 黑白名单 | 5.4 | ❌ IPFilter.Allow 恒 true |
| 7 | TLS 支持 | 5.4 | ❌ TLSConfig 恒 nil |

### 1.3 非目标

- Enterprise 分布式限流(Redis)—— Enterprise 迭代
- 管理后台 8081 端口 TLS —— 内网假设,本轮不做
- 多租户 RBAC —— Enterprise 迭代

---

## 2. 实现顺序(依赖分层)

```
① 限流器重构      ← 独立:token_bucket + sliding_window 双策略,RPS+TPM 双维度
② 限流配置存储    ← 存储接口扩展 + 新表 rate_limit_configs
③ 限流中间件三层匹配 + TPM 回补  ← 依赖 ①②
④ 负载均衡        ← 独立:新表 upstreams + 加权随机选上游
⑤ 透传精确路由 + IP 黑白名单 + TLS  ← 独立:acceptor 层 + proxy 端点表
⑥ 端到端验证
```

---

## 3. 限流增强(模块 ①②③)

### 3.1 限流器策略化

`RateLimitPlugin` 接口签名不变(Allow/Status/Reset)。新增 `RecordTokens` 用于 TPM 回补,`ReloadConfig` 用于配置热加载:

```go
type RateLimitPlugin interface {
	Init(config map[string]interface{}) error
	Allow(tenantID string, model string, tokens int) (allowed bool, remaining int64, err error)
	Status(tenantID string, model string) (current int64, limit int64, resetAt time.Time)
	Reset(tenantID string, model string) error
	RecordTokens(tenantID string, model string, tokens int) error // TPM 回补(请求完成后)
	ReloadConfig() error                                            // 配置变更后重载(管理后台写操作触发)
}
```

> 接口变更影响:当前唯一实现 MemRateLimiter + enterprise 工厂引用。两处同步。

### 3.2 限流器内部结构

工厂创建时注入存储(配置源)与 config.yaml 默认值:

```go
NewRateLimiter(storage plugin.StoragePlugin, defaultRPS int, defaultTPM int64, defaultStrategy string) *RateLimiter
```

内部:
- **配置缓存**:进程内 `map[configKey]*RateLimitConfig`,ReloadConfig 时从 storage.ListRateLimitConfigs 全量刷新
- **桶存储**:`map[bucketKey]*bucket`,bucketKey = tenant|model;每桶含 RPS 与 TPM 两个子限流器
- **策略**:每桶按其配置的 strategy 创建 token_bucket 或 sliding_window 实例

### 3.3 三层配置匹配(优先级:模型级 > 租户级 > 全局)

```
resolveConfig(tenant, model):
  1. 缓存查 (tenant, model)     — 模型级
  2. 缓存查 (tenant, "")        — 租户级
  3. 缓存查 ("", "")            — 全局
  命中第一个 enabled 的返回;都无 → config.yaml 默认 (defaultRPS, defaultTPM, defaultStrategy)
```

### 3.4 双维度双策略

每桶维护 RPS 与 TPM 两个计数器,**任一超限即拒绝**。

**token_bucket**(令牌桶,允许突发):
- RPS 桶:容量=rps,每秒填充 rps 个令牌;每请求取 1
- TPM 桶:容量=tpm,每秒填充 tpm/60 个令牌;RecordTokens 时取 tokens 个

**sliding_window**(滑动窗口计数):
- RPS:1 秒窗口内请求计数,超 rps 拒
- TPM:60 秒窗口内 token 累计,超 tpm 拒

### 3.5 TPM 预检+回补流程

```
① 限流中间件(路由后,拿到 rc.ModelConfig.ModelName):
   Allow(tenant, model, 0)
   → 校验 RPS(取 1 令牌)+ TPM 当前累计是否已 >= tpm(不预扣 token)
   → 通过放行,超限 429 rate_limit(RPS)/ token_limit(TPM)
② 代理内核转发完成拿到 usage.total_tokens:
   RecordTokens(tenant, model, totalTokens) 回补 TPM 计数
   → 流式请求在末尾 usage 分片解析后回补
```

> TPM 为软限:超限影响后续请求,符合 PRD"每分钟 Token 总量限制"。与额度扣减(IncrementAPIKeyUsage)同"预检放行+事后回补"模式。

> **回补路径可行性(自检确认)**:`ProxyCore` 通过 `p.pipeline.rateLimiter` 已可访问限流器(Pipeline 持有 rateLimiter 字段),无需新增依赖注入。非流式在 finalizeAudit 前回补,流式在末尾 usage 分片解析后回补。

> **命名注意**:`config.RateLimitConfig`(pkg/config,含 Strategy/DefaultRPS/DefaultTPM,已存在)与 `plugin.RateLimitConfig`(pkg/plugin,限流规则实体,本次加 ID/时间)同名但不同包,代码中分别限定包名引用,勿混淆。

### 3.6 RateLimitConfig 存储

新表 `rate_limit_configs`(对齐 plugin.RateLimitConfig,新增 id 主键):

```sql
CREATE TABLE rate_limit_configs (
  id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL DEFAULT '',
  model_name TEXT NOT NULL DEFAULT '',
  requests_per_sec INTEGER NOT NULL,
  tokens_per_min INTEGER NOT NULL,
  strategy TEXT NOT NULL DEFAULT 'token_bucket',
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  UNIQUE(tenant_id, model_name)
);
```

`plugin.RateLimitConfig` 结构体增加 ID/CreatedAt/UpdatedAt 字段(当前只有业务字段)。

存储接口新增:
```go
	GetRateLimitConfig(tenantID, modelName string) (*RateLimitConfig, error)
	SaveRateLimitConfig(cfg *RateLimitConfig) error
	ListRateLimitConfigs(page, size int) ([]*RateLimitConfig, int64, error)
	DeleteRateLimitConfig(id string) error
```

三实现(mem/sql/dynamic)+ 两 DDL(sqlite/mysql)同步。

### 3.7 Admin 限流管理 API(pkg/admin/rate_limit.go)

```
POST   /api/rate-limits       创建(tenant_id/model_name/requests_per_sec/tokens_per_min/strategy)
GET    /api/rate-limits       分页列表
PUT    /api/rate-limits/:id   更新
DELETE /api/rate-limits/:id   删除
```

字段校验:requests_per_sec 1-100000、tokens_per_min 1-1000000000、strategy oneof(token_bucket/sliding_window)。写操作成功后调 `rateLimiter.ReloadConfig()` 使配置生效。

> 依赖:AdminServer 需持有 rateLimiter 引用(当前只有 storage)。NewAdminServer 增加 rateLimiter 参数。

---

## 4. 负载均衡(模块 ④)

### 4.1 数据模型 — 独立 upstreams 表

```sql
CREATE TABLE upstreams (
  id TEXT PRIMARY KEY,
  model_config_id TEXT NOT NULL,
  base_url TEXT NOT NULL,
  api_key TEXT NOT NULL,          -- AES-GCM 加密(复用 crypto.go)
  encrypted INTEGER NOT NULL DEFAULT 1,
  weight INTEGER NOT NULL DEFAULT 1,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE INDEX idx_upstreams_model ON upstreams(model_config_id);
```

`plugin.Upstream` 新结构体。存储接口新增:
```go
	ListUpstreams(modelConfigID string) ([]*Upstream, error)
	SaveUpstream(up *Upstream) error
	DeleteUpstream(id string) error
	GetUpstreamByID(id string) (*Upstream, error)
```

### 4.2 兼容策略

`ModelConfig` 保留 base_url/api_key/weight 作为**默认上游**,`GetModelConfig` 返回单条的假设不变。运行时:

```
路由中间件查 ModelConfig 后,加载 storage.ListUpstreams(cfg.ID):
  - 非空(过滤 enabled)→ rc.Upstreams = []Upstream,代理内核按 weight 选一个
  - 为空 → 回退 ModelConfig.base_url/api_key(单上游行为不变)
```

RequestContext 增加 `Upstreams []plugin.Upstream` 字段。

### 4.3 加权随机选上游

```go
// selectUpstream 按 weight 加权随机选一个 enabled 上游
// 空 → 返回 nil(调用方回退 ModelConfig);单元素 → 直接返回;权重和 0 → 等概率
func selectUpstream(ups []plugin.Upstream) *plugin.Upstream
```

代理内核 handleProxy/handlePassThrough 转发前:选中 upstream 覆盖 cfg 的 base_url/api_key(局部变量,不改存储)。随机源 `math/rand`(Go 运行时,非 workflow 脚本环境,可用)。测试用统计断言(N 次分布近似权重比)或注入固定 seed。

### 4.4 Admin 上游管理 API(pkg/admin/upstream.go)

```
POST   /api/models/:id/upstreams   创建(base_url/api_key/weight)
GET    /api/models/:id/upstreams   列表(api_key 脱敏不回显)
PUT    /api/upstreams/:uid         更新
DELETE /api/upstreams/:uid         删除
```

---

## 5. 透传精确路由 + IP + TLS(模块 ⑤)

### 5.1 透传端点精确路由(proxy.go)

按 PRD 8.5 端点表精确匹配 + 方法校验,替换 default 兜底:

```
透传端点(路径 → 允许方法):
  POST /v1/completions, /v1/moderations
  POST /v1/images/generations, /v1/images/edits, /v1/images/variations
  POST /v1/audio/speech, /v1/audio/transcriptions, /v1/audio/translations
  GET|POST /v1/files
  GET|DELETE /v1/files/{id}
  GET /v1/files/{id}/content
```

- 尾斜杠归一化(TrimRight 后匹配)
- 匹配端点但方法不符 → 405 method_not_allowed(OpenAI 格式)
- 完全未知路径 → 404 model_not_found(现有行为)
- 透传端点仍走鉴权+限流+审计(入口从 default 改为精确集合;GET 端点沿用"首个启用模型"作上游)

### 5.2 IP 黑白名单(acceptor.go IPFilter)

配置:
```yaml
ip_filter:
  mode: disabled       # disabled/whitelist/blacklist
  whitelist: []         # CIDR 或 IP
  blacklist: []
```

```go
type IPFilter struct {
	mode      string
	whitelist []*net.IPNet  // 单 IP 转为 /32 或 /128
	blacklist []*net.IPNet
}
// Allow: disabled→true;whitelist→命中才 true;blacklist→命中则 false
func (f *IPFilter) Allow(ip string) bool
```

接入点:代理服务中间件链最外层(鉴权前)检查客户端 IP(复用 clientIP 的 XFF 逻辑),拒绝返回 403 forbidden(OpenAI 格式)。IPFilter 由 config 构造,注入 Acceptor。

### 5.3 TLS(acceptor.go TLSHandler + main.go)

配置:
```yaml
tls:
  enabled: false
  cert_file: ""
  key_file: ""
  min_version: "1.2"
```

```go
// TLSConfig enabled 时 LoadX509KeyPair 返回 *tls.Config(MinVersion 由 min_version 决定,默认 1.2),
// 未 enabled 返回 (nil, nil);证书加载失败返回 error(启动 fatal)
func (h *TLSHandler) TLSConfig() (*tls.Config, error)
```

main.go:代理服务 TLS 非 nil → `ListenAndServeTLS(cert, key)`(证书已在 tls.Config,传空串亦可),否则 `ListenAndServe`。管理后台 8081 不启用 TLS。

---

## 6. 配置文件新增(config.yaml)

> `rate_limit` 段已存在(strategy/default_rps/default_tpm),无需改动;本次仅**新增** `tls` 与 `ip_filter` 两段。

```yaml
tls:
  enabled: false
  cert_file: ""
  key_file: ""
  min_version: "1.2"

ip_filter:
  mode: disabled             # disabled/whitelist/blacklist
  whitelist: []
  blacklist: []
```

config.go 新增 TLSConfig、IPFilterConfig 结构体与默认值 + apply 回填(注意 bool/slice 字段 apply 处理);config.RateLimitConfig 已有 strategy/default_rps/default_tpm,不动。

---

## 7. 测试策略

| 模块 | 测试 |
|------|------|
| token_bucket | 突发放行(容量内)、超 RPS 拒、匀速填充恢复;TPM 桶超限拒 |
| sliding_window | 1s/60s 窗口滚动、边界计数 |
| 三层配置匹配 | 模型级>租户级>全局 + 缺省回退;ReloadConfig 生效 |
| TPM 回补 | Allow(0) 预检放行 → RecordTokens → 下次 Allow 反映累计 |
| RateLimitConfig 存储 | CRUD + UNIQUE(tenant,model) + 三实现一致(SQLite 覆盖) |
| 限流 Admin API | CRUD + 校验 + 写后 ReloadConfig |
| upstreams 存储 | CRUD + 加密往返 + 按 model_config_id 列表 |
| 负载均衡 | 加权分布统计断言、单上游回退、全禁用回退 ModelConfig |
| 上游 Admin API | CRUD + api_key 脱敏 |
| 透传精确路由 | 各端点 200、方法不符 405、未知 404、尾斜杠 |
| IP 过滤 | disabled/whitelist/blacklist + CIDR + XFF |
| TLS | 配置加载(cert 对/缺失 error);httptest TLS 启动验证 |
| 端到端 | 扩展 e2e:多上游转发 + 限流配置生效 + TPM 回补 |

---

## 8. 涉及文件清单

```
新增:
  pkg/plugin/oss/limit_bucket.go       # token_bucket 双桶(RPS+TPM)
  pkg/plugin/oss/limit_sliding.go      # sliding_window 双窗口
  pkg/plugin/oss/ratelimiter.go        # RateLimiter:三层配置解析 + 策略分派 + 缓存
  pkg/admin/rate_limit.go              # 限流配置 CRUD API
  pkg/admin/upstream.go                # 上游管理 API
修改:
  pkg/plugin/interface.go              # RateLimitPlugin 增 RecordTokens/ReloadConfig;
                                       # StoragePlugin 增限流配置/上游 CRUD;
                                       # RateLimitConfig 加 ID/时间;新增 Upstream 结构
  pkg/plugin/oss/limit_mem.go          # 保留或并入 ratelimiter(token_bucket 退化为默认)
  pkg/plugin/oss/storage_sql.go / storage_mem.go / storage_dynamic.go  # 新表 CRUD
  pkg/plugin/oss/storage_sqlite.go / storage_mysql.go  # DDL:rate_limit_configs + upstreams
  pkg/plugin/oss/factory.go            # CreateRateLimiter 注入 storage + 默认值
  pkg/plugin/enterprise/factory.go     # 同步 RateLimitPlugin 接口变更
  pkg/core/middleware_limit.go         # 三层匹配 + 策略 + 429 rate_limit/token_limit
  pkg/core/proxy.go                    # selectUpstream 选上游;透传端点精确路由;TPM RecordTokens 回补
  pkg/core/middleware_route.go         # 加载 upstreams 到 rc
  pkg/core/context.go                  # RequestContext 增 Upstreams 字段
  pkg/core/acceptor.go                 # IPFilter 真实规则;TLSHandler 真实证书;Acceptor 接 IP 检查
  pkg/config/config.go                 # TLSConfig/IPFilterConfig 结构 + 默认
  cmd/gateway/main.go                  # TLS 启动分支;IPFilter/RateLimiter 注入;AdminServer 传 rateLimiter
  pkg/admin/server.go / router.go      # NewAdminServer 加 rateLimiter;注册限流/上游路由
  config.yaml                          # tls/ip_filter 示例 + rate_limit 默认
```

---

## 9. 决策记录

| # | 决策 | 理由 |
|---|------|------|
| 1 | 负载均衡用独立 upstreams 表 | GetModelConfig 返回单条假设不变,鉴权/路由/Admin 零影响 |
| 2 | ModelConfig 保留默认上游回退 | 向后兼容,单上游行为不变 |
| 3 | 加权随机而非严格轮询 | 无状态、并发安全、分布等价 |
| 4 | TPM 预检+事后回补(软限) | 与额度扣减同模式;转发前拿不到 usage |
| 5 | 配置缓存 + 写后 ReloadConfig | 避免每请求查库;OSS 单进程无需分布式失效 |
| 6 | 两种限流策略都实现 | PRD 3.3 明确列出 |
| 7 | 三层配置维度(模型>租户>全局) | PRD 3.3 字段约束 |
| 8 | 8081 管理后台不启用 TLS | 内网部署假设,YAGNI |
| 9 | RateLimitPlugin 增 RecordTokens/ReloadConfig | 现有 Allow/Status/Reset 不足以支撑 TPM 回补与热加载 |
