# 收尾迭代设计：E8 信创包 + PRD 缺口补全 + 工程债清偿

日期：2026-08-26
状态：已经用户分节评审通过
范围裁决：三批全做（A→B→C 顺序），一次收尾 PRD 全部剩余需求与已承诺工程债
前置：E1-E7 已完成；本迭代完成后 PRD 功能矩阵无空格

## 0. 范围总览与用户裁决记录

| # | 裁决点 | 结论 |
|---|--------|------|
| R1 | 本轮范围 | 三批全做，A→B→C |
| R2 | 达梦/金仓真库验证 | 环境变量门控 + 单测为准（照 NG_MYSQL_DSN 先例），待信创环境再做真库 smoke |
| R3 | 分布式限流方案 | Redis 集中计数（go-redis 仅 enterprise 编译引用），未配置回退本地实现 |

明确排除：SM4 国密加密（PRD 无要求）；聚合式 MCP 虚拟上游等既往 YAGNI 决议维持不变。

---

## 1. 批次 A：E8 信创包

### 1.1 存储方言层（不复制 CRUD）

现状 `oss/storage_sql.go` 以 `isMySQL()` 分支承载 MySQL/SQLite 共享 CRUD。新增两库若各抄一份将翻倍维护成本。抽取方言层：

```go
// pkg/plugin/oss/sql_dialect.go（OSS，新增）
type sqlDialect struct {
    Name        string                       // mysql / sqlite / dm / kingbase
    Placeholder func(n int) string           // "?", "$1..$n"
    Upsert      func(table, keyCols, setClause string) string
    // Upsert 返回追加在 INSERT 之后的子句：
    //   mysql:     ON DUPLICATE KEY UPDATE <set>
    //   sqlite:    ON CONFLICT(<key>) DO UPDATE SET <set>
    //   kingbase:  同 sqlite（PG 兼容）
    //   dm:        MERGE INTO 完整语句（占位符风格同 mysql）
    MigrateIndex func(...)                   // information_schema 判列/索引的方言差异收拢于此
}
func dialectFor(name string) sqlDialect
```

- `storage_sql.go` 全部 SQL 构造点改为经 dialect 生成——一次性机械改造，MySQL/SQLite 行为零变化（双矩阵回归保护）
- **人大金仓**：KingbaseES V8R6 PostgreSQL 兼容模式 → 驱动用 `github.com/lib/pq`，包装注册名 `kingbase`；DSN 形如 `kingbase://user:pass@host:54321/db`，打开前转 PG URL
- **达梦**：驱动 `gitee.com/chunanyong/dm`（纯 Go database/sql，注册名 `dm`），DSN `dm://user:pass@host:5236`；UPSERT 用 `MERGE INTO ... USING ... WHEN MATCHED THEN UPDATE`
- 标识符一律不加引号的小写（DM 自动大写规范化，查询不受影响）
- **驱动引用仅 enterprise 编译可见**：`factory_enterprise.go` 增加 `driver=="dm"/"kingbase"` 分支并 blank import 驱动包（照 mysql blank import 教训）；OSS 配置这两个 driver → 启动 unknown driver 报错
- 连接池参数（max_open_conns/max_idle_conns）对全部 SQL 驱动生效（顺带清偿 C1 的死字段项）

### 1.2 SM3 指纹落地

- 引入 `github.com/emmansun/gmsm/sm3`（纯 Go 国密库，enterprise-only 引用）
- `tamper.go` 算法注册表注册 `sm3`；`fingerprint_algo: sm3` 放行；未知值回退 sha256 兜底保留
- 换算法语义钉死（与既有"选定后不可更换"承诺一致）：校验始终按**当前配置算法**对存储指纹重算比对——切换算法会使历史指纹全部误报篡改，属运维侧承诺约束，非代码兼容义务；该行为以测试钉死

### 1.3 验证策略

- 方言 SQL 形状单测锁死（Placeholder/Upsert/MERGE 语句快照断言）
- 真库门控测试：`NG_DM_DSN` / `NG_KINGBASE_DSN` 缺省 Skip
- `GOOS=linux GOARCH=arm64` 双版本交叉编译通过（PRD 场景 2 飞腾/鲲鹏）

---

## 2. 批次 B：PRD 缺口补全

### 2.1 Redis 分布式限流

- 新增 `plugin/enterprise/ratelimit_redis.go`：`DistributedRateLimiter` 实现既有 `plugin.RateLimitPlugin` 接口（Pipeline 面向接口，零侵入）
- Lua 脚本原子"读桶→判定→扣减"（token_bucket 与 sliding_window 两套）；TPM 固定窗口 INCR+EXPIRE；键空间 `rl:{tenant}:{model}:{...}`
- 阈值热加载：ReloadConfig 从存储刷新本地阈值缓存（脚本按次携带阈值参数）
- **可用性**：Redis 故障回退内嵌本地限流器继续判定 + Warn 留痕（比裸 fail-open 更稳，符合全仓降级口径）
- 配置（bool 不参与 applyDefaults；未启用回退本地实现，现状零变化）：

```yaml
rate_limit:
  ...
  distributed:               # Enterprise only：分布式限流(distributed_ratelimit 门控)
    enabled: false
    redis_addr: "127.0.0.1:6379"
    redis_password: ""
    redis_db: 0
```

- 新增 `license.FeatureDistributedRateLimit = "distributed_ratelimit"`；装配点移至门控解析后、pipeline.Build 快照前
- 依赖：go-redis v9 仅 enterprise tag；测试用 miniredis
- systemInfo 增加 `rate_limiter_mode: local|redis`

### 2.2 API Key 批量操作

- `POST /api/api-keys/batch-create`：`{name_prefix, count(1-100), quota, expires_at?, allowed_models?}` → 生成 N 个；明文列表仅此一次返回；租户归属按调用者 scope 注入
- `POST /api/api-keys/batch-delete`：`{ids[]}` → 返回 `{deleted, missing[]}`；租户隔离照 scopeTenant（只能删本租户的）
- 权限 `api_key:write`；webui ApiKeyList 批量创建弹窗 + 多选删除

### 2.3 输出内容风控（复用 E4 规则体系）

- `PrivacyRule.RuleType` 扩展第三类 `output`（仅作用响应侧）；新增 `Action` 字段 `redact`（默认，存量行为零变化）| `block`
- 引擎新增 `DetectOutput(body)`：编译缓存中 output 类规则匹配响应体
- block 命中：非流式整体替换为 OpenAI 形状 `{"error":{"type":"content_filter","code":"output_blocked"}}`（HTTP 200）；流式停止透传后续帧并补 `[DONE]`；均记 `security_events`（RuleName=`output_blocked`）
- 白名单复用 privacy_whitelist；PrivacyRule 加列增量迁移（照 E5 先例）；webui PrivacyRules 类型/action 扩展

---

## 3. 批次 C：工程债清偿

### C1 生产化第二批

| 项 | 方案 |
|---|---|
| metrics | `/metrics` Prometheus 文本格式自拼零依赖（请求计数/状态码分布/token 用量；队列与限流 gauge）；代理端口免鉴权（照 /healthz 先例） |
| core 访问日志 | 每请求一条 zap Info（method/path/status/duration_ms/request_id）挂数据面 finalize 出口 |
| 断连取消 | 上游转发统一 `http.NewRequestWithContext(req.Context())`，客户端断连即取消上游请求 |
| encrypt_key 强制显式 | 删除默认密钥，未配置启动 Fatal 并提示生成方式（破坏性变更，config.yaml 注释同步） |
| 连接池死字段 | storage Init 接收 cfg max/max_idle 注入 sql.DB（并入 A1 方言改造） |

### C2 P2 遗留

| 项 | 方案 |
|---|---|
| XFF 伪造审计 IP | `security.trusted_proxies` CIDR 白名单（空=只用 RemoteAddr，安全默认收紧） |
| createAPIKey 回显 key_hash | 响应删除该字段 |
| POST 重试非幂等 / 每请求新建 Client | 共享单例 Transport；重试范围收紧为**连接类失败与幂等方法**——POST 收到上游响应后的 5xx 自动重试移除（行为变化，验收注明；未发出请求的连接失败仍可安全重试） |
| 限流桶无淘汰 / Reload 清计数 | 空闲桶惰性清理（sessionStore 模式）；Reload 只换阈值保留计数 |
| 留存清理无界 DELETE | 分批 DELETE…LIMIT 循环删净 |
| 环境变量覆盖 | `NEURALGATE_` 前缀白名单覆盖 yaml（proxy/admin 地址、storage.dsn、log.level、admin.bootstrap_password） |
| 日志轮转 | file 输出接 lumberjack（size+份数） |

### C3 E7 minor backlog

1. mergeUpstreamHeaders 保留键黑名单：Mcp-Session-Id / Accept / Content-Type / Host 不被 mcp_servers.Headers 覆盖
2. `/v1/mcp/servers/{id}` 缺 `/mcp` 后缀拒绝（404）；body 读错误仅 MaxBytesError 映射 413，其余归 400
3. MCP 分支限流 429 改 JSON-RPC 形状（mcp.WriteJSONRPCError）
4. admin mcp-servers 重名校验改精确查询消除 TOCTOU；SQL UNIQUE 冲突映射 409
5. JSON 路径 io.Copy 错误感知：写客户端失败不再记 success（改 failed + client disconnected）
6. 截断 UTF-8 rune 边界安全化；双层截断去冗余（保留 enterprise recorder 层，relay 只做 cappedBuffer 上限）
7. webui MCPAuditLogs 补时间范围筛选（后端 start/end 已支持）
8. 测试补充：notification 透传、srv.Headers 到达上游且不可覆盖协议头、413 上限、限流 429 头

---

## 4. 全局约束与验证

- 所有 Go 文件 Apache-2.0 头；注释方案风；新 enterprise 文件带构建标签
- 双矩阵全绿贯穿：`go test -tags oss ./...` 与 `go test -race -tags enterprise ./...`
- 新外部依赖全部限定 enterprise tag 引用（dm/pq/gmsm/go-redis）；miniredis/lumberjack 为测试或基础设施依赖可进 OSS 侧（lumberjack 若 OSS 日志使用）
- 三批各自独立成提交序列，批间双矩阵回归
- 总收尾：双 vet + gofmt + 双构建 + arm64 交叉编译 + 端到端冒烟（分布式限流双实例场景 miniredis 单测为准）+ memory 总账收官

## 5. 决策记录

| # | 决策 | 理由 |
|---|------|------|
| A | 存储适配走方言层而非复制 storage_dm/storage_kingbase | 千行 CRUD 复制不可维护；架构文档的文件建议让位于实现质量 |
| B | 分布式限流实现 RateLimitPlugin 接口而非改管道 | Pipeline 已面向接口，零侵入；本地实现内嵌作降级路径 |
| C | 输出风控复用 PrivacyRule 体系加 output 类 | 规则 CRUD/白名单/webui/事件链路全部现成，增量最小 |
| D | SM4 不做 | PRD 无要求，YAGNI |
| E | metrics 自拼文本格式 | 避免 client_golang 重依赖；暴露面小够用 |
