# Enterprise E6 合规运维 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 补齐 PRD 3.8 剩余三项——Kafka 外推(franz-go)、合规报表(定时生成+留存)、热更新验收；`license.FeatureCompliance` 门控。

**Architecture:** KafkaTarget 实现既有 ExportTarget 接口挂入 TailExporter 循环；报表聚合走 QueryAuditLogs 翻页 + 纯函数聚合器，ReportScheduler(仿 E3 Tasks) 定时+启动补扫，compliance_reports 表 UPSERT 幂等留存。

**Tech Stack:** Go + kgo(franz-go,仅 enterprise 编译) + gin + zap；webui Vue3 + element-plus。

## Global Constraints

- 所有 Go 文件带 Apache-2.0 头 `Copyright 2026 FanYaNan`；注释只写方案
- enterprise 新文件一律 `//go:build enterprise`；franz-go 依赖只允许出现在带该 tag 的文件
- 双矩阵全绿：`go test -tags oss ./...` 与 `go test -race -tags enterprise ./...`
- `compliance.enabled` 默认 false；bool 不参与 applyDefaults；未启用零行为变化
- 报表为全局域数据：三 handler 均挂 globalOnlyGuard（租户内用户 403）
- 改 webui 必须同轮 `make build-webui` 并提交 dist（E5 复盘教训）
- README 不改动

### 关键接线事实（探索已确认）

- `NewExportTarget(type, endpoint, apiKey string)` 仅 export_tail.go Init 一处调用（108 行），扩签名需同步透传 `config["topic"]`
- main.go exporter.Init 的 config map 需补 `"topic": cfg.Export.Topic`
- E3 Tasks 模式：stopCh/doneCh 双通道 + close(stopCh) 幂等 Stop + <-doneCh 等待退出
- QueryAuditLogs 支持 filter.StartTime/EndTime，mem/sql 行为一致
- shutdown 序列锚点：stopTamper() 之后、auditor.Shutdown() 之前插入 stopCompliance()

---

### Task 1: 报表存储层

**Files:**
- Modify: `pkg/plugin/interface.go`
- Modify: `pkg/plugin/oss/storage_mem.go`、`storage_sql.go`、`storage_sqlite.go`、`storage_mysql.go`、`storage_dynamic.go`
- Test: `pkg/plugin/oss/storage_compliance_test.go`

**Interfaces (Produces):**
```go
// interface.go
type DimensionStat struct {
    Key      string `json:"key"`      // 模型名/租户ID;空串归 "(global)"
    Requests int64  `json:"requests"`
    Tokens   int64  `json:"tokens"`
}
type ReportContent struct {
    TotalRequests int64           `json:"total_requests"`
    TotalTokens   int64           `json:"total_tokens"`
    Error4xx      int64           `json:"error_4xx"`
    Error5xx      int64           `json:"error_5xx"`
    StreamCount   int64           `json:"stream_count"`
    ByModel       []DimensionStat `json:"by_model"`
    ByTenant      []DimensionStat `json:"by_tenant"`
}
type ComplianceReport struct {
    ID          string         `json:"id"`
    PeriodType  string         `json:"period_type"`  // day | week | month
    PeriodStart time.Time      `json:"period_start"` // 所在周期起点(本地时区0点)
    PeriodEnd   time.Time      `json:"period_end"`
    GeneratedAt time.Time      `json:"generated_at"`
    Content     *ReportContent `json:"content"`
}
const ( PeriodDay = "day"; PeriodWeek = "week"; PeriodMonth = "month" )

// StoragePlugin 追加五方法:
SaveComplianceReport(report *ComplianceReport) error   // UPSERT by (period_type, period_start),覆盖保留原 id
ListComplianceReports(page, size int) ([]*ComplianceReport, int64, error) // period_start 倒序
GetComplianceReport(id string) (*ComplianceReport, error)
FindComplianceReportByPeriod(periodType string, periodStart time.Time) (*ComplianceReport, error) // 调度判重用
CountComplianceReports() (int64, error)
```

DDL（sqlite/mysql 同构，时间 INTEGER/BIGINT ms，content TEXT 存 JSON）：
```sql
CREATE TABLE IF NOT EXISTS compliance_reports (
    id TEXT PRIMARY KEY,
    period_type TEXT NOT NULL,
    period_start INTEGER NOT NULL,
    period_end INTEGER NOT NULL,
    generated_at INTEGER NOT NULL,
    content TEXT NOT NULL DEFAULT '{}',
    UNIQUE(period_type, period_start)
)
```

- [ ] Step 1.1 测试先行 `storage_compliance_test.go`：mem Save→Get→List 往返(Content 深度相等)；同期重复 Save 覆盖且总数不变、id 不变；FindByPeriod 命中/未命中 ErrNotFound；sqlite Init 建表+UPSERT 幂等
- [ ] Step 1.2 实现至 `go test -tags oss ./pkg/plugin/...` 绿（SQL UPSERT 方言分支照 SaveAdminUser 模式；UNIQUE 冲突更新 content/generated_at/period_end）
- [ ] Step 1.3 commit `feat(plugin): E6 合规报表存储层(period 幂等 UPSERT)`


---

### Task 2: Kafka 外推目标

**Files:**
- Modify: `go.mod`/`go.sum`（`go get github.com/twmb/franz-go`）
- Create: `pkg/plugin/enterprise/export_kafka.go`
- Modify: `pkg/plugin/enterprise/export_target.go`（工厂加 kafka 分支，签名扩 topic）、`export_tail.go`（Init 透传 topic）
- Modify: `pkg/config/config.go`（ExportConfig += Topic string `yaml:"topic"`）、`cmd/gateway/main.go`（exporter.Init 补 `"topic"` 键）
- Test: `pkg/plugin/enterprise/export_kafka_test.go`

**Interfaces (Produces):**
```go
NewExportTarget(exportType, endpoint, apiKey, topic string) (ExportTarget, error) // 四参新签名
func NewKafkaTarget(brokers, topic string) (*KafkaTarget, error)
// brokers=逗号分隔;topic 空→默认 "neuralgate-audit";api_key 本期对 kafka 忽略(留 SASL 注释)
// Send: 每条日志一条 Record{Topic, Key:[]byte(log.RequestID), Value:JSON},ProduceSync 整批
// TestConnection: kgo.Client.Ping(ctx 5s);Close: client.Close()
```

- [ ] Step 2.1 失败测试：brokers 空→error；topic 空构造后内部默认值可断言(暴露 getter 或测 Send 组装)；消息 key=request_id、value 可反序列化为 AuditLog（用注入的 mock kgo client? kgo 无接口——拆纯函数 `kafkaRecords(topic, logs)` 返回 []*kgo.Record 直接断言）；NG_KAFKA_BROKER 门控真连测试（缺省 t.Skip）
- [ ] Step 2.2 实现 + 工厂分支 `case "kafka": return NewKafkaTarget(endpoint, topic)`；TailExporter.Init 读 `config["topic"]` 传四参
- [ ] Step 2.3 `go build -tags oss ./...`(确认 oss 不引 franz-go) + `go test -race -tags enterprise ./pkg/plugin/enterprise/` 绿
- [ ] Step 2.4 commit `feat(enterprise): E6 Kafka 外推目标(franz-go)`

### Task 3: 报表聚合与生成

**Files:**
- Create: `pkg/plugin/enterprise/compliance_report.go`
- Test: `pkg/plugin/enterprise/compliance_report_test.go`

**Interfaces (Produces):**
```go
func BuildRange(periodType string, ref time.Time) (time.Time, time.Time)
// day=ref 当日0点起24h;week=所在周周一0点起7天(周一起始);month=自然月;未知类型→day 兜底
func AggregateReport(logs []*plugin.AuditLog) *plugin.ReportContent
// TotalRequests=len;TotalTokens=Σlog.TotalTokens;400-499→Error4xx;>=500→Error5xx;
// IsStream→StreamCount;ByModel 按 log.ModelName、ByTenant 按 TenantID(空→"(global)");
// 两维度均 Requests 降序、同数 Key 字典序(确定性);空日志返回全零值非 nil
func GenerateComplianceReport(storage plugin.StoragePlugin, logger *zap.Logger,
    periodType string, ref time.Time) (*plugin.ComplianceReport, error)
// QueryAuditLogs({StartTime,End_time},page,1000) 翻页拉全→Aggregate→Save(UPSERT)
```

- [ ] Step 3.1 失败测试：
  - BuildRange 三周期边界：week 用固定周三 `time.Date(2026,8,26,...)` 断言起点为 2026-08-24(周一)；跨月周(2026-09-02 周三 → 起点 2026-08-31 周一)；month 闰年 2 月(2024-02-15 → end=2024-03-01)
  - AggregateReport 样本：3 条日志(200 流式+404+502)、两模型两租户、含空租户 → 六项总量与分布逐字段精确断言
  - Generate 幂等：MemStorage 预置区间内审计→生成→再生成→CountComplianceReports==1 且 GeneratedAt 更新
- [ ] Step 3.2 实现至 `go test -race -tags enterprise ./pkg/plugin/enterprise/` 绿
- [ ] Step 3.3 commit `feat(enterprise): E6 报表聚合器与周期生成(纯函数+幂等)`


### Task 4: 报表调度器

**Files:**
- Create: `pkg/plugin/enterprise/compliance_scheduler.go`
- Test: `pkg/plugin/enterprise/compliance_scheduler_test.go`

**Interfaces (Produces):**
```go
type ReportScheduler struct {
    storage plugin.StoragePlugin; logger *zap.Logger
    stopCh chan struct{}; doneCh chan struct{}
}
func NewReportScheduler(storage plugin.StoragePlugin, logger *zap.Logger) *ReportScheduler
func (s *ReportScheduler) Start()  // 先 catchUpMissing() 再 go loop();loop tick=1min
func (s *ReportScheduler) Stop()   // close(stopCh) 幂等 + <-doneCh(仿 E3 Tasks)

// 纯函数供单测:
func dueReports(now time.Time) [][2]string
// 返回此刻应存在而可能缺失的 [{period_type, ref 的 RFC3339 日期串}...]:
// now≥当日00:05 → 昨日 day;now 为周一且≥00:10 → 上周 week(ref=8天前);
// now 为每月1日且≥00:15 → 上月 month(ref=上月1日)
func (s *ReportScheduler) catchUpMissing() // 回扫 35 天:对每天 ref 补 day,并对该 ref 所在周/月补 week/month;
                                           // 每项先 FindComplianceReportByPeriod 判重,缺失才 generate
```

- [ ] Step 4.1 失败测试：
  - `dueReports` 三分支：`time.Date(2026,8,25,0,6,0,...)` 含昨日 day；周一 `2026-08-24 00:11`(周一) 含 week；`2026-09-01 00:16`(周二) 不含 week 但含 month；00:04 时刻返回空
  - catchUp：MemStorage 预置跨 3 天审计→catchUpMissing→day/week/month 各有报表；重复执行不再新增
  - Stop 幂等：Start 后立即 Stop×2 不 panic 且 doneCh 关闭（-race）
- [ ] Step 4.2 实现至 enterprise 包测试绿；tick 循环体用 runWithRecover 兜底 panic
- [ ] Step 4.3 commit `feat(enterprise): E6 报表调度器(定时+启动补扫+幂等)`

### Task 5: admin API 与 CSV 导出

**Files:**
- Create: `pkg/admin/compliance.go`
- Modify: `pkg/admin/router.go`（authz 组注册三路由）
- Test: `pkg/admin/compliance_test.go`

**Interfaces (Produces):**
```go
authz.GET("/compliance-reports", RequirePermission(system:read), globalOnlyGuard, listComplianceReports)
authz.GET("/compliance-reports/:id", RequirePermission(system:read), globalOnlyGuard, getComplianceReport) // ?format=json|csv 缺省 json
authz.POST("/compliance-reports/generate", RequirePermission(system:write), globalOnlyGuard, generateComplianceReport)
// generate body: {"period_type":"day|week|month","start":"2006-01-02"(可空,空取当前周期所在起点)}
func complianceToCSV(r *plugin.ComplianceReport) string // 纯函数:
// section,key,requests,tokens 表头;summary 行(total_requests/total_tokens/error_4xx/error_5xx/stream_count,
// requests 列放数值 tokens 留空);model_*/tenant_* 维度行
```

- [ ] Step 5.1 失败测试（复用 rbacFixture 风格 superTok）：列表分页倒序；JSON 下载字段完整；CSV 断言含表头与 summary/model/tenant 行数；generate 指定 start 生成后同 start 再生成覆盖(Count==1)；租户内用户三接口 403(globalOnlyGuard)；format=xml → 400
- [ ] Step 5.2 实现至 `go test -tags oss ./pkg/admin/` 绿
- [ ] Step 5.3 commit `feat(admin): E6 合规报表查询/下载/手动补生成 API`

### Task 6: 配置与 BuildTag 接线

**Files:**
- Modify: `pkg/config/config.go`（ComplianceConfig{Enabled bool} + Config.Compliance，bool 不进 applyDefaults）、`config.yaml`（export 段后加 compliance 段；export 段补 topic 注释行）
- Create: `cmd/gateway/compliance_enterprise.go` / `compliance_oss.go`
- Modify: `cmd/gateway/main.go`、`cmd/gateway/main_test.go`

**核心代码：**
```go
// main.go shouldStartCompliance(仿 shouldStartPrivacy):
// enabled=false→"配置未启用(compliance.enabled=false)";缺 feature→"授权未包含 compliance 功能"
// enterprise 版:
func setupCompliance(gate core.LicenseGate, cfg config.Config,
	storage plugin.StoragePlugin, logger *zap.Logger) func() {
	start, reason := shouldStartCompliance(gate, cfg.Compliance.Enabled)
	if !start {
		logger.Info("合规运维未启用", zap.String("reason", reason))
		return nil
	}
	sched := enterprise.NewReportScheduler(storage, logger)
	sched.Start()
	logger.Info("合规报表调度已启用")
	return sched.Stop
}
// oss 版: 同签名恒 return nil
// main 步骤12(步骤11 rbac 之后): stopCompliance := setupCompliance(gate, *cfg, storage, logger)
// shutdown 序列: stopTamper() 调用之后插入 if stopCompliance != nil { stopCompliance() }
```

config.yaml：
```yaml
export:
  ...                          # 既有字段
  topic: ""                    # Kafka 目标 topic(仅 type=kafka 使用,空=neuralgate-audit)

compliance:                    # Enterprise only：合规运维(compliance 门控)
  enabled: false               # 是否启用合规报表调度；默认 false，需显式开启且授权包含 compliance
```

- [ ] Step 6.1 TestShouldStartCompliance 四分支测试先行(照 TestShouldStartRBAC)
- [ ] Step 6.2 实现 → `go test -tags oss ./pkg/config/ ./cmd/...` 绿 + `go build -tags enterprise ./...` 绿
- [ ] Step 6.3 commit `feat(gateway): E6 合规报表调度门控接线(compliance.enabled+FeatureCompliance)`

### Task 7: webui 合规报表页

**Files:**
- Create: `webui/src/api/compliance.ts`、`webui/src/views/ComplianceReports.vue`
- Modify: `webui/src/types/index.ts`(ReportItem/ReportContent)、`webui/src/router.ts`(/compliance-reports)、`webui/src/App.vue`(菜单 system:read 显隐)

页面规格：周期类型筛选下拉(day/week/month/全部)+表格(period_type/period_start~period_end/generated_at)+「下载 JSON」「下载 CSV」按钮(window.open 带 token?——client 用 X-Admin-Token 头，改用 axios blob 下载)+「立即生成」弹窗(period_type+日期)。菜单项挂 `hasPerm('system:read')`。

- [ ] Step 7.1 五处编写(blob 下载用 client.get({responseType:'blob'}) 自建 a 标签导出)
- [ ] Step 7.2 `npx vue-tsc --noEmit` 通过 → **`make build-webui` 并 git add pkg/admin/webui/dist** → commit `feat(webui): E6 合规报表页与产物重建`

### Task 8: 热更新验收与矩阵收尾

**Files:**
- Create: `pkg/core/hot_reload_test.go`
- Modify: `.superpowers` 无;memory 更新

热更新集成测试(证明不停机生效)：
```go
TestModelConfigHotReload: RouteMatchMiddleware(storage, registry) 链上两次请求同一 model 名;
中间改存储中该模型 Enabled=false → 第二次请求 404 model_not_found(无重启、无缓存失效动作)
```

- [ ] Step 8.1 hot_reload_test.go 编写并通过(-race)
- [ ] Step 8.2 `rtk proxy go vet -tags oss ./... && rtk proxy go vet -tags enterprise ./...` 双 vet 干净
- [ ] Step 8.3 oss 全量 + enterprise `-race` 全量双绿;双版本二进制构建
- [ ] Step 8.4 端到端冒烟：licensegen 签发 compliance 授权→启动企业版(compliance.enabled=true)→日志出现「合规报表调度已启用」→启动即 catchUp 生成昨日日报→`GET /api/compliance-reports` 可见→手动 generate 周报→CSV 下载头几行正确；若本机 docker 可用则起单节点 KRaft Kafka 验证 kafka 外推真连(不可用则注明跳过，单测覆盖为准)；无 compliance 授权时日志给出门控原因且零行为变化
- [ ] Step 8.5 更新项目进度总账 memory(E6 完成)

## 决策记录（实施期新增）

| # | 决策 | 理由 |
|---|------|------|
| A | FindComplianceReportByPeriod 单独成方法 | 调度判重需按业务键查询而非翻页遍历 |
| B | kafkaRecords 拆纯函数测消息组装 | kgo.Client 无接口可 mock;真连留给 NG_KAFKA_BROKER 门控 |
| C | dueReports 抽纯函数 | 时间触发逻辑可测,loop 仅做 ticker 驱动 |
| D | 手动 generate 是测试唯一注入口 | 调度时间不注入,避免为测试污染构造器签名 |
| E | catchUp 回扫跳过零审计数据周期;定时 tick 到期项无条件生成 | Step 4.1 测试断言 dayCount==3 为准;留存语义要求到期必建 |
| F | generate 经 AdminServer 注入 ReportGenerator 函数(默认 nil→503),Task 6 企业接线注入真实现 | pkg/admin 无构建标签且不得依赖 enterprise 包(E4 解耦哲学+EnableRBAC 注入先例);否则 oss 矩阵无法编译 |
