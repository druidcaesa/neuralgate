# 生产化第一批修复完成记录（2026-08-25）

## 背景

全仓生产就绪审计发现 2 个 P0、6 个 P1 阻断项。本批修复上线阻断项，为后续商业版（E4-E8）开发铺底。规格来源：审计报告会话记录；实施按 TDD 逐项推进。

## 交付内容

### P0-① 管理面认证（数据库账号方案）

- `pkg/admin/auth.go`（新增）：`SessionManager`（HMAC-SHA256 会话，进程级密钥，重启失效）、`RequireAuth` 中间件（X-Admin-Token / Bearer 兼容）、登录防爆破（5 次失败锁 1 分钟）、CORS 白名单化、`EnsureBootstrapAdmin` 引导
- 认证默认开启（fail-closed）；`DisableAuth` 仅供既有处理器级测试
- token 载荷绑定口令哈希摘要：改密/禁用账号后旧 token 立即失效
- 存储：`plugin.AdminUser` 类型 + `admin_users` 表（mem/sqlite/mysql 三实现）+ `StoragePlugin` 4 方法
- bootstrap：无账号时创建 admin——密码取 `admin.bootstrap_password`，缺省随机生成打印一次
- webui：Login 页、路由守卫、axios 拦截器（token 注入 + 401 回登录页）、改密对话框、登出

### P0-② 企业版接入持久化

- `oss.NewDynamicStorage()` 导出；enterprise 工厂 `CreateStorage` 改返回之，`cfg.Storage.Driver/DSN` 在企业版生效（原硬编码 MemStorage）

### P1 项

- 审计列容量：MySQL `audit_logs` 四列 TEXT→MEDIUMTEXT；`ensureAuditColumnSizes` 按 information_schema 增量迁移存量库（仅 text 列 ALTER）
- 落库可见化：`ProxyCore.WithLogger` + `DisconnectHandler.WithLogger`；6 处被吞的审计错误改为 Warn/Error 日志
- 流式写超时治理：proxy server 层 WriteTimeout 置 0；非流式按 max(30s, 上游超时+10s) 设写截止；流式分片间滚动 60s 截止（活跃流不限总时长）
- SQLite 并发：DSN 注入 `_pragma=busy_timeout(5000)&journal_mode(WAL)&synchronous(NORMAL)`
- 后台任务 panic 兜底：`runWithRecover` 接入 Tasks.cycleLoop 与 TailExporter.run/Close

### 配置与文档

- config 新增 `admin:` 段（bootstrap_password / allowed_origins），config.yaml 与 README 同步
- README 新增「管理后台认证」章节（curl 示例 + 行为说明）

## 测试矩阵

- oss：196 passed（原 177，新增存储 CRUD×3、PRAGMA、认证 15 例等）
- enterprise：231 passed（原 209，新增 panic 恢复等）
- vet 双 tag 干净；enterprise 编译通过；vue-tsc 通过；webui build 产物已更新
- 冒烟实测（真实二进制）：无 token 401 → 登录发 token → CRUD 通 → 改密后旧密码/旧 token 失效 → 重启后引导跳过且新密码可登录

## 已知边界（后续批次）

- 会话密钥进程级：多副本部署需粘滞或共享会话（E5/RBAC 一并考虑）
- 上游请求未绑 r.Context()（断连不取消上游）、零 metrics、core 访问日志缺失——第二批
- encrypt_key 默认值仍存在（第二批强制显式配置）
