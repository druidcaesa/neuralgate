# Enterprise E4 数据隐私合规 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现 E4 四项——PII 双向动态脱敏、Prompt 注入拦截(403+安全事件留痕)、规则库 CRUD、风控白名单；`license.FeaturePrivacy` 门控，默认关闭。

**Architecture:** 规则/白名单/安全事件入库（privacy_rules、privacy_whitelist、security_events 三表），PrivacyEngine 30s TTL 缓存重载；core.Middleware 形态挂固定链之后、代理内核之前；响应侧包装 ResponseWriter 做替换。BuildTag 两版 setupPrivacy 接线。

**Tech Stack:** Go 1.x + gin + zap + modernc.org/sqlite / mysql；webui Vue3 + element-plus + axios。

## Global Constraints

- 所有 Go 文件带 Apache-2.0 头 `Copyright 2026 FanYaNan`；注释只写方案不带 Phase/文档引用
- enterprise 包文件一律 `//go:build enterprise`（含 _test.go）；cmd/gateway 两版接线文件同款
- 测试命令 `go test -tags oss ./...` 与 `go test -tags enterprise ./...` 双矩阵全绿；`-race`
- privacy.enabled 默认 false；bool 字段不参与 applyDefaults
- 注入命中响应体：`{"error":{"message":"请求被安全策略拦截","type":"prompt_injection_blocked","param":null,"code":"prompt_injection_blocked"}}` status=403
- 种子写入仅在空表时执行（重启不重复插入）
- README 不改动（对外门面口径）

### 对设计文档的两处偏差（已定案）

1. **种子常量位置**：设计文档 §4.1 写 `enterprise/privacy_seed.go`，但种子由 OSS 存储层建表后写入（两版本共表），oss 包不能反向 import enterprise。改为 **`pkg/plugin/privacy_seed.go`**（package plugin），oss/enterprise 共用。
2. **中间件检查范围**：仅 `Content-Type: application/json` 的 POST 体做检查。audio/images/files 为二进制/multipart，整读+1MB 截断会损坏转发；超 1MB 的 JSON 体经 MultiReader 还原原流放行不检查。

### 关键接线事实（探索已确认）

- `pipeline.Build()` 在 `proxyCore.Handler()` 时快照中间件链 → **setupPrivacy 必须在 `acceptor := core.NewAcceptor(proxyCore.Handler(), ipf)` 之前调用**（acceptor 创建下移）。
- chat/completions 路径 body 已被路由中间件缓存在 `rc.RequestBody` 并恢复 `r.Body`；透传端点读 `r.Body` → 脱敏后必须**同时更新 r.Body 与 rc.RequestBody**。
- 上游响应头复制跳过 Content-Length（copyResponseHeaders），替换长度变化无需修正响应长度头。
- SQL UPSERT 方言分支模式照抄 SaveAdminUser（MySQL `VALUES(col)` / SQLite `excluded.col`）。

---

### Task 1: 隐私存储层（结构体 + 接口 + 种子 + 三实现 + DDL）

**Files:**
- Modify: `pkg/plugin/interface.go`（结构体 + StoragePlugin 8 方法）
- Create: `pkg/plugin/privacy_seed.go`
- Modify: `pkg/plugin/oss/storage_mem.go`（字段 + 方法）
- Modify: `pkg/plugin/oss/storage_sql.go`(方法 + 种子写入)
- Modify: `pkg/plugin/oss/storage_sqlite.go`、`storage_mysql.go`（DDL）
- Modify: `pkg/plugin/oss/storage_dynamic.go`（委托）
- Test: `pkg/plugin/privacy_seed_test.go`、`pkg/plugin/oss/storage_privacy_test.go`

**Interfaces (Produces):**
```go
plugin.PrivacyRule{ID, RuleType, Name, Pattern, Replacement, Scope string; Enabled bool; CreatedAt, UpdatedAt time.Time}
plugin.PrivacyWhitelistEntry{ID, Pattern, Note string; Enabled bool; CreatedAt time.Time}
plugin.SecurityEvent{ID, RequestID, RuleName, Snippet, ClientIP, ModelName string; CreatedAt time.Time}
const plugin.PrivacyRuleTypePII = "pii"; plugin.PrivacyRuleTypeInjection = "injection"
const plugin.PrivacyScopeRequest/Response/Both = "request"/"response"/"both"
StoragePlugin += SavePrivacyRule/DeletePrivacyRule/ListPrivacyRules(ruleType *string) ([]*PrivacyRule, error)
            += SavePrivacyWhitelistEntry/DeletePrivacyWhitelistEntry/ListPrivacyWhitelistEntries() ([]*PrivacyWhitelistEntry, error)
            += SaveSecurityEvent/ListSecurityEvents(page, size int) ([]*SecurityEvent, int64, error)
plugin.DefaultPrivacyRules() []*PrivacyRule   // Task1 SQL 种子与 Task2 测试共用
```

- [x] Step 1.1 interface.go 结构体与常量（追加在 RateLimitConfig 之后）：三结构体带 json tag、类型/作用域常量、接口 8 方法段注释「隐私合规(Enterprise)：规则库 CRUD + 白名单 + 安全事件」
- [x] Step 1.2 pkg/plugin/privacy_seed.go：DefaultPrivacyRules() 返回 §5 十条种子（4 PII scope=both + 6 injection scope=request replacement 空）
- [x] Step 1.3 storage_mem.go：三 map/slice 字段 + NewMemStorage 初始化 + 8 方法（规则按 CreatedAt,ID 升序；事件倒序分页 normalizePage；副本返回）
- [x] Step 1.4 sqlite/mysql DDL 各加三表（privacy_rules 带 rule_type 索引、security_events 带 created_at 索引）
- [x] Step 1.5 storage_sql.go：8 方法 + scanPrivacyRule + SavePrivacyRule 方言 upsert + seedPrivacyRules(db) 空表插种子（Init 建表成功后调用）
- [x] Step 1.6 storage_dynamic.go 尾部委托 8 方法
- [x] Step 1.7 测试：种子全部可编译+样本命中；mem 三组 CRUD 往返/删除 ErrNotFound/事件分页；sqlite Init 两次同库种子只写一次且表齐全
- [x] Step 1.8 `go test -tags oss ./pkg/plugin/...` 全绿 → commit `feat(plugin): E4 隐私合规存储层(规则库/白名单/安全事件)`

### Task 2: PrivacyEngine

**Files:**
- Create: `pkg/plugin/enterprise/privacy_engine.go`（//go:build enterprise）
- Test: `pkg/plugin/enterprise/privacy_engine_test.go`

**Interfaces (Produces):**
```go
NewPrivacyEngine(storage plugin.StoragePlugin, ttl time.Duration, logger *zap.Logger) *PrivacyEngine
(*PrivacyEngine) Whitelisted(body []byte) bool
(*PrivacyEngine) Sanitize(body []byte, scope string) ([]byte, bool)
(*PrivacyEngine) DetectInjection(body []byte) *plugin.PrivacyRule  // nil=未命中
```
语义：首次访问/TTL 过期触发 reload；禁用条目与非法正则跳过+Warn；加载失败沿用旧缓存仅刷新时间戳；正则执行 panic→降级放行；replacement 用 ReplaceAllLiteral（字面量，不解释 $1）。并发安全（RWMutex 快照换入）。

- [x] Step 2.1 失败测试：四类 PII 命中与 changed 标记；13 位数字不误判银行卡；scope=request 规则在 response 作用域不生效；中英注入样本命中、普通提问放行；白名单命中豁免、disabled 条目不豁免；短 TTL(20ms) 下改库 ≤TTL 生效；并发读写 -race
- [x] Step 2.2 实现 → `go test -race -tags enterprise ./pkg/plugin/enterprise/` 全绿 → commit `feat(enterprise): E4 隐私防护引擎(TTL 重载/脱敏/注入检测/白名单)`

### Task 3: 隐私中间件

**Files:**
- Create: `pkg/plugin/enterprise/privacy_middleware.go`
- Test: `pkg/plugin/enterprise/privacy_middleware_test.go`

**Interfaces (Produces):**
```go
NewPrivacyMiddleware(engine *PrivacyEngine, auditor plugin.AuditPipeline,
    storage plugin.StoragePlugin, logger *zap.Logger) core.Middleware
```
流程：RequestContext 缺失或非 JSON POST → 直通；body >1MB → MultiReader 还原直通；白名单 → 直通；注入命中 → SaveSecurityEvent(snippet 截 256 字符) + auditor.Submit+Finalize(status=403 自行补审计) + 403 JSON；request 脱敏命中 → 替换 r.Body/rc.RequestBody/Content-Length；next 包 sanitizeResponseWriter（Write 前 response 脱敏，Flusher+Unwrap 透传）。

- [x] Step 3.1 失败测试：403 分支（事件入库+审计 QueryAuditLogs Status=403 恰一条+响应 JSON type）；PII 请求转发 handler 读到脱敏 body 且 rc.RequestBody 一致；响应侧替换；白名单整体豁免；GET 直通；超限 JSON 放行原文
- [x] Step 3.2 实现 → `go test -race -tags enterprise ./...` 全绿 → commit `feat(enterprise): E4 隐私防护中间件(注入拦截403/双向脱敏/审计短路留痕)`

### Task 4: 配置与 BuildTag 接线

**Files:**
- Modify: `pkg/config/config.go`（PrivacyConfig + Config.Privacy，bool 不进 applyDefaults）、`config.yaml`（export 后加 privacy 段）
- Create: `cmd/gateway/privacy_enterprise.go` / `privacy_oss.go`
- Modify: `cmd/gateway/main.go`（shouldStartPrivacy + 步骤10 setupPrivacy + acceptor 下移）
- Test: `cmd/gateway/main_test.go`（TestShouldStartPrivacy）、`pkg/config/config_test.go`（privacy yaml 解析）

setupPrivacy 签名两版一致：
`func setupPrivacy(gate core.LicenseGate, cfg config.Config, pipeline *core.Pipeline, auditor plugin.AuditPipeline, storage plugin.StoragePlugin, logger *zap.Logger)`
enterprise 版：shouldStartPrivacy 不过→Info 原因返回；否则 NewPrivacyEngine(storage, 30s, logger) + pipeline.Use(NewPrivacyMiddleware(...)) + Info 启用日志。oss 版空操作。

main.go：步骤6 中删掉 acceptor 行；步骤9 setupTamper 之后加「步骤10」setupPrivacy，然后才 `acceptor := core.NewAcceptor(proxyCore.Handler(), ipf)`。

- [x] Step 4.1 config 解析测试 + shouldStartPrivacy 三分支测试先行
- [x] Step 4.2 实现 → `go test -tags oss ./pkg/config/ ./cmd/...` 绿 → `go build -tags enterprise ./...` 绿 → commit `feat(gateway): E4 隐私防护门控接线(privacy.enabled+FeaturePrivacy)`

### Task 5: admin API

**Files:**
- Create: `pkg/admin/privacy.go`
- Modify: `pkg/admin/router.go`（authz 组注册 8 条路由）
- Test: `pkg/admin/privacy_test.go`

路由：POST/GET/PUT/DELETE `/api/privacy-rules`（创建校验 binding oneof + regexp.Compile 失败 400；injection 强制 scope=request/replacement 空）；POST/GET/DELETE `/api/privacy-whitelist`；GET `/api/security-events?page=&size=`。

- [x] Step 5.1 httptest 用例先行：合法创建 200、非法正则 400、非法 scope 400(binding)、列表 rule_type 过滤、更新、删除 404 分支、白名单 CRUD、events 分页
- [x] Step 5.2 实现 → `go test -tags oss ./pkg/admin/` 绿 → commit `feat(admin): E4 隐私规则库/白名单/安全事件 API`

### Task 6: webui

**Files:**
- Create: `webui/src/api/privacy.ts`、`webui/src/views/PrivacyRules.vue`、`webui/src/views/SecurityEvents.vue`
- Modify: `webui/src/types/index.ts`、`webui/src/router.ts`（/privacy-rules、/security-events）、`webui/src/App.vue`（菜单 Lock/Bell 两项）

PrivacyRules.vue 三 tab：脱敏规则(pii)/注入检测(injection)/白名单——表格 + enabled 开关(el-switch 调 PUT) + 编辑弹窗 + 删除 popconfirm；SecurityEvents.vue 只读分页表（仿 TamperAlertList）。

- [x] Step 6.1 五文件编写
- [x] Step 6.2 `cd webui && npx vue-tsc --noEmit` 通过 → commit `feat(webui): E4 隐私规则库与安全事件页面`

### Task 7: 双编译矩阵验证收尾

- [x] Step 7.1 `make vet && make test`（oss 全量）
- [x] Step 7.2 `go test -race -tags enterprise ./...`
- [x] Step 7.3 `go build -tags oss ./cmd/gateway && go build -tags enterprise ./cmd/gateway`
- [x] Step 7.4 更新项目进度总账 memory（E4 完成，下一步 E5-E8）

## 决策记录（实施期新增）

| # | 决策 | 理由 |
|---|------|------|
| A | 种子常量放 pkg/plugin | OSS 建表即需播种；oss 不能 import enterprise |
| B | 仅 JSON POST 体检查 | multipart 二进制截断损坏转发；超限 MultiReader 还原放行 |
| C | 响应审计存上游原文 | rc.ResponseBody 在包装层之前采集；客户端收到脱敏文本，审计保留模型真实输出 |
| D | acceptor 创建下移至 Use 之后 | pipeline.Build 快照中间件链，晚挂不生效 |
