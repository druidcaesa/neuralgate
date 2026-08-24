# Enterprise E3 审计防篡改存证 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现 PRD 3.5 防篡改五项——SHA256 存证计算(enterprise 独占)、定期哈希校验、篡改告警持久化与后台展示、留存清理、MySQL 权限隔离 SQL 模板。

**Architecture:** SimpleAuditor 加可选指纹钩子(enterprise 注入,OSS nil 零变化)；Verifier 全库滚动扫描比对，不一致写 `audit_tamper_alerts`(upsert 去重)；Retainer 每小时按 retention_days 清理；指纹为除指纹字段外全内容的长度前缀序列化+摘要 hex，算法经注册表参数化(本期仅 sha256，SM3 留 E8)。

**Tech Stack:** Go 标准库 crypto/sha256（零新增依赖）；既有 gin/webui。

## Global Constraints

- 所有 `.go` 文件带 Apache-2.0 许可证头（Copyright 2026 FanYaNan）
- 注释只写方案，不引用阶段编号/计划文档
- module `github.com/druidcaesa/neuralgate`（go 1.26.5），零新增依赖（ID 生成用已有 google/uuid）
- 双编译矩阵全绿：`go build/test -race -tags oss ./...` 与 `-tags enterprise ./...`
- OSS 行为零变化：不注入钩子则指纹恒空、不启动清理
- 中文注释/日志/错误消息；commit 格式 `feat(scope): 中文描述`
- 指纹算法选定后不可更换（换则历史指纹失配误报）

---

### Task 1: 存储接口扩展 + 内存实现

**Files:**
- Modify: `pkg/plugin/interface.go`（StoragePlugin 三方法+SetTamperAlertResolved；新结构 TamperAlert；新类型 FingerprintFunc/FingerprintHook）
- Modify: `pkg/plugin/oss/storage_mem.go`（tamperAlerts map + 四方法）
- Test: `pkg/plugin/oss/storage_mem_test.go`（追加）

**Interfaces:**
- Produces:
  ```go
  type TamperAlert struct{ ID, AuditLogID, Reason string; Resolved bool; FirstSeenAt, LastCheckedAt time.Time }
  type FingerprintFunc func(log *AuditLog) string
  type FingerprintHook interface{ SetFingerprintFunc(fn FingerprintFunc) }
  // StoragePlugin 新增：
  DeleteAuditLogsBefore(cutoff time.Time) (int64, error)
  SaveTamperAlerts(alerts []*TamperAlert) error   // 同一 AuditLogID 未处置→更新 LastCheckedAt/Reason；否则插入(生成 ID)
  ListTamperAlerts(resolved *bool, page, size int) ([]*TamperAlert, int64, error)
  SetTamperAlertResolved(id string, resolved bool) error
  ```

- [ ] **Step 1: 写失败测试**（storage_mem_test.go 追加；构造 helper `alert(id string) *plugin.TamperAlert` 返回 AuditLogID=id 的未处置告警）：

```go
func TestMemSaveListTamperAlerts(t *testing.T) {
	s := NewMemStorage()
	if err := s.SaveTamperAlerts([]*plugin.TamperAlert{alert("req-1")}); err != nil {
		t.Fatal(err)
	}
	// upsert：同 AuditLogID 再存更新而非新增
	if err := s.SaveTamperAlerts([]*plugin.TamperAlert{alert("req-1")}); err != nil {
		t.Fatal(err)
	}
	all, total, err := s.ListTamperAlerts(nil, 1, 10)
	if err != nil || total != 1 || len(all) != 1 {
		t.Fatalf("upsert 后应仍 1 条: total=%d err=%v", total, err)
	}
	if all[0].FirstSeenAt.After(all[0].LastCheckedAt) {
		t.Error("LastCheckedAt 应不早于 FirstSeenAt")
	}
	// resolved 过滤
	no := false
	if n, _ := s.ListTamperAlerts(&no, 1, 10); len(n) != 0 && total == 1 {
		t.Errorf("resolved=false 过滤应为空")
	}
}

func TestMemResolveAndDeleteBefore(t *testing.T) {
	s := NewMemStorage()
	_ = s.SaveAuditLog(&plugin.AuditLog{ID: "old", RequestID: "old", CreatedAt: time.Now().Add(-48 * time.Hour)})
	_ = s.SaveAuditLog(&plugin.AuditLog{ID: "new", RequestID: "new", CreatedAt: time.Now()})
	n, err := s.DeleteAuditLogsBefore(time.Now().Add(-24 * time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("应删 1 条: n=%d err=%v", n, err)
	}
	if _, err := s.GetAuditLogByID("old"); err == nil { // GetAuditLogByID 若无此方法用 QueryAuditLogs(RequestID)
		t.Error("过期日志应已删除")
	}
}
```

（执行时以存储现有查询方法为准调整断言 API。）

- [ ] **Step 2:** `go test ./pkg/plugin/oss/ -run 'TestMemSaveList|TestMemResolve'` → FAIL（方法未定义）
- [ ] **Step 3: 实现** interface.go 与 storage_mem.go 按上述签名；mem 用 `uuid.NewString()` 生成告警 ID，`NewMemStorage` 初始化 `tamperAlerts: make(map[string]*plugin.TamperAlert)`；upsert/List/Delete 均在 `s.mu.Lock()` 内完成。
- [ ] **Step 4:** 测试 PASS + `go build ./...`
- [ ] **Step 5:** Commit `feat(plugin): 存储接口扩展篡改告警与留存删除(mem)`

---

### Task 2: sqlite/mysql 实现与建表

**Files:**
- Modify: `pkg/plugin/oss/storage_sqlite.go`（ensure 加 audit_tamper_alerts 表 + 四方法）
- Modify: `pkg/plugin/oss/storage_mysql.go`（同上，MySQL 方言）
- Test: `pkg/plugin/oss/storage_sqlite_test.go` / mysql 测试沿用 NG_MYSQL_DSN 跳过模式

建表语句：

```sql
CREATE TABLE IF NOT EXISTS audit_tamper_alerts (
  id             VARCHAR(36) PRIMARY KEY,
  audit_log_id   VARCHAR(64) NOT NULL,
  reason         TEXT NOT NULL,
  resolved       INTEGER NOT NULL DEFAULT 0,   -- mysql: TINYINT NOT NULL DEFAULT 0
  first_seen_at  BIGINT NOT NULL,
  last_checked_at BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_tamper_alerts_log ON audit_tamper_alerts(audit_log_id); -- mysql 无 IF NOT EXISTS,判 information_schema 或容忍重复错误
```

四方法语义与 mem 一致：upsert 按 `audit_log_id WHERE resolved=0` 查存在性；时间戳沿用 `timeToMS` 毫秒惯例（与 audit_logs.created_at 一致）。sqlite 测试断言 upsert/resolved 过滤/resolve 更新/delete 计数；mysql 用例复制 sqlite 断言并挂 `NG_MYSQL_DSN` skip 守卫。

Steps: 失败测试 → FAIL → 实现 → PASS(sqlite + mysql 可用时) → Commit `feat(plugin): 篡改告警与留存删除落库(sqlite/mysql)`

---

### Task 3: SimpleAuditor 指纹钩子

**Files:**
- Modify: `pkg/plugin/oss/audit_simple.go`
- Test: `pkg/plugin/oss/audit_simple_test.go`（追加）

**Interfaces:**
- Consumes: Task 1 `plugin.FingerprintHook/FingerprintFunc`
- Produces: `func (a *SimpleAuditor) SetFingerprintFunc(fn plugin.FingerprintFunc)`——fn 非 nil 时三处 `SaveAuditLog` 前置 `log.SHA256Fingerprint = fn(log)`

测试要点：
```go
// fn 不注入:Finalize 落库后指纹为空(OSS 行为零变化)
// 注入固定值函数后:Finalize / MarkDisconnect / Shutdown 三路径指纹均被填充
```
Steps: 失败测试 → FAIL → 实现(struct 加 `fingerprintFn plugin.FingerprintFunc`,三处落库前 `if a.fingerprintFn != nil`) → PASS → Commit `feat(oss): 审计器可选指纹钩子`

---

### Task 4: enterprise 指纹计算与算法注册表

**Files:**
- Create: `pkg/plugin/enterprise/tamper.go`
- Test: `pkg/plugin/enterprise/tamper_test.go`

**Interfaces:**
- Produces:
  ```go
  func Fingerprint(algo string, log *plugin.AuditLog) string // 注册表查实现;未知回退 sha256
  func fingerprintSHA256(log *plugin.AuditLog) string        // 长度前缀序列化+sha256 hex
  ```
- 序列化字段顺序（长度前缀 `%d:%s;`）：ID、RequestID、TenantID、APIKeyID、ModelName、Provider、RequestMethod、RequestPath、RequestHeaders(**key 字典序 k:v;\n 段**)、RequestBody、ResponseStatus、ResponseBody、SSEChunks(逐条 Index/EventType/Data/Timestamp RFC3339)、PromptTokens、CompletionTokens、TotalTokens、Duration、ClientIP、IsStream、Disconnected、DisconnectReason、CreatedAt UTC RFC3339

测试要点：确定性；任一内容字段变化指纹变；Headers 插入顺序不同指纹相同；不含指纹字段自身（log.SHA256Fingerprint 预置垃圾值不影响结果）。
Steps: 失败测试 → FAIL → 实现(bytes.Buffer 拼 `%d:%s;`，Headers 排序后拼子段，sha256.Sum256 hex) → PASS → Commit `feat(enterprise): 审计指纹确定性计算与算法注册表`

### Task 5: Verifier 校验任务 + Retainer 留存清理

**Files:**
- Create: `pkg/plugin/enterprise/tamper_verify.go`
- Create: `pkg/plugin/enterprise/tamper_retention.go`
- Test: `pkg/plugin/enterprise/tamper_verify_test.go`

**Interfaces:**
- Consumes: Task 1 存储方法；Task 4 `Fingerprint(algo, log)`
- Produces:
  ```go
  type Tasks struct{ /* verifier+retainer */ }
  func NewTasks(storage plugin.StoragePlugin, algo string, verifyInterval time.Duration, verifyBatchSize int, retention time.Duration, logger *zap.Logger) *Tasks
  func (t *Tasks) Start()          // 启动两个 goroutine
  func (t *Tasks) Stop()           // 停止并等待退出
  // 可测内部方法：
  func (t *Tasks) verifyOnce()     // 全库滚动扫描一轮(页大小=verifyBatchSize,DESC 翻页),比对→SaveTamperAlerts
  func (t *Tasks) retainOnce()     // DeleteAuditLogsBefore(now-retention)
  ```

verifyOnce 语义：`QueryAuditLogs({}, page, batchSize)` 从 page=1 起翻页至短页；每条 `Fingerprint(algo, l)` 与存储指纹比对（指纹为空的记录跳过——历史未存证数据不误报）；不一致收集为告警，本轮末尾一次性 `SaveTamperAlerts(upsert)`；Warn 日志。测试：篡改 RequestBody→verifyOnce 产生告警且二次扫描不重复插入；指纹空跳过。
Retainer：每小时 tick（测试直接调 retainOnce）删除计数 Info 日志。测试：预插新旧日志→retainOnce→旧删新留。
Steps: 失败测试 → FAIL → 实现(stopCh/doneCh + ticker goroutine) → PASS → Commit `feat(enterprise): 审计哈希定期校验与留存清理任务`

---

### Task 6: 配置字段 + main 接线 + SQL 模板

**Files:**
- Modify: `pkg/config/config.go`（AuditConfig += VerifyInterval/VerifyBatchSize/FingerprintAlgo + Default 回填 + apply）
- Modify: `cmd/gateway/main.go`（步骤9 shouldStartTamper 注入钩子+启动 Tasks；关闭序列 Tasks.Stop 插在 auditor.Shutdown 前；license import 已有）
- Modify: `config.yaml`（audit 段补三行注释）
- Create: `docs/sql/mysql_tamper_readonly.sql`
- Test: `cmd/gateway/main_test.go` 追加 shouldStartTamper 用例

```go
// shouldStartTamper 判断防篡改启动条件（配置启用 + 授权含 tamper_proof）；不满足给出原因
func shouldStartTamper(gate core.LicenseGate, enabled bool) (bool, string) {
	if !enabled {
		return false, "配置未启用(enable_sha256=false)"
	}
	if !gate.HasFeature(license.FeatureTamperProof) {
		return false, "授权未包含 tamper_proof 功能"
	}
	return true, ""
}
```

main 步骤 9（步骤 8 外推之后）：

```go
	// 9. 审计防篡改（tamper_proof 门控）
	if setter, can := auditor.(plugin.FingerprintHook); can {
		if start, reason := shouldStartTamper(gate, cfg.Audit.EnableSHA256); !start {
			logger.Info("审计防篡改未启用", zap.String("reason", reason))
		} else {
			setter.SetFingerprintFunc(func(log *plugin.AuditLog) string {
				return tamper.Fingerprint(cfg.Audit.FingerprintAlgo, log)
			})
			retention := time.Duration(cfg.Audit.RetentionDays) * 24 * time.Hour
			tamperTasks := tamper.NewTasks(storage, cfg.Audit.FingerprintAlgo,
				cfg.Audit.VerifyInterval, cfg.Audit.VerifyBatchSize, retention, logger)
			tamperTasks.Start()
			defer tamperTasks.Stop()
			logger.Info("审计防篡改已启用",
				zap.String("algo", cfg.Audit.FingerprintAlgo),
				zap.Duration("verify_interval", cfg.Audit.VerifyInterval))
		}
	}
```

config 默认：VerifyInterval=24h、VerifyBatchSize=1000、FingerprintAlgo="sha256"；apply 同结构体回填。SQL 模板含 CREATE USER/GRANT SELECT(两张审计表)/REVOKE 示例与 SQLite 替代说明注释。

Steps: 失败测试 → FAIL → 实现 → PASS+双标签构建 → Commit `feat(gateway): 防篡改门控接线与留存校验任务启动`

---

### Task 7: admin 告警 API

**Files:**
- Create: `pkg/admin/tamper.go`
- Modify: `pkg/admin/router.go`（注册 GET /api/tamper-alerts、PATCH /api/tamper-alerts/:id）
- Modify: `pkg/admin/system.go`（追加 `"tamper": {"unresolved_count": N}`——ListTamperAlerts(false指针,1,1).total）
- Test: `pkg/admin/tamper_test.go`

```go
// listTamperAlerts GET /api/tamper-alerts?resolved=&page=&size=
func (s *AdminServer) listTamperAlerts(c *gin.Context) {
	var resolvedPtr *bool
	switch c.Query("resolved") {
	case "true":
		v := true
		resolvedPtr = &v
	case "false":
		v := false
		resolvedPtr = &v
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	alerts, total, err := s.storage.ListTamperAlerts(resolvedPtr, page, size)
	if err != nil {
		Error(c, http.StatusInternalServerError, 500, err.Error())
		return
	}
	OK(c, gin.H{"items": alerts, "total": total, "page": page, "size": size})
}

// resolveTamperAlert PATCH /api/tamper-alerts/:id body {"resolved":true}
```

Steps: 失败测试(list 分页/resolved 过滤、resolve 更新、system 含 tamper 计数) → FAIL → 实现 → PASS → Commit `feat(admin): 篡改告警查询与处置 API`

---

### Task 8: webui 告警展示

**Files:**
- Modify: `webui/src/types/index.ts`（TamperAlertItem、SystemInfo.tamper）
- Create: `webui/src/api/tamper.ts`（listTamperAlerts/resolveTamperAlert）
- Create: `webui/src/views/TamperAlertList.vue`（表格：日志ID/原因/首次发现/最近检查/处置按钮；resolved 过滤切换）
- Modify: `webui/src/views/SystemInfo.vue`（unresolved_count>0 → 红色 el-alert「检测到 N 条审计篡改告警」链接到列表页）
- Modify: `webui/src/router.ts` + `App.vue`（路由 /tamper-alerts + 菜单「防篡改告警」，图标 Warning）

Steps: 实现类型/API/视图/路由 → `npx vue-tsc --noEmit` 通过 → `npm run build` → Commit `feat(webui): 防篡改告警列表与系统页红色提示`

---

### Task 9: 全量验证矩阵 + 端到端冒烟

- [ ] gofmt/vet 双标签干净；双标签构建通过；`go test -race` 双矩阵全绿（oss 基线 167+新增、enterprise 相应增长）
- [ ] 冒烟：真实密钥签发含 tamper_proof 的授权 → 企业版网关启动（enable_sha256:true）→ 发一条代理请求 → sqlite 库中该记录 sha256_fingerprint 非空且可重算一致 → 手工 UPDATE 篡改 RequestBody（sqlite3 CLI）→ 将 verify_interval 调成 2s 重启 → 日志出现篡改告警 → `/api/tamper-alerts` 可见 → webui SystemInfo 红色提示 → PATCH 处置后计数归零
- [ ] 反向：授权仅含 audit_stream → 日志「审计防篡改未启用 reason=授权未包含 tamper_proof 功能」且新记录指纹为空
- [ ] 收尾提交与计划转完成记录

## Self-Review 结论

- 规格覆盖：§4.1 文件清单逐项对应 T1-T8；§4.4 数据流落 T5；§4.5 门控/关闭序列落 T6；§4.6 后台落 T7/T8；§4.7 SQL 模板落 T6；PRD 6.5 四验收项分别由 T3/T6(SQL)/T5+T8/T5 覆盖
- 类型一致性：TamperAlert 字段、四方法签名、FingerprintHook 在 T1 定义后各任务引用一致
- 无占位符；执行时以存储现有查询 API 为准微调断言方式已在 T1 标注


## 实施结果（2026-08-24 完成）

| 提交 | 内容 |
|------|------|
| 1fae9df | 存储接口扩展篡改告警与留存删除(mem/SQLStorage/dynamic 三实现+建表) |
| 77427f0 | SimpleAuditor 可选指纹钩子(三落库路径,nil 零变化) |
| e027ac1 | 指纹确定性计算与算法注册表(sm3 接缝) |
| 7b043f0 | Verifier 校验+Retainer 留存任务(upsert 去重/Stop 幂等) |
| a2a9442 | shouldStartTamper 门控接线(setupTamper 按 BuildTag 两版)+SQL 只读账号模板 |
| 9492019 | admin 告警查询/处置 API + system 未处置计数(TamperAlert 补 json 标签) |
| b0678d6 | webui 告警列表页+SystemInfo 红色横幅+路由菜单(dist 已更新) |

**验证矩阵**：gofmt/vet 双标签干净；`go test -race` oss 177 / enterprise 209 全绿。

**端到端冒烟证据**：
- 企业版启动含 tamper_proof 授权+enable_sha256 → 日志「审计防篡改已启用 algo=sha256」，`/api/system` 返回 `tamper.unresolved_count`
- 触发代理请求后经 `/api/audit-logs` 实测记录携带 64 位指纹(fp_len=64)
- 反向：授权仅含 audit_stream → 「审计防篡改未启用 reason=授权未包含 tamper_proof 功能」
- 篡改检测→告警 upsert 去重→处置 API 全链路由单测对真实存储覆盖(TestVerifyOnceDetectsTamperingAndDedups 等)
- 备注：本机沙箱对后台子进程新建数据库文件存在跨调用可见性干扰，sqlite 文件级手工篡改演示不可复现；不影响功能正确性(探针程序直连 SQLStorage 写读正常,94KB 六表齐全)

**执行期修正**：main.go 共享代码不直接 import enterprise 包——setupTamper 按 BuildTag 拆两版(factory_oss 空实现/factory_enterprise 实装)，与既有 factory 模式一致。
