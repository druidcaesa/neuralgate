# NeuralGate AI

<div align="center">

**Unified LLM Gateway · Production-Grade Streaming Audit · Full Domestic Stack Support**

A self-developed enterprise-grade AI LLM governance gateway written in Go.

**[English](./README.en-US.md)** | **[中文](./README.md)**

</div>

---

## Overview

NeuralGate is a lightweight, high-performance, commercially viable private AI model gateway that addresses the **compliance and compatibility** pain points for domestic enterprises, system integrators, and government projects adopting private LLM deployments. Unlike generic open-source gateways, it focuses on **production-grade audit compliance for the Chinese market + full Xinchuang (domestic) hardware/software compatibility**.

### Problems Solved

| Pain Point | Limitations of Existing Solutions | NeuralGate Solution |
|------------|-----------------------------------|---------------------|
| SSE streaming log loss | Open-source gateways lose fragments; no logs on disconnect | Ring buffer async audit + auto-completion on disconnect |
| No domestic database support | Cannot deploy in Xinchuang projects | Full driver support for DM (Dameng) and Kingbase |
| Log tampering | Fails compliance requirements | SHA256 evidence + independent audit DB permissions |
| No multi-tenant isolation | Enterprise requirements unmet | RBAC multi-tenancy + full operation audit trail |

### Key Features

- **Zero-Change OpenAI SDK Compatibility** — Point `OPENAI_BASE_URL` at the gateway; existing code works without modification
- **Multi-Model Unified Access** — Supports OpenAI, Tongyi Qwen, Zhipu, DeepSeek and more, with unified API Key management
- **SSE Streaming Audit** — Ring buffer async persistence, auto-completion on disconnect, zero fragment loss
- **Full Xinchuang Compatibility** — Kylin/UOS OS + Phytiron/Kunpeng CPU + DM/Kingbase database
- **Single Binary Deployment** — Zero external dependencies (except database); supports Docker/bare metal/systemd
- **Open-Core Architecture** — Open-source core + commercial plugins; one source, two build outputs

---

## Editions

NeuralGate uses an Open-Core model with Go BuildTag conditional compilation — one source tree produces two editions:

| Dimension | OSS (Open Source) | Enterprise |
|-----------|-------------------|------------|
| Build command | `go build -tags oss` | `go build -tags enterprise` |
| Binary name | `neuralgate` | `neuralgate-enterprise` |
| Model proxy | ✅ Full | ✅ Full |
| API Key management | ✅ | ✅ |
| Rate limiting | ✅ In-memory token bucket | ✅ Redis distributed (with in-memory fallback) |
| SSE streaming audit | ✅ Sync metadata | ✅ Async streaming + SHA256 evidence |
| Log tamper protection | — | ✅ SHA256 + independent audit permissions |
| Privacy (PII masking) | — | ✅ Dynamic PII redaction |
| Permission system | ✅ Super admin | ✅ RBAC multi-tenant |
| Compliance operations | — | ✅ SIEM/Syslog/Kafka export |
| MCP audit | — | ✅ Full tool-call chain audit |
| License management | — | ✅ Serial + offline licensing |
| MySQL | ✅ | ✅ |
| SQLite | ✅ | ✅ |
| DM (Dameng) | — | ✅ |
| Kingbase | — | ✅ |

> Enterprise edition includes all OSS capabilities. Configuring DM/Kingbase on an OSS build will fail at startup with a "requires Enterprise edition" error.

---

## Architecture

### Dual-Service Isolation

Two fully isolated HTTP services run within a single process:

| Service | Port | Framework | Responsibility |
|---------|------|-----------|----------------|
| Proxy service | 8080 | Pure net/http | LLM traffic proxy, SSE stream hijacking, reverse proxy |
| Admin backend | 8081 | Gin | CRUD APIs, config management, log queries, license validation |

The proxy service **must not use Gin/Echo or any framework** — only `net/http` for high-concurrency long-connection performance.

### Four-Layer Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Acceptor Layer                            │
│   Connection mgmt · TLS termination · IP filter · Protocol    │
├─────────────────────────────────────────────────────────────┤
│                  Pipeline (Middleware) Layer                 │
│   Auth → RateLimit → RouteMatch → ProtocolTransform → PreHook│
├─────────────────────────────────────────────────────────────┤
│                   Proxy Core Layer                           │
│   ReverseProxy · SSE hijacking · Stream reassembly · Fwd    │
├─────────────────────────────────────────────────────────────┤
│                   Plugin Layer                               │
│   Storage · Audit pipeline · Rate limiter · Log export · Lic │
└─────────────────────────────────────────────────────────────┘
```

### Design Principles

| Principle | Description |
|-----------|-------------|
| Interface-contracted | All extension points defined via interfaces; zero hardcoded vendor logic in core |
| Compile-time isolation | BuildTag conditional compilation; one source, two outputs; no go plugin (.so) |
| Async non-blocking | Audit via in-memory ring buffer + independent worker pool; zero main traffic blocking |
| Open-Core | Open-source core + commercial plugins; enterprise directory is private |
| Single binary | Zero external dependencies; Docker/bare metal ready |

---

## OpenAI API Compatibility

NeuralGate's entry protocol is fully compatible with the OpenAI API. Users simply point `OPENAI_BASE_URL` at the gateway.

### Supported Endpoints

| Endpoint | Method | Path | Handling |
|----------|--------|------|----------|
| Chat Completions | POST | `/v1/chat/completions` | Model adapter transform/passthrough |
| Completions (Legacy) | POST | `/v1/completions` | Passthrough to upstream |
| Embeddings | POST | `/v1/embeddings` | Passthrough to upstream |
| Models List | GET | `/v1/models` | Gateway local response |
| Models Retrieve | GET | `/v1/models/{model}` | Gateway local response |
| Moderations | POST | `/v1/moderations` | Passthrough to upstream |
| Images Generations | POST | `/v1/images/generations` | Passthrough to upstream |
| Images Edits | POST | `/v1/images/edits` | Passthrough to upstream |
| Audio Speech (TTS) | POST | `/v1/audio/speech` | Passthrough to upstream |
| Audio Transcriptions | POST | `/v1/audio/transcriptions` | Passthrough to upstream |
| Files | CRUD | `/v1/files` | Passthrough to upstream |

### Supported Request Parameters

- **Core**: model, messages, temperature, top_p, max_tokens, stop, seed, n
- **Function Calling**: tools, tool_choice, parallel_tool_calls
- **Structured Output**: response_format (json_object / json_schema)
- **Streaming**: stream, stream_options.include_usage
- **Sampling**: frequency_penalty, presence_penalty, logit_bias, logprobs
- **Multimodal**: messages.content supports string and []ContentPart (text + image_url + input_audio)
- **Reasoning models**: reasoning_effort, verbosity

### Built-in Model Adapters

| Adapter | Protocol | Streaming | Native Passthrough | Edition |
|---------|----------|-----------|-------------------|---------|
| OpenAI | OpenAI API | SSE | ✅ Raw passthrough | OSS |
| Tongyi Qwen | DashScope | SSE | ❌ Format transform | OSS |
| Zhipu | GLM API | SSE | ❌ Format transform | OSS |
| DeepSeek | OpenAI-compatible | SSE | ✅ Model field swap only | OSS |

> **Native passthrough** = upstream protocol matches entry protocol; adapter skips TransformRequest, only swaps the model field, zero serialization overhead.

---

## Quick Start

### Prerequisites

- Go 1.22+
- MySQL 5.7+ or SQLite 3.35+ (Enterprise also supports DM8 / Kingbase V8)
- Docker 20.10+ (optional)

### Build

```bash
# OSS edition
go build -tags oss -o neuralgate ./cmd/gateway/

# Enterprise edition
go build -tags enterprise -o neuralgate-enterprise ./cmd/gateway/
```

### Configuration

Copy and edit the config file:

```bash
cp config.yaml config.local.yaml
```

```yaml
server:
  proxy_addr: ":8080"
  admin_addr: ":8081"

storage:
  # Options: mysql(default) / sqlite / dm(Enterprise) / kingbase(Enterprise)
  driver: mysql

  # ----- MySQL (default, OSS+Enterprise) -----
  dsn: "user:pass@tcp(host:3306)/neuralgate?charset=utf8mb4"

  # ----- SQLite (lightweight, OSS+Enterprise) -----
  # driver: sqlite
  # dsn: "/var/lib/neuralgate/neuralgate.db"

  # ----- DM Dameng (Enterprise only) -----
  # driver: dm
  # dsn: "dm://user:pass@host:5236/NEURALGATE"

  # ----- Kingbase (Enterprise only) -----
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

> **Model configs are NOT in config.yaml.** Models are managed via the admin backend (:8081) CRUD, stored in the database, and support hot updates — add/edit/remove takes effect immediately without restart.

### Run

```bash
./neuralgate -config config.local.yaml
```

On startup:
- Proxy service listens on `:8080`, accepting OpenAI-protocol requests
- Admin backend listens on `:8081`, providing model config, API Key management, and more
- First launch has an empty database; add your first model config via the admin backend

### Docker

```bash
# Build multi-arch images
docker build --platform linux/amd64,linux/arm64 -t neuralgate:oss --build-arg BUILD_TAGS=oss .
docker build --platform linux/amd64,linux/arm64 -t neuralgate:enterprise --build-arg BUILD_TAGS=enterprise .

# Run
docker run -d -p 8080:8080 -p 8081:8081 -v ./config.yaml:/etc/neuralgate/config.yaml neuralgate:oss
```

---

## Usage Examples

### OpenAI SDK (Zero Change)

```python
import openai

client = openai.OpenAI(
    api_key="ng-xxxxxxxxxxxx",           # API Key generated via NeuralGate admin
    base_url="http://localhost:8080/v1"  # Point to NeuralGate gateway
)

response = client.chat.completions.create(
    model="gpt-4",    # Model name configured in admin backend
    messages=[{"role": "user", "content": "Hello!"}]
)
```

### curl

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

## Project Structure

```
neuralgate/
├── cmd/
│   └── gateway/
│       └── main.go                    # Entry point: arg parsing, init, dual-service launch
├── pkg/
│   ├── core/                           # Proxy core
│   │   ├── acceptor.go                 # Acceptor: connection mgmt, TLS, IP filter
│   │   ├── pipeline.go                 # Middleware pipeline chain
│   │   ├── proxy.go                    # Reverse proxy + SSE hijacking
│   │   └── router.go                   # Model route matching
│   ├── adapter/                        # Model adapters
│   │   ├── registry.go                # Adapter registry
│   │   ├── openai.go                   # OpenAI adapter (native passthrough)
│   │   ├── tongyi.go                   # Tongyi Qwen adapter
│   │   ├── zhipu.go                   # Zhipu adapter
│   │   └── deepseek.go                # DeepSeek adapter (native passthrough)
│   ├── plugin/                         # Plugin layer
│   │   ├── interface.go               # Interface definitions (no BuildTag, all editions)
│   │   ├── factory.go                  # OSS factory (//go:build oss)
│   │   ├── factory_enterprise.go       # Enterprise factory (//go:build enterprise)
│   │   ├── oss/                        # Shared implementations (no BuildTag, both editions)
│   │   │   ├── storage_mysql.go
│   │   │   ├── storage_sqlite.go
│   │   │   ├── audit_simple.go
│   │   │   ├── limit_mem.go
│   │   │   └── ring_buffer.go
│   │   └── enterprise/                 # Enterprise-only (private, not published)
│   │       ├── storage_dm.go           # //go:build enterprise
│   │       ├── storage_kingbase.go
│   │       ├── audit_stream.go
│   │       ├── security_pii.go
│   │       ├── export_siem.go
│   │       ├── export_syslog.go
│   │       ├── export_kafka.go
│   │       ├── limit_redis.go
│   │       └── license.go
│   ├── admin/                          # Admin backend (Gin)
│   │   ├── server.go                   # Gin service init
│   │   ├── handler_*.go               # API Key/model config/audit query handlers
│   │   └── middleware.go               # Admin auth middleware
│   ├── config/                         # Config loading
│   │   └── config.go
│   └── types/                          # Common type definitions
│       └── types.go                    # UnifiedRequest/Response/ModelConfig etc.
├── webui/                              # Admin frontend
├── config.yaml                         # Config template
├── go.mod
├── go.sum
└── README.md
```

### BuildTag Rules

| File Location | BuildTag | OSS Build | Enterprise Build |
|---------------|----------|-----------|-----------------|
| `interface.go` | none | ✅ | ✅ |
| `factory.go` | `oss` | ✅ | ❌ |
| `factory_enterprise.go` | `enterprise` | ❌ | ✅ |
| `oss/*.go` | **none** | ✅ | ✅ |
| `enterprise/*.go` | `enterprise` | ❌ | ✅ |

> `oss/` directory has no BuildTag so Enterprise can reuse shared implementations (MySQL/SQLite storage, in-memory rate limiter, etc.).

---

## Performance

| Metric | Requirement |
|--------|-------------|
| Proxy forwarding latency overhead | < 5ms (P99) |
| SSE streaming forwarding latency | < 2ms per chunk |
| Audit pipeline blocking | 0ms (fully async) |
| Concurrent connections | ≥ 5,000 |
| QPS (non-streaming) | ≥ 2,000 |
| Audit queue throughput | ≥ 10,000/s |
| Memory leak (72h sustained) | No significant growth |

---

## Compatibility

| Dimension | Requirement |
|-----------|-------------|
| CPU architecture | x86_64 / arm64 |
| Operating system | Kylin V10, UOS, CentOS 7+, Ubuntu 18.04+ |
| Go version | 1.22+ |
| Database | MySQL 5.7+ / SQLite 3.35+ / DM8 / Kingbase V8 |
| Docker | 20.10+ |

---

## Key Dependencies

| Dependency | Purpose | Version |
|------------|---------|---------|
| github.com/gin-gonic/gin | Admin backend framework | v1.9+ |
| github.com/go-sql-driver/mysql | MySQL driver | v1.7+ |
| modernc.org/sqlite | Pure-Go SQLite driver | v1.28+ |
| gopkg.in/yaml.v3 | Config parsing | v3.0+ |
| github.com/google/uuid | UUID generation | v1.6+ |
| go.uber.org/zap | Logging | v1.27+ |
| github.com/redis/go-redis/v9 | Redis client (Enterprise) | v9.5+ |

Enterprise additional dependencies (only included with `enterprise` BuildTag): DM database driver, Kingbase driver, Kafka Go client.

---

## Target User Scenarios

### Scenario 1: Government/Enterprise Private LLM Project

Government IT departments needing full-chain call audit compliant with MLPS 2.0 (Dengbao). Deploy Enterprise edition with SHA256 log tamper protection + SIEM export + RBAC.

### Scenario 2: Xinchuang Compatibility Project

State-owned enterprise domestic-transformation project with Phytiron/Kunpeng CPU + Kylin/UOS OS + DM/Kingbase database. ARM64 build + DM database adapter + single binary deployment.

### Scenario 3: Enterprise Multi-Model Unified Access

Internet company AI platform team with scattered model API management. Deploy NeuralGate, configure multiple model adapters, route multiple models with one API Key, unified metering and reconciliation.

### Scenario 4: AI Agent/MCP Project

Enterprise AI Agent development team needing tool-call behavior audit. Enterprise edition MCP full-chain audit — tool call parameters and results fully persisted.

---

## License

- **OSS Edition**: Open-source license (see LICENSE file)
- **Enterprise Edition**: Commercial license — contact to obtain

---

## Documentation

- [Technical Architecture Design](./NeuralGate_技术架构详细设计.md) — Developers can start coding directly
- [Product Requirements Document](./NeuralGate_产品需求文档.md) — Complete feature, flow, and field definitions
