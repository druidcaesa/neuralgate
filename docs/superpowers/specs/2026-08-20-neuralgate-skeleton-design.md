# NeuralGate 基础架构搭建 — 设计规格

> **日期**: 2026-08-20
> **状态**: 已批准
> **依据**: `../../NeuralGate_技术架构详细设计.md`（V1.0）
> **范围**: Phase 1-2 + 四层内核框架（骨架 + 分层框架），业务逻辑后置

---

## 1. 目标与范围

### 1.1 目标

根据 `../../NeuralGate_技术架构详细设计.md` 搭建项目基础架构，产出**可编译、可启动、契约完整**的骨架工程，后续业务开发（Phase 3-10）在骨架上填充实现，无需重构。

### 1.2 范围（本次包含）

| 项 | 内容 |
|----|------|
| 项目初始化 | go.mod（模块名 `github.com/druidcaesa/neuralgate`）、目录结构、git init、Makefile、.gitignore、推送脚本 |
| 契约完整 | 文档第 3/4/8 节全部数据结构与接口定义，原样照搬（RequestContext、ModelConfig、APIKey、AuditLog、Plugin 全部接口、ModelAdapter 接口等） |
| 分层框架 | 四层（Acceptor / Pipeline / ProxyCore / Plugin）类结构与方法签名立起，管道链组装完整实现，业务方法 stub |
| 双服务 | 代理服务(:8080, net/http) + 管理后台(:8081, Gin)，照文档 10.1 启动流程与优雅关闭 |
| 配置加载 | config.yaml 解析 + 默认值（完整实现） |
| 可验证 | `-tags oss` / `-tags enterprise` 双编译通过，启动后健康检查与占位响应符合 OpenAI 错误格式 |

### 1.3 非目标（本次明确排除，后续 Phase 填充）

- Phase 3：MySQL/SQLite 真实存储、持久化审计、真实限流
- Phase 4：鉴权/限流/路由的真实校验逻辑、SSE 劫持细节、上游转发
- Phase 5：适配器的请求/响应转换实现
- Phase 6：管理后台 CRUD 业务
- Phase 7：Enterprise 插件（达梦/金仓/Redis/SIEM/PII/授权）
- Docker 化、CI、性能压测

---

## 2. 环境约束

| 项 | 值 |
|----|----|
| Go | 1.26.5（满足文档要求 1.22+） |
| 模块名 | `github.com/druidcaesa/neuralgate` |
| 依赖 | 仅 4 个：`gin-gonic/gin`、`gopkg.in/yaml.v3`、`go.uber.org/zap`、`google/uuid` |
| 不引入 | mysql / sqlite / redis 驱动（Phase 3/7 再加） |

---

## 3. 目录结构与文件清单

```
neuralgate/
├── cmd/gateway/main.go          # 入口：启动双服务 + 优雅关闭
├── pkg/core/                    # 内核四层
│   ├── context.go               # RequestContext（照文档 3.1，契约完整）
│   ├── version.go               # VersionInfo（照文档 7.3）+ 编译期注入变量
│   ├── acceptor.go              # ConnectionManager/TLSHandler/IPFilter/ProtocolParser 结构体+方法签名
│   ├── pipeline.go              # Middleware 类型 + Pipeline 链组装（框架逻辑，完整实现）
│   ├── middleware_auth.go       # 鉴权中间件壳（提取 Key→写上下文→放行）
│   ├── middleware_limit.go      # 限流中间件壳
│   ├── middleware_route.go      # 路由中间件壳
│   ├── proxy.go                 # ProxyCore：ServeHTTP 接管 /v1/*，骨架期返回占位错误
│   ├── sse_writer.go            # SSEResponseWriter（照文档 5.2 结构体，Write 最简）
│   ├── sse_reassembler.go       # StreamReassembler 结构体+stub
│   └── disconnect_handler.go    # 断连检测 stub
├── pkg/adapter/
│   ├── interface.go             # ModelAdapter 完整接口（照文档 8.3，含 SupportsNativeProxy）
│   ├── registry.go              # AdapterRegistry（并发安全 map，完整实现）
│   ├── openai.go                # OpenAI adapter 壳（Name() 真实返回，转换方法 stub）
│   ├── tongyi.go                # 同上
│   ├── zhipu.go                 # 同上
│   └── deepseek.go              # 同上
├── pkg/plugin/
│   ├── interface.go             # 全量接口 + 数据结构（照文档第 3/4 节，契约完整）
│   ├── factory.go               # OSS 工厂（//go:build oss）
│   ├── factory_enterprise.go    # Enterprise 工厂（//go:build enterprise）
│   ├── oss/                     # 共享实现（无 BuildTag，两版本都编译）
│   │   ├── storage_mem.go       # 内存存储 mock（骨架期替代 MySQL/SQLite）
│   │   ├── audit_simple.go      # 简单同步审计（最简实现）
│   │   ├── limit_mem.go         # 内存限流（最简实现）
│   │   └── ring_buffer.go       # 环形队列（基础数据结构，完整实现）
├── pkg/admin/
│   ├── server.go                # Gin 服务初始化
│   ├── router.go                # 健康检查 + 空路由组占位（CRUD 是 Phase 6）
│   └── middleware.go            # CORS 等中间件壳
├── pkg/config/
│   └── config.go                # config.yaml 解析 + 默认值（完整实现）
├── config.yaml                  # 照文档 10.3，storage.driver 默认 mem
├── Makefile                     # build-oss / build-enterprise / run / test
├── push-private.sh              # 全量推送私有仓库（地址占位）
├── push-github-oss.sh           # 过滤 enterprise 推 GitHub（地址占位）
├── .gitignore                   # 照文档 12.3
└── README.md
```

---

## 4. 关键设计决策

### 4.1 内存存储 mock（`oss/storage_mem.go`）

- 实现 `StoragePlugin` 接口，方法签名完整，内部为内存 map + mutex
- `config.yaml` 的 `storage.driver` 增加 `mem` 值并设为默认；mysql/sqlite 配置保留在注释中
- 工厂按 driver 分发逻辑从第一天立起，Phase 3 只添加 `case "mysql"` / `case "sqlite"`
- 理由：文档启动流程（10.1 步骤 3-4）依赖存储连接与模型加载，内存实现让双服务真实可启动，无需外部依赖

### 4.2 BuildTag 与工厂

| 文件 | BuildTag | 说明 |
|------|----------|------|
| `pkg/plugin/interface.go` | 无 | 全版本编译 |
| `pkg/plugin/factory.go` | `//go:build oss` | OSS 工厂：CreateStorage→mem、CreateAuditor→simple、CreateRateLimiter→mem、Exporter/LicenseValidator→nil |
| `pkg/plugin/factory_enterprise.go` | `//go:build enterprise` | 骨架期与 OSS 工厂相同（复用 OSS 实现，Exporter/LicenseValidator 返回 nil） |
| `pkg/plugin/oss/*.go` | 无 | 两版本都编译 |

- `enterprise/` 目录 Phase 7 创建，骨架阶段不建占位文件
- 验证命令：`go build -tags oss -o neuralgate ./cmd/gateway/`、`go build -tags enterprise -o neuralgate-enterprise ./cmd/gateway/`

### 4.3 中间件壳策略

- `Middleware func(next http.Handler) http.Handler` 类型 + Pipeline 链组装**完整实现**（照文档 2.2 固定顺序：Auth→RateLimit→RouteMatch→PreHook）
- 中间件内部逻辑最简：auth 只从 Header 提取 API Key 写入 RequestContext 后放行（不做存储校验）；limit/route 直接放行
- 具体校验逻辑属业务，Phase 4 填充

### 4.4 骨架期 /v1/* 行为

- ProxyCore 的 `ServeHTTP` 接管 `/v1/*`，返回 OpenAI 格式占位错误：
  `{"error":{"message":"service not initialized","type":"api_error","code":"service_unavailable"}}`，HTTP 503
- 代理服务 `GET /healthz` → 200；管理后台 `GET /healthz` → 200
- 保持文档 8.7 错误格式契约可见可验证

### 4.5 适配器壳

- 4 个适配器注册进 `AdapterRegistry`（并发安全 map，完整实现）
- `Name()` 返回真实名称（openai/tongyi/zhipu/deepseek）；`SupportsNativeProxy()` 返回 true（OpenAI/DeepSeek）或 false（通义/智谱）
- 转换方法返回 `not implemented` 错误 stub

### 4.6 git 与工程化

- `git init` 本地仓库；双远端地址未提供，推送脚本中地址为占位符，待用户提供后配置 `git remote`
- .gitignore 照文档 12.3（*.lic、编译产物、config.local.yaml）
- Makefile 目标：`build-oss`、`build-enterprise`、`run`、`test`

---

## 5. 启动流程（骨架期）

照文档 10.1：

1. 解析命令行参数（-config 路径、-version）
2. 加载 config.yaml → Config 结构体
3. 初始化插件工厂 → CreateStorage（mem）→ CreateAuditor（simple）→ CreateRateLimiter（mem）
4. 从存储加载模型配置到内存路由表（mem 存储返回空表）
5. 初始化 AdapterRegistry，注册 4 个适配器
6. 初始化内核：NewPipeline → NewProxyCore → NewAcceptor
7. 初始化管理后台 NewAdminServer（健康检查 + 空路由组）
8. 并发启动双服务（:8080 / :8081）
9. 信号监听 SIGINT/SIGTERM
10. 优雅关闭：双服务 Shutdown → 审计管道 Flush → 存储/限流器 Close

---

## 6. 验证标准

| 检查项 | 命令/操作 | 预期 |
|--------|-----------|------|
| OSS 编译 | `make build-oss` | 成功，产物 neuralgate |
| Enterprise 编译 | `make build-enterprise` | 成功，产物 neuralgate-enterprise |
| 静态检查 | `go vet ./...` | 无输出 |
| 启动 | `./neuralgate -config config.yaml` | 双服务启动日志正常，版本信息打印 |
| 代理健康检查 | `curl :8080/healthz` | 200 |
| 代理占位响应 | `curl :8080/v1/models` | 503 + OpenAI 格式错误 JSON |
| 后台健康检查 | `curl :8081/healthz` | 200 |
| 优雅关闭 | 发送 SIGTERM | 日志显示有序 Shutdown，进程退出码 0 |
