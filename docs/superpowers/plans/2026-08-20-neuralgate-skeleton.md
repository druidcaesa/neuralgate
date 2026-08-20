# NeuralGate 基础架构搭建 实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标：** 搭建 NeuralGate 可编译、可启动、契约完整的骨架工程（模块名 `github.com/druidcaesa/neuralgate`），业务逻辑后置。

**架构：** 单进程双服务（代理服务 :8080 纯 net/http + 管理后台 :8081 Gin）；四层分层（Acceptor / Pipeline / ProxyCore / Plugin）；BuildTag 双版本编译（oss / enterprise）；骨架期使用内存存储 mock，双服务真实可启动。

**技术栈：** Go 1.26（net/http、gin-gonic/gin、gopkg.in/yaml.v3、go.uber.org/zap、google/uuid）

**依据规格：** `docs/superpowers/specs/2026-08-20-neuralgate-skeleton-design.md`
**依据设计文档：** `NeuralGate_技术架构详细设计.md`（V1.0）

---

## 实现修正说明（与规格/设计文档的偏差，均已定案）

1. **工厂包位置修正**：设计文档第 9 节把 `factory.go` / `factory_enterprise.go` 放在 `pkg/plugin/` 根目录，但 Go 存在包循环依赖：`pkg/plugin` 定义接口 → `pkg/plugin/oss` 实现需 import `pkg/plugin` → 工厂（在 `pkg/plugin` 内）再 import `pkg/plugin/oss` 即构成 `plugin → oss → plugin` 循环，无法编译。**修正：** 工厂移入 `pkg/plugin/oss/factory.go`（package oss）与 `pkg/plugin/enterprise/factory.go`（package enterprise）；main 包内通过两个 BuildTag 注入文件（`cmd/gateway/factory_oss.go` / `factory_enterprise.go`）选择工厂。BuildTag 机制、目录隔离（push-github-oss.sh 按目录过滤）全部保留。
2. **config.yaml 的 `storage.driver` 默认 `mem`**（规格 4.1 定案），mysql/sqlite 配置保留在注释中。
3. **文档 5.3 与 5.6 矛盾取舍**：环形队列按 5.3 实现阻塞语义（notFull/notEmpty 条件变量），5.6 的"丢弃最旧事件"属 Phase 7 Enterprise 审计策略，骨架期不实现。
4. **文档 3.5 补全**：`Tenant`/`User` 中引用的 `TenantStatus`/`UserStatus` 类型文档未定义，契约中补全定义。
5. **错误约定**：存储查询未命中返回 `oss.ErrNotFound`（文档接口未定义，实现层补充）。

---

## 文件结构总览

| 路径 | 职责 | 创建任务 |
|------|------|----------|
| `go.mod` / `go.sum` | 模块定义与依赖 | 1 |
| `config.yaml` | 默认配置（driver: mem） | 1 |
| `Makefile` | build-oss / build-enterprise / run / test / vet | 1、14 |
| `.gitignore` | 照设计文档 12.3 | 1 |
| `README.md` | 项目说明 | 1 |
| `push-private.sh` / `push-github-oss.sh` | 双仓库推送（地址占位） | 1 |
| `pkg/config/config.go` | 配置加载与默认值（完整实现） | 1 |
| `pkg/config/config_test.go` | 配置加载测试 | 1 |
| `pkg/plugin/interface.go` | 全部接口 + 数据结构（契约，照文档第 3/4 节） | 2 |
| `pkg/core/context.go` | RequestContext + context 传递 | 4 |
| `pkg/core/version.go` | VersionInfo + 编译期注入变量 | 4 |
| `pkg/adapter/interface.go` | ModelAdapter + 统一请求/响应结构（契约，照文档 4.7/8.3） | 3 |
| `pkg/plugin/oss/ring_buffer.go` | 环形队列（完整实现） | 5 |
| `pkg/plugin/oss/storage_mem.go` | 内存存储（完整实现） | 6 |
| `pkg/plugin/oss/audit_simple.go` | 简单同步审计 | 7 |
| `pkg/plugin/oss/limit_mem.go` | 内存限流 | 7 |
| `pkg/plugin/oss/factory.go` | OSS 工厂（无 BuildTag，package oss） | 8 |
| `pkg/plugin/enterprise/factory.go` | Enterprise 工厂（`//go:build enterprise`，骨架期复用 OSS 实现） | 8 |
| `pkg/adapter/registry.go` | 适配器注册中心（完整实现） | 9 |
| `pkg/adapter/openai.go` 等 4 文件 | 适配器壳 | 9 |
| `pkg/core/pipeline.go` | Middleware 类型 + 链组装（完整实现） | 10 |
| `pkg/core/middleware_auth.go` 等 3 文件 | 中间件壳 | 10 |
| `pkg/core/proxy.go` | ProxyCore + OpenAI 错误格式 | 11 |
| `pkg/core/sse_writer.go` / `sse_reassembler.go` / `disconnect_handler.go` / `acceptor.go` | SSE 劫持与接入层壳 | 11 |
| `pkg/admin/server.go` / `router.go` / `middleware.go` | Gin 后台 | 12 |
| `cmd/gateway/main.go` | 双服务启动 + 优雅关闭 | 13 |
| `cmd/gateway/factory_oss.go` / `factory_enterprise.go` | BuildTag 工厂注入点 | 13 |

**包依赖方向**（无循环）：`pkg/config` 无依赖；`pkg/plugin` 无依赖；`pkg/plugin/oss` → `pkg/plugin`；`pkg/plugin/enterprise` → `pkg/plugin` + `pkg/plugin/oss`；`pkg/core` → `pkg/plugin` + `pkg/adapter` + uuid；`pkg/adapter` 无依赖；`pkg/admin` → `pkg/plugin` + gin + zap；`cmd/gateway` → 全部。

---

## 任务 1：项目初始化 + 配置加载

**文件：**
- 创建：`go.mod`、`config.yaml`、`Makefile`、`.gitignore`、`README.md`、`push-private.sh`、`push-github-oss.sh`
- 创建：`pkg/config/config.go`、`pkg/config/config_test.go`

- [ ] **步骤 1：编写失败的测试**（先建目录与 go.mod）

```bash
cd /Users/fanyanan/work/go/neuralgate
go mod init github.com/druidcaesa/neuralgate
go get gopkg.in/yaml.v3@latest go.uber.org/zap@latest github.com/google/uuid@latest github.com/gin-gonic/gin@latest
```

创建 `pkg/config/config_test.go`：

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCompleteFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := `
server:
  proxy_addr: ":9090"
  admin_addr: ":9091"
  read_timeout: 10s
  write_timeout: 20s
  idle_timeout: 60s
  max_header_bytes: 2097152
storage:
  driver: mysql
  dsn: "user:pass@tcp(host:3306)/db"
  max_open_conns: 5
  max_idle_conns: 2
audit:
  queue_size: 1024
  worker_count: 2
  batch_size: 10
  flush_interval: 1s
  enable_sha256: false
  retention_days: 30
rate_limit:
  strategy: sliding_window
  default_rps: 100
  default_tpm: 1000
export:
  type: kafka
  endpoint: "http://kafka:9092"
  batch_size: 5
  flush_interval: 2s
license:
  file_path: "/tmp/license.lic"
  offline_mode: true
log:
  level: debug
  format: console
  output: stderr
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := []struct{ got, want interface{} }{
		{cfg.Server.ProxyAddr, ":9090"},
		{cfg.Server.AdminAddr, ":9091"},
		{cfg.Server.MaxHeaderBytes, 2097152},
		{cfg.Storage.Driver, "mysql"},
		{cfg.Storage.MaxOpenConns, 5},
		{cfg.Audit.QueueSize, 1024},
		{cfg.Audit.FlushInterval.String(), "1s"},
		{cfg.RateLimit.Strategy, "sliding_window"},
		{cfg.RateLimit.DefaultRPS, 100},
		{cfg.Export.Type, "kafka"},
		{cfg.License.OfflineMode, true},
		{cfg.Log.Level, "debug"},
	}
	for _, w := range want {
		if w.got != w.want {
			t.Errorf("got %v, want %v", w.got, w.want)
		}
	}
}

func TestLoadMissingFieldsUseDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("server:\n  proxy_addr: \":9090\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.ProxyAddr != ":9090" {
		t.Errorf("ProxyAddr = %q, want :9090", cfg.Server.ProxyAddr)
	}
	if cfg.Server.AdminAddr != ":8081" {
		t.Errorf("AdminAddr = %q, want default :8081", cfg.Server.AdminAddr)
	}
	if cfg.Storage.Driver != "mem" {
		t.Errorf("Driver = %q, want default mem", cfg.Storage.Driver)
	}
	if cfg.Audit.QueueSize != 65536 {
		t.Errorf("QueueSize = %d, want default 65536", cfg.Audit.QueueSize)
	}
	if cfg.RateLimit.DefaultRPS != 10 {
		t.Errorf("DefaultRPS = %d, want default 10", cfg.RateLimit.DefaultRPS)
	}
	if cfg.Log.Level != "info" {
		t.Errorf("Log.Level = %q, want default info", cfg.Log.Level)
	}
}

func TestLoadNonexistentFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err == nil {
		t.Fatal("Load() expected error for missing file")
	}
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./pkg/config/ -v`
预期：FAIL，`undefined: Load`（Load 尚未实现）

- [ ] **步骤 3：实现配置加载**

创建 `pkg/config/config.go`：

```go
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 系统级配置（不含模型配置，模型配置存储在数据库中）
type Config struct {
	Server    ServerConfig    `yaml:"server"`
	Storage   StorageConfig   `yaml:"storage"`
	Audit     AuditConfig     `yaml:"audit"`
	RateLimit RateLimitConfig `yaml:"rate_limit"`
	Export    ExportConfig    `yaml:"export"`
	License   LicenseConfig   `yaml:"license"`
	Log       LogConfig       `yaml:"log"`
}

type ServerConfig struct {
	ProxyAddr      string        `yaml:"proxy_addr"`
	AdminAddr      string        `yaml:"admin_addr"`
	ReadTimeout    time.Duration `yaml:"read_timeout"`
	WriteTimeout   time.Duration `yaml:"write_timeout"`
	IdleTimeout    time.Duration `yaml:"idle_timeout"`
	MaxHeaderBytes int           `yaml:"max_header_bytes"`
}

type StorageConfig struct {
	Driver       string `yaml:"driver"`
	DSN          string `yaml:"dsn"`
	MaxOpenConns int    `yaml:"max_open_conns"`
	MaxIdleConns int    `yaml:"max_idle_conns"`
}

type AuditConfig struct {
	QueueSize     int           `yaml:"queue_size"`
	WorkerCount   int           `yaml:"worker_count"`
	BatchSize     int           `yaml:"batch_size"`
	FlushInterval time.Duration `yaml:"flush_interval"`
	EnableSHA256  bool          `yaml:"enable_sha256"`
	RetentionDays int           `yaml:"retention_days"`
}

type RateLimitConfig struct {
	Strategy    string `yaml:"strategy"`
	DefaultRPS  int    `yaml:"default_rps"`
	DefaultTPM  int64  `yaml:"default_tpm"`
}

type ExportConfig struct {
	Type          string        `yaml:"type"`
	Endpoint      string        `yaml:"endpoint"`
	APIKey        string        `yaml:"api_key"`
	BatchSize     int           `yaml:"batch_size"`
	FlushInterval time.Duration `yaml:"flush_interval"`
}

type LicenseConfig struct {
	FilePath    string `yaml:"file_path"`
	OfflineMode bool   `yaml:"offline_mode"`
}

type LogConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
	Output string `yaml:"output"`
}

// Default 返回默认配置
func Default() *Config {
	return &Config{
		Server: ServerConfig{
			ProxyAddr:      ":8080",
			AdminAddr:      ":8081",
			ReadTimeout:    30 * time.Second,
			WriteTimeout:   30 * time.Second,
			IdleTimeout:    120 * time.Second,
			MaxHeaderBytes: 1 << 20,
		},
		Storage: StorageConfig{
			Driver:       "mem",
			MaxOpenConns: 20,
			MaxIdleConns: 10,
		},
		Audit: AuditConfig{
			QueueSize:     65536,
			WorkerCount:   4,
			BatchSize:     100,
			FlushInterval: 5 * time.Second,
			RetentionDays: 90,
		},
		RateLimit: RateLimitConfig{
			Strategy:   "token_bucket",
			DefaultRPS: 10,
			DefaultTPM: 100000,
		},
		Export: ExportConfig{
			Type:          "siem",
			BatchSize:     50,
			FlushInterval: 10 * time.Second,
		},
		License: LicenseConfig{
			FilePath: "/etc/neuralgate/license.lic",
		},
		Log: LogConfig{
			Level:  "info",
			Format: "json",
			Output: "stdout",
		},
	}
}

// Load 加载配置文件，缺失字段使用默认值
func Load(path string) (*Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}
	cfg.applyDefaults()
	return cfg, nil
}

// applyDefaults 将零值字段替换为默认值
// 注意：bool 字段不在此处理（无法区分"未设置"与"显式 false"）
func (c *Config) applyDefaults() {
	d := Default()
	if c.Server.ProxyAddr == "" {
		c.Server.ProxyAddr = d.Server.ProxyAddr
	}
	if c.Server.AdminAddr == "" {
		c.Server.AdminAddr = d.Server.AdminAddr
	}
	if c.Server.ReadTimeout == 0 {
		c.Server.ReadTimeout = d.Server.ReadTimeout
	}
	if c.Server.WriteTimeout == 0 {
		c.Server.WriteTimeout = d.Server.WriteTimeout
	}
	if c.Server.IdleTimeout == 0 {
		c.Server.IdleTimeout = d.Server.IdleTimeout
	}
	if c.Server.MaxHeaderBytes == 0 {
		c.Server.MaxHeaderBytes = d.Server.MaxHeaderBytes
	}
	if c.Storage.Driver == "" {
		c.Storage.Driver = d.Storage.Driver
	}
	if c.Storage.MaxOpenConns == 0 {
		c.Storage.MaxOpenConns = d.Storage.MaxOpenConns
	}
	if c.Storage.MaxIdleConns == 0 {
		c.Storage.MaxIdleConns = d.Storage.MaxIdleConns
	}
	if c.Audit.QueueSize == 0 {
		c.Audit.QueueSize = d.Audit.QueueSize
	}
	if c.Audit.WorkerCount == 0 {
		c.Audit.WorkerCount = d.Audit.WorkerCount
	}
	if c.Audit.BatchSize == 0 {
		c.Audit.BatchSize = d.Audit.BatchSize
	}
	if c.Audit.FlushInterval == 0 {
		c.Audit.FlushInterval = d.Audit.FlushInterval
	}
	if c.Audit.RetentionDays == 0 {
		c.Audit.RetentionDays = d.Audit.RetentionDays
	}
	if c.RateLimit.Strategy == "" {
		c.RateLimit.Strategy = d.RateLimit.Strategy
	}
	if c.RateLimit.DefaultRPS == 0 {
		c.RateLimit.DefaultRPS = d.RateLimit.DefaultRPS
	}
	if c.RateLimit.DefaultTPM == 0 {
		c.RateLimit.DefaultTPM = d.RateLimit.DefaultTPM
	}
	if c.Export.Type == "" {
		c.Export.Type = d.Export.Type
	}
	if c.Export.BatchSize == 0 {
		c.Export.BatchSize = d.Export.BatchSize
	}
	if c.Export.FlushInterval == 0 {
		c.Export.FlushInterval = d.Export.FlushInterval
	}
	if c.License.FilePath == "" {
		c.License.FilePath = d.License.FilePath
	}
	if c.Log.Level == "" {
		c.Log.Level = d.Log.Level
	}
	if c.Log.Format == "" {
		c.Log.Format = d.Log.Format
	}
	if c.Log.Output == "" {
		c.Log.Output = d.Log.Output
	}
}
```

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./pkg/config/ -v`
预期：PASS（3 个测试全部通过）

- [ ] **步骤 5：创建工程化文件**

`config.yaml`：

```yaml
server:
  proxy_addr: ":8080"        # 代理服务监听地址
  admin_addr: ":8081"        # 管理后台监听地址
  read_timeout: 30s          # 读超时（非流式请求）
  write_timeout: 30s         # 写超时（非流式请求）
  idle_timeout: 120s         # 空闲超时
  max_header_bytes: 1048576  # 最大Header大小(1MB)

storage:
  # ===== 数据库驱动选择 =====
  # 可选值: mem(骨架期默认) / mysql / sqlite / dm(Enterprise) / kingbase(Enterprise)
  # mem 为骨架阶段内存存储（Phase 3 切换 mysql/sqlite；dm/kingbase 需 Enterprise 编译）
  driver: mem
  dsn: "user:pass@tcp(host:3306)/neuralgate?charset=utf8mb4"
  max_open_conns: 20          # 最大连接数
  max_idle_conns: 10          # 最大空闲连接

audit:
  queue_size: 65536          # 环形队列大小
  worker_count: 4            # worker数量
  batch_size: 100            # 批量写入大小
  flush_interval: 5s         # 刷新间隔
  enable_sha256: true        # SHA256存证（Enterprise）
  retention_days: 90         # 日志保留天数

rate_limit:
  strategy: token_bucket     # token_bucket/sliding_window
  default_rps: 10            # 默认每秒请求数
  default_tpm: 100000        # 默认每分钟Token数

export:                       # Enterprise only
  type: siem                  # siem/syslog/kafka
  endpoint: "https://siem.example.com/api"
  api_key: ""
  batch_size: 50
  flush_interval: 10s

license:                      # Enterprise only
  file_path: "/etc/neuralgate/license.lic"
  offline_mode: false

log:
  level: info                 # debug/info/warn/error
  format: json                # json/console
  output: stdout              # stdout/file path
```

`.gitignore`：

```gitignore
# 商业授权文件
*.lic
license/

# 编译产物
/neuralgate
/neuralgate-enterprise

# 本地配置（含密钥）
config.local.yaml

# Go 构建与测试产物
*.test
*.out
vendor/
```

`push-private.sh`（照设计文档 12.2，远端地址待用户提供后替换）：

```bash
#!/bin/bash
# 推送全量代码到私有仓库（含enterprise目录）
# 用法: ./push-private.sh "本次提交说明"
# 示例: ./push-private.sh "feat: 新增达梦存储适配器"

COMMIT_MSG="$1"
if [ -z "$COMMIT_MSG" ]; then
  echo "用法: ./push-private.sh \"提交说明\""
  echo "示例: ./push-private.sh \"feat: 新增达梦存储适配器\""
  exit 1
fi

# 自动获取当前分支名
CURRENT_BRANCH=$(git branch --show-current)
if [ -z "$CURRENT_BRANCH" ]; then
  echo "错误: 无法获取当前分支名，请确保不在 detached HEAD 状态"
  exit 1
fi

TIMESTAMP=$(date +%Y%m%d_%H%M%S)
git add -A
git commit -m "[$TIMESTAMP] $COMMIT_MSG"
git push origin-private "$CURRENT_BRANCH"
echo "Private repo pushed to branch [$CURRENT_BRANCH]: [$TIMESTAMP] $COMMIT_MSG"
```

`push-github-oss.sh`（照设计文档 12.2；sparse-checkout 列表不含 enterprise 目录）：

```bash
#!/bin/bash
# 推送开源代码到GitHub（自动过滤enterprise目录）
# 用法: ./push-github-oss.sh "本次提交说明"
# 示例: ./push-github-oss.sh "fix: 修复SSE流式分片丢失问题"

COMMIT_MSG="$1"
if [ -z "$COMMIT_MSG" ]; then
  echo "用法: ./push-github-oss.sh \"提交说明\""
  echo "示例: ./push-github-oss.sh \"fix: 修复SSE流式分片丢失问题\""
  exit 1
fi

# 自动获取当前分支名，推送到同名远程分支
CURRENT_BRANCH=$(git branch --show-current)
if [ -z "$CURRENT_BRANCH" ]; then
  echo "错误: 无法获取当前分支名，请确保不在 detached HEAD 状态"
  exit 1
fi

TIMESTAMP=$(date +%Y%m%d_%H%M%S)
TEMP_BRANCH="oss-release-$TIMESTAMP"
git checkout -b "$TEMP_BRANCH"

# 使用 sparse-checkout 排除 enterprise 目录
git sparse-checkout init --cone
git sparse-checkout set pkg/core pkg/adapter pkg/plugin/interface.go pkg/plugin/oss pkg/admin pkg/config cmd webui config.yaml go.mod go.sum

# 提交过滤后的代码
git rm -r --cached pkg/plugin/enterprise 2>/dev/null || true
git commit -m "[$TIMESTAMP] $COMMIT_MSG"

# 推送到GitHub同名分支（remote 地址待用户提供后配置）
git push origin-github "$TEMP_BRANCH":"$CURRENT_BRANCH" --force

# 清理临时分支
git sparse-checkout disable
git checkout "$CURRENT_BRANCH"
git branch -D "$TEMP_BRANCH"

echo "GitHub OSS repo pushed to branch [$CURRENT_BRANCH]: [$TIMESTAMP] $COMMIT_MSG (Enterprise code excluded)"
```

`Makefile`：

```makefile
VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS := -X github.com/druidcaesa/neuralgate/pkg/core.Version=$(VERSION) \
           -X github.com/druidcaesa/neuralgate/pkg/core.BuildTime=$(BUILD_TIME) \
           -X github.com/druidcaesa/neuralgate/pkg/core.GitCommit=$(GIT_COMMIT)

.PHONY: build-oss build-enterprise run test vet

build-oss:
	go build -tags oss -ldflags "$(LDFLAGS)" -o neuralgate ./cmd/gateway/

build-enterprise:
	go build -tags enterprise -ldflags "$(LDFLAGS)" -o neuralgate-enterprise ./cmd/gateway/

run:
	go run -tags oss ./cmd/gateway/ -config config.yaml

test:
	go test -tags oss ./...

vet:
	go vet -tags oss ./...
```

`README.md`：

```markdown
# NeuralGate

AI 大模型治理网关：LLM 流量代理 + 审计 + 限流 + 模型路由（双服务隔离架构）。

- 代理服务 `:8080`：纯 net/http，处理 LLM 流量（高并发/流式/SSE）
- 管理后台 `:8081`：Gin，配置管理与日志查询

## 快速开始（骨架阶段）

```bash
make build-oss        # 编译 OSS 版（产物 neuralgate）
./neuralgate -config config.yaml
curl :8080/healthz    # 代理健康检查
curl :8081/healthz    # 后台健康检查
```

## 双版本编译

| 版本 | 命令 | 说明 |
|------|------|------|
| OSS | `make build-oss` | 开源版 |
| Enterprise | `make build-enterprise` | 含商业插件（达梦/金仓/Redis/SIEM/授权） |

## 目录结构

- `cmd/gateway/` 程序入口（双服务启动）
- `pkg/core/` 内核四层（接入层/管道层/代理内核/断连处理）
- `pkg/adapter/` 模型适配器（OpenAI/通义/智谱/DeepSeek）
- `pkg/plugin/` 插件层（接口 + oss 共享实现 + enterprise 商业实现）
- `pkg/admin/` Gin 管理后台
- `pkg/config/` 配置加载

## 双仓库发布

- `./push-private.sh "msg"` 全量推送到私有仓库
- `./push-github-oss.sh "msg"` 过滤 enterprise 后推送到 GitHub
```

（远端地址在用户提供后配置：`git remote add origin-private <地址>`、`git remote add origin-github <地址>`。）

- [ ] **步骤 6：Commit**

```bash
cd /Users/fanyanan/work/go/neuralgate
chmod +x push-private.sh push-github-oss.sh
git add go.mod go.sum config.yaml Makefile .gitignore README.md push-private.sh push-github-oss.sh pkg/config
git commit -m "chore: 项目初始化与配置加载（模块、工程化文件、config 解析）
Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## 任务 2：插件契约包

**文件：**
- 创建：`pkg/plugin/interface.go`

- [ ] **步骤 1：创建契约文件（照设计文档原样搬入）**

将 `NeuralGate_技术架构详细设计.md` 以下代码块原样复制到 `pkg/plugin/interface.go`（`package plugin`，无 BuildTag）：

| 文档章节 | 内容 | 包含类型 |
|----------|------|----------|
| 3.1 | RequestContext | 复制到 `pkg/core/context.go`（任务 3），**不在此文件** |
| 3.2 | ModelConfig | ModelConfig |
| 3.3 | APIKey / APIKeyStatus | APIKey、APIKeyStatus、APIKeyStatusActive/Disabled/Expired |
| 3.4 | AuditLog / SSEChunk | AuditLog、SSEChunk |
| 3.5 | Tenant / Role / User | Tenant、Role、User + **补全文档遗漏的** TenantStatus/UserStatus（见下） |
| 3.6 | RateLimitConfig | RateLimitConfig |
| 3.7 | LicenseInfo | LicenseInfo |
| 4.1 | PluginFactory | PluginFactory |
| 4.2 | StoragePlugin / AuditLogFilter | StoragePlugin、AuditLogFilter |
| 4.3 | AuditPipeline / AuditEvent / AuditConfig / AuditMeta | AuditPipeline、AuditEvent、AuditEventType（5 个常量）、AuditConfig、AuditMeta |
| 4.4 | RateLimitPlugin | RateLimitPlugin |
| 4.5 | LogExporter / ExporterType | LogExporter、ExporterType（3 个常量） |
| 4.6 | LicenseValidator | LicenseValidator |

**补全**（文档 3.5 引用了未定义的类型，追加到文件末尾）：

```go
// TenantStatus 租户状态
type TenantStatus string

const (
	TenantStatusActive   TenantStatus = "active"
	TenantStatusDisabled TenantStatus = "disabled"
)

// UserStatus 后台用户状态
type UserStatus string

const (
	UserStatusActive   UserStatus = "active"
	UserStatusDisabled UserStatus = "disabled"
)
```

**注意**：`RequestContext` 定义在 `pkg/core/context.go`（任务 3），其余全部照文档。文件完成后应包含：APIKeyStatus、APIKey、ModelConfig、AuditLog、SSEChunk、TenantStatus、Tenant、Role、UserStatus、User、RateLimitConfig、LicenseInfo、PluginFactory、StoragePlugin、AuditLogFilter、AuditPipeline、AuditEvent、AuditEventType、AuditConfig、AuditMeta、RateLimitPlugin、LogExporter、ExporterType、LicenseValidator。

- [ ] **步骤 2：验证编译**

运行：`go build ./pkg/plugin/`
预期：编译通过，无输出

- [ ] **步骤 3：Commit**

```bash
git add pkg/plugin/interface.go
git commit -m "feat: 插件契约层——全部接口与数据结构定义（照设计文档第3/4节）
Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## 任务 3：适配器契约包

**文件：**
- 创建：`pkg/adapter/interface.go`

- [ ] **步骤 1：创建契约文件（照设计文档原样搬入）**

将 `NeuralGate_技术架构详细设计.md` 第 4.7 节代码块（ModelAdapter 接口 + UnifiedRequest 系列全部数据结构，含 Message/ContentPart/Tool/ToolCall/ResponseFormat/UnifiedResponse/SSE 结构）原样复制到 `pkg/adapter/interface.go`（`package adapter`），并将 `ModelAdapter` 接口替换为 8.3 节增强版（含 `SupportsNativeProxy()`、`TransformRequest(req *UnifiedRequest, rawBody []byte)`、`ParseStreamUsage(chunk []byte)`）。

文件应包含类型：ModelAdapter、UnifiedRequest、Message、ContentPart、ImageURLPart、AudioPart、Tool、ToolFunction、ToolCall、ToolCallFunction、ResponseFormat、ResponseJSONSchema、StreamOptions、UnifiedResponse、Choice、TokenUsage、LogprobsResult、LogprobContent、LogprobTokenAlt、UnifiedSSEChunk、SSEChoice。

- [ ] **步骤 2：验证编译**

运行：`go build ./pkg/adapter/`
预期：编译通过，无输出

- [ ] **步骤 3：Commit**

```bash
git add pkg/adapter/interface.go
git commit -m "feat: 适配器契约层——ModelAdapter 接口与统一请求/响应结构（照设计文档4.7/8.3）
Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## 任务 4：核心上下文与版本信息

**文件：**
- 创建：`pkg/core/context.go`、`pkg/core/version.go`、`pkg/core/context_test.go`

- [ ] **步骤 1：编写失败的测试**

创建 `pkg/core/context_test.go`：

```go
package core

import (
	"context"
	"testing"
)

func TestRequestContextRoundTrip(t *testing.T) {
	ctx := context.Background()
	rc := &RequestContext{RequestID: "r1"}
	ctx = WithRequestContext(ctx, rc)
	got, ok := RequestContextFrom(ctx)
	if !ok {
		t.Fatal("RequestContextFrom() ok = false, want true")
	}
	if got != rc {
		t.Error("RequestContextFrom() returned different pointer")
	}
}

func TestRequestContextFromEmpty(t *testing.T) {
	_, ok := RequestContextFrom(context.Background())
	if ok {
		t.Error("RequestContextFrom() ok = true on empty context, want false")
	}
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./pkg/core/ -run TestRequestContext -v`
预期：FAIL，`undefined: RequestContext`（测试引用的类型不存在，编译失败）

- [ ] **步骤 3：实现 context.go 与 version.go**

创建 `pkg/core/context.go`：

```go
package core

import (
	"context"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/adapter"
	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// RequestContext 贯穿整个请求生命周期的上下文（照设计文档 3.1）
type RequestContext struct {
	RequestID     string             // 全局唯一请求ID
	TenantID      string             // 租户ID
	APIKeyID      string             // API Key ID
	ModelConfig   *plugin.ModelConfig // 匹配到的模型配置
	Adapter       adapter.ModelAdapter // 模型适配器实例
	StartTime     time.Time          // 请求开始时间
	ClientIP      string             // 客户端IP
	RequestMethod string             // HTTP方法
	RequestPath   string             // 请求路径
	RequestHeaders map[string]string // 请求头
	RequestBody   []byte             // 请求体
	ResponseStatus int               // 响应状态码
	ResponseBody  []byte             // 响应体（非流式）
	SSEChunks     []plugin.SSEChunk  // SSE分片列表（流式）
	EndTime       time.Time          // 请求结束时间
	PromptTokens  int                // Prompt Token数
	CompletionTokens int             // Completion Token数
	TotalTokens   int                // 总Token数
	Error         error              // 错误信息
	IsStream      bool               // 是否流式请求
	Disconnected  bool               // 客户端是否断开
}

// requestContextKey 中间件链传递 RequestContext 的 context key
type requestContextKey struct{}

// WithRequestContext 将 RequestContext 写入 context
func WithRequestContext(ctx context.Context, rc *RequestContext) context.Context {
	return context.WithValue(ctx, requestContextKey{}, rc)
}

// RequestContextFrom 从 context 取出 RequestContext
func RequestContextFrom(ctx context.Context) (*RequestContext, bool) {
	rc, ok := ctx.Value(requestContextKey{}).(*RequestContext)
	return rc, ok
}
```

创建 `pkg/core/version.go`：

```go
package core

// VersionInfo 版本信息（照设计文档 7.3）
type VersionInfo struct {
	Version   string   // 版本号
	BuildTime string   // 编译时间
	GitCommit string   // Git提交
	Edition   string   // 版本类型：oss/enterprise
	Features  []string // 可用功能列表
}

// 以下变量通过 Makefile -ldflags 注入
var (
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"
)
```

（本任务依赖 `pkg/plugin`（任务 2）与 `pkg/adapter`（任务 3）的接口文件，按顺序执行即可。）

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./pkg/core/ -v`
预期：PASS

- [ ] **步骤 5：Commit**

```bash
git add pkg/core/context.go pkg/core/version.go pkg/core/context_test.go
git commit -m "feat: 核心层 RequestContext 与版本信息定义
Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## 任务 5：环形队列

**文件：**
- 创建：`pkg/plugin/oss/ring_buffer.go`、`pkg/plugin/oss/ring_buffer_test.go`

- [ ] **步骤 1：编写失败的测试**

创建 `pkg/plugin/oss/ring_buffer_test.go`：

```go
package oss

import (
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

func TestPushPop(t *testing.T) {
	rb := NewRingBuffer(4)
	ev := &plugin.AuditEvent{RequestID: "r1", EventType: plugin.AuditEventRequestStart}
	if err := rb.Push(ev); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	got, err := rb.Pop()
	if err != nil {
		t.Fatalf("Pop() error = %v", err)
	}
	if got != ev {
		t.Error("Pop() returned different event")
	}
}

func TestPushBlocksWhenFull(t *testing.T) {
	rb := NewRingBuffer(2)
	if err := rb.Push(&plugin.AuditEvent{RequestID: "r1"}); err != nil {
		t.Fatal(err)
	}
	if err := rb.Push(&plugin.AuditEvent{RequestID: "r2"}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		_ = rb.Push(&plugin.AuditEvent{RequestID: "r3"}) // 队列满，应阻塞
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("Push() should block when buffer is full")
	case <-time.After(100 * time.Millisecond):
	}
	// 消费一个，Push 应解除阻塞
	if _, err := rb.Pop(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("Push() did not unblock after Pop()")
	}
}

func TestPopBlocksWhenEmpty(t *testing.T) {
	rb := NewRingBuffer(4)
	done := make(chan struct{})
	go func() {
		_, _ = rb.Pop() // 队列空，应阻塞
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("Pop() should block when buffer is empty")
	case <-time.After(100 * time.Millisecond):
	}
	if err := rb.Push(&plugin.AuditEvent{RequestID: "r1"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("Pop() did not unblock after Push()")
	}
}

func TestCloseUnblocksAndRejects(t *testing.T) {
	rb := NewRingBuffer(4)
	done := make(chan struct{})
	go func() {
		_, _ = rb.Pop()
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	if err := rb.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("Close() did not unblock Pop()")
	}
	if err := rb.Push(&plugin.AuditEvent{RequestID: "r1"}); err != ErrBufferClosed {
		t.Errorf("Push() after Close() = %v, want ErrBufferClosed", err)
	}
}

func TestNewRingBufferInvalidSize(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewRingBuffer(0) should panic")
		}
	}()
	NewRingBuffer(0)
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./pkg/plugin/oss/ -run TestRing -v`
预期：FAIL，`undefined: NewRingBuffer`（编译失败）

- [ ] **步骤 3：实现环形队列（照设计文档 5.3）**

创建 `pkg/plugin/oss/ring_buffer.go`：

```go
package oss

import (
	"errors"
	"sync"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// ErrBufferClosed 环形队列已关闭
var ErrBufferClosed = errors.New("ring buffer closed")

// RingBuffer 环形队列（照设计文档 5.3）
// 固定大小内存预分配；队列满时阻塞写入方，队列空时阻塞消费方；
// 支持优雅关闭：Shutdown 后不再接收新数据，flush 剩余数据
type RingBuffer struct {
	buf           []*plugin.AuditEvent
	size          int
	head          int // 写入位置
	tail          int // 读取位置
	mu            sync.Mutex
	notFull       *sync.Cond
	notEmpty      *sync.Cond
	closed        bool
	overflowCount int64 // 溢出计数（保留字段，Phase 7 使用）
}

// NewRingBuffer 创建环形队列；size 必须为正数，否则 panic
func NewRingBuffer(size int) *RingBuffer {
	if size <= 0 {
		panic("ring buffer size must be positive")
	}
	rb := &RingBuffer{
		buf:  make([]*plugin.AuditEvent, size),
		size: size,
	}
	rb.notFull = sync.NewCond(&rb.mu)
	rb.notEmpty = sync.NewCond(&rb.mu)
	return rb
}

// Push 写入事件；队列满时阻塞等待；关闭后返回 ErrBufferClosed
func (rb *RingBuffer) Push(event *plugin.AuditEvent) error {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	for rb.isFull() {
		if rb.closed {
			return ErrBufferClosed
		}
		rb.notFull.Wait()
	}
	if rb.closed {
		return ErrBufferClosed
	}
	rb.buf[rb.head] = event
	rb.head = (rb.head + 1) % rb.size
	rb.notEmpty.Signal()
	return nil
}

// Pop 取出事件；队列空时阻塞等待；关闭且队列空时返回 ErrBufferClosed
// （关闭后队列中剩余数据仍可 Pop 取出）
func (rb *RingBuffer) Pop() (*plugin.AuditEvent, error) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	for rb.isEmpty() {
		if rb.closed {
			return nil, ErrBufferClosed
		}
		rb.notEmpty.Wait()
	}
	ev := rb.buf[rb.tail]
	rb.buf[rb.tail] = nil
	rb.tail = (rb.tail + 1) % rb.size
	rb.notFull.Signal()
	return ev, nil
}

// Close 关闭队列，唤醒所有阻塞的 Push/Pop
func (rb *RingBuffer) Close() error {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.closed = true
	rb.notFull.Broadcast()
	rb.notEmpty.Broadcast()
	return nil
}

// OverflowCount 返回溢出计数（骨架期恒为 0）
func (rb *RingBuffer) OverflowCount() int64 {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return rb.overflowCount
}

func (rb *RingBuffer) isFull() bool {
	return (rb.head+1)%rb.size == rb.tail
}

func (rb *RingBuffer) isEmpty() bool {
	return rb.head == rb.tail
}
```

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./pkg/plugin/oss/ -v`
预期：PASS（5 个测试全部通过）

- [ ] **步骤 5：Commit**

```bash
git add pkg/plugin/oss/ring_buffer.go pkg/plugin/oss/ring_buffer_test.go
git commit -m "feat: 环形队列实现（阻塞语义，照设计文档5.3）
Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## 任务 6：内存存储

**文件：**
- 创建：`pkg/plugin/oss/storage_mem.go`、`pkg/plugin/oss/storage_mem_test.go`

- [ ] **步骤 1：编写失败的测试**

创建 `pkg/plugin/oss/storage_mem_test.go`：

```go
package oss

import (
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

func TestAPIKeyCRUD(t *testing.T) {
	s := NewMemStorage()
	if err := s.Init(nil); err != nil {
		t.Fatal(err)
	}
	key := &plugin.APIKey{
		ID:        "k1",
		KeyHash:   "hash1",
		KeyPrefix: "ng-test",
		TenantID:  "t1",
		Name:      "测试Key",
		Status:    plugin.APIKeyStatusActive,
		Quota:     1000,
		CreatedAt: time.Now(),
	}
	if err := s.SaveAPIKey(key); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetAPIKey("hash1")
	if err != nil {
		t.Fatalf("GetAPIKey() error = %v", err)
	}
	if got.ID != "k1" || got.TenantID != "t1" {
		t.Errorf("GetAPIKey() = %+v, want ID=k1 TenantID=t1", got)
	}
	if _, err := s.GetAPIKey("nope"); err != ErrNotFound {
		t.Errorf("GetAPIKey(missing) = %v, want ErrNotFound", err)
	}
	if err := s.UpdateAPIKeyQuota("k1", 500); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetAPIKey("hash1"); got.UsedQuota != 500 {
		t.Errorf("UsedQuota = %d, want 500", got.UsedQuota)
	}
	keys, total, err := s.ListAPIKeys("t1", 1, 10)
	if err != nil || total != 1 || len(keys) != 1 {
		t.Errorf("ListAPIKeys() = %d items/%d total, want 1/1", len(keys), total)
	}
	if err := s.DeleteAPIKey("k1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetAPIKey("hash1"); err != ErrNotFound {
		t.Errorf("GetAPIKey(after delete) = %v, want ErrNotFound", err)
	}
}

func TestModelConfigCRUD(t *testing.T) {
	s := NewMemStorage()
	cfg := &plugin.ModelConfig{
		ID:            "m1",
		ModelName:     "gpt-4",
		Provider:      "openai",
		ProviderModel: "gpt-4",
		BaseURL:       "https://api.openai.com/v1",
		Enabled:       true,
	}
	if err := s.SaveModelConfig(cfg); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetModelConfig("gpt-4")
	if err != nil || got.Provider != "openai" {
		t.Fatalf("GetModelConfig() = %+v, %v", got, err)
	}
	cfgs, total, err := s.ListModelConfigs(1, 10)
	if err != nil || total != 1 || len(cfgs) != 1 {
		t.Errorf("ListModelConfigs() = %d/%d, want 1/1", len(cfgs), total)
	}
	if err := s.DeleteModelConfig("m1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetModelConfig("gpt-4"); err != ErrNotFound {
		t.Errorf("GetModelConfig(after delete) = %v, want ErrNotFound", err)
	}
}

func TestAuditLogSaveAndQuery(t *testing.T) {
	s := NewMemStorage()
	now := time.Now()
	logs := []*plugin.AuditLog{
		{ID: "a1", RequestID: "r1", TenantID: "t1", ModelName: "gpt-4", ResponseStatus: 200, RequestBody: `{"model":"gpt-4"}`, CreatedAt: now},
		{ID: "a2", RequestID: "r2", TenantID: "t2", ModelName: "qwen-max", ResponseStatus: 429, RequestBody: `{"model":"qwen-max"}`, CreatedAt: now.Add(-time.Hour)},
	}
	if err := s.BatchSaveAuditLogs(logs); err != nil {
		t.Fatal(err)
	}
	got, total, err := s.QueryAuditLogs(plugin.AuditLogFilter{TenantID: "t1"}, 1, 10)
	if err != nil || total != 1 || len(got) != 1 || got[0].ID != "a1" {
		t.Errorf("QueryAuditLogs(tenant) = %d/%d %+v, want 1/1", len(got), total, err)
	}
	got, total, _ = s.QueryAuditLogs(plugin.AuditLogFilter{ModelName: "qwen-max"}, 1, 10)
	if total != 1 || got[0].ID != "a2" {
		t.Errorf("QueryAuditLogs(model) total = %d, want 1", total)
	}
	got, total, _ = s.QueryAuditLogs(plugin.AuditLogFilter{Keyword: "gpt-4"}, 1, 10)
	if total != 1 || got[0].ID != "a1" {
		t.Errorf("QueryAuditLogs(keyword) total = %d, want 1", total)
	}
	// 分页
	for i := 0; i < 5; i++ {
		_ = s.SaveAuditLog(&plugin.AuditLog{ID: "b" + string(rune('0'+i)), RequestID: "x" + string(rune('0'+i)), CreatedAt: now})
	}
	got, total, _ = s.QueryAuditLogs(plugin.AuditLogFilter{}, 2, 3)
	if total != 7 || len(got) != 3 {
		t.Errorf("QueryAuditLogs(page2) = %d/%d, want 3/7", len(got), total)
	}
}

func TestPingAndClose(t *testing.T) {
	s := NewMemStorage()
	if err := s.Ping(); err != nil {
		t.Errorf("Ping() error = %v", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./pkg/plugin/oss/ -run TestAPIKey -v`
预期：FAIL，`undefined: NewMemStorage`（编译失败）

- [ ] **步骤 3：实现内存存储**

创建 `pkg/plugin/oss/storage_mem.go`：

```go
package oss

import (
	"errors"
	"sort"
	"strings"
	"sync"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// ErrNotFound 记录不存在
var ErrNotFound = errors.New("record not found")

// MemStorage 内存存储实现（骨架期使用，Phase 3 替换为 MySQL/SQLite）
type MemStorage struct {
	mu           sync.RWMutex
	apiKeys      map[string]*plugin.APIKey      // keyHash -> key
	modelConfigs map[string]*plugin.ModelConfig // modelName -> config
	auditLogs    []*plugin.AuditLog             // 按写入顺序
}

// NewMemStorage 创建内存存储
func NewMemStorage() *MemStorage {
	return &MemStorage{
		apiKeys:      make(map[string]*plugin.APIKey),
		modelConfigs: make(map[string]*plugin.ModelConfig),
	}
}

// Init 初始化存储连接（内存实现无需连接）
func (s *MemStorage) Init(config map[string]interface{}) error { return nil }

// ===== API Key 管理 =====

func (s *MemStorage) GetAPIKey(keyHash string) (*plugin.APIKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if k, ok := s.apiKeys[keyHash]; ok {
		return k, nil
	}
	return nil, ErrNotFound
}

func (s *MemStorage) SaveAPIKey(key *plugin.APIKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.apiKeys[key.KeyHash] = key
	return nil
}

func (s *MemStorage) UpdateAPIKeyQuota(keyID string, usedQuota int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range s.apiKeys {
		if k.ID == keyID {
			k.UsedQuota = usedQuota
			return nil
		}
	}
	return ErrNotFound
}

func (s *MemStorage) ListAPIKeys(tenantID string, page, size int) ([]*plugin.APIKey, int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var all []*plugin.APIKey
	for _, k := range s.apiKeys {
		if tenantID == "" || k.TenantID == tenantID {
			all = append(all, k)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	page, size = normalizePage(page, size)
	start := (page - 1) * size
	if start > len(all) {
		start = len(all)
	}
	end := start + size
	if end > len(all) {
		end = len(all)
	}
	return all[start:end], int64(len(all)), nil
}

func (s *MemStorage) DeleteAPIKey(keyID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for hash, k := range s.apiKeys {
		if k.ID == keyID {
			delete(s.apiKeys, hash)
			return nil
		}
	}
	return ErrNotFound
}

// ===== 模型配置管理 =====

func (s *MemStorage) GetModelConfig(modelName string) (*plugin.ModelConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if c, ok := s.modelConfigs[modelName]; ok {
		return c, nil
	}
	return nil, ErrNotFound
}

func (s *MemStorage) ListModelConfigs(page, size int) ([]*plugin.ModelConfig, int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var all []*plugin.ModelConfig
	for _, c := range s.modelConfigs {
		all = append(all, c)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ModelName < all[j].ModelName })
	page, size = normalizePage(page, size)
	start := (page - 1) * size
	if start > len(all) {
		start = len(all)
	}
	end := start + size
	if end > len(all) {
		end = len(all)
	}
	return all[start:end], int64(len(all)), nil
}

func (s *MemStorage) SaveModelConfig(config *plugin.ModelConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.modelConfigs[config.ModelName] = config
	return nil
}

func (s *MemStorage) DeleteModelConfig(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, c := range s.modelConfigs {
		if c.ID == id {
			delete(s.modelConfigs, name)
			return nil
		}
	}
	return ErrNotFound
}

// ===== 审计日志 =====

func (s *MemStorage) SaveAuditLog(log *plugin.AuditLog) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.auditLogs = append(s.auditLogs, log)
	return nil
}

func (s *MemStorage) BatchSaveAuditLogs(logs []*plugin.AuditLog) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.auditLogs = append(s.auditLogs, logs...)
	return nil
}

func (s *MemStorage) QueryAuditLogs(filter plugin.AuditLogFilter, page, size int) ([]*plugin.AuditLog, int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var matched []*plugin.AuditLog
	for _, l := range s.auditLogs {
		if !matchAuditLog(l, filter) {
			continue
		}
		matched = append(matched, l)
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].CreatedAt.After(matched[j].CreatedAt) })
	page, size = normalizePage(page, size)
	start := (page - 1) * size
	if start > len(matched) {
		start = len(matched)
	}
	end := start + size
	if end > len(matched) {
		end = len(matched)
	}
	return matched[start:end], int64(len(matched)), nil
}

// ===== 健康检查 =====

func (s *MemStorage) Ping() error { return nil }

func (s *MemStorage) Close() error { return nil }

// normalizePage 分页参数规范化：page>=1，size 取 [1,100]
func normalizePage(page, size int) (int, int) {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 10
	}
	if size > 100 {
		size = 100
	}
	return page, size
}

// matchAuditLog 按过滤器匹配审计日志
func matchAuditLog(l *plugin.AuditLog, f plugin.AuditLogFilter) bool {
	if f.TenantID != "" && l.TenantID != f.TenantID {
		return false
	}
	if f.APIKeyID != "" && l.APIKeyID != f.APIKeyID {
		return false
	}
	if f.ModelName != "" && l.ModelName != f.ModelName {
		return false
	}
	if f.Status != 0 && l.ResponseStatus != f.Status {
		return false
	}
	if f.IsStream != nil && l.IsStream != *f.IsStream {
		return false
	}
	if f.StartTime != nil && l.CreatedAt.Before(*f.StartTime) {
		return false
	}
	if f.EndTime != nil && l.CreatedAt.After(*f.EndTime) {
		return false
	}
	if f.Keyword != "" {
		haystack := strings.Join([]string{
			l.RequestID, l.TenantID, l.APIKeyID, l.ModelName,
			l.RequestBody, l.ResponseBody, l.DisconnectReason,
		}, " ")
		if !strings.Contains(haystack, f.Keyword) {
			return false
		}
	}
	return true
}
```

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./pkg/plugin/oss/ -v`
预期：PASS（9 个测试全部通过，含任务 5 的 5 个）

- [ ] **步骤 5：Commit**

```bash
git add pkg/plugin/oss/storage_mem.go pkg/plugin/oss/storage_mem_test.go
git commit -m "feat: 内存存储实现（骨架期替代 MySQL/SQLite，接口契约完整）
Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## 任务 7：简单审计与内存限流

**文件：**
- 创建：`pkg/plugin/oss/audit_simple.go`、`pkg/plugin/oss/audit_simple_test.go`、`pkg/plugin/oss/limit_mem.go`、`pkg/plugin/oss/limit_mem_test.go`

- [ ] **步骤 1：编写失败的测试**

创建 `pkg/plugin/oss/audit_simple_test.go`：

```go
package oss

import (
	"testing"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

func TestFinalizeSavesAuditLog(t *testing.T) {
	storage := NewMemStorage()
	auditor := NewSimpleAuditor(storage)
	if err := auditor.Init(plugin.AuditConfig{}); err != nil {
		t.Fatal(err)
	}
	_ = auditor.Submit(&plugin.AuditEvent{RequestID: "r1", EventType: plugin.AuditEventRequestStart})
	_ = auditor.SubmitSSEChunk("r1", &plugin.SSEChunk{Index: 0, Data: "data: {\"choices\":[]}"})
	_ = auditor.Finalize("r1", &plugin.AuditMeta{
		ResponseStatus:   200,
		PromptTokens:     10,
		CompletionTokens: 5,
		TotalTokens:      15,
		Duration:         120,
	})
	logs, total, err := storage.QueryAuditLogs(plugin.AuditLogFilter{}, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("total = %d, want 1", total)
	}
	l := logs[0]
	if l.RequestID != "r1" || l.ResponseStatus != 200 || l.TotalTokens != 15 {
		t.Errorf("audit log = %+v, want r1/200/15", l)
	}
	if len(l.SSEChunks) != 1 || l.SSEChunks[0].Index != 0 {
		t.Errorf("SSEChunks = %+v, want 1 chunk", l.SSEChunks)
	}
}

func TestMarkDisconnectSavesLog(t *testing.T) {
	storage := NewMemStorage()
	auditor := NewSimpleAuditor(storage)
	_ = auditor.SubmitSSEChunk("r2", &plugin.SSEChunk{Index: 0, Data: "data: hello"})
	_ = auditor.MarkDisconnect("r2", "client_closed_connection")
	logs, total, _ := storage.QueryAuditLogs(plugin.AuditLogFilter{}, 1, 10)
	if total != 1 {
		t.Fatalf("total = %d, want 1", total)
	}
	if !logs[0].Disconnected || logs[0].DisconnectReason != "client_closed_connection" {
		t.Errorf("log = %+v, want Disconnected=true", logs[0])
	}
}

func TestShutdownFlushesPending(t *testing.T) {
	storage := NewMemStorage()
	auditor := NewSimpleAuditor(storage)
	_ = auditor.SubmitSSEChunk("r3", &plugin.SSEChunk{Index: 0, Data: "data: hi"})
	if err := auditor.Shutdown(); err != nil {
		t.Fatal(err)
	}
	_, total, _ := storage.QueryAuditLogs(plugin.AuditLogFilter{}, 1, 10)
	if total != 1 {
		t.Errorf("total after Shutdown = %d, want 1", total)
	}
}
```

创建 `pkg/plugin/oss/limit_mem_test.go`：

```go
package oss

import (
	"testing"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

func TestMemRateLimiterAllow(t *testing.T) {
	rl := NewMemRateLimiter()
	if err := rl.Init(map[string]interface{}{"default_rps": 3}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		allowed, _, err := rl.Allow("t1", "gpt-4", 10)
		if err != nil || !allowed {
			t.Fatalf("Allow() #%d = %v, %v; want allowed", i+1, allowed, err)
		}
	}
	allowed, remaining, _ := rl.Allow("t1", "gpt-4", 10)
	if allowed || remaining != 0 {
		t.Errorf("Allow() #4 = %v/%d, want false/0", allowed, remaining)
	}
}

func TestMemRateLimiterWindowReset(t *testing.T) {
	rl := NewMemRateLimiter()
	_ = rl.Init(map[string]interface{}{"default_rps": 1})
	allowed, _, _ := rl.Allow("t1", "gpt-4", 0)
	if !allowed {
		t.Fatal("first Allow() should be allowed")
	}
	allowed, _, _ = rl.Allow("t1", "gpt-4", 0)
	if allowed {
		t.Fatal("second Allow() should be rejected")
	}
	// Status 应显示重置时间在当前窗口之后
	current, limit, _ := rl.Status("t1", "gpt-4")
	if current != 2 || limit != 1 {
		t.Errorf("Status() = %d/%d, want 2/1", current, limit)
	}
}

func TestMemRateLimiterReset(t *testing.T) {
	rl := NewMemRateLimiter()
	_ = rl.Init(map[string]interface{}{"default_rps": 1})
	_, _ = rl.Allow("t1", "gpt-4", 0)
	_ = rl.Reset("t1", "gpt-4")
	allowed, _, _ := rl.Allow("t1", "gpt-4", 0)
	if !allowed {
		t.Error("Allow() after Reset() should be allowed")
	}
}

func TestMemRateLimiterStatus(t *testing.T) {
	rl := NewMemRateLimiter()
	_ = rl.Init(map[string]interface{}{"default_rps": 10})
	current, limit, resetAt := rl.Status("t1", "gpt-4")
	if current != 0 || limit != 10 || resetAt.IsZero() {
		t.Errorf("Status() = %d/%d/%v, want 0/10/non-zero", current, limit, resetAt)
	}
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./pkg/plugin/oss/ -run TestFinalize -v`
预期：FAIL，`undefined: NewSimpleAuditor`（编译失败）

- [ ] **步骤 3：实现简单审计**

创建 `pkg/plugin/oss/audit_simple.go`：

```go
package oss

import (
	"sync"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// SimpleAuditor 简单同步审计（OSS 版，照设计文档 6.3）
// 分片与元数据在请求结束时同步组装落库
type SimpleAuditor struct {
	storage plugin.StoragePlugin
	mu      sync.Mutex
	pending map[string]*plugin.AuditLog // requestID -> 组装中的日志
}

// NewSimpleAuditor 创建简单审计器
func NewSimpleAuditor(storage plugin.StoragePlugin) *SimpleAuditor {
	return &SimpleAuditor{
		storage: storage,
		pending: make(map[string]*plugin.AuditLog),
	}
}

// Init 初始化审计管道
func (a *SimpleAuditor) Init(config plugin.AuditConfig) error { return nil }

// Submit 提交审计事件（骨架期仅处理请求开始事件）
func (a *SimpleAuditor) Submit(event *plugin.AuditEvent) error {
	if event.EventType != plugin.AuditEventRequestStart {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.pending[event.RequestID]; !ok {
		a.pending[event.RequestID] = &plugin.AuditLog{
			ID:        event.RequestID,
			RequestID: event.RequestID,
			CreatedAt: event.Timestamp,
		}
	}
	return nil
}

// BatchSubmit 批量提交
func (a *SimpleAuditor) BatchSubmit(events []*plugin.AuditEvent) error {
	for _, ev := range events {
		if err := a.Submit(ev); err != nil {
			return err
		}
	}
	return nil
}

// SubmitSSEChunk 提交流式分片，追加到组装中的日志
func (a *SimpleAuditor) SubmitSSEChunk(requestID string, chunk *plugin.SSEChunk) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	log, ok := a.pending[requestID]
	if !ok {
		log = &plugin.AuditLog{ID: requestID, RequestID: requestID}
		a.pending[requestID] = log
	}
	log.SSEChunks = append(log.SSEChunks, *chunk)
	return nil
}

// Finalize 标记请求结束，组装完整日志并落库
func (a *SimpleAuditor) Finalize(requestID string, meta *plugin.AuditMeta) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	log, ok := a.pending[requestID]
	if !ok {
		log = &plugin.AuditLog{ID: requestID, RequestID: requestID}
		a.pending[requestID] = log
	}
	log.ResponseStatus = meta.ResponseStatus
	log.PromptTokens = meta.PromptTokens
	log.CompletionTokens = meta.CompletionTokens
	log.TotalTokens = meta.TotalTokens
	log.Duration = meta.Duration
	if err := a.storage.SaveAuditLog(log); err != nil {
		return err
	}
	delete(a.pending, requestID)
	return nil
}

// MarkDisconnect 标记客户端断连，保存已收集内容
func (a *SimpleAuditor) MarkDisconnect(requestID string, reason string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	log, ok := a.pending[requestID]
	if !ok {
		log = &plugin.AuditLog{ID: requestID, RequestID: requestID}
		a.pending[requestID] = log
	}
	log.Disconnected = true
	log.DisconnectReason = reason
	if err := a.storage.SaveAuditLog(log); err != nil {
		return err
	}
	delete(a.pending, requestID)
	return nil
}

// Shutdown 关闭管道，flush 剩余数据
func (a *SimpleAuditor) Shutdown() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for requestID, log := range a.pending {
		if err := a.storage.SaveAuditLog(log); err != nil {
			return err
		}
		delete(a.pending, requestID)
	}
	return nil
}
```

创建 `pkg/plugin/oss/limit_mem.go`：

```go
package oss

import (
	"sync"
	"time"
)

// windowEntry 固定窗口计数（每秒重置）
type windowEntry struct {
	count       int
	windowStart time.Time
}

// MemRateLimiter 内存限流（固定窗口，骨架期最简实现；令牌桶 Phase 4 精细化）
type MemRateLimiter struct {
	mu         sync.Mutex
	windows    map[string]*windowEntry
	defaultRPS int
}

// NewMemRateLimiter 创建内存限流器
func NewMemRateLimiter() *MemRateLimiter {
	return &MemRateLimiter{
		windows:    make(map[string]*windowEntry),
		defaultRPS: 10,
	}
}

// Init 初始化限流配置，支持 "default_rps" (int) 配置项
func (l *MemRateLimiter) Init(config map[string]interface{}) error {
	if v, ok := config["default_rps"]; ok {
		if rps, ok := v.(int); ok && rps > 0 {
			l.defaultRPS = rps
		}
	}
	return nil
}

// Allow 尝试获取令牌：当前秒窗口内计数未超过上限则允许
func (l *MemRateLimiter) Allow(tenantID string, model string, tokens int) (bool, int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := tenantID + "|" + model
	now := time.Now()
	e, ok := l.windows[key]
	if !ok || now.Sub(e.windowStart) >= time.Second {
		e = &windowEntry{windowStart: now}
		l.windows[key] = e
	}
	e.count++
	if e.count > l.defaultRPS {
		return false, 0, nil
	}
	return true, int64(l.defaultRPS - e.count), nil
}

// Status 获取当前限流状态
func (l *MemRateLimiter) Status(tenantID string, model string) (int64, int64, time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := tenantID + "|" + model
	e, ok := l.windows[key]
	if !ok {
		return 0, int64(l.defaultRPS), time.Now().Add(time.Second)
	}
	return int64(e.count), int64(l.defaultRPS), e.windowStart.Add(time.Second)
}

// Reset 重置限流计数器
func (l *MemRateLimiter) Reset(tenantID string, model string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.windows, tenantID+"|"+model)
	return nil
}
```

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./pkg/plugin/oss/ -v`
预期：PASS（16 个测试全部通过）

- [ ] **步骤 5：Commit**

```bash
git add pkg/plugin/oss/audit_simple.go pkg/plugin/oss/audit_simple_test.go pkg/plugin/oss/limit_mem.go pkg/plugin/oss/limit_mem_test.go
git commit -m "feat: OSS 简单审计与内存限流实现
Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## 任务 8：BuildTag 工厂

**文件：**
- 创建：`pkg/plugin/oss/factory.go`、`pkg/plugin/enterprise/factory.go`

- [ ] **步骤 1：实现 OSS 工厂**

创建 `pkg/plugin/oss/factory.go`（package oss，**无 BuildTag**，两版本都编译）：

```go
package oss

import "github.com/druidcaesa/neuralgate/pkg/plugin"

// ossFactory OSS 工厂（照设计文档 6.2：仅注册 OSS 实现）
type ossFactory struct {
	storage plugin.StoragePlugin
}

// NewPluginFactory 返回 OSS 版插件工厂
func NewPluginFactory() plugin.PluginFactory {
	return &ossFactory{}
}

// CreateStorage 创建内存存储（骨架期；Phase 3 按 config.driver 分发 mysql/sqlite）
func (f *ossFactory) CreateStorage() plugin.StoragePlugin {
	if f.storage == nil {
		f.storage = NewMemStorage()
	}
	return f.storage
}

// CreateAuditor 创建简单审计器（与 CreateStorage 共享同一存储实例）
func (f *ossFactory) CreateAuditor() plugin.AuditPipeline {
	return NewSimpleAuditor(f.CreateStorage())
}

// CreateRateLimiter 创建内存限流器
func (f *ossFactory) CreateRateLimiter() plugin.RateLimitPlugin {
	return NewMemRateLimiter()
}

// CreateExporter OSS 版无日志外推
func (f *ossFactory) CreateExporter() plugin.LogExporter { return nil }

// CreateLicenseValidator OSS 版无授权校验
func (f *ossFactory) CreateLicenseValidator() plugin.LicenseValidator { return nil }
```

- [ ] **步骤 2：实现 Enterprise 工厂（骨架期复用 OSS 实现）**

创建 `pkg/plugin/enterprise/factory.go`：

```go
//go:build enterprise

package enterprise

import (
	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
)

// enterpriseFactory Enterprise 工厂（照设计文档 6.2：OSS 实现 + 商业增强）
// 骨架期全部复用 OSS 实现；达梦/金仓/Redis/SIEM/授权为 Phase 7 内容
type enterpriseFactory struct {
	storage plugin.StoragePlugin
}

// NewPluginFactory 返回 Enterprise 版插件工厂
func NewPluginFactory() plugin.PluginFactory {
	return &enterpriseFactory{}
}

// CreateStorage 创建存储（骨架期：内存存储；Phase 7 按 config.driver 支持达梦/金仓）
func (f *enterpriseFactory) CreateStorage() plugin.StoragePlugin {
	if f.storage == nil {
		f.storage = oss.NewMemStorage()
	}
	return f.storage
}

// CreateAuditor 创建审计器（骨架期：简单审计；Phase 7 切换流式审计）
func (f *enterpriseFactory) CreateAuditor() plugin.AuditPipeline {
	return oss.NewSimpleAuditor(f.CreateStorage())
}

// CreateRateLimiter 创建限流器（骨架期：内存限流；Phase 7 支持 Redis，不可用时降级）
func (f *enterpriseFactory) CreateRateLimiter() plugin.RateLimitPlugin {
	return oss.NewMemRateLimiter()
}

// CreateExporter 日志外推（Phase 7 实现 SIEM/Syslog/Kafka）
func (f *enterpriseFactory) CreateExporter() plugin.LogExporter { return nil }

// CreateLicenseValidator 授权校验（Phase 7 实现）
func (f *enterpriseFactory) CreateLicenseValidator() plugin.LicenseValidator { return nil }
```

- [ ] **步骤 3：双版本编译验证**

运行：

```bash
go build -tags oss ./pkg/...
go build -tags enterprise ./pkg/...
```

预期：两次均编译通过，无输出

- [ ] **步骤 4：Commit**

```bash
git add pkg/plugin/oss/factory.go pkg/plugin/enterprise/factory.go
git commit -m "feat: BuildTag 双版本工厂（OSS + Enterprise 骨架期复用 OSS 实现）
Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## 任务 9：适配器注册中心与适配器壳

**文件：**
- 创建：`pkg/adapter/registry.go`、`pkg/adapter/registry_test.go`、`pkg/adapter/openai.go`、`pkg/adapter/tongyi.go`、`pkg/adapter/zhipu.go`、`pkg/adapter/deepseek.go`、`pkg/adapter/adapters_test.go`

- [ ] **步骤 1：编写失败的测试**

创建 `pkg/adapter/registry_test.go`：

```go
package adapter

import (
	"errors"
	"testing"
)

func TestRegisterAndGet(t *testing.T) {
	r := NewAdapterRegistry()
	r.Register(NewOpenAIAdapter())
	got, err := r.Get("openai")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Name() != "openai" {
		t.Errorf("Name() = %q, want openai", got.Name())
	}
}

func TestGetUnknown(t *testing.T) {
	r := NewAdapterRegistry()
	_, err := r.Get("unknown-provider")
	if !errors.Is(err, ErrAdapterNotFound) {
		t.Errorf("Get() error = %v, want ErrAdapterNotFound", err)
	}
}

func TestRegisterOverwrites(t *testing.T) {
	r := NewAdapterRegistry()
	r.Register(NewOpenAIAdapter())
	r.Register(&fakeAdapter{name: "openai"})
	got, err := r.Get("openai")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name() != "openai" {
		t.Errorf("overwrite failed, Name() = %q", got.Name())
	}
}

// fakeAdapter 用于覆盖注册测试的替代适配器
type fakeAdapter struct {
	name string
}

func (a *fakeAdapter) Name() string              { return a.name }
func (a *fakeAdapter) SupportsNativeProxy() bool { return false }
func (a *fakeAdapter) TransformRequest(req *UnifiedRequest, rawBody []byte) (*http.Request, error) {
	return nil, ErrAdapterNotFound
}
func (a *fakeAdapter) TransformResponse(resp *http.Response) (*UnifiedResponse, error) {
	return nil, ErrAdapterNotFound
}
func (a *fakeAdapter) TransformStreamChunk(chunk []byte) (*UnifiedSSEChunk, error) {
	return nil, ErrAdapterNotFound
}
func (a *fakeAdapter) ParseTokenUsage(resp *http.Response) (int, int, int) { return 0, 0, 0 }
func (a *fakeAdapter) ParseStreamUsage(chunk []byte) (int, int, int)       { return 0, 0, 0 }
func (a *fakeAdapter) ParseError(resp *http.Response) (int, string)        { return 0, "" }
```

（`fakeAdapter` 的测试文件需 `import "net/http"`。）

创建 `pkg/adapter/adapters_test.go`：

```go
package adapter

import "testing"

func TestBuiltinAdapters(t *testing.T) {
	cases := []struct {
		name     string
		adapter  ModelAdapter
		native   bool
	}{
		{"openai", NewOpenAIAdapter(), true},
		{"tongyi", NewTongyiAdapter(), false},
		{"zhipu", NewZhipuAdapter(), false},
		{"deepseek", NewDeepSeekAdapter(), true},
	}
	for _, c := range cases {
		if c.adapter.Name() != c.name {
			t.Errorf("%s Name() = %q", c.name, c.adapter.Name())
		}
		if c.adapter.SupportsNativeProxy() != c.native {
			t.Errorf("%s SupportsNativeProxy() = %v, want %v", c.name, c.adapter.SupportsNativeProxy(), c.native)
		}
	}
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./pkg/adapter/ -run TestRegister -v`
预期：FAIL，`undefined: NewAdapterRegistry`（编译失败）

- [ ] **步骤 3：实现注册中心与 4 个适配器壳**

创建 `pkg/adapter/registry.go`：

```go
package adapter

import (
	"errors"
	"sync"
)

// ErrAdapterNotFound 适配器未注册
var ErrAdapterNotFound = errors.New("adapter not found")

// AdapterRegistry 模型适配器注册中心（照设计文档 8.1）
type AdapterRegistry struct {
	adapters map[string]ModelAdapter // provider -> adapter
	mu       sync.RWMutex
}

// NewAdapterRegistry 创建注册中心
func NewAdapterRegistry() *AdapterRegistry {
	return &AdapterRegistry{adapters: make(map[string]ModelAdapter)}
}

// Register 注册适配器（同名覆盖）
func (r *AdapterRegistry) Register(adapter ModelAdapter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adapters[adapter.Name()] = adapter
}

// Get 按 provider 获取适配器
func (r *AdapterRegistry) Get(provider string) (ModelAdapter, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	adapter, ok := r.adapters[provider]
	if !ok {
		return nil, ErrAdapterNotFound
	}
	return adapter, nil
}
```

创建 `pkg/adapter/openai.go`（转换方法骨架期 stub，Phase 5 填充）：

```go
package adapter

import (
	"errors"
	"net/http"
)

// OpenAIAdapter OpenAI 适配器（原生兼容：入口协议与上游一致，原样透传）
type OpenAIAdapter struct{}

// NewOpenAIAdapter 创建 OpenAI 适配器
func NewOpenAIAdapter() *OpenAIAdapter { return &OpenAIAdapter{} }

func (a *OpenAIAdapter) Name() string { return "openai" }

func (a *OpenAIAdapter) SupportsNativeProxy() bool { return true }

// 以下转换方法为骨架期 stub，Phase 5 填充实现
func (a *OpenAIAdapter) TransformRequest(req *UnifiedRequest, rawBody []byte) (*http.Request, error) {
	return nil, errors.New("not implemented")
}

func (a *OpenAIAdapter) TransformResponse(resp *http.Response) (*UnifiedResponse, error) {
	return nil, errors.New("not implemented")
}

func (a *OpenAIAdapter) TransformStreamChunk(chunk []byte) (*UnifiedSSEChunk, error) {
	return nil, errors.New("not implemented")
}

func (a *OpenAIAdapter) ParseTokenUsage(resp *http.Response) (int, int, int) { return 0, 0, 0 }

func (a *OpenAIAdapter) ParseStreamUsage(chunk []byte) (int, int, int) { return 0, 0, 0 }

func (a *OpenAIAdapter) ParseError(resp *http.Response) (int, string) { return 0, "" }
```

创建 `pkg/adapter/tongyi.go`（异构上游，需转换）：

```go
package adapter

import (
	"errors"
	"net/http"
)

// TongyiAdapter 通义千问适配器（DashScope 协议，需转换）
type TongyiAdapter struct{}

// NewTongyiAdapter 创建通义千问适配器
func NewTongyiAdapter() *TongyiAdapter { return &TongyiAdapter{} }

func (a *TongyiAdapter) Name() string { return "tongyi" }

func (a *TongyiAdapter) SupportsNativeProxy() bool { return false }

func (a *TongyiAdapter) TransformRequest(req *UnifiedRequest, rawBody []byte) (*http.Request, error) {
	return nil, errors.New("not implemented")
}

func (a *TongyiAdapter) TransformResponse(resp *http.Response) (*UnifiedResponse, error) {
	return nil, errors.New("not implemented")
}

func (a *TongyiAdapter) TransformStreamChunk(chunk []byte) (*UnifiedSSEChunk, error) {
	return nil, errors.New("not implemented")
}

func (a *TongyiAdapter) ParseTokenUsage(resp *http.Response) (int, int, int) { return 0, 0, 0 }

func (a *TongyiAdapter) ParseStreamUsage(chunk []byte) (int, int, int) { return 0, 0, 0 }

func (a *TongyiAdapter) ParseError(resp *http.Response) (int, string) { return 0, "" }
```

创建 `pkg/adapter/zhipu.go`（模式与 tongyi.go 相同，Name() 返回 `"zhipu"`，SupportsNativeProxy() 返回 false，方法体同 stub）。

创建 `pkg/adapter/deepseek.go`（模式与 openai.go 相同，Name() 返回 `"deepseek"`，SupportsNativeProxy() 返回 true，方法体同 stub）。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./pkg/adapter/ -v`
预期：PASS（`registry_test.go` 需 `import "net/http"`）

- [ ] **步骤 5：Commit**

```bash
git add pkg/adapter/
git commit -m "feat: 适配器注册中心与四个内置适配器壳（OpenAI/通义/智谱/DeepSeek）
Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## 任务 10：管道中间件层

**文件：**
- 创建：`pkg/core/pipeline.go`、`pkg/core/middleware_auth.go`、`pkg/core/middleware_limit.go`、`pkg/core/middleware_route.go`、`pkg/core/pipeline_test.go`

- [ ] **步骤 1：编写失败的测试**

创建 `pkg/core/pipeline_test.go`：

```go
package core

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
)

func TestPipelineOrder(t *testing.T) {
	var calls []string
	record := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, name+"-in")
				next.ServeHTTP(w, r)
				calls = append(calls, name+"-out")
			})
		}
	}
	p := NewPipeline(oss.NewMemStorage(), oss.NewMemRateLimiter(), nil)
	p.Use(record("m1"))
	p.Use(record("m2"))
	handler := p.Apply(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "handler")
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	want := []string{"m1-in", "m2-in", "handler", "m2-out", "m1-out"}
	if len(calls) != len(want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("calls = %v, want %v", calls, want)
		}
	}
}

func TestAuthMiddlewareCreatesRequestContext(t *testing.T) {
	p := NewPipeline(oss.NewMemStorage(), oss.NewMemRateLimiter(), nil)
	var gotRC *RequestContext
	p.Use(AuthMiddleware(p.storage))
	handler := p.Apply(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc, ok := RequestContextFrom(r.Context())
		if !ok {
			t.Error("RequestContext not in context")
			return
		}
		gotRC = rc
	}))
	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer ng-test-key")
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if gotRC == nil {
		t.Fatal("RequestContext was not created")
	}
	if gotRC.RequestID == "" {
		t.Error("RequestID should not be empty")
	}
	if gotRC.APIKeyID != "ng-test-key" {
		t.Errorf("APIKeyID = %q, want ng-test-key", gotRC.APIKeyID)
	}
	if gotRC.RequestMethod != "GET" || gotRC.RequestPath != "/v1/models" {
		t.Errorf("RequestMethod/Path = %s %s", gotRC.RequestMethod, gotRC.RequestPath)
	}
	if _, ok := gotRC.RequestHeaders["Authorization"]; !ok {
		t.Error("RequestHeaders should contain Authorization")
	}
}

func TestRateLimitAndRouteMiddlewaresPassThrough(t *testing.T) {
	p := NewPipeline(oss.NewMemStorage(), oss.NewMemRateLimiter(), nil)
	hit := false
	handler := p.Build(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/chat/completions", nil))
	if !hit {
		t.Error("handler was not reached through Build() chain")
	}
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./pkg/core/ -run TestPipeline -v`
预期：FAIL，`undefined: NewPipeline`（编译失败）

- [ ] **步骤 3：实现 Pipeline 与三个中间件壳**

创建 `pkg/core/pipeline.go`：

```go
package core

import (
	"net/http"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// Middleware 管道中间件（照设计文档 2.2）
type Middleware func(next http.Handler) http.Handler

// Pipeline 管道中间件层：按固定顺序执行预处理链路
type Pipeline struct {
	storage     plugin.StoragePlugin
	rateLimiter plugin.RateLimitPlugin
	auditor     plugin.AuditPipeline
	middlewares []Middleware
}

// NewPipeline 创建管道
func NewPipeline(storage plugin.StoragePlugin, rateLimiter plugin.RateLimitPlugin, auditor plugin.AuditPipeline) *Pipeline {
	return &Pipeline{
		storage:     storage,
		rateLimiter: rateLimiter,
		auditor:     auditor,
	}
}

// Use 追加自定义中间件（在固定链之后执行）
func (p *Pipeline) Use(mw Middleware) {
	p.middlewares = append(p.middlewares, mw)
}

// Apply 将中间件链包装到 handler 上
func (p *Pipeline) Apply(handler http.Handler) http.Handler {
	h := handler
	all := append(append([]Middleware{}, p.middlewares...), p.fixedChain()...)
	// 逆序包装：第一个中间件最先执行
	for i := len(all) - 1; i >= 0; i-- {
		h = all[i](h)
	}
	return h
}

// fixedChain 固定顺序中间件链（照设计文档 2.2，不可调换）：
// 鉴权 → 限流 → 路由匹配（协议转换与前置钩子 Phase 4 接入）
func (p *Pipeline) fixedChain() []Middleware {
	return []Middleware{
		AuthMiddleware(p.storage),
		RateLimitMiddleware(p.rateLimiter),
		RouteMatchMiddleware(p.storage),
	}
}

// Build 将固定链 + 自定义中间件应用到 handler（路由入口）
func (p *Pipeline) Build(handler http.Handler) http.Handler {
	return p.Apply(handler)
}
```

创建 `pkg/core/middleware_auth.go`：

```go
package core

import (
	"net/http"
	"strings"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/google/uuid"
)

// AuthMiddleware 鉴权中间件（骨架期：提取 Bearer API Key 写入 RequestContext 后放行；
// Phase 4 调用 storage.GetAPIKey 校验并返回 401）
func AuthMiddleware(storage plugin.StoragePlugin) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rc := &RequestContext{
				RequestID:     uuid.NewString(),
				StartTime:     time.Now(),
				ClientIP:      r.RemoteAddr,
				RequestMethod: r.Method,
				RequestPath:   r.URL.Path,
				RequestHeaders: headerMap(r.Header),
			}
			if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
				rc.APIKeyID = strings.TrimPrefix(auth, "Bearer ")
			}
			ctx := WithRequestContext(r.Context(), rc)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// headerMap http.Header 转换为 map[string]string（取每个头的第一个值）
func headerMap(h http.Header) map[string]string {
	m := make(map[string]string, len(h))
	for k, v := range h {
		if len(v) > 0 {
			m[k] = v[0]
		}
	}
	return m
}
```

创建 `pkg/core/middleware_limit.go`：

```go
package core

import (
	"net/http"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// RateLimitMiddleware 限流中间件（骨架期直接放行；
// Phase 4 调用 rateLimiter.Allow 并返回 429 + OpenAI 限流 Header）
func RateLimitMiddleware(rateLimiter plugin.RateLimitPlugin) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}
```

创建 `pkg/core/middleware_route.go`：

```go
package core

import (
	"net/http"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// RouteMatchMiddleware 路由匹配中间件（骨架期直接放行；
// Phase 4 解析请求体 model 字段 → storage.GetModelConfig → 写入 ctx.ModelConfig，
// 未匹配返回 404）
func RouteMatchMiddleware(storage plugin.StoragePlugin) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}
```

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./pkg/core/ -v`
预期：PASS（5 个测试：含任务 4 的 2 个）

- [ ] **步骤 5：Commit**

```bash
git add pkg/core/pipeline.go pkg/core/middleware_auth.go pkg/core/middleware_limit.go pkg/core/middleware_route.go pkg/core/pipeline_test.go
git commit -m "feat: 管道中间件层——中间件链组装与三个中间件壳
Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## 任务 11：代理内核与接入层壳

**文件：**
- 创建：`pkg/core/proxy.go`、`pkg/core/sse_writer.go`、`pkg/core/sse_reassembler.go`、`pkg/core/disconnect_handler.go`、`pkg/core/acceptor.go`、`pkg/core/proxy_test.go`

- [ ] **步骤 1：编写失败的测试**

创建 `pkg/core/proxy_test.go`：

```go
package core

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/druidcaesa/neuralgate/pkg/adapter"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
)

func TestProxyCoreSkeletonResponse(t *testing.T) {
	pipeline := NewPipeline(oss.NewMemStorage(), oss.NewMemRateLimiter(), nil)
	registry := adapter.NewAdapterRegistry()
	proxy := NewProxyCore(pipeline, registry)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	proxy.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
	var body struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body.Error.Type != "api_error" || body.Error.Code != "service_unavailable" {
		t.Errorf("error body = %+v, want api_error/service_unavailable", body.Error)
	}
}

func TestIPFilterDefaultsAllow(t *testing.T) {
	f := NewIPFilter()
	if !f.Allow("192.168.1.1") {
		t.Error("Allow() should default to true")
	}
}

func TestProtocolParserIsSSE(t *testing.T) {
	p := NewProtocolParser()
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("Accept", "text/event-stream")
	if !p.IsSSE(req) {
		t.Error("IsSSE() = false, want true for text/event-stream Accept")
	}
	req2 := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	if p.IsSSE(req2) {
		t.Error("IsSSE() = true, want false without Accept header")
	}
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./pkg/core/ -run TestProxyCore -v`
预期：FAIL，`undefined: NewProxyCore`（编译失败）

- [ ] **步骤 3：实现 ProxyCore、SSE 壳与接入层**

创建 `pkg/core/proxy.go`：

```go
package core

import (
	"encoding/json"
	"net/http"

	"github.com/druidcaesa/neuralgate/pkg/adapter"
)

// ProxyCore 代理内核层（照设计文档 2.3）
// 骨架期：所有 /v1/* 请求返回 OpenAI 格式占位错误（503 service_unavailable）；
// Phase 4 接入 ReverseProxy 转发、SSE 劫持、断连补全
type ProxyCore struct {
	pipeline *Pipeline
	registry *adapter.AdapterRegistry
}

// NewProxyCore 创建代理内核
func NewProxyCore(pipeline *Pipeline, registry *adapter.AdapterRegistry) *ProxyCore {
	return &ProxyCore{pipeline: pipeline, registry: registry}
}

// Handler 返回经管道包装的代理入口
func (p *ProxyCore) Handler() http.Handler {
	return p.pipeline.Build(http.HandlerFunc(p.proxyHandler))
}

// proxyHandler 代理处理入口（骨架期占位）
func (p *ProxyCore) proxyHandler(w http.ResponseWriter, r *http.Request) {
	writeOpenAIError(w, http.StatusServiceUnavailable, "api_error", "service_unavailable", "service not initialized")
}

// openAIErrorBody OpenAI 错误响应体（照设计文档 8.7 格式契约）
type openAIErrorBody struct {
	Error openAIError `json:"error"`
}

type openAIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Param   any    `json:"param"`
	Code    string `json:"code"`
}

// writeOpenAIError 按 OpenAI 错误格式写响应
func writeOpenAIError(w http.ResponseWriter, status int, etype, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(openAIErrorBody{
		Error: openAIError{Message: message, Type: etype, Param: nil, Code: code},
	})
}
```

创建 `pkg/core/sse_writer.go`：

```go
package core

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// SSEResponseWriter 劫持 SSE 流量（照设计文档 5.2 结构）
// 骨架期：原样写入客户端；分片解析与审计投递 Phase 4/7 填充
type SSEResponseWriter struct {
	http.ResponseWriter // 嵌入原始 Writer
	requestID    string
	auditor      plugin.AuditPipeline
	mu           sync.Mutex
	startWrite   time.Time
	headerWritten bool
}

// NewSSEResponseWriter 包装 ResponseWriter 为 SSE 劫持器
func NewSSEResponseWriter(w http.ResponseWriter, requestID string, auditor plugin.AuditPipeline) *SSEResponseWriter {
	return &SSEResponseWriter{
		ResponseWriter: w,
		requestID:      requestID,
		auditor:        auditor,
		startWrite:     time.Now(),
	}
}

// Write 写入原始 Writer（推送客户端）；骨架期不做分片解析与审计投递
func (w *SSEResponseWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.headerWritten = true
	return w.ResponseWriter.Write(data)
}

// WriteHeader 记录状态码并转发
func (w *SSEResponseWriter) WriteHeader(code int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.headerWritten = true
	w.ResponseWriter.WriteHeader(code)
}

// Flush 刷新，确保客户端实时收到数据
func (w *SSEResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack 支持连接劫持（断连检测用）
func (w *SSEResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("underlying writer does not support hijacking")
	}
	return hj.Hijack()
}
```

创建 `pkg/core/sse_reassembler.go`：

```go
package core

import (
	"errors"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// StreamReassembler 分片重组器（照设计文档 2.3）
// 骨架期 stub，Phase 4 实现：分片拼接生成完整应答
type StreamReassembler struct{}

// NewStreamReassembler 创建重组器
func NewStreamReassembler() *StreamReassembler { return &StreamReassembler{} }

// Reassemble 将分片列表重组为完整应答
func (r *StreamReassembler) Reassemble(chunks []plugin.SSEChunk) (string, error) {
	return "", errors.New("not implemented")
}
```

创建 `pkg/core/disconnect_handler.go`：

```go
package core

import (
	"context"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// DisconnectHandler 断连检测与补全（照设计文档 2.3/5.4）
type DisconnectHandler struct {
	auditor plugin.AuditPipeline
}

// NewDisconnectHandler 创建断连处理器
func NewDisconnectHandler(auditor plugin.AuditPipeline) *DisconnectHandler {
	return &DisconnectHandler{auditor: auditor}
}

// Watch 监听请求上下文取消信号；断开后标记审计日志（骨架期可用行为，补全逻辑 Phase 4 细化）
func (h *DisconnectHandler) Watch(ctx context.Context, requestID string) {
	<-ctx.Done()
	if h.auditor != nil {
		_ = h.auditor.MarkDisconnect(requestID, "client_disconnected")
	}
}
```

创建 `pkg/core/acceptor.go`：

```go
package core

import (
	"crypto/tls"
	"net"
	"net/http"
	"strings"
)

// Acceptor 接入层（照设计文档 2.1）：连接管理、TLS、IP 过滤、协议解析
// 骨架期：仅持有组件并透传 handler；各组件逻辑 Phase 4 填充
type Acceptor struct {
	handler http.Handler
	connMgr *ConnectionManager
	tls     *TLSHandler
	ipf     *IPFilter
	parser  *ProtocolParser
}

// NewAcceptor 创建接入层
func NewAcceptor(handler http.Handler) *Acceptor {
	return &Acceptor{
		handler: handler,
		connMgr: NewConnectionManager(),
		tls:     NewTLSHandler(),
		ipf:     NewIPFilter(),
		parser:  NewProtocolParser(),
	}
}

// Handler 返回经接入层包装的 handler（骨架期直接返回；IP 过滤 Phase 4 接入）
func (a *Acceptor) Handler() http.Handler { return a.handler }

// ConnectionManager 连接生命周期管理（骨架期空实现；最大连接数/空闲超时 Phase 4）
type ConnectionManager struct{}

func NewConnectionManager() *ConnectionManager { return &ConnectionManager{} }

// OnStateChange ConnState 回调（http.Server.ConnState 挂接点）
func (m *ConnectionManager) OnStateChange(c net.Conn, state http.ConnState) {}

// TLSHandler TLS 终止（骨架期空实现；证书加载 Phase 4）
type TLSHandler struct{}

func NewTLSHandler() *TLSHandler { return &TLSHandler{} }

// TLSConfig 返回 TLS 配置（骨架期返回 nil，表示不启用 TLS）
func (h *TLSHandler) TLSConfig() *tls.Config { return nil }

// IPFilter IP 黑白名单（骨架期默认全部放行；规则匹配 Phase 4）
type IPFilter struct{}

func NewIPFilter() *IPFilter { return &IPFilter{} }

// Allow 是否允许该 IP 访问（骨架期恒为 true）
func (f *IPFilter) Allow(ip string) bool { return true }

// ProtocolParser HTTP 协议解析（骨架期仅提供 SSE 判断）
type ProtocolParser struct{}

func NewProtocolParser() *ProtocolParser { return &ProtocolParser{} }

// IsSSE 检测流式请求（Accept: text/event-stream 时动态取消 WriteTimeout，照设计文档 2.1）
func (p *ProtocolParser) IsSSE(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/event-stream")
}
```

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./pkg/core/ -v`
预期：PASS（8 个测试：含任务 4/10 的 5 个）

- [ ] **步骤 5：Commit**

```bash
git add pkg/core/proxy.go pkg/core/sse_writer.go pkg/core/sse_reassembler.go pkg/core/disconnect_handler.go pkg/core/acceptor.go pkg/core/proxy_test.go
git commit -m "feat: 代理内核与接入层壳（ProxyCore/SSE 劫持/断连/连接管理）
Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## 任务 12：Gin 管理后台

**文件：**
- 创建：`pkg/admin/server.go`、`pkg/admin/router.go`、`pkg/admin/middleware.go`、`pkg/admin/server_test.go`

- [ ] **步骤 1：编写失败的测试**

创建 `pkg/admin/server_test.go`：

```go
package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
	"go.uber.org/zap"
)

func TestHealthz(t *testing.T) {
	s := NewAdminServer(oss.NewMemStorage(), zap.NewNop())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/healthz", nil)
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestAPIPing(t *testing.T) {
	s := NewAdminServer(oss.NewMemStorage(), zap.NewNop())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/ping", nil)
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestCORSMiddleware(t *testing.T) {
	s := NewAdminServer(oss.NewMemStorage(), zap.NewNop())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/healthz", nil)
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("OPTIONS status = %d, want 204", rec.Code)
	}
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./pkg/admin/ -v`
预期：FAIL，`undefined: NewAdminServer`（编译失败）

- [ ] **步骤 3：实现管理后台**

创建 `pkg/admin/server.go`：

```go
package admin

import (
	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// AdminServer 管理后台（Gin，照设计文档 1.1）
// 低并发短连接：CRUD 接口、配置管理、日志查询、授权校验
type AdminServer struct {
	storage plugin.StoragePlugin
	logger  *zap.Logger
	engine  *gin.Engine
}

// NewAdminServer 创建管理后台
func NewAdminServer(storage plugin.StoragePlugin, logger *zap.Logger) *AdminServer {
	gin.SetMode(gin.ReleaseMode)
	s := &AdminServer{storage: storage, logger: logger}
	s.engine = gin.New()
	s.engine.Use(gin.Recovery(), CORS())
	s.registerRoutes(s.engine)
	return s
}

// Router 返回 Gin 路由
func (s *AdminServer) Router() *gin.Engine { return s.engine }

// Run 启动后台服务
func (s *AdminServer) Run(addr string) error {
	return s.engine.Run(addr)
}
```

创建 `pkg/admin/router.go`：

```go
package admin

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// registerRoutes 注册路由
// 骨架期：健康检查 + API 占位组；CRUD 路由 Phase 6 注册
func (s *AdminServer) registerRoutes(r *gin.Engine) {
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	api := r.Group("/api")
	{
		api.GET("/ping", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"message": "pong"})
		})
	}
}
```

创建 `pkg/admin/middleware.go`：

```go
package admin

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// CORS 跨域中间件（后台页面开发期使用）
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
```

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./pkg/admin/ -v`
预期：PASS（3 个测试）

- [ ] **步骤 5：Commit**

```bash
git add pkg/admin/
git commit -m "feat: Gin 管理后台骨架（健康检查 + 空路由组 + CORS）
Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## 任务 13：双服务启动入口

**文件：**
- 创建：`cmd/gateway/main.go`、`cmd/gateway/factory_oss.go`、`cmd/gateway/factory_enterprise.go`

- [ ] **步骤 1：创建 BuildTag 工厂注入文件**

创建 `cmd/gateway/factory_oss.go`：

```go
//go:build oss

package main

import (
	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
)

const edition = "oss"

// newPluginFactory 由 BuildTag 决定返回哪个版本的插件工厂
func newPluginFactory() plugin.PluginFactory {
	return oss.NewPluginFactory()
}
```

创建 `cmd/gateway/factory_enterprise.go`：

```go
//go:build enterprise

package main

import (
	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/enterprise"
)

const edition = "enterprise"

// newPluginFactory 由 BuildTag 决定返回哪个版本的插件工厂
func newPluginFactory() plugin.PluginFactory {
	return enterprise.NewPluginFactory()
}
```

- [ ] **步骤 2：创建主入口**

创建 `cmd/gateway/main.go`（照设计文档 10.1 启动流程）：

```go
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/adapter"
	"github.com/druidcaesa/neuralgate/pkg/admin"
	"github.com/druidcaesa/neuralgate/pkg/config"
	"github.com/druidcaesa/neuralgate/pkg/core"
	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func main() {
	// 1. 解析命令行参数
	configPath := flag.String("config", "config.yaml", "配置文件路径")
	showVersion := flag.Bool("version", false, "显示版本信息")
	flag.Parse()

	if *showVersion {
		fmt.Printf("NeuralGate %s (edition=%s, build=%s, commit=%s)\n",
			core.Version, edition, core.BuildTime, core.GitCommit)
		return
	}

	// 2. 加载配置文件
	cfg, err := config.Load(*configPath)
	if err != nil {
		logFatal("加载配置失败", err)
	}
	logger := initLogger(cfg.Log)
	defer logger.Sync()

	logger.Info("NeuralGate 启动",
		zap.String("version", core.Version),
		zap.String("edition", edition),
		zap.String("build_time", core.BuildTime),
		zap.String("git_commit", core.GitCommit),
	)

	// 3. 初始化插件工厂（BuildTag 决定实现）
	factory := newPluginFactory()
	storage := factory.CreateStorage()
	if err := storage.Init(map[string]interface{}{"driver": cfg.Storage.Driver}); err != nil {
		logger.Fatal("存储初始化失败", zap.Error(err))
	}
	auditor := factory.CreateAuditor()
	if err := auditor.Init(plugin.AuditConfig{
		QueueSize:     cfg.Audit.QueueSize,
		WorkerCount:   cfg.Audit.WorkerCount,
		BatchSize:     cfg.Audit.BatchSize,
		FlushInterval: cfg.Audit.FlushInterval,
		EnableSHA256:  cfg.Audit.EnableSHA256,
		RetentionDays: cfg.Audit.RetentionDays,
	}); err != nil {
		logger.Fatal("审计初始化失败", zap.Error(err))
	}
	rateLimiter := factory.CreateRateLimiter()
	if err := rateLimiter.Init(map[string]interface{}{
		"default_rps": cfg.RateLimit.DefaultRPS,
		"default_tpm": cfg.RateLimit.DefaultTPM,
	}); err != nil {
		logger.Fatal("限流初始化失败", zap.Error(err))
	}
	logger.Info("插件工厂初始化完成", zap.String("storage_driver", cfg.Storage.Driver))

	// 4. 从存储加载模型配置（骨架期：内存存储返回空表，仅打印数量；
	//    路由表与热更新 Phase 4/6）
	models, total, err := storage.ListModelConfigs(1, 100)
	if err != nil {
		logger.Warn("加载模型配置失败", zap.Error(err))
	} else {
		logger.Info("加载模型配置", zap.Int("total", total), zap.Int("loaded", len(models)))
	}

	// 5. 初始化模型适配器注册中心
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	registry.Register(adapter.NewTongyiAdapter())
	registry.Register(adapter.NewZhipuAdapter())
	registry.Register(adapter.NewDeepSeekAdapter())

	// 6. 初始化代理内核
	pipeline := core.NewPipeline(storage, rateLimiter, auditor)
	proxyCore := core.NewProxyCore(pipeline, registry)
	acceptor := core.NewAcceptor(proxyCore.Handler())

	// 7. 初始化管理后台
	adminServer := admin.NewAdminServer(storage, logger)

	// 8. 启动双服务（并发）
	proxyServer := &http.Server{
		Addr:           cfg.Server.ProxyAddr,
		Handler:        acceptor.Handler(),
		ReadTimeout:    cfg.Server.ReadTimeout,
		WriteTimeout:   cfg.Server.WriteTimeout,
		IdleTimeout:    cfg.Server.IdleTimeout,
		MaxHeaderBytes: cfg.Server.MaxHeaderBytes,
		// ConnState 回调：追踪连接状态（Phase 4 接入 ConnectionManager）
		ConnState: func(c net.Conn, state http.ConnState) {},
	}
	adminHTTPServer := &http.Server{
		Addr:    cfg.Server.AdminAddr,
		Handler: adminServer.Router(),
	}

	errCh := make(chan error, 2)
	go func() {
		logger.Info("代理服务启动", zap.String("addr", cfg.Server.ProxyAddr))
		if err := proxyServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("代理服务: %w", err)
		}
	}()
	go func() {
		logger.Info("管理后台启动", zap.String("addr", cfg.Server.AdminAddr))
		if err := adminHTTPServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("管理后台: %w", err)
		}
	}()

	// 9. 信号监听（优雅关闭）
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		logger.Info("收到退出信号", zap.String("signal", sig.String()))
	case err := <-errCh:
		logger.Fatal("服务异常退出", zap.Error(err))
	}

	// 10. Shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := proxyServer.Shutdown(shutdownCtx); err != nil {
		logger.Warn("代理服务关闭异常", zap.Error(err))
	}
	if err := adminHTTPServer.Shutdown(shutdownCtx); err != nil {
		logger.Warn("管理后台关闭异常", zap.Error(err))
	}
	if err := auditor.Shutdown(); err != nil {
		logger.Warn("审计管道关闭异常", zap.Error(err))
	}
	if err := storage.Close(); err != nil {
		logger.Warn("存储关闭异常", zap.Error(err))
	}
	logger.Info("NeuralGate 已退出")
}

// initLogger 按配置初始化 zap 日志
func initLogger(cfg config.LogConfig) *zap.Logger {
	level := zap.NewAtomicLevel()
	if err := level.UnmarshalText([]byte(cfg.Level)); err != nil {
		level.SetLevel(zap.InfoLevel)
	}
	var encoder zapcore.Encoder
	if cfg.Format == "json" {
		encoder = zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig())
	} else {
		encoder = zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig())
	}
	return zap.New(zapcore.NewCore(encoder, zapcore.AddSync(os.Stdout), level))
}

// logFatal 打印错误并退出（zap 初始化前的兜底）
func logFatal(msg string, err error) {
	fmt.Fprintf(os.Stderr, "FATAL %s: %v\n", msg, err)
	os.Exit(1)
}
```

- [ ] **步骤 3：双版本编译 + 全量测试验证**

运行：

```bash
go build -tags oss -o /tmp/neuralgate-oss ./cmd/gateway/
go build -tags enterprise -o /tmp/neuralgate-enterprise ./cmd/gateway/
go vet -tags oss ./...
go test -tags oss ./...
```

预期：两次编译成功；vet 无输出；全部测试 PASS

- [ ] **步骤 4：Commit**

```bash
git add cmd/gateway/
git commit -m "feat: 双服务启动入口（代理 :8080 + 后台 :8081，优雅关闭；BuildTag 工厂注入）
Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## 任务 14：端到端验证

**文件：**
- 无新增文件（仅验证）

- [ ] **步骤 1：Makefile 构建验证**

运行：

```bash
cd /Users/fanyanan/work/go/neuralgate
make build-oss
make build-enterprise
ls -la neuralgate neuralgate-enterprise
```

预期：两个二进制生成，`neuralgate` 明显小于 `neuralgate-enterprise`（或大小接近，均为骨架期代码）

- [ ] **步骤 2：启动与健康检查验证**

运行：

```bash
./neuralgate -config config.yaml > /tmp/neuralgate.log 2>&1 &
echo $! > /tmp/neuralgate.pid
sleep 1
curl -s -o /dev/null -w "%{http_code}\n" http://127.0.0.1:8080/healthz
curl -s http://127.0.0.1:8080/v1/models
echo
curl -s -o /dev/null -w "%{http_code}\n" http://127.0.0.1:8081/healthz
curl -s -o /dev/null -w "%{http_code}\n" http://127.0.0.1:8081/api/ping
```

预期：

- `:8080/healthz` → `200`
- `:8080/v1/models` → `{"error":{"message":"service not initialized","type":"api_error","param":null,"code":"service_unavailable"}}`
- `:8081/healthz` → `200`
- `:8081/api/ping` → `200`
- `/tmp/neuralgate.log` 含 "NeuralGate 启动" 与双服务启动日志

- [ ] **步骤 3：优雅关闭验证**

运行：

```bash
kill -TERM $(cat /tmp/neuralgate.pid)
sleep 1
tail -20 /tmp/neuralgate.log
```

预期：日志显示"收到退出信号"→"代理服务关闭"→"管理后台关闭"→"NeuralGate 已退出"；`kill -0 $(cat /tmp/neuralgate.pid)` 报错（进程已退出）

- [ ] **步骤 4：`-version` 验证**

运行：

```bash
./neuralgate -version
```

预期：输出 `NeuralGate dev (edition=oss, build=unknown, commit=unknown)`（Makefile 构建后为注入的真实值）

- [ ] **步骤 5：清理并提交**

运行：

```bash
rm -f neuralgate neuralgate-enterprise /tmp/neuralgate.log /tmp/neuralgate.pid
git status
```

预期：工作区干净（仅剩未跟踪的 `.idea/`、`.DS_Store`——若 `.gitignore` 未覆盖，补充 `.idea/`、`.DS_Store` 到 `.gitignore` 并提交）

```bash
git add .gitignore
git commit -m "chore: 补充 IDE 与系统文件到 .gitignore
Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## 完成定义

- [ ] `make build-oss` 与 `make build-enterprise` 双编译通过
- [ ] `go test -tags oss ./...` 与 `go vet -tags oss ./...` 全部通过
- [ ] 双服务启动、健康检查、占位响应、优雅关闭、`-version` 全部验证通过
- [ ] 全部任务 commit 完成（本地 git 仓库）
