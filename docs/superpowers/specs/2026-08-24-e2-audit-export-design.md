# Enterprise E2 审计日志外推(audit_stream)— 设计文档

> **日期**: 2026-08-24
> **目标**: 实现 Enterprise 版全量审计日志外推——SIEM(HTTP JSON) 与 Syslog(RFC5424) 双目标、存储尾随轮询、失败重试有界缓冲、LicenseGate 门控(E2 为首个消费者)
> **前置**: E1 授权管理已完成(`license.FeatureAuditStream` 常量、LicenseGate 门控、NopGate 兜底);`plugin.LogExporter` 接口与 `config.ExportConfig` 已就位但无实现、无人调用
> **版本**: V1.0

---

## 1. 背景与现状

- PRD 3.8 合规运维要求 SIEM/Syslog/Kafka 三种外推;本期交付前两种,Kafka 因第三方客户端依赖过重留后续。
- `plugin.LogExporter{Init,Export,BatchExport,TestConnection,Close}` 接口已定义;两个工厂 `CreateExporter()` 均返回 nil,main.go 未调用。
- `config.ExportConfig{Type,Endpoint,APIKey,BatchSize,FlushInterval}` 已存在(config.yaml 有 export 段),缺 PRD 要求的 `enabled` 开关。
- 审计日志由 `SimpleAuditor` 在 Finalize/MarkDisconnect 时经 `SaveAuditLog` 落库——外推以此为数据源。
- E1 约定:`gate` 由首个企业功能消费者引入接线,即本项目。

## 2. 目标 / 非目标

**目标**

1. 企业版在授权含 `audit_stream` 功能且配置启用时,将运行期新增审计日志准实时外推到 SIEM 或 Syslog。
2. 失败可靠:指数退避重试 + 有界缓冲,不阻塞代理主流程。
3. OSS 版行为零变化(CreateExporter 仍 nil)。

**非目标(YAGNI)**

- Kafka 外推(重依赖 sarama/franz-go,后续迭代)
- 历史日志回填(只推进程运行期新增)
- TLS/mTLS(沿用内网假设)、压缩
- webui 外推配置页、外推管理 API、动态改配置(配置走 config.yaml 重启生效)
- /api/system 的 exporter 状态字段

## 3. 关键决策(已与用户确认)

| # | 决策 | 理由 |
|---|------|------|
| 1 | **范围 = SIEM + Syslog** | 两者零外部依赖(标准库 net/http + net);Kafka 需重客户端库,违背零依赖惯例 |
| 2 | **存储尾随轮询取数** | 零侵入共享审计路径,新代码全部落在 enterprise 包;批量节奏与 batch_size/flush_interval 天然对齐;代价是约一个 flush 周期的延迟(默认 10s,符合工程预期) |
| 3 | **失败 = 重试 + 有界缓冲** | 指数退避 1s→30s 封顶;缓冲 10000 条满则丢最旧 + warn;不丢新数据、不阻塞主流程 |
| 4 | **门控点 = 启动条件** | license 有效 && HasFeature(audit_stream) && export.enabled 三者齐备才启动外推;降级态启动的进程不外推 |
| 5 | **LogExporter 接口不改** | Init(map) 即启动后台循环,Close 停止;与 storage/rateLimiter 的 Init 携带配置模式一致 |

## 4. 架构

### 4.1 组件与文件

```
新增(pkg/plugin/enterprise/,//go:build enterprise):
  export_tail.go      # TailExporter:时间游标拉取循环 + 有界重试缓冲,实现 plugin.LogExporter
  export_target.go    # ExportTarget 接口 + SIEMTarget / SyslogTarget
  export_tail_test.go
  export_target_test.go
修改:
  pkg/config/config.go     # ExportConfig 增加 Enabled bool(yaml: enabled,bool 不参与 applyDefaults)
  cmd/gateway/main.go      # 创建 exporter → 门控判断 → Init 启动;shutdown 序列插入 exporter 收尾
  config.yaml              # export 段注释完善(enabled 默认 false)
```

### 4.2 数据流

```
TailExporter 后台 goroutine(每 flush_interval tick):
  QueryAuditLogs({StartTime: cursor}, 升序分页) 拉新增审计日志
    → 批量(≤batch_size)交给 target.Send(logs)
        → 成功: 游标推进到最后一条的 CreatedAt
        → 失败: 整批入缓冲,进入退避重试(独立于拉取循环继续拉取入缓冲)
Close():
  停止 ticker → 最终一轮拉取(auditor.Shutdown 落库的尾部日志) → 缓冲尽力清空 → target.Close()
```

### 4.3 ExportTarget 接口

```go
// Send 批量推送;返回 error 表示整批失败需重试
type ExportTarget interface {
    Send(logs []*plugin.AuditLog) error
    TestConnection() error
    Close() error
}
```

- **SIEMTarget**: HTTP POST endpoint,body = 全量 AuditLog 数组 JSON;Header `Authorization: Bearer <api_key>`(api_key 非空时)与 `Content-Type: application/json`;2xx 视为成功。
- **SyslogTarget**: endpoint 支持 `udp://host:port` / `tcp://host:port`(缺省 udp);RFC5424 报文,TCP 用 RFC6587 octet-count 分帧;每条一报文,PRI=facility(user 1)×8+severity,severity 按 ResponseStatus≥500 或 Disconnected 取 err(3),否则 info(6);APP-NAME=NeuralGate,MSGID=RequestID,STRUCTURED-DATA=`-`,MSG=单条 AuditLog JSON。

### 4.4 TailExporter 细节

- **游标**: 启动时 = 进程当前时刻(只推运行期新增);推进 = 本批最后一条 CreatedAt;查询窗口向前多取 1 秒重叠,配合「当前游标边界 ID 集合」去重防同秒漏推/重推。拉取按 CreatedAt 升序分页(现有存储若未显式排序则补齐排序),单页上限 1000,读到不足一页为止。
- **缓冲**: 有界环形 10000 条;push 满则丢最旧并记 warn;重试成功后按批弹出。
- **退避**: 失败后下次推送延迟 min(30s, 1s×2^连续失败次数),成功归零;拉取循环不受影响持续入缓冲。
- **LogExporter 适配**: `Init(map)` 解析 type/endpoint/api_key/batch_size/flush_interval 并构造对应 target、启动循环;`Export/BatchExport` 直接入缓冲(兼容接口,主路径不走);`TestConnection` 透传;`Close` 按 4.2 序列收尾。

## 5. 配置

```yaml
export:                       # Enterprise only：audit_stream 门控的外推配置
  enabled: false              # 是否启用外推(默认 false)
  type: siem                  # siem/syslog
  endpoint: "https://siem.example.com/api"   # syslog 形如 udp://10.0.0.1:514 或 tcp://...
  api_key: ""                 # SIEM 认证密钥(syslog 忽略)
  batch_size: 50              # 批量大小 1-1000
  flush_interval: 10s         # 拉取/推送节奏 1-60s
```

- `ExportConfig` 增加 `Enabled bool`;bool 字段不参与 applyDefaults(既有约定)。
- main.go 组装 Init 参数来自 cfg.Export。

## 6. main.go 接线与 shutdown 序列

```
启动: exporter := factory.CreateExporter()      // oss→nil;enterprise→未初始化的 TailExporter
      if exporter != nil && cfg.Export.Enabled && gate.HasFeature(license.FeatureAuditStream):
          exporter.Init(map...)                  // 成功即开始外推;失败仅 Warn 不中断
      三条件缺一: 记 Info 日志说明原因(未授权/未启用)

关闭(固定顺序): proxy/admin Shutdown → auditor.Shutdown(落库全部 pending)
      → exporter.Close()(停止循环 → 最终一轮拉取兜住 auditor 尾部落库 → 缓冲尽力清空)
      → storage.Close()
```

## 7. 测试策略(TDD,go test -race,双编译矩阵)

| 单元 | 测试点 |
|------|--------|
| SIEMTarget | httptest 服务端断言:JSON 数组完整字段、Bearer 头、2xx 成功;非 2xx 返回 error |
| SyslogTarget | 本地 UDP/TCP listener 断言 RFC5424 结构(PRI/APP-NAME/MSGID/MSG=JSON)、TCP octet-count 分帧、severity 映射 |
| TailExporter 拉取 | 内存存储预插日志 → 短 interval → 目标收到且游标推进;重复 tick 无重推 |
| 重试 | 目标先 500 后恢复 → 退避后补投成功;连续失败封顶 30s |
| 缓冲 | 超 10000 条丢最旧且告警 |
| Close 收尾 | auditor.Shutdown 新落库的日志被最终拉取推出 |
| 门控决策 | 条件判定函数:缺 feature/enabled/validator 各分支不启动 |
| 编译矩阵 | go build/test -tags oss 与 -tags enterprise 全绿;oss 行为零变化 |

## 8. 决策记录

| # | 决策 | 理由 |
|---|------|------|
| 1 | Kafka 延后 | 第三方依赖重,测试需集群 |
| 2 | 尾随轮询而非审计器回调 | 不改共享 OSS 代码;延迟可接受 |
| 3 | 重试+有界缓冲(丢最旧) | 可用性优先,合规场景接受极小概率丢弃但保新数据 |
| 4 | 接口不变,Init 即启动 | 遵循现有工厂模式,避免接口膨胀 |
| 5 | 只推运行期日志 | 回填语义复杂度高,YAGNI |
