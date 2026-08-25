# Enterprise E6 合规运维(compliance) — 设计文档

> **日期**: 2026-08-25
> **目标**: 补齐 PRD 3.8 剩余三项——Kafka 外推、合规报表导出(定时生成+留存)、热更新验收；门控 `license.FeatureCompliance`
> **前置**: E2 的 ExportTarget/TailExporter 外推骨架与退避重试体系;E3 的 Tasks 后台任务模式;E5 的 globalOnlyGuard 与权限码
> **版本**: V1.0

---

## 1. 背景与现状

E2 已交付 SIEM/Syslog 双外推目标(`ExportTarget` 接口 + `TailExporter` 时间游标拉取循环 + 失败倍增退避)。PRD 3.8 剩余:Kafka 外推、合规报表导出、热更新。「集群高可用」不在功能清单内,单二进制架构下明确 YAGNI。

「热更新」大半已自然存在:模型配置/上游每请求直查存储,限流写后 ReloadConfig,隐私规则与租户状态 30s TTL。本期以集成测试固化证据并注明冷项。

## 2. 目标 / 非目标

**目标**:
1. Kafka 外推:franz-go 真客户端实现第三种 ExportTarget
2. 合规报表:日报/周报/月报定时生成入库留存,列表查询 + JSON/CSV 下载 + 手动补生成
3. 热更新:逐项核实并补集成测试,输出冷热清单

**非目标(YAGNI)**:
- 集群高可用(PRD 功能清单未列;单二进制架构)
- Kafka SASL/TLS 认证(api_key 字段本期对 kafka 不生效,接口注释留扩展位)
- 报表自动清理(日/周/月报年增量 <500 行,量级可忽略)
- 租户级报表拆分下载(报表为全局域,租户内用户 403)

## 3. 关键决策(已与用户确认)

| # | 决策 | 理由 |
|---|------|------|
| 1 | 三项全做 | 一次清完 PRD 3.8 功能清单 |
| 2 | franz-go 真客户端 | 纯 Go 无 CGo;真协议无需用户额外部署组件 |
| 3 | 报表定时生成+留存 | 用户选择;审计留档场景需要历史可溯 |
| 4 | 启动时补齐缺失期报 | 宕机跨期不丢报;冒烟亦即时可见 |
| 5 | 手动补生成 API | 测试注入与运维兜底双用途;幂等覆盖 |
| 6 | topic 为新增可选配置字段 | PRD 字段表无 topic 但 Kafka 必需;记录为必要延伸 |
| 7 | 热更新以测试+文档交付 | 能力已存在,补证据而非造轮子 |

## 4. 架构

### 4.1 文件清单

```
修改 go.mod/go.sum               # github.com/twmb/franz-go(pkg/kgo) 仅 enterprise 编译引用
新增 pkg/plugin/enterprise/export_kafka.go   # KafkaTarget 实现 ExportTarget;
                                              # NewExportTarget 工厂注册 "kafka" 分支
修改 pkg/config/config.go        # ExportConfig += Topic string;新增 ComplianceConfig{Enabled bool}
修改 config.yaml                 # export.topic 注释;compliance.enabled 开关段
修改 pkg/plugin/interface.go     # ComplianceReport 结构;StoragePlugin += 报表三方法
修改 oss storage_mem/sql/sqlite/mysql/dynamic  # compliance_reports 表建表+CRUD 实现
新增 enterprise/compliance_report.go         # 聚合器纯函数 + ReportScheduler(仿 Tasks 模式)
新增 cmd/gateway/compliance_enterprise.go / compliance_oss.go  # setupCompliance BuildTag 两版
修改 cmd/gateway/main.go         # shouldStartCompliance + 步骤12 接线(报表调度随构建版本)
修改 pkg/admin/compliance.go     # 列表/下载/手动补生成 三 handler + router 注册(system:read/write)
新增 webui views ComplianceReports.vue + api/compliance.ts + 菜单项
                                  # 改 webui 必须同轮 make build-webui 提交 dist(E5 复盘教训)
各对应 _test.go;core 热更新集成测试 hot_reload_test.go
```

### 4.2 数据模型

```go
// ComplianceReport 合规报表(period_type+period_start 唯一)
type ComplianceReport struct {
    ID          string          `json:"id"`
    PeriodType  string          `json:"period_type"` // day | week | month
    PeriodStart time.Time       `json:"period_start"` // 日=当日0点 周=周一0点 月=1日0点(本地时区)
    PeriodEnd   time.Time       `json:"period_end"`
    GeneratedAt time.Time       `json:"generated_at"`
    Content     *ReportContent  `json:"content"`
}

// ReportContent 聚合快照(JSON 存储)
type ReportContent struct {
    TotalRequests int64           `json:"total_requests"`
    TotalTokens   int64           `json:"total_tokens"`
    Error4xx      int64           `json:"error_4xx"`
    Error5xx      int64           `json:"error_5xx"`
    StreamCount   int64           `json:"stream_count"`
    ByModel       []DimensionStat `json:"by_model"`
    ByTenant      []DimensionStat `json:"by_tenant"`
}
type DimensionStat struct {
    Key    string `json:"key"`    // 模型名/租户ID(""=全局Key 归入 "(global)")
    Requests int64 `json:"requests"`
    Tokens   int64 `json:"tokens"`
}
```

StoragePlugin 新增：
SaveComplianceReport(report)（UPSERT 按 period_type+period_start，重复生成覆盖）/
ListComplianceReports(page,size)/GetComplianceReport(id)/CountComplianceReports()。

聚合数据源复用 QueryAuditLogs(filter StartTime/EndTime) 翻页拉取——不新增专用 SQL 聚合，
量级(单周期百万内)可接受且三存储行为天然一致。

### 4.3 Kafka 外推

```go
// KafkaTarget: endpoint = "b1:9092,b2:9092"(逗号分隔 broker);topic 取 ExportConfig.Topic,
// 空 → 默认 "neuralgate-audit";api_key 本期忽略(留 SASL 扩展注释)
func NewKafkaTarget(endpoint, topic string) (*KafkaTarget, error)
// Send: kgo.Client Produce 同步等待全部分区 ack;消息 key=request_id(分区内有序),value=单条日志 JSON
// TestConnection: client.Ping(bootstrap 连接)
// Close: client.Close()
```

`NewExportTarget` 工厂加 "kafka" 分支，签名扩为 NewExportTarget(type, endpoint, apiKey, topic string)。
TailExporter.Init 透传 cfg["topic"]；ExportConfig.Topic yaml 字段默认空。
franz-go 仅被 //go:build enterprise 文件 import,OSS 构建零依赖。

### 4.4 报表调度器(enterprise/compliance_report.go)

```
Aggregator: AggregateReport(logs []*plugin.AuditLog) *ReportContent   # 纯函数
buildRange(periodType, refTime): (start,end)   # 返回 refTime 所在周期:
                                               # day=当日0点起24h / week=所在周周一0点起7天 / month=自然月
generate(storage, periodType, refTime): 对 refTime 所在周期拉审计→聚合→UPSERT
                                               # 幂等,重复执行覆盖;调度器传"昨日/上周一/上月1日"
                                               # 手动补生成传用户指定 start(取其所在周期)

ReportScheduler(仿 E3 Tasks):
  Start(): goroutine 循环,tick=1min:
    - 00:05 后且当日日报缺失 → 生成昨日日报
    - 周一 00:10 后且上周周报缺失 → 生成
    - 每月1日 00:15 后且上月月报缺失 → 生成
  Start 时先跑一轮 catchUpMissing(): 从今天回扫 N 天(N=35,覆盖一个月粒度)补齐缺失期报
  Stop(): ctx 取消 + Wait
```

调度判定用「查库是否已有该期」而非内存状态,重启安全。

### 4.5 admin API 与权限

- `GET /api/compliance-reports?page&size`(system:read)列表倒序
- `GET /api/compliance-reports/:id?format=json|csv`(system:read);CSV 行列化:
  维度节展开为 section 行(model_rows/tenant_rows)
- `POST /api/compliance-reports/generate`(system:write)body {period_type,start}(start 缺省按当前周期);
  已存在覆盖返回现有 id
- 三者均挂 globalOnlyGuard:报表为全局域,租户内用户 403(沿用 E5)
- 手动补生成是调度器的唯一测试注入口(调度时间不可注入时用例走 generate 直测聚合与幂等)

### 4.6 门控与接线

```
shouldStartCompliance(gate, enabled): enabled=false→"配置未启用(compliance.enabled=false)";
  缺 feature→"授权未包含 compliance 功能"
main 步骤12: stopCompliance := setupCompliance(gate, cfg, storage, logger) —— BuildTag 两版:
  enterprise 版: start 则构造 ReportScheduler.Start() 并返回 Stop;
  oss 版: 恒 nil 空操作(Kafka 目标注册在 enterprise 包工厂内,OSS 本就不可达)
Shutdown 序列: stopTamper 之后、auditor.Shutdown 之前调用 stopCompliance()
config.yaml compliance 段置于 export 与 privacy 之间
```

### 4.7 热更新清单(测试固化)

| 配置项 | 生效方式 | 动作 |
|---|---|---|
| 模型配置/上游 | 即时(每请求直查) | 新增 hot_reload_test.go 集成证明 |
| 限流规则 | 写后 ReloadConfig | 既有用例纳入矩阵确认 |
| 隐私规则/白名单 | ≤30s TTL | E4 用例覆盖 |
| 租户禁用 | ≤30s TTL | E5 用例覆盖 |
| 角色/权限 | 重登录生效 | 规格既定,README 不写(对外口径) |
| License/外推目标 | 重启生效 | 已知限制,代码注释注明 |

## 5. 配置

```yaml
export:                       # Enterprise only：审计日志外推(audit_stream 门控)
  ...                         # 既有字段不变
  topic: ""                   # Kafka 目标 topic(仅 type=kafka 使用,空=neuralgate-audit)

compliance:                   # Enterprise only：合规运维(compliance 门控)
  enabled: false              # 是否启用合规报表调度；默认 false，需显式开启且授权包含 compliance
```

`ComplianceConfig{Enabled bool}` bool 不参与 applyDefaults。

## 6. 测试策略(TDD,go test -race,双编译矩阵)

| 单元 | 测试点 |
|------|--------|
| Aggregator | 构造审计样本精确断言六项总量与两维度分布;空区间零值;流式计数 |
| buildRange | day/week(跨月周)/month 边界(闰年2月) |
| generate 幂等 | 同期重复生成覆盖不重复插;启动 catchUp 回扫补缺 |
| Scheduler | tick 判定逻辑抽纯函数测;Stop 干净退出(-race) |
| Kafka | endpoint 解析/topic 默认值/消息 key 组装;NG_KAFKA_BROKER 门控真连(缺省 skip) |
| 存储 | compliance_reports CRUD+UPSERT 幂等三存储一致性(mem/sqlite,mysql NG_MYSQL_DSN) |
| admin API | 列表/JSON 下载/CSV 行列化/手动补生成覆盖/租户内用户 403 |
| 热更新 | 改模型配置不停机生效的端到端 httptest |
| 矩阵 | OSS 行为零变化;enterprise 全绿;webui vue-tsc + dist 重建提交 |

## 7. 决策记录

| # | 决策 | 理由 |
|---|------|------|
| 1 | 聚合走 QueryAuditLogs 翻页而非 SQL GROUP BY | 三存储行为一致;避免方言分支;量级可控 |
| 2 | 调度判定查库而非内存 | 重启安全;catchUp 与定时共用同一幂等入口 |
| 3 | CSV 由 JSON 快照行列化 | 存储只存一份真相;导出格式可扩展 |
| 4 | topic 默认 neuralgate-audit | PRD 无该字段但协议必需;空值兜底 |
| 5 | 报表调度器随 setupCompliance 接线而非独立 gate | compliance.enabled 同时控制报表与未来合规子项,单一开关语义清晰 |
