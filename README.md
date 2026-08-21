# NeuralGate AI

<div align="center">

**统一大模型入口 · 生产级流式审计 · 国产环境全兼容**

Go 自研企业级 AI 大模型治理网关

**[English](./README.en-US.md)** | **[中文](./README.md)**

</div>

---

## 项目简介

NeuralGate 是一个轻量、高性能、可商业化的私有化 AI 模型网关，解决国内政企、集成商私有化大模型落地的**合规与兼容**痛点。区别于通用开源网关，主打 **国内生产级审计合规 + 信创软硬件兼容**。

### 解决什么问题

| 痛点 | 现有方案缺陷 | NeuralGate 解决方案 |
|------|-------------|-------------------|
| SSE 流式日志丢失 | 开源网关丢片段、断连无日志 | 环形队列异步审计 + 断连自动补全 |
| 无国产数据库适配 | 无法落地信创项目 | 达梦/人大金仓完整驱动适配 |
| 日志可篡改 | 不满足等保要求 | SHA256 存证 + 审计库独立权限 |
| 无多租户隔离 | 企业级需求无法满足 | RBAC 多租户 + 全量操作审计 |

### 核心特性

- **OpenAI SDK 零改动兼容** — 用户只需将 `OPENAI_BASE_URL` 指向网关地址，原有代码零修改即可使用
- **多模型统一接入** — 支持 OpenAI、通义千问、智谱、DeepSeek 等模型供应商，统一 API Key 管理
- **SSE 流式审计** — 环形队列异步落库，断连自动补全，分片不丢失
- **信创全兼容** — 麒麟/统信 OS + 飞腾/鲲鹏 CPU + 达梦/人大金仓数据库
- **单二进制部署** — 零外部依赖（除数据库外），支持 Docker/裸机/systemd
- **Open-Core 架构** — 开源内核 + 商业插件，一套源码两套产物

---

## 版本说明

NeuralGate 采用 Open-Core 模式，通过 Go BuildTag 条件编译，一套源码产出两个版本：

| 维度 | OSS（开源版） | Enterprise（商业版） |
|------|--------------|----------------------|
| 编译命令 | `go build -tags oss` | `go build -tags enterprise` |
| 二进制名称 | `neuralgate` | `neuralgate-enterprise` |
| 模型代理 | ✅ 全功能 | ✅ 全功能 |
| API Key 管理 | ✅ | ✅ |
| 限流管理 | ✅ 内存令牌桶 | ✅ Redis 分布式限流（可降级内存） |
| SSE 流式审计 | ✅ 同步元数据 | ✅ 异步流式 + SHA256 存证 |
| 日志防篡改 | — | ✅ SHA256 + 独立审计权限 |
| 隐私安全 | — | ✅ PII 动态脱敏 |
| 权限体系 | ✅ 超级管理员 | ✅ RBAC 多租户 |
| 合规运维 | — | ✅ SIEM/Syslog/Kafka 外推 |
| MCP 审计 | — | ✅ 工具调用全链路审计 |
| 授权管理 | — | ✅ 序列号 + 离线授权 |
| MySQL | ✅ | ✅ |
| SQLite | ✅ | ✅ |
| 达梦 | — | ✅ |
| 人大金仓 | — | ✅ |

> Enterprise 版包含 OSS 全部能力。OSS 版配置达梦/金仓驱动时启动会报错提示"需要 Enterprise 版本"。

---

## 架构设计

### 双服务隔离

单进程内运行两个完全隔离的 HTTP 服务：

| 服务 | 端口 | 框架 | 职责 |
|------|------|------|------|
| 代理服务 | 8080 | 纯 net/http | LLM 流量代理、SSE 流式劫持、反向代理 |
| 管理后台 | 8081 | Gin | CRUD 接口、配置管理、日志查询、授权校验 |

代理服务**禁止引入 Gin/Echo 等框架**，仅使用 `net/http` 原生能力，保证高并发长连接性能。

### 四层分层架构

```
┌─────────────────────────────────────────────────────────────┐
│                     接入层 (Acceptor)                        │
│   连接管理 · TLS终止 · IP黑白名单 · 协议解析 · 长连接超时适配     │
├─────────────────────────────────────────────────────────────┤
│                管道中间件层 (Pipeline)                        │
│   鉴权 → 限流 → 路由匹配 → 模型协议转换 → 前置钩子             │
├─────────────────────────────────────────────────────────────┤
│                 代理内核层 (Proxy Core)                      │
│   ReverseProxy · SSE分片劫持 · 流式重组 · 异常补偿 · 模型转发   │
├─────────────────────────────────────────────────────────────┤
│                插件扩展层 (Plugin Layer)                     │
│   存储插件 · 审计流水线 · 限流插件 · 日志导出 · 授权插件        │
└─────────────────────────────────────────────────────────────┘
```

### 设计原则

| 原则 | 说明 |
|------|------|
| 接口契约化 | 所有扩展点通过接口定义，内核零硬编码厂商逻辑 |
| 编译隔离 | BuildTag 条件编译，一套源码两套产物，禁止 go plugin (.so) |
| 异步非阻塞 | 审计链路通过内存环形队列 + 独立 worker 池，主流量零阻塞 |
| Open-Core | 开源内核 + 商业插件，enterprise 目录私有不公开 |
| 单二进制 | 零外部依赖，支持 Docker/裸机部署 |

---

## OpenAI API 兼容性

NeuralGate 入口协议与 OpenAI API 完全兼容，用户只需将 `OPENAI_BASE_URL` 指向网关地址即可。

### 支持的端点

| 端点 | 方法 | 路径 | 处理方式 |
|------|------|------|----------|
| Chat Completions | POST | `/v1/chat/completions` | 模型适配器转换/透传 |
| Completions (Legacy) | POST | `/v1/completions` | 透传到上游 |
| Embeddings | POST | `/v1/embeddings` | 透传到上游 |
| Models List | GET | `/v1/models` | 网关本地响应 |
| Models Retrieve | GET | `/v1/models/{model}` | 网关本地响应 |
| Moderations | POST | `/v1/moderations` | 透传到上游 |
| Images Generations | POST | `/v1/images/generations` | 透传到上游 |
| Images Edits | POST | `/v1/images/edits` | 透传到上游 |
| Audio Speech (TTS) | POST | `/v1/audio/speech` | 透传到上游 |
| Audio Transcriptions | POST | `/v1/audio/transcriptions` | 透传到上游 |
| Files | CRUD | `/v1/files` | 透传到上游 |

### 支持的请求参数

- **基础参数**：model, messages, temperature, top_p, max_tokens, stop, seed, n
- **Function Calling**：tools, tool_choice, parallel_tool_calls
- **结构化输出**：response_format (json_object / json_schema)
- **流式控制**：stream, stream_options.include_usage
- **采样控制**：frequency_penalty, presence_penalty, logit_bias, logprobs
- **多模态**：messages.content 支持 string 和 []ContentPart（text + image_url + input_audio）
- **Reasoning 模型**：reasoning_effort, verbosity

### 内置模型适配器

| 适配器 | 协议 | 流式 | 原生透传 | 版本 |
|--------|------|------|----------|------|
| OpenAI | OpenAI API | SSE | ✅ 原样透传 | OSS |
| 通义千问 | DashScope | SSE | ❌ 格式转换 | OSS |
| 智谱 | GLM API | SSE | ❌ 格式转换 | OSS |
| DeepSeek | OpenAI 兼容 | SSE | ✅ 仅替换 model 字段 | OSS |

> **原生透传** = 上游协议与入口协议一致，适配器跳过 TransformRequest，仅替换 model 字段后原样转发，零序列化损耗。

---

## 快速开始

### 环境要求

- Go 1.22+
- MySQL 5.7+ 或 SQLite 3.35+（Enterprise 额外支持达梦8/人大金仓V8）
- Docker 20.10+（可选）

### 编译

```bash
# OSS 版本
go build -tags oss -o neuralgate ./cmd/gateway/

# Enterprise 版本
go build -tags enterprise -o neuralgate-enterprise ./cmd/gateway/
```

### 配置

复制配置文件并修改：

```bash
cp config.yaml config.local.yaml
```

```yaml
server:
  proxy_addr: ":8080"
  admin_addr: ":8081"

storage:
  # 可选值: mysql(默认) / sqlite / dm(Enterprise) / kingbase(Enterprise)
  driver: mysql

  # ----- MySQL (默认，OSS+Enterprise 均可用) -----
  dsn: "user:pass@tcp(host:3306)/neuralgate?charset=utf8mb4"

  # ----- SQLite (轻量部署，OSS+Enterprise 均可用) -----
  # driver: sqlite
  # dsn: "/var/lib/neuralgate/neuralgate.db"

  # ----- 达梦数据库 (仅 Enterprise 编译生效) -----
  # driver: dm
  # dsn: "dm://user:pass@host:5236/NEURALGATE"

  # ----- 人大金仓 (仅 Enterprise 编译生效) -----
  # driver: kingbase
  # dsn: "kingbase://user:pass@host:54321/neuralgate"

  max_open_conns: 20
  max_idle_conns: 10

audit:
  queue_size: 65536
  worker_count: 4
  batch_size: 100
  flush_interval: 5s
  enable_sha256: true       # Enterprise
  retention_days: 90

rate_limit:
  strategy: token_bucket
  default_rps: 10
  default_tpm: 100000

log:
  level: info
  format: json
  output: stdout
```

> **模型配置不在 config.yaml 中**。模型通过管理后台页面（:8081）CRUD 管理，存储在数据库中，支持热更新——增删改模型后立即生效，无需重启。

### 启动

```bash
./neuralgate -config config.local.yaml
```

启动后：
- 代理服务监听 `:8080`，接收 OpenAI 协议请求
- 管理后台监听 `:8081`，提供模型配置、API Key 管理等管理功能
- 首次启动数据库为空，需通过管理后台添加首个模型配置

### Docker 部署

```bash
# 构建多架构镜像
docker build --platform linux/amd64,linux/arm64 -t neuralgate:oss --build-arg BUILD_TAGS=oss .
docker build --platform linux/amd64,linux/arm64 -t neuralgate:enterprise --build-arg BUILD_TAGS=enterprise .

# 运行
docker run -d -p 8080:8080 -p 8081:8081 -v ./config.yaml:/etc/neuralgate/config.yaml neuralgate:oss
```

---

## 使用示例

### 接入 OpenAI SDK（零改动）

```python
import openai

client = openai.OpenAI(
    api_key="ng-xxxxxxxxxxxx",       # NeuralGate 管理后台生成的 API Key
    base_url="http://localhost:8080/v1"  # 指向 NeuralGate 网关
)

response = client.chat.completions.create(
    model="gpt-4",    # 管理后台配置的模型名称
    messages=[{"role": "user", "content": "Hello!"}]
)
```

### curl 调用

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ng-xxxxxxxxxxxx" \
  -d '{
    "model": "gpt-4",
    "messages": [{"role": "user", "content": "Hello!"}],
    "stream": true
  }'
```

---

## 项目目录结构

```
neuralgate/
├── cmd/
│   └── gateway/
│       └── main.go                    # 入口，解析参数、初始化、启动双服务
├── pkg/
│   ├── core/                           # 代理内核
│   │   ├── acceptor.go                 # 接入层：连接管理、TLS、IP过滤
│   │   ├── pipeline.go                 # 管道中间件链
│   │   ├── proxy.go                    # 反向代理 + SSE劫持
│   │   └── router.go                   # 模型路由匹配
│   ├── adapter/                        # 模型适配器
│   │   ├── registry.go                # 适配器注册中心
│   │   ├── openai.go                   # OpenAI 适配器（原生透传）
│   │   ├── tongyi.go                   # 通义千问适配器
│   │   ├── zhipu.go                   # 智谱适配器
│   │   └── deepseek.go                # DeepSeek 适配器（原生透传）
│   ├── plugin/                         # 插件层
│   │   ├── interface.go               # 接口定义（无 BuildTag，全版本编译）
│   │   ├── factory.go                  # OSS 工厂 (//go:build oss)
│   │   ├── factory_enterprise.go       # Enterprise 工厂 (//go:build enterprise)
│   │   ├── oss/                        # 共享实现（无 BuildTag，两版本都编译）
│   │   │   ├── storage_mysql.go
│   │   │   ├── storage_sqlite.go
│   │   │   ├── audit_simple.go
│   │   │   ├── limit_mem.go
│   │   │   └── ring_buffer.go
│   │   └── enterprise/                 # Enterprise 专属（私有，不公开）
│   │       ├── storage_dm.go           # //go:build enterprise
│   │       ├── storage_kingbase.go
│   │       ├── audit_stream.go
│   │       ├── security_pii.go
│   │       ├── export_siem.go
│   │       ├── export_syslog.go
│   │       ├── export_kafka.go
│   │       ├── limit_redis.go
│   │       └── license.go
│   ├── admin/                          # 管理后台 (Gin)
│   │   ├── server.go                   # Gin 服务初始化
│   │   ├── handler_*.go               # API Key/模型配置/审计查询 handler
│   │   └── middleware.go               # 管理后台鉴权中间件
│   ├── config/                         # 配置加载
│   │   └── config.go
│   └── types/                          # 公共类型定义
│       └── types.go                    # UnifiedRequest/Response/ModelConfig 等
├── webui/                              # 管理后台前端
├── config.yaml                         # 配置文件模板
├── go.mod
├── go.sum
└── README.md
```

### BuildTag 规则

| 文件位置 | BuildTag | OSS 编译 | Enterprise 编译 |
|----------|----------|---------|----------------|
| `interface.go` | 无 | ✅ | ✅ |
| `factory.go` | `oss` | ✅ | ❌ |
| `factory_enterprise.go` | `enterprise` | ❌ | ✅ |
| `oss/*.go` | **无** | ✅ | ✅ |
| `enterprise/*.go` | `enterprise` | ❌ | ✅ |

> `oss/` 目录不设 BuildTag 是为了确保 Enterprise 版能复用 MySQL/SQLite 存储等共享实现。

---

## 性能指标

| 指标 | 要求 |
|------|------|
| 代理转发延迟增加 | < 5ms (P99) |
| SSE 流式转发延迟 | < 2ms (每分片) |
| 审计链路阻塞 | 0ms（完全异步） |
| 并发连接数 | ≥ 5,000 |
| QPS | ≥ 2,000（非流式） |
| 审计队列吞吐 | ≥ 10,000/s |
| 连续运行内存泄漏 | 72h 无明显增长 |

---

## 兼容性

| 维度 | 要求 |
|------|------|
| CPU 架构 | x86_64 / arm64 |
| 操作系统 | 麒麟 V10、统信 UOS、CentOS 7+、Ubuntu 18.04+ |
| Go 版本 | 1.22+ |
| 数据库 | MySQL 5.7+ / SQLite 3.35+ / 达梦8 / 人大金仓 V8 |
| Docker | 20.10+ |

---

## 关键依赖

| 依赖 | 用途 | 版本 |
|------|------|------|
| github.com/gin-gonic/gin | 管理后台框架 | v1.9+ |
| github.com/go-sql-driver/mysql | MySQL 驱动 | v1.7+ |
| modernc.org/sqlite | 纯 Go SQLite 驱动 | v1.28+ |
| gopkg.in/yaml.v3 | 配置解析 | v3.0+ |
| github.com/google/uuid | UUID 生成 | v1.6+ |
| go.uber.org/zap | 日志库 | v1.27+ |
| github.com/redis/go-redis/v9 | Redis 客户端 (Enterprise) | v9.5+ |

Enterprise 额外依赖（仅 `enterprise` BuildTag 编译时引入）：达梦数据库驱动、人大金仓驱动、Kafka Go 客户端。

---

## 目标用户场景

### 场景1：政企私有化大模型项目

政府单位信息化部门，需要可过等保 2.0 的全链路调用审计。部署 Enterprise 版，启用 SHA256 日志防篡改 + SIEM 外推 + RBAC 权限。

### 场景2：信创兼容项目

央企国产化改造项目，飞腾/鲲鹏 CPU + 麒麟/统信 OS + 达梦/人大金仓数据库。ARM64 编译 + 达梦数据库适配 + 单二进制部署。

### 场景3：企业多模型统一接入

互联网公司 AI 平台团队，多个模型 API 分散管理。部署 NeuralGate，配置多个模型适配器，一个 API Key 路由多模型，统一计量对账。

### 场景4：AI Agent/MCP 项目

企业 AI Agent 开发团队，Agent 工具调用行为需审计。Enterprise 版 MCP 全链路审计，工具调用参数与结果全留存。

---

## License

- **OSS 版本**：开源协议（详见 LICENSE 文件）
- **Enterprise 版本**：商业授权，联系获取

---

## 相关文档

- [技术架构详细设计](./NeuralGate_技术架构详细设计.md) — 开发者拿到可直接进入编码
- [产品需求文档](./NeuralGate_产品需求文档.md) — 功能、流程、字段约束完整定义
