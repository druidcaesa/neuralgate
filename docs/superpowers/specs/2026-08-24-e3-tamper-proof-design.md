# Enterprise E3 审计防篡改存证(tamper_proof)— 设计文档

> **日期**: 2026-08-24
> **目标**: 实现 PRD 3.5 日志防篡改全量五项——SHA256 存证计算(enterprise 独占)、定期哈希校验、篡改告警持久化与后台展示、留存策略清理、审计库权限隔离交付物
> **前置**: E1 门控(`license.FeatureTamperProof` 已定义未消费);E2 确立的 `shouldStartXxx(gate, enabled)` 门控模式;现有 `AuditLog.SHA256Fingerprint` 字段、DB 列、`EnableSHA256` 配置均为空壳(无任何计算逻辑)
> **版本**: V1.0

---

## 1. 背景与现状

PRD 3.5 五个子功能现状：SHA256 存证(❌ 字段/DB 列/配置在，无计算逻辑)、审计库独立权限(❌ 无交付物)、留存策略(⚠️ 配置在无清理任务)、定期校验(❌ 无)、篡改告警(❌ 无)。

审计落库集中在共享的 `SimpleAuditor`(Finalize/MarkDisconnect/Shutdown 三处调 `SaveAuditLog`)；存储层有 mem/sqlite/mysql(dynamic 复用 sql)三套实现。

## 2. 目标 / 非目标

**目标**：见标题五项；OSS 版行为零变化（不携带指纹逻辑、默认不触发清理）。

**非目标(YAGNI)**：
- 哈希链(prev_hash)防删除检测——PRD 以 DB 权限解决"不可删除"，链式重构成本高
- 告警外部通知(webhook/邮件/SIEM 联动)——SIEM 外推(E2)天然把被篡改记录差异带出
- 金仓/达梦权限脚本——E8 信创迭代
- audit_logs 表加"最后校验时间戳"列——校验检查时间记于告警表 `LastCheckedAt`
- 非对称签名抵赖性——PRD 口径为 SHA256 摘要比对

## 3. 关键决策(已与用户确认)

| # | 决策 | 理由 |
|---|------|------|
| 1 | **范围 = 全量五项**(含 MySQL 受限账号 SQL 模板作为权限隔离交付物) | 完整覆盖 PRD 3.5 与验收 6.5 |
| 2 | **SimpleAuditor 加可选指纹钩子**，enterprise 装配时注入 | 零重复代码；三处落库点自动全覆盖；OSS 不注入则行为零变化 |
| 3 | **StoragePlugin 接口扩展 + 告警持久化**(新表 audit_tamper_alerts) | PRD「管理员查看告警」要求重启不丢；留存清理本就需要新方法 |
| 4 | **指纹 = 除指纹字段外全部内容的确定性序列化 + sha256 hex** | 沿用 license 载荷的长度前缀方案，杜绝分隔符歧义 |
| 5 | **门控 = shouldStartTamper(gate, EnableSHA256)** 双条件 | 与 E2 同模式；gate 为 E1 定义的首个 tamper_proof 消费者 |
| 6 | **校验采用全库滚动扫描**，页大小 = verify_batch_size | 语义简单正确；告警 upsert 天然去重 |

## 4. 架构

### 4.1 文件清单

```
修改:
  pkg/plugin/interface.go          # StoragePlugin += DeleteAuditLogsBefore/SaveTamperAlerts/ListTamperAlerts;
                                    # 新结构 TamperAlert;新类型 FingerprintFunc 与可选接口 FingerprintHook
  pkg/plugin/oss/storage_mem.go    # 三方法(内存实现)
  pkg/plugin/oss/storage_sqlite.go # 建表 audit_tamper_alerts(幂等)+三方法
  pkg/plugin/oss/storage_sql.go    # mysql 建表(幂等)+三方法
  pkg/plugin/oss/audit_simple.go   # 钩子字段+SetFingerprintFunc;三处落库前调用
  pkg/config/config.go             # AuditConfig += VerifyInterval(默认24h)/VerifyBatchSize(默认1000)
  cmd/gateway/main.go              # 步骤9 注入钩子+启动校验/清理任务;关闭序列插入任务停止
  config.yaml                      # audit 段补 verify_interval/verify_batch_size 注释
  pkg/admin/router.go / system.go  # 告警路由;system 追加 tamper.unresolved_count
新增:
  pkg/plugin/enterprise/tamper.go           # Fingerprint(*AuditLog) string;NewTamperTasks 装配
  pkg/plugin/enterprise/tamper_verify.go    # Verifier:全库滚动扫描比对+告警 upsert
  pkg/plugin/enterprise/tamper_retention.go # Retainer:每小时 DeleteAuditLogsBefore(now-retention)
  pkg/admin/tamper.go                       # GET /api/tamper-alerts;PATCH /api/tamper-alerts/:id
  docs/sql/mysql_tamper_readonly.sql        # 业务受限账号授权模板
  各对应 _test.go
webui:
  types/index.ts + api/tamper.ts + views/TamperAlertList.vue + SystemInfo.vue 红色告警 + router
```

### 4.2 指纹口径（签算两侧必须一致）

对以下字段按固定顺序做「长度前缀」序列化（同 `license.CanonicalPayload` 格式）后取 sha256 hex：
ID、RequestID、TenantID、APIKeyID、ModelName、Provider、RequestMethod、RequestPath、
RequestHeaders(**map 按 key 字典序**)、RequestBody、ResponseStatus、ResponseBody、
SSEChunks(逐条 Index:EventType:Data:Timestamp)、PromptTokens、CompletionTokens、TotalTokens、
Duration、ClientIP、IsStream、Disconnected、DisconnectReason、CreatedAt(UTC RFC3339)。
**不含** SHA256Fingerprint 自身。时间统一 UTC；Headers 排序保证 map 确定性。

### 4.3 存储接口扩展

```go
// FingerprintFunc 审计指纹计算函数(enterprise 提供,OSS 为 nil)
type FingerprintFunc func(log *AuditLog) string

// FingerprintHook 可选能力:审计器支持注入指纹计算
type FingerprintHook interface{ SetFingerprintFunc(fn FingerprintFunc) }

// TamperAlert 篡改告警
type TamperAlert struct {
	ID            string    // 主键
	AuditLogID    string    // 被篡改日志 ID
	Reason        string    // 不一致描述
	Resolved      bool      // 是否已处置
	FirstSeenAt   time.Time // 首次发现
	LastCheckedAt time.Time // 最近一次确认仍不一致
}

// StoragePlugin 新增:
DeleteAuditLogsBefore(cutoff time.Time) (int64, error)
SaveTamperAlerts(alerts []*TamperAlert) error // 同一 AuditLogID 存在未处置告警则更新 LastCheckedAt/Reason,否则插入
ListTamperAlerts(resolved *bool, page, size int) ([]*TamperAlert, int64, error) // nil=全部
```

sqlite/mysql 建表幂等（CREATE TABLE IF NOT EXISTS，沿用现有 ensure 机制）。

### 4.4 数据流

```
落库: SimpleAuditor 三处 SaveAuditLog 前 → fn != nil 时 log.SHA256Fingerprint = fn(log)
校验(Verifier): 每 verify_interval tick → 按 CreatedAt DESC 全库分页滚动扫描(页大小=verify_batch_size,
  内存游标记录已扫页位,扫完回绕;重启从头) → 重算指纹比对 →
  不一致: SaveTamperAlerts(upsert 未处置告警,更新 LastCheckedAt) + Warn 日志
  一致: 无动作
留存(Retainer): 每 1h tick → DeleteAuditLogsBefore(now - retention_days*24h) → Info 日志删除条数
```

### 4.5 门控与启动/关闭序列

```go
// main.go 步骤 9（步骤 8 外推之后）
if setter, can := auditor.(plugin.FingerprintHook); can {
    if start, reason := shouldStartTamper(gate, cfg.Audit.EnableSHA256); !start {
        logger.Info("审计防篡改未启用", zap.String("reason", reason))
    } else {
        setter.SetFingerprintFunc(tamper.Fingerprint)
        tasks := tamper.NewTasks(storage, cfg.Audit, logger) // Verifier+Retainer
        tasks.Start()
        // defer 式收尾登记进关闭序列
    }
}
// shouldStartTamper(gate, enabled): enabled=false→"配置未启用(enable_sha256=false)";
//                                   缺 feature→"授权未包含 tamper_proof 功能"
```

```
关闭(在 E2 序列基础上前置任务停止):
proxy/admin Shutdown → tamper Tasks.Stop → auditor.Shutdown → exporter.Close → storage.Close
```

### 4.6 管理后台

- `/api/system` 追加 `"tamper": {"unresolved_count": N}`（ListTamperAlerts(resolved=false,1,1).total）
- `GET /api/tamper-alerts?resolved=true|false&page=&size=`（统一响应包装）
- `PATCH /api/tamper-alerts/:id` body `{"resolved":true}`
- webui：SystemInfo 页 unresolved_count>0 时红色 el-alert（点击跳转告警页）；新增 TamperAlertList.vue（表格：日志ID/原因/首次发现/最近检查/处置按钮）+ 路由菜单 + api/types

### 4.7 权限隔离交付物

`docs/sql/mysql_tamper_readonly.sql`：创建业务账号并对 audit_logs/audit_tamper_alerts 仅授 SELECT 的语句模板（含回收误授的 REVERSE 示例）；SQLite 无账号体系，文件内注释给出替代措施（部署账号隔离 + 文件权限 0640）。金仓/达梦留 E8。

## 5. 配置

```yaml
audit:
  enable_sha256: true         # SHA256存证（Enterprise，需 tamper_proof 授权）
  retention_days: 90          # 日志保留天数
  verify_interval: 24h        # 哈希校验间隔（Enterprise）
  verify_batch_size: 1000     # 每次校验批次大小（Enterprise）
```

`config.AuditConfig` 增加 `VerifyInterval time.Duration`、`VerifyBatchSize int`；applyDefaults 回填默认（bool 例外约定不变）。

## 6. 测试策略(TDD,go test -race,双编译矩阵)

| 单元 | 测试点 |
|------|--------|
| Fingerprint | 同内容稳定；任一字段变化指纹变；Headers 乱序不影响；指纹字段自身不入算 |
| Hook 注入 | OSS 不注入→三落库路径指纹均空；注入后三路径(Finalize/MarkDisconnect/Shutdown)均带指纹 |
| Verifier | 篡改 RequestBody 后扫描产生告警；再次扫描去重不重复插入；正常记录无告警 |
| Retainer | cutoff 边界删除计数正确，未到期保留 |
| 存储三实现 | SaveTamperAlerts upsert 语义；ListTamperAlerts resolved 过滤分页；DeleteAuditLogsBefore(sqlite/mysql 沿用既有测试模式，mysql 用 NG_MYSQL_DSN) |
| admin | system 含 unresolved_count；告警列表/处置 API |
| 门控 | shouldStartTamper 各分支 |
| 矩阵 | oss 行为零变化(167 基线)；enterprise 全绿；webui vue-tsc 通过 |

## 7. PRD 6.5 验收对照

| 验收项 | 实现点 |
|--------|--------|
| 哈希生成(每条有指纹) | 钩子三落库路径全覆盖 |
| 不可修改/不可删除(DB 权限) | mysql_tamper_readonly.sql 交付物 |
| 篡改检测→告警显示被篡改记录 | Verifier+告警表+webui 列表(含 AuditLogID 可跳审计查询) |
| 留存策略超期清理 | Retainer+DeleteAuditLogsBefore |

## 8. 决策记录

| # | 决策 | 理由 |
|---|------|------|
| 1 | 钩子而非独立企业审计器 | 零重复、OSS 零变化、三落库点自然覆盖 |
| 2 | 告警持久化新表 | 重启不丢；「查看告警」语义完整 |
| 3 | 全库滚动扫描而非增量游标 | 语义简单；告警 upsert 幂等去重 |
| 4 | 校验时间戳记于告警表 | 免给 audit_logs 加列 |
| 5 | 清理任务仅 enterprise 启动 | PRD 留存策略属 3.5 Enterprise 功能；OSS 不动用户数据 |
