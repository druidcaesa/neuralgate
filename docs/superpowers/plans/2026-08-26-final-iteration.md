# 收尾迭代 实施计划（E8 信创包 + PRD 缺口补全 + 工程债清偿）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 三批收尾——E8 信创包（达梦/金仓/SM3）、PRD 缺口（分布式限流/API Key 批量/输出风控）、工程债清偿（生产化第二批+P2+E7 backlog），完成后 PRD 功能矩阵无空格。

**Architecture:** 存储适配走 OSS 方言层（不复制 CRUD）；分布式限流实现既有 RateLimitPlugin 接口；输出风控复用 PrivacyRule 体系；工程债逐项小修。

**Tech Stack:** 新增 enterprise-only 依赖：gitee.com/chunanyong/dm、github.com/lib/pq、github.com/emmansun/gmsm、github.com/redis/go-redis/v9；基础设施依赖：miniredis(测试)、lumberjack(日志轮转)。

## Global Constraints

- Apache-2.0 头 `Copyright 2026 FanYaNan`；注释方案风；enterprise 新文件带构建标签
- 双矩阵贯穿全绿：`go test -tags oss ./...` 与 `go test -race -tags enterprise ./...`；每任务收尾跑一次
- 新外部驱动/SDK 仅限 enterprise tag 引用；OSS 配置 dm/kingbase 启动报 unknown driver
- bool 配置不参与 applyDefaults；未启用零行为变化
- 改 webui 同轮 make build-webui 提交 dist
- 执行方式：内联串行（控制者直接实现，沿用 E7 模式）

### 关键接线事实

- `storage_sql.go` 共 12 处 `isMySQL()` 分支；UPSERT 后缀内联在各 Save 处
- tamper.go:33 `fingerprintAlgos` 注册表已留 sm3 接缝（未知值回退 sha256）
- main.go:87 `rateLimiter := factory.CreateRateLimiter()` 在门控解析(~line 150)之前——B1 需移位至 Build 快照(line ~208 acceptor)前
- doWithRetry(proxy.go:431) 每次新建 http.Client；两处调用(:214/:593)
- clientIP(middleware_auth.go:101) 无条件信任 XFF
- SimpleAuditor.MarkDisconnect 幂等（audit_simple.go:95/:124 已有防重）

---

## 阶段 A：E8 信创包

### Task A1: sqlDialect 方言层抽取

**Files:**
- Create: `pkg/plugin/oss/sql_dialect.go`
- Modify: `pkg/plugin/oss/storage_sql.go`（12 处分支改走 dialect）
- Test: `pkg/plugin/oss/sql_dialect_test.go`

**Interfaces (Produces):**
```go
type sqlDialect struct {
    Name string // mysql/sqlite（本任务仅此两种；dm/kingbase 由 A2 增设）
}
func dialectFor(name string) sqlDialect
func (d sqlDialect) UpsertSuffix(table string, setPairs []string) string
// mysql:     " ON DUPLICATE KEY UPDATE a=VALUES(a), b=VALUES(b)"
// sqlite:    " ON CONFLICT(<pk>) DO UPDATE SET a=excluded.a, ..."
// 约定：调用方按表传 setPairs(["key_hash=VALUES(key_hash)", ...])，PK 恒为 id
func (s *SQLStorage) ph(query string) string // 本任务恒原样返回；A2 为 kingbase 实现 ?→$n 重写
```

- [ ] Step 1.1 dialectFor/UpsertSuffix 单测先行（两种方言的语句快照断言）
- [ ] Step 1.2 机械改造 12 处分支为 dialect 调用；`isMySQL()` 保留供 A2 过渡或删除
- [ ] Step 1.3 双矩阵全量回归绿（MySQL/SQLite 行为零变化）；commit `refactor(plugin): E8 存储方言层抽取(mysql/sqlite 行为零变化)`

### Task A2: 达梦/金仓驱动接线

**Files:**
- Create: `pkg/plugin/oss/storage_ddl_dm.go`、`pkg/plugin/oss/storage_ddl_kingbase.go`(DDL 变体,带 build tag? 否——纯字符串构造可 OSS 化避免标签纠缠；驱动引用才需 enterprise)、`cmd/gateway/drivers_enterprise.go`(blank import dm/pq/gmsm/go-redis)
- Modify: `pkg/plugin/oss/sql_dialect.go`(dm/kingbase 两方言: ph 重写+UpsertSuffix[MERGE/ON CONFLICT])、`pkg/plugin/oss/storage_sqlite.go`同构建表入口抽象(Init 按 driver 分派 DDL)、`pkg/plugin/oss/factory.go`/`factory_enterprise.go`(CreateStorage 分支)、`config.yaml`(dm/kingbase 示例注释已有,核对)
- Test: `pkg/plugin/oss/sql_dialect_test.go`扩充(ph 重写快照/MERGE 语句快照)、`pkg/plugin/enterprise/drivers_live_test.go`(NG_DM_DSN/NG_KINGBASE_DSN 门控 Skip)

**Interfaces (Produces):**
```go
// sql_dialect.go 追加
Name == "kingbase": ph 把第 n 个 "?" 替换为 "$n"(字符串字面量内无 ? 的前提成立,全库 SQL 已审计)
Name == "dm":       UpsertSuffix 特殊——返回空串并由 MergeInto(table, onKeys, setPairs) 生成完整 MERGE 语句
func MergeInto(table string, joinCols, setPairs []string) string
// Init 分派: driver=="dm"/"kingbase" → NewSQLStorage(driver) + 各自 DDL 函数(dm 用 user_tables 存在性检查实现幂等;
// kingbase 走 CREATE TABLE IF NOT EXISTS + CREATE INDEX IF NOT EXISTS, MEDIUMTEXT→TEXT, TINYINT→SMALLINT)
// factory_enterprise.CreateStorage: cfg.Storage.Driver=="dm"/"kingbase" → oss.NewSQLStorageForDriver(driver)(blank import 在 drivers_enterprise.go)
// OSS 侧 factory 不认识这两个 driver → 保持 unknown driver 报错
```

- [ ] Step 2.1 ph/MergeInto 快照测试先行；kingbase 占位符重写用例含多占位符与无占位符
- [ ] Step 2.2 实现 dialect 扩展+DDL 构造+工厂分支+blank import；`go build -tags oss ./...`(确认 OSS 不引驱动)与 `go build -tags enterprise ./...` 双绿
- [ ] Step 2.3 env-gated live 测试骨架(Ping+建表幂等+SaveMCPServer 往返)；本机 Skip
- [ ] Step 2.4 `GOOS=linux GOARCH=arm64 go build` 双版本交叉编译通过；commit `feat(plugin): E8 达梦/人大金仓存储适配(方言层+企业工厂接线)`

### Task A3: SM3 指纹落地

**Files:**
- Modify: `pkg/plugin/enterprise/tamper.go`(注册表加 sm3)、`config.yaml`(fingerprint_algo 注释更新)
- Test: `pkg/plugin/enterprise/tamper_sm3_test.go`

- [ ] Step 3.1 测试先行：Fingerprint("sm3") 返回 64hex 且≠sha256 值；未知算法仍回退 sha256；换算法后历史记录按当前算法重算比对(校验器语义测试)
- [ ] Step 3.2 gmsm/sm3 接入注册表；commit `feat(enterprise): E8 SM3 国密指纹算法落地`

## 阶段 B：PRD 缺口补全

### Task B1: Redis 分布式限流

**Files:**
- Create: `pkg/plugin/enterprise/ratelimit_redis.go`、`ratelimit_redis_test.go`
- Modify: `pkg/license/license.go`(FeatureDistributedRateLimit="distributed_ratelimit")、`pkg/config/config.go`(RateLimitConfig 加 Distributed 子结构)、`config.yaml`、`cmd/gateway/main.go`(装配移位: rateLimiter 创建移到门控解析后, distributed 启用且过门控→NewDistributedRateLimiter 包装本地实现)、`pkg/admin/system.go`(rate_limiter_mode)
- Test: miniredis 单测(Allow 原子扣减并发/Status/Reset/RecordTokens/ReloadConfig/**redis 宕机回退本地判定**)

**Interfaces (Produces):**
```go
type DistributedRateLimiter struct {
    local plugin.RateLimitPlugin // 降级路径与阈值缓存载体
    rdb   redis.Cmdable
    thresholds func(tenant, model string) (rps int, tpm int, strategy string) // ReloadConfig 刷新
}
func NewDistributedRateLimiter(local plugin.RateLimitPlugin, addr, pass string, db int) *DistributedRateLimiter
// Allow: Lua 原子读桶→判定→扣减(token_bucket/sliding_window 两脚本); TPM INCR+EXPIRE 固定窗
// redis 错误 → local.Allow 兜底 + Warn(每键限频一次防刷屏)
// Status/Reset/RecordTokens/ReloadConfig 对应映射; ReloadConfig 同时刷新 local
```
- [ ] Step 4.1 miniredis 测试先行(含宕机回退用例: miniredis.Close 后 Allow 仍出结果且来自 local)
- [ ] Step 4.2 实现至绿；四分支门控测试(shouldStartDistributedRateLimit 照 compliance)；main.go 装配移位回归(e2e 冒烟脚本级手测一次)；commit `feat(enterprise): E-final Redis 分布式限流(原子扣减+故障回退本地)`

### Task B2: API Key 批量操作

**Files:**
- Modify: `pkg/admin/api_key.go`(batch-create/batch-delete handler)、`pkg/admin/router.go`、`webui/src/api/apiKey.ts`、`webui/src/views/ApiKeyList.vue`
- Test: `pkg/admin/api_key_batch_test.go`(照 rbacFixture)

**Interfaces:** batch-create `{name_prefix,count 1-100,quota,expires_at?,allowed_models?}`→`{items:[{id,name,key}]}` 明文仅此一次；batch-delete `{ids[]}`→`{deleted:int,missing:[]}`；租户 scope 照现有 createAPIKey/deleteAPIKey 语义
- [ ] Step 6.1 测试先行(创建 N 个明文唯一/超 count 400/scoped 用户只能删本租户/missing 列表正确)
- [ ] Step 6.2 实现至 admin 绿；webui 五处改动+vue-tsc+build-webui；commit `feat(admin): E-final API Key 批量创建与批量删除`

### Task B3: 输出内容风控

**Files:**
- Modify: `pkg/plugin/interface.go`(PrivacyRule.RuleType 增 `output`;Action 字段 redact|block)、三存储加列迁移(E5 先例)、`pkg/plugin/privacy_seed.go`?(不动种子)、`pkg/plugin/enterprise/privacy_engine.go`(DetectOutput+编译分组)、`pkg/core/middleware_privacy.go`(响应侧 block 拦截:非流式整体替换 content_filter 错误;流式停帧+[DONE];记 security_events output_blocked)、admin/webui 规则页扩展
- Test: engine DetectOutput 单测+中间件 block 行为测试(非流式/流式各一)

- [ ] Step 7.1 引擎与拦截测试先行
- [ ] Step 7.2 实现至双矩阵绿；存量规则 Action 空=redact 零变化断言；webui 扩展；commit `feat(enterprise): E-final 输出内容风控(output 规则类+block 拦截)`

## 阶段 C：工程债清偿

### Task C1: metrics 与访问日志
- Create: `pkg/core/metrics.go`(自拼 Prometheus 文本: ng_requests_total{status}/ng_tokens_total/ng_audit_queue_usage 等, atomic 计数器)、挂代理端口 /metrics 免鉴权(AuthMiddleware healthz 同款白名单)；访问日志 zap Info 挂数据面 finalize 出口
- Test: metrics 渲染快照+计数断言；commit `feat(core): E-final /metrics 暴露与访问日志`

### Task C2: 断连取消与重试收敛
- Modify: proxy.go doWithRetry——共享包级 Transport(单例)；req.WithContext 已有链路核实；重试收紧: 连接类错误(err 无 Response)任何方法可重试；收到响应后仅 GET/HEAD 对 5xx 重试(POST 5xx 重试移除,**行为变化**); 更新相关既有测试预期
- commit `fix(core): E-final 上游转发断连取消+共享连接池+重试幂等收敛`

### Task C3: 配置强化
- encrypt_key 默认值删除→未配置 Fatal 提示生成命令；环境变量覆盖 NEURALGATE_(PROXY_ADDR/ADMIN_ADDR/STORAGE_DSN/LOG_LEVEL/ADMIN_BOOTSTRAP_PASSWORD) 白名单在 config.Load 后应用；日志 file 输出接 lumberjack(log.output 为文件路径时启用 size 200MB×7)
- Test: 覆盖优先级/缺 encrypt_key Fatal 分支(main 层测试或抽出校验函数)；commit `feat(config): E-final 配置强化(密钥强制/环境变量覆盖/日志轮转)`

### Task C4: 安全修正
- XFF: config 增加 security.trusted_proxies([]CIDR)；clientIP 仅当 RemoteAddr 命中可信代理才解析 XFF 左起第一个非可信 IP；空列表=只用 RemoteAddr(**安全默认收紧**,e2e 若依赖 XFF 需配白名单——检查 e2e_test 并同步)
- createAPIKey 响应删 key_hash 字段(同步 webui types 若引用)
- commit `fix(security): E-final XFF 可信代理白名单+API Key 不回显哈希`

### Task C5: 资源治理
- 限流桶惰性清理(RateLimiter 内桶 map 访问计数触发 TTL 清扫,sessionStore 模式)+Reload 只换阈值保留计数(改 ReloadConfig 实现为阈值覆盖而非重建桶)
- 留存清理分批 DELETE…LIMIT(循环至 RowsAffected<batch)
- commit `fix(core): E-final 限流桶淘汰与保留式重载+留存清理分批删除`

### Task C6: E7 backlog 八条
1. mergeUpstreamHeaders 保留键黑名单(Mcp-Session-Id/Accept/Content-Type/Host)
2. parseMCPServerID 要求 /mcp 后缀否则 404；body 读错误仅 errors.Is(*http.MaxBytesError)→413 其余 400
3. mcpBranch 429 改 mcp.WriteJSONRPCError(JSON-RPC 形状)
4. admin mcp-servers 重名: 存储层加 GetMCPServerByName(name) 精确查询(mem/sql/dynamic)；SQL UNIQUE 冲突错误分类映射 409(strings.Contains "UNIQUE"/"duplicate")
5. JSON 路径 io.Copy 错误感知: copy err!=nil→审计 failed+"client disconnected"
6. truncateForAudit rune 边界安全化并从 relay 移除(recorder 层唯一截断点,cappedBuffer 上限保留)
7. webui MCPAuditLogs 时间范围 el-date-picker(type=daterange)
8. 补测试: notification 透传/srv.Headers 到达上游且协议头不被覆盖/413/限流 429 体形状
- Test: 各项对应断言；commit `fix(e7): 终审 backlog 清偿(协议头黑名单/路由严格化/TOCTOU/UTF-8 截断等八项)`

### FINAL Task: 收尾验收
- [ ] 双 vet + 双矩阵全量 + gofmt + 双构建 + arm64 交叉编译
- [ ] 端到端冒烟: mcp_audit 正例回归(确认 backlog 清偿未破坏)+Redis 分布式限流 miniredis 场景+输出风控 block 非流式实测+批量 Key 实测
- [ ] 台账/memory 总账收官(PRD 功能矩阵全 ✅)

## 决策记录（实施期新增）

| # | 决策 | 理由 |
|---|------|------|
| A | 方言层以 ph() 运行时重写 ?→$n 而非改写全部语句 | 44 条语句机械改写风险高；运行时重写单点可控(全库 SQL 无字面量 ?,已审计) |
| B | DM DDL 幂等用 user_tables 查询而非 IF NOT EXISTS | DM8 不支持该语法;无真库环境下语句生成单测+env-gated live 兜底 |
| C | 分布式限流内嵌本地实现作降级路径 | 比 fail-open 更稳:redis 故障时单实例保护仍在 |
| D | 输出风控 block 流式为尽力而为 | 已透传帧不可撤回,协议限制 |
