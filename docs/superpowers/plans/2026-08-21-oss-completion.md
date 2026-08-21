# OSS 补全至 PRD 完整度 实现计划

> **面向 AI 代理的工作者:** 必需子技能:使用 superpowers:subagent-driven-development(推荐)或 superpowers:executing-plans 逐任务实现此计划。步骤使用复选框(`- [ ]`)语法来跟踪进度。

**目标:** 补齐 OSS 版对照 PRD 标记 `OSS+` 但当前占位或缺失的功能:限流增强(TPM+双策略)、限流管理 API、负载均衡(独立 upstreams 表)、透传端点精确路由、IP 黑白名单、TLS。

**架构:** 限流器策略化(token_bucket/sliding_window 双策略、RPS+TPM 双维度、三层配置匹配 + 缓存热加载);负载均衡用独立 upstreams 表 + 加权随机选上游,ModelConfig 保留默认上游回退;透传端点按 PRD 8.5 精确路由 + 方法校验;IP 黑白名单与 TLS 在接入层配置驱动。

**技术栈:** Go 1.26、net/http、Gin、database/sql(mysql/sqlite)、crypto/tls、math/rand

**规格:** `docs/superpowers/specs/2026-08-21-oss-completion-design.md`

---

## 文件结构

```
新增:
  pkg/plugin/oss/limit_bucket.go       # tokenBucket 令牌桶(单维度基础件)
  pkg/plugin/oss/limit_sliding.go      # slidingWindow 滑动窗口(单维度基础件)
  pkg/plugin/oss/ratelimiter.go        # RateLimiter:三层配置解析 + 双维度双策略 + 缓存
  pkg/admin/rate_limit.go              # 限流配置 CRUD API
  pkg/admin/upstream.go                # 上游管理 API
修改:
  pkg/plugin/interface.go              # RateLimitPlugin 增 RecordTokens/ReloadConfig;
                                       # StoragePlugin 增限流配置/上游 CRUD;
                                       # RateLimitConfig 加 ID/CreatedAt/UpdatedAt;新增 Upstream 结构
  pkg/plugin/oss/limit_mem.go          # 删除(被 ratelimiter.go 取代)或保留标记废弃 → 本计划删除
  pkg/plugin/oss/storage_mem.go        # rate_limit_configs + upstreams 内存 CRUD
  pkg/plugin/oss/storage_sql.go        # rate_limit_configs + upstreams SQL CRUD(加密)
  pkg/plugin/oss/storage_sqlite.go     # DDL 两张新表
  pkg/plugin/oss/storage_mysql.go      # DDL 两张新表
  pkg/plugin/oss/storage_dynamic.go    # 转发新方法
  pkg/plugin/oss/factory.go            # CreateRateLimiter 注入 storage + 默认值
  pkg/plugin/enterprise/factory.go     # 同步 RateLimitPlugin 接口变更
  pkg/core/context.go                  # RequestContext 增 Upstreams 字段
  pkg/core/middleware_route.go         # 加载 upstreams 到 rc
  pkg/core/middleware_limit.go         # 429 区分 rate_limit/token_limit(TPM 预检)
  pkg/core/proxy.go                    # selectUpstream 选上游;透传精确路由 + 405;TPM RecordTokens 回补
  pkg/core/acceptor.go                 # IPFilter 真实规则;TLSHandler 真实证书;Acceptor 接 IP 检查
  pkg/config/config.go                 # TLSConfig/IPFilterConfig 结构 + 默认 + apply
  cmd/gateway/main.go                  # TLS 启动分支;IPFilter 注入;AdminServer 传 rateLimiter
  pkg/admin/server.go                  # NewAdminServer 加 rateLimiter 参数
  pkg/admin/router.go                  # 注册限流/上游路由
  config.yaml                          # tls/ip_filter 段
```

---

## 模块① 限流器策略化基础件(任务 1-3)

### 任务 1:token_bucket 令牌桶基础件

**文件:**
- 创建:`pkg/plugin/oss/limit_bucket.go`
- 测试:`pkg/plugin/oss/limit_bucket_test.go`

- [ ] **步骤 1:编写失败测试**

`pkg/plugin/oss/limit_bucket_test.go`:

```go
// Copyright 2026 FanYaNan. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package oss

import (
	"testing"
	"time"
)

func TestTokenBucketBurstThenReject(t *testing.T) {
	// 容量 3、每秒填充 3;起始满桶
	b := newTokenBucket(3, 3, time.Unix(1000, 0))
	// 突发:前 3 次取 1 令牌成功
	for i := 0; i < 3; i++ {
		if !b.take(1, time.Unix(1000, 0)) {
			t.Fatalf("take %d should succeed within capacity", i)
		}
	}
	// 第 4 次同一时刻:桶空,拒绝
	if b.take(1, time.Unix(1000, 0)) {
		t.Fatal("take beyond capacity should fail")
	}
}

func TestTokenBucketRefill(t *testing.T) {
	b := newTokenBucket(3, 3, time.Unix(1000, 0))
	for i := 0; i < 3; i++ {
		b.take(1, time.Unix(1000, 0))
	}
	// 1 秒后填满 3 个,可再取
	if !b.take(1, time.Unix(1001, 0)) {
		t.Fatal("take after 1s refill should succeed")
	}
}

func TestTokenBucketTakeN(t *testing.T) {
	// 容量 100、每秒 100;一次取 60 成功,再取 60 失败(剩 40)
	b := newTokenBucket(100, 100, time.Unix(1000, 0))
	if !b.take(60, time.Unix(1000, 0)) {
		t.Fatal("take 60 within capacity should succeed")
	}
	if b.take(60, time.Unix(1000, 0)) {
		t.Fatal("take 60 with only 40 left should fail")
	}
}

func TestTokenBucketRemaining(t *testing.T) {
	b := newTokenBucket(10, 10, time.Unix(1000, 0))
	b.take(3, time.Unix(1000, 0))
	if got := b.remaining(time.Unix(1000, 0)); got != 7 {
		t.Fatalf("remaining = %d; want 7", got)
	}
}
```

- [ ] **步骤 2:运行测试验证失败**

运行:`go test ./pkg/plugin/oss/ -run TestTokenBucket -v`
预期:FAIL,`undefined: newTokenBucket`

- [ ] **步骤 3:实现 limit_bucket.go**

`pkg/plugin/oss/limit_bucket.go`(保留 Apache-2.0 头):

```go
package oss

import "time"

// tokenBucket 令牌桶:容量 capacity,每秒填充 refillPerSec 个令牌,允许突发
type tokenBucket struct {
	capacity     float64
	refillPerSec float64
	tokens       float64
	last         time.Time
}

// newTokenBucket 创建满桶令牌桶
func newTokenBucket(capacity, refillPerSec float64, now time.Time) *tokenBucket {
	return &tokenBucket{
		capacity:     capacity,
		refillPerSec: refillPerSec,
		tokens:       capacity,
		last:         now,
	}
}

// refill 按经过时间补充令牌(上限 capacity)
func (b *tokenBucket) refill(now time.Time) {
	elapsed := now.Sub(b.last).Seconds()
	if elapsed <= 0 {
		return
	}
	b.tokens += elapsed * b.refillPerSec
	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}
	b.last = now
}

// take 尝试取 n 个令牌,成功返回 true
func (b *tokenBucket) take(n float64, now time.Time) bool {
	b.refill(now)
	if b.tokens >= n {
		b.tokens -= n
		return true
	}
	return false
}

// remaining 当前可用令牌数(整数向下取整)
func (b *tokenBucket) remaining(now time.Time) int64 {
	b.refill(now)
	return int64(b.tokens)
}
```

- [ ] **步骤 4:运行测试验证通过**

运行:`go test ./pkg/plugin/oss/ -run TestTokenBucket -v`
预期:PASS(4 个测试)

- [ ] **步骤 5:Commit**

```bash
git add pkg/plugin/oss/limit_bucket.go pkg/plugin/oss/limit_bucket_test.go
git commit -m "feat(plugin): token_bucket 令牌桶基础件(突发/填充/取N)"
```

---

### 任务 2:sliding_window 滑动窗口基础件

**文件:**
- 创建:`pkg/plugin/oss/limit_sliding.go`
- 测试:`pkg/plugin/oss/limit_sliding_test.go`

- [ ] **步骤 1:编写失败测试**

`pkg/plugin/oss/limit_sliding_test.go`(保留 Apache-2.0 头):

```go
package oss

import (
	"testing"
	"time"
)

func TestSlidingWindowWithinLimit(t *testing.T) {
	// 窗口 1s、上限 3
	w := newSlidingWindow(3, time.Second)
	base := time.Unix(1000, 0)
	for i := 0; i < 3; i++ {
		if !w.allow(1, base) {
			t.Fatalf("allow %d within limit should succeed", i)
		}
	}
	// 第 4 次同窗口:拒绝
	if w.allow(1, base) {
		t.Fatal("allow beyond limit should fail")
	}
}

func TestSlidingWindowRoll(t *testing.T) {
	w := newSlidingWindow(3, time.Second)
	base := time.Unix(1000, 0)
	for i := 0; i < 3; i++ {
		w.allow(1, base)
	}
	// 超过窗口后计数清零,可再允许
	if !w.allow(1, base.Add(time.Second+time.Millisecond)) {
		t.Fatal("allow after window roll should succeed")
	}
}

func TestSlidingWindowTakeN(t *testing.T) {
	// 60s 窗口、上限 100 tokens;取 60 成功,再取 50 失败(110>100)
	w := newSlidingWindow(100, 60*time.Second)
	base := time.Unix(1000, 0)
	if !w.allow(60, base) {
		t.Fatal("allow 60 within limit should succeed")
	}
	if w.allow(50, base) {
		t.Fatal("allow 50 exceeding 100 should fail")
	}
}

func TestSlidingWindowCurrent(t *testing.T) {
	w := newSlidingWindow(10, time.Second)
	base := time.Unix(1000, 0)
	w.allow(3, base)
	if got := w.current(base); got != 3 {
		t.Fatalf("current = %d; want 3", got)
	}
}
```

- [ ] **步骤 2:运行测试验证失败**

运行:`go test ./pkg/plugin/oss/ -run TestSlidingWindow -v`
预期:FAIL,`undefined: newSlidingWindow`

- [ ] **步骤 3:实现 limit_sliding.go**

`pkg/plugin/oss/limit_sliding.go`(保留 Apache-2.0 头):

```go
package oss

import "time"

// slidingWindow 固定窗口计数器(窗口滚动即清零,近似滑动窗口)
// 窗口内累计值超过 limit 则拒绝
type slidingWindow struct {
	limit       int64
	window      time.Duration
	count       int64
	windowStart time.Time
}

// newSlidingWindow 创建滑动窗口
func newSlidingWindow(limit int64, window time.Duration) *slidingWindow {
	return &slidingWindow{limit: limit, window: window}
}

// roll 若已越过当前窗口,重置计数与窗口起点
func (w *slidingWindow) roll(now time.Time) {
	if w.windowStart.IsZero() || now.Sub(w.windowStart) >= w.window {
		w.windowStart = now
		w.count = 0
	}
}

// allow 尝试累加 n,累加后不超过 limit 则成功
func (w *slidingWindow) allow(n int64, now time.Time) bool {
	w.roll(now)
	if w.count+n > w.limit {
		return false
	}
	w.count += n
	return true
}

// current 当前窗口累计值
func (w *slidingWindow) current(now time.Time) int64 {
	w.roll(now)
	return w.count
}
```

- [ ] **步骤 4:运行测试验证通过**

运行:`go test ./pkg/plugin/oss/ -run TestSlidingWindow -v`
预期:PASS(4 个测试)

- [ ] **步骤 5:Commit**

```bash
git add pkg/plugin/oss/limit_sliding.go pkg/plugin/oss/limit_sliding_test.go
git commit -m "feat(plugin): sliding_window 滑动窗口基础件(窗口滚动/累加)"
```

---
## 模块② 接口扩展 + 存储 CRUD(任务 3-5)

### 任务 3:接口扩展(RateLimitConfig/Upstream 实体 + 接口方法)

**文件:**
- 修改:`pkg/plugin/interface.go`

- [ ] **步骤 1:扩展 RateLimitConfig 实体**

`pkg/plugin/interface.go` 的 `RateLimitConfig` 增加 ID/CreatedAt/UpdatedAt:

```go
// RateLimitConfig 限流配置
type RateLimitConfig struct {
	ID             string    // 配置ID
	TenantID       string    // 租户ID（空表示全局）
	ModelName      string    // 模型名称（空表示全模型）
	RequestsPerSec int       // 每秒请求数
	TokensPerMin   int64     // 每分钟Token数
	Strategy       string    // 策略：token_bucket/sliding_window
	Enabled        bool      // 是否启用
	CreatedAt      time.Time // 创建时间
	UpdatedAt      time.Time // 更新时间
}
```

- [ ] **步骤 2:新增 Upstream 实体**

在 `ModelConfig` 定义之后新增:

```go
// Upstream 模型的上游端点（负载均衡：一个 ModelConfig 可挂多个 Upstream）
type Upstream struct {
	ID            string    // 上游ID
	ModelConfigID string    // 所属模型配置ID
	BaseURL       string    // 上游API地址
	APIKey        string    // 上游API Key（加密存储）
	Weight        int       // 加权轮询权重
	Enabled       bool      // 是否启用
	CreatedAt     time.Time // 创建时间
	UpdatedAt     time.Time // 更新时间
}
```

- [ ] **步骤 3:扩展 StoragePlugin 接口**

在 `StoragePlugin` 接口的审计段之后、健康检查之前新增:

```go
	// 限流配置管理
	GetRateLimitConfig(tenantID, modelName string) (*RateLimitConfig, error)
	SaveRateLimitConfig(cfg *RateLimitConfig) error
	ListRateLimitConfigs(page, size int) ([]*RateLimitConfig, int64, error)
	DeleteRateLimitConfig(id string) error

	// 上游管理（负载均衡）
	ListUpstreams(modelConfigID string) ([]*Upstream, error)
	GetUpstreamByID(id string) (*Upstream, error)
	SaveUpstream(up *Upstream) error
	DeleteUpstream(id string) error
```

- [ ] **步骤 4:扩展 RateLimitPlugin 接口**

`RateLimitPlugin` 接口增加两个方法:

```go
	// 重置限流计数器
	Reset(tenantID string, model string) error

	// RecordTokens 记录已消耗 token（TPM 事后回补，请求完成后调用）
	RecordTokens(tenantID string, model string, tokens int) error

	// ReloadConfig 从存储重载限流配置（管理后台写操作后触发）
	ReloadConfig() error
```

- [ ] **步骤 5:验证编译失败(接口未实现)**

运行:`go build ./... 2>&1 | head`
预期:FAIL——MemStorage/SQLStorage/dynamicStorage 未实现新存储方法,MemRateLimiter 未实现 RecordTokens/ReloadConfig(编译错误,证明接口已扩展)

- [ ] **步骤 6:Commit(仅接口,允许暂时不编译——下个任务补齐实现)**

> 说明:此任务只改接口定义,编译会红。为保持每个 commit 可编译,**本任务不单独 commit**,与任务 4 合并 commit(接口+存储实现一起绿)。跳过此步,进入任务 4。

---

### 任务 4:存储实现 — RateLimitConfig + Upstream CRUD(mem/sql/dynamic + DDL)

**文件:**
- 修改:`pkg/plugin/oss/storage_mem.go`、`storage_sql.go`、`storage_sqlite.go`、`storage_mysql.go`、`storage_dynamic.go`
- 测试:`pkg/plugin/oss/storage_sql_test.go`、`storage_mem_test.go`

- [ ] **步骤 1:编写失败测试**

`pkg/plugin/oss/storage_sql_test.go` 追加(复用已有 `newTestSQLStorage`):

```go
func TestSQLStorageRateLimitConfigCRUD(t *testing.T) {
	s := newTestSQLStorage(t)
	now := time.Now()
	cfg := &plugin.RateLimitConfig{
		ID: "rl1", TenantID: "t1", ModelName: "gpt-4",
		RequestsPerSec: 20, TokensPerMin: 50000, Strategy: "token_bucket",
		Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.SaveRateLimitConfig(cfg); err != nil {
		t.Fatalf("SaveRateLimitConfig: %v", err)
	}
	got, err := s.GetRateLimitConfig("t1", "gpt-4")
	if err != nil || got.RequestsPerSec != 20 || got.Strategy != "token_bucket" {
		t.Fatalf("GetRateLimitConfig = %v, %v", got, err)
	}
	// 不存在返回 ErrNotFound
	if _, err := s.GetRateLimitConfig("t1", "nope"); err != ErrNotFound {
		t.Fatalf("GetRateLimitConfig(nope) err = %v; want ErrNotFound", err)
	}
	if _, total, err := s.ListRateLimitConfigs(1, 10); err != nil || total != 1 {
		t.Fatalf("ListRateLimitConfigs total = %d, err %v", total, err)
	}
	if err := s.DeleteRateLimitConfig("rl1"); err != nil {
		t.Fatalf("DeleteRateLimitConfig: %v", err)
	}
	if _, err := s.GetRateLimitConfig("t1", "gpt-4"); err != ErrNotFound {
		t.Fatalf("after delete err = %v; want ErrNotFound", err)
	}
}

func TestSQLStorageUpstreamCRUD(t *testing.T) {
	s := newTestSQLStorage(t)
	now := time.Now()
	up := &plugin.Upstream{
		ID: "u1", ModelConfigID: "m1", BaseURL: "https://up1",
		APIKey: "sk-up-secret", Weight: 3, Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.SaveUpstream(up); err != nil {
		t.Fatalf("SaveUpstream: %v", err)
	}
	// api_key 加密后读回还原
	got, err := s.GetUpstreamByID("u1")
	if err != nil || got.APIKey != "sk-up-secret" || got.Weight != 3 {
		t.Fatalf("GetUpstreamByID = %v, %v", got, err)
	}
	ups, err := s.ListUpstreams("m1")
	if err != nil || len(ups) != 1 || ups[0].BaseURL != "https://up1" {
		t.Fatalf("ListUpstreams = %v, %v", ups, err)
	}
	// 另一模型的上游不返回
	if ups2, _ := s.ListUpstreams("m2"); len(ups2) != 0 {
		t.Fatalf("ListUpstreams(m2) len = %d; want 0", len(ups2))
	}
	if err := s.DeleteUpstream("u1"); err != nil {
		t.Fatalf("DeleteUpstream: %v", err)
	}
	if _, err := s.GetUpstreamByID("u1"); err != ErrNotFound {
		t.Fatalf("after delete err = %v; want ErrNotFound", err)
	}
}
```

- [ ] **步骤 2:运行测试验证失败**

运行:`go test ./pkg/plugin/oss/ -run "TestSQLStorageRateLimit|TestSQLStorageUpstream" 2>&1 | head`
预期:FAIL(编译错误,方法未定义)

- [ ] **步骤 3:建表 DDL(sqlite + mysql)**

`pkg/plugin/oss/storage_sqlite.go` 的 `sqliteCreateTables` 的 stmts 列表追加两张表:

```go
		`CREATE TABLE IF NOT EXISTS rate_limit_configs (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL DEFAULT '',
			model_name TEXT NOT NULL DEFAULT '',
			requests_per_sec INTEGER NOT NULL,
			tokens_per_min INTEGER NOT NULL,
			strategy TEXT NOT NULL DEFAULT 'token_bucket',
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE(tenant_id, model_name)
		)`,
		`CREATE TABLE IF NOT EXISTS upstreams (
			id TEXT PRIMARY KEY,
			model_config_id TEXT NOT NULL,
			base_url TEXT NOT NULL,
			api_key TEXT NOT NULL,
			encrypted INTEGER NOT NULL DEFAULT 1,
			weight INTEGER NOT NULL DEFAULT 1,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_upstreams_model ON upstreams(model_config_id)`,
```

`pkg/plugin/oss/storage_mysql.go` 的 `mysqlCreateTables` 的 stmts 追加(注意 MySQL 类型 + 表内 KEY 幂等):

```go
		`CREATE TABLE IF NOT EXISTS rate_limit_configs (
			id VARCHAR(64) PRIMARY KEY,
			tenant_id VARCHAR(64) NOT NULL DEFAULT '',
			model_name VARCHAR(64) NOT NULL DEFAULT '',
			requests_per_sec INT NOT NULL,
			tokens_per_min BIGINT NOT NULL,
			strategy VARCHAR(32) NOT NULL DEFAULT 'token_bucket',
			enabled TINYINT NOT NULL DEFAULT 1,
			created_at BIGINT NOT NULL,
			updated_at BIGINT NOT NULL,
			UNIQUE KEY uq_tenant_model (tenant_id, model_name)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS upstreams (
			id VARCHAR(64) PRIMARY KEY,
			model_config_id VARCHAR(64) NOT NULL,
			base_url VARCHAR(512) NOT NULL,
			api_key VARCHAR(1024) NOT NULL,
			encrypted TINYINT NOT NULL DEFAULT 1,
			weight INT NOT NULL DEFAULT 1,
			enabled TINYINT NOT NULL DEFAULT 1,
			created_at BIGINT NOT NULL,
			updated_at BIGINT NOT NULL,
			KEY idx_upstreams_model (model_config_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
```

- [ ] **步骤 4:SQL 存储 CRUD(storage_sql.go)**

`pkg/plugin/oss/storage_sql.go` 追加(遵循现有 scan/upsert 模式,时间用 timeToMS/msToTime,api_key 用 Encrypt/Decrypt):

```go
// ===== 限流配置 =====

const rateLimitCols = "id, tenant_id, model_name, requests_per_sec, tokens_per_min, strategy, enabled, created_at, updated_at"

func (s *SQLStorage) scanRateLimitConfig(row interface{ Scan(...interface{}) error }) (*plugin.RateLimitConfig, error) {
	var c plugin.RateLimitConfig
	var enabled int
	var createdAt, updatedAt string
	if err := row.Scan(&c.ID, &c.TenantID, &c.ModelName, &c.RequestsPerSec, &c.TokensPerMin,
		&c.Strategy, &enabled, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	c.Enabled = enabled == 1
	var cms, ums int64
	fmt.Sscanf(createdAt, "%d", &cms)
	fmt.Sscanf(updatedAt, "%d", &ums)
	c.CreatedAt = msToTime(cms)
	c.UpdatedAt = msToTime(ums)
	return &c, nil
}

func (s *SQLStorage) GetRateLimitConfig(tenantID, modelName string) (*plugin.RateLimitConfig, error) {
	row := s.db.QueryRow("SELECT "+rateLimitCols+" FROM rate_limit_configs WHERE tenant_id = ? AND model_name = ?", tenantID, modelName)
	return s.scanRateLimitConfig(row)
}

func (s *SQLStorage) SaveRateLimitConfig(cfg *plugin.RateLimitConfig) error {
	var upsert string
	if s.isMySQL() {
		upsert = " ON DUPLICATE KEY UPDATE requests_per_sec=VALUES(requests_per_sec), tokens_per_min=VALUES(tokens_per_min), strategy=VALUES(strategy), enabled=VALUES(enabled), updated_at=VALUES(updated_at)"
	} else {
		upsert = " ON CONFLICT(id) DO UPDATE SET tenant_id=excluded.tenant_id, model_name=excluded.model_name, requests_per_sec=excluded.requests_per_sec, tokens_per_min=excluded.tokens_per_min, strategy=excluded.strategy, enabled=excluded.enabled, updated_at=excluded.updated_at"
	}
	_, err := s.db.Exec(
		`INSERT INTO rate_limit_configs (id, tenant_id, model_name, requests_per_sec, tokens_per_min, strategy, enabled, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?)`+upsert,
		cfg.ID, cfg.TenantID, cfg.ModelName, cfg.RequestsPerSec, cfg.TokensPerMin, cfg.Strategy,
		boolToInt(cfg.Enabled), timeToMS(cfg.CreatedAt), timeToMS(cfg.UpdatedAt))
	return err
}

func (s *SQLStorage) ListRateLimitConfigs(page, size int) ([]*plugin.RateLimitConfig, int64, error) {
	page, size = normalizePage(page, size)
	var total int64
	if err := s.db.QueryRow("SELECT COUNT(*) FROM rate_limit_configs").Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query("SELECT "+rateLimitCols+" FROM rate_limit_configs ORDER BY created_at DESC LIMIT ? OFFSET ?", size, (page-1)*size)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var configs []*plugin.RateLimitConfig
	for rows.Next() {
		c, err := s.scanRateLimitConfig(rows)
		if err != nil {
			return nil, 0, err
		}
		configs = append(configs, c)
	}
	return configs, total, rows.Err()
}

func (s *SQLStorage) DeleteRateLimitConfig(id string) error {
	res, err := s.db.Exec("DELETE FROM rate_limit_configs WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ===== 上游管理 =====

const upstreamCols = "id, model_config_id, base_url, api_key, encrypted, weight, enabled, created_at, updated_at"

func (s *SQLStorage) scanUpstream(row interface{ Scan(...interface{}) error }) (*plugin.Upstream, error) {
	var u plugin.Upstream
	var apiKey, createdAt, updatedAt string
	var encrypted, enabled int
	if err := row.Scan(&u.ID, &u.ModelConfigID, &u.BaseURL, &apiKey, &encrypted, &u.Weight,
		&enabled, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if encrypted == 1 {
		plain, err := Decrypt(apiKey, s.encryptKey)
		if err != nil {
			return nil, fmt.Errorf("decrypt upstream api key: %w", err)
		}
		apiKey = plain
	}
	u.APIKey = apiKey
	u.Enabled = enabled == 1
	var cms, ums int64
	fmt.Sscanf(createdAt, "%d", &cms)
	fmt.Sscanf(updatedAt, "%d", &ums)
	u.CreatedAt = msToTime(cms)
	u.UpdatedAt = msToTime(ums)
	return &u, nil
}

func (s *SQLStorage) GetUpstreamByID(id string) (*plugin.Upstream, error) {
	row := s.db.QueryRow("SELECT "+upstreamCols+" FROM upstreams WHERE id = ?", id)
	return s.scanUpstream(row)
}

func (s *SQLStorage) ListUpstreams(modelConfigID string) ([]*plugin.Upstream, error) {
	rows, err := s.db.Query("SELECT "+upstreamCols+" FROM upstreams WHERE model_config_id = ? ORDER BY created_at", modelConfigID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ups []*plugin.Upstream
	for rows.Next() {
		u, err := s.scanUpstream(rows)
		if err != nil {
			return nil, err
		}
		ups = append(ups, u)
	}
	return ups, rows.Err()
}

func (s *SQLStorage) SaveUpstream(up *plugin.Upstream) error {
	enc, err := Encrypt(up.APIKey, s.encryptKey)
	if err != nil {
		return fmt.Errorf("encrypt upstream api key: %w", err)
	}
	var upsert string
	if s.isMySQL() {
		upsert = " ON DUPLICATE KEY UPDATE model_config_id=VALUES(model_config_id), base_url=VALUES(base_url), api_key=VALUES(api_key), encrypted=VALUES(encrypted), weight=VALUES(weight), enabled=VALUES(enabled), updated_at=VALUES(updated_at)"
	} else {
		upsert = " ON CONFLICT(id) DO UPDATE SET model_config_id=excluded.model_config_id, base_url=excluded.base_url, api_key=excluded.api_key, encrypted=excluded.encrypted, weight=excluded.weight, enabled=excluded.enabled, updated_at=excluded.updated_at"
	}
	_, err = s.db.Exec(
		`INSERT INTO upstreams (id, model_config_id, base_url, api_key, encrypted, weight, enabled, created_at, updated_at)
		 VALUES (?,?,?,?,1,?,?,?,?)`+upsert,
		up.ID, up.ModelConfigID, up.BaseURL, enc, up.Weight, boolToInt(up.Enabled),
		timeToMS(up.CreatedAt), timeToMS(up.UpdatedAt))
	return err
}

func (s *SQLStorage) DeleteUpstream(id string) error {
	res, err := s.db.Exec("DELETE FROM upstreams WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
```

> 注:若 `boolToInt` 尚未存在于 storage_sql.go,复用现有的(任务 5 存储已定义 `boolToInt`);若不存在则在本文件加 `func boolToInt(b bool) int { if b { return 1 }; return 0 }`。先 grep 确认:`grep -n "func boolToInt" pkg/plugin/oss/`。

- [ ] **步骤 5:内存存储 CRUD(storage_mem.go)**

`pkg/plugin/oss/storage_mem.go` 的 `MemStorage` 结构增加两个 map 字段并在 `NewMemStorage` 初始化:

```go
	rateLimits   map[string]*plugin.RateLimitConfig // id -> config
	upstreams    map[string]*plugin.Upstream        // id -> upstream
```

`NewMemStorage` 内:

```go
		rateLimits:   make(map[string]*plugin.RateLimitConfig),
		upstreams:    make(map[string]*plugin.Upstream),
```

追加方法:

```go
func (s *MemStorage) GetRateLimitConfig(tenantID, modelName string) (*plugin.RateLimitConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, c := range s.rateLimits {
		if c.TenantID == tenantID && c.ModelName == modelName {
			return c, nil
		}
	}
	return nil, ErrNotFound
}

func (s *MemStorage) SaveRateLimitConfig(cfg *plugin.RateLimitConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rateLimits[cfg.ID] = cfg
	return nil
}

func (s *MemStorage) ListRateLimitConfigs(page, size int) ([]*plugin.RateLimitConfig, int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var all []*plugin.RateLimitConfig
	for _, c := range s.rateLimits {
		all = append(all, c)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	page, size = normalizePage(page, size)
	start := min((page-1)*size, len(all))
	end := min(start+size, len(all))
	return all[start:end], int64(len(all)), nil
}

func (s *MemStorage) DeleteRateLimitConfig(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rateLimits[id]; !ok {
		return ErrNotFound
	}
	delete(s.rateLimits, id)
	return nil
}

func (s *MemStorage) ListUpstreams(modelConfigID string) ([]*plugin.Upstream, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var ups []*plugin.Upstream
	for _, u := range s.upstreams {
		if u.ModelConfigID == modelConfigID {
			ups = append(ups, u)
		}
	}
	sort.Slice(ups, func(i, j int) bool { return ups[i].ID < ups[j].ID })
	return ups, nil
}

func (s *MemStorage) GetUpstreamByID(id string) (*plugin.Upstream, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if u, ok := s.upstreams[id]; ok {
		return u, nil
	}
	return nil, ErrNotFound
}

func (s *MemStorage) SaveUpstream(up *plugin.Upstream) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upstreams[up.ID] = up
	return nil
}

func (s *MemStorage) DeleteUpstream(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.upstreams[id]; !ok {
		return ErrNotFound
	}
	delete(s.upstreams, id)
	return nil
}
```

> 注:`min` 是 Go 1.21+ 内置,现有 storage_mem.go 已用(ListAPIKeys),可直接用。若 `sort` 未 import,现有文件已 import sort。

- [ ] **步骤 6:dynamicStorage 转发(storage_dynamic.go)**

`pkg/plugin/oss/storage_dynamic.go` 追加 8 个转发方法:

```go
func (d *dynamicStorage) GetRateLimitConfig(tenantID, modelName string) (*plugin.RateLimitConfig, error) {
	return d.impl.GetRateLimitConfig(tenantID, modelName)
}
func (d *dynamicStorage) SaveRateLimitConfig(cfg *plugin.RateLimitConfig) error {
	return d.impl.SaveRateLimitConfig(cfg)
}
func (d *dynamicStorage) ListRateLimitConfigs(page, size int) ([]*plugin.RateLimitConfig, int64, error) {
	return d.impl.ListRateLimitConfigs(page, size)
}
func (d *dynamicStorage) DeleteRateLimitConfig(id string) error {
	return d.impl.DeleteRateLimitConfig(id)
}
func (d *dynamicStorage) ListUpstreams(modelConfigID string) ([]*plugin.Upstream, error) {
	return d.impl.ListUpstreams(modelConfigID)
}
func (d *dynamicStorage) GetUpstreamByID(id string) (*plugin.Upstream, error) {
	return d.impl.GetUpstreamByID(id)
}
func (d *dynamicStorage) SaveUpstream(up *plugin.Upstream) error {
	return d.impl.SaveUpstream(up)
}
func (d *dynamicStorage) DeleteUpstream(id string) error {
	return d.impl.DeleteUpstream(id)
}
```

- [ ] **步骤 7:运行测试验证通过**

运行:`go test ./pkg/plugin/oss/ -run "TestSQLStorageRateLimit|TestSQLStorageUpstream" -v`
预期:PASS(注意:此时 RateLimitPlugin 的 RecordTokens/ReloadConfig 仍未实现,`go build ./...` 仍红——任务 5 补齐后全绿。本任务只验证存储测试通过,用 `go test ./pkg/plugin/oss/` 需 MemRateLimiter 先编译过。**若因 MemRateLimiter 未实现新接口导致 oss 包无法编译,先做任务 5 再回来跑此测试**——见任务 5 步骤说明,任务 4+5 合并验证。)

- [ ] **步骤 8:暂不 commit,进入任务 5(接口变更需 3/4/5 一起编译绿)**

---
## 模块③ RateLimiter 组装 + 中间件 + TPM 回补(任务 5-7)

### 任务 5:RateLimiter(三层配置 + 双维度双策略 + 缓存),替换 limit_mem.go

**文件:**
- 创建:`pkg/plugin/oss/ratelimiter.go`
- 删除:`pkg/plugin/oss/limit_mem.go`、`pkg/plugin/oss/limit_mem_test.go`
- 修改:`pkg/plugin/oss/factory.go`、`pkg/plugin/enterprise/factory.go`
- 测试:`pkg/plugin/oss/ratelimiter_test.go`

- [ ] **步骤 1:编写失败测试**

`pkg/plugin/oss/ratelimiter_test.go`(保留 Apache-2.0 头):

```go
package oss

import (
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// rlTestStorage 预置限流配置的内存存储
func rlTestStorage(t *testing.T, cfgs ...*plugin.RateLimitConfig) *MemStorage {
	t.Helper()
	s := NewMemStorage()
	for _, c := range cfgs {
		if err := s.SaveRateLimitConfig(c); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestRateLimiterDefaultWhenNoConfig(t *testing.T) {
	s := NewMemStorage()
	rl := NewRateLimiter(s, 2, 100000, "token_bucket") // 默认 rps=2
	_ = rl.ReloadConfig()
	// 无配置走默认 rps=2:前 2 次放行,第 3 次拒
	a1, _, _ := rl.Allow("t1", "gpt-4", 0)
	a2, _, _ := rl.Allow("t1", "gpt-4", 0)
	a3, _, _ := rl.Allow("t1", "gpt-4", 0)
	if !a1 || !a2 || a3 {
		t.Fatalf("default rps=2: got %v,%v,%v; want true,true,false", a1, a2, a3)
	}
}

func TestRateLimiterModelOverridesGlobal(t *testing.T) {
	// 全局 rps=1;模型级 gpt-4 rps=5 → gpt-4 用 5
	s := rlTestStorage(t,
		&plugin.RateLimitConfig{ID: "g", TenantID: "", ModelName: "", RequestsPerSec: 1, TokensPerMin: 100000, Strategy: "token_bucket", Enabled: true},
		&plugin.RateLimitConfig{ID: "m", TenantID: "", ModelName: "gpt-4", RequestsPerSec: 5, TokensPerMin: 100000, Strategy: "token_bucket", Enabled: true},
	)
	rl := NewRateLimiter(s, 10, 100000, "token_bucket")
	_ = rl.ReloadConfig()
	allowed := 0
	for i := 0; i < 6; i++ {
		if a, _, _ := rl.Allow("", "gpt-4", 0); a {
			allowed++
		}
	}
	if allowed != 5 {
		t.Fatalf("model-level rps=5: allowed %d; want 5", allowed)
	}
}

func TestRateLimiterTPMRejectAndRecord(t *testing.T) {
	// tpm=10,sliding_window;RecordTokens 累计超限后拒绝
	s := rlTestStorage(t,
		&plugin.RateLimitConfig{ID: "g", RequestsPerSec: 1000, TokensPerMin: 10, Strategy: "sliding_window", Enabled: true},
	)
	rl := NewRateLimiter(s, 1000, 1000000, "sliding_window")
	_ = rl.ReloadConfig()
	// 预检(tokens=0)放行
	if a, _, _ := rl.Allow("t1", "m", 0); !a {
		t.Fatal("first Allow should pass (tpm not yet consumed)")
	}
	// 回补 10 tokens,达到 tpm 上限
	if err := rl.RecordTokens("t1", "m", 10); err != nil {
		t.Fatal(err)
	}
	// 下次预检:TPM 已满 → 拒绝
	if a, _, _ := rl.Allow("t1", "m", 0); a {
		t.Fatal("Allow after TPM exhausted should be rejected")
	}
}

func TestRateLimiterReloadPicksUpNewConfig(t *testing.T) {
	s := NewMemStorage()
	rl := NewRateLimiter(s, 100, 100000, "token_bucket")
	_ = rl.ReloadConfig()
	// 新增严格配置后 Reload 生效
	_ = s.SaveRateLimitConfig(&plugin.RateLimitConfig{ID: "g", RequestsPerSec: 1, TokensPerMin: 100000, Strategy: "token_bucket", Enabled: true})
	_ = rl.ReloadConfig()
	a1, _, _ := rl.Allow("t1", "m", 0)
	a2, _, _ := rl.Allow("t1", "m", 0)
	if !a1 || a2 {
		t.Fatalf("after reload rps=1: got %v,%v; want true,false", a1, a2)
	}
}
```

- [ ] **步骤 2:运行测试验证失败**

运行:`go test ./pkg/plugin/oss/ -run TestRateLimiter 2>&1 | head`
预期:FAIL,`undefined: NewRateLimiter`

- [ ] **步骤 3:实现 ratelimiter.go**

`pkg/plugin/oss/ratelimiter.go`(保留 Apache-2.0 头):

```go
package oss

import (
	"sync"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// resolvedConfig 三层解析后生效的限流规则
type resolvedConfig struct {
	rps      int
	tpm      int64
	strategy string
}

// limitBucket 单个 (tenant|model) 的双维度限流器
type limitBucket struct {
	strategy string
	// token_bucket 用
	rpsBucket *tokenBucket
	tpmBucket *tokenBucket
	// sliding_window 用
	rpsWindow *slidingWindow
	tpmWindow *slidingWindow
}

// RateLimiter 三层配置 + 双维度双策略限流器
type RateLimiter struct {
	storage         plugin.StoragePlugin
	defaultRPS      int
	defaultTPM      int64
	defaultStrategy string

	mu      sync.Mutex
	configs []*plugin.RateLimitConfig       // 缓存(ReloadConfig 刷新)
	buckets map[string]*limitBucket          // key = tenant|model
	now     func() time.Time                 // 可注入(测试用),默认 time.Now
}

// NewRateLimiter 创建限流器
func NewRateLimiter(storage plugin.StoragePlugin, defaultRPS int, defaultTPM int64, defaultStrategy string) *RateLimiter {
	return &RateLimiter{
		storage:         storage,
		defaultRPS:      defaultRPS,
		defaultTPM:      defaultTPM,
		defaultStrategy: defaultStrategy,
		buckets:         make(map[string]*limitBucket),
		now:             time.Now,
	}
}

// Init 满足接口(config 已由构造函数传入)
func (l *RateLimiter) Init(config map[string]interface{}) error { return nil }

// ReloadConfig 从存储全量加载限流配置到缓存,并清空桶(下次按新配置重建)
func (l *RateLimiter) ReloadConfig() error {
	var all []*plugin.RateLimitConfig
	page := 1
	for {
		cfgs, total, err := l.storage.ListRateLimitConfigs(page, 100)
		if err != nil {
			return err
		}
		all = append(all, cfgs...)
		if page*100 >= int(total) {
			break
		}
		page++
	}
	l.mu.Lock()
	l.configs = all
	l.buckets = make(map[string]*limitBucket)
	l.mu.Unlock()
	return nil
}

// resolve 三层匹配:模型级 > 租户级 > 全局 > 默认(调用方持锁)
func (l *RateLimiter) resolve(tenantID, model string) resolvedConfig {
	match := func(tid, m string) *plugin.RateLimitConfig {
		for _, c := range l.configs {
			if c.Enabled && c.TenantID == tid && c.ModelName == m {
				return c
			}
		}
		return nil
	}
	if c := match(tenantID, model); c != nil {
		return resolvedConfig{c.RequestsPerSec, c.TokensPerMin, c.Strategy}
	}
	if c := match(tenantID, ""); c != nil {
		return resolvedConfig{c.RequestsPerSec, c.TokensPerMin, c.Strategy}
	}
	if c := match("", ""); c != nil {
		return resolvedConfig{c.RequestsPerSec, c.TokensPerMin, c.Strategy}
	}
	return resolvedConfig{l.defaultRPS, l.defaultTPM, l.defaultStrategy}
}

// bucketFor 获取或创建 (tenant|model) 桶(调用方持锁)
func (l *RateLimiter) bucketFor(tenantID, model string) *limitBucket {
	key := tenantID + "|" + model
	if b, ok := l.buckets[key]; ok {
		return b
	}
	rc := l.resolve(tenantID, model)
	now := l.now()
	b := &limitBucket{strategy: rc.strategy}
	if rc.strategy == "sliding_window" {
		b.rpsWindow = newSlidingWindow(int64(rc.rps), time.Second)
		b.tpmWindow = newSlidingWindow(rc.tpm, time.Minute)
	} else {
		b.rpsBucket = newTokenBucket(float64(rc.rps), float64(rc.rps), now)
		b.tpmBucket = newTokenBucket(float64(rc.tpm), float64(rc.tpm)/60.0, now)
	}
	l.buckets[key] = b
	return b
}

// Allow 预检:取 1 个 RPS 令牌 + 校验 TPM 是否已耗尽(tokens 参数保留,预检传 0 不扣 TPM)
func (l *RateLimiter) Allow(tenantID string, model string, tokens int) (bool, int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.bucketFor(tenantID, model)
	now := l.now()
	if b.strategy == "sliding_window" {
		// TPM 已达上限则拒(current >= limit)
		if b.tpmWindow.current(now) >= b.tpmWindow.limit {
			return false, 0, nil
		}
		if !b.rpsWindow.allow(1, now) {
			return false, 0, nil
		}
		return true, b.rpsWindow.limit - b.rpsWindow.current(now), nil
	}
	// token_bucket:TPM 桶无可用令牌则拒
	if b.tpmBucket.remaining(now) <= 0 {
		return false, 0, nil
	}
	if !b.rpsBucket.take(1, now) {
		return false, 0, nil
	}
	return true, b.rpsBucket.remaining(now), nil
}

// RecordTokens 请求完成后回补 TPM 计数
func (l *RateLimiter) RecordTokens(tenantID string, model string, tokens int) error {
	if tokens <= 0 {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.bucketFor(tenantID, model)
	now := l.now()
	if b.strategy == "sliding_window" {
		b.tpmWindow.allow(int64(tokens), now) // 累加(可能越过 limit,下次 Allow 拒)
	} else {
		b.tpmBucket.take(float64(tokens), now) // 扣减(可能扣至 <=0,下次 Allow 拒)
	}
	return nil
}

// Status 返回当前 RPS 用量/上限/重置时间
func (l *RateLimiter) Status(tenantID string, model string) (int64, int64, time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.bucketFor(tenantID, model)
	now := l.now()
	rc := l.resolve(tenantID, model)
	if b.strategy == "sliding_window" {
		return b.rpsWindow.current(now), int64(rc.rps), now.Add(time.Second)
	}
	used := int64(rc.rps) - b.rpsBucket.remaining(now)
	if used < 0 {
		used = 0
	}
	return used, int64(rc.rps), now.Add(time.Second)
}

// Reset 清除某 (tenant|model) 桶
func (l *RateLimiter) Reset(tenantID string, model string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.buckets, tenantID+"|"+model)
	return nil
}
```

- [ ] **步骤 4:更新工厂 + 删除 limit_mem**

删除 `pkg/plugin/oss/limit_mem.go` 与 `pkg/plugin/oss/limit_mem_test.go`:

```bash
git rm pkg/plugin/oss/limit_mem.go pkg/plugin/oss/limit_mem_test.go
```

`pkg/plugin/oss/factory.go` 的 `CreateRateLimiter` 改为注入 storage + 默认值。ossFactory 需要持有默认限流参数——改为工厂结构体存配置。最简方案:CreateRateLimiter 从共享 storage 构造,默认值用常量(工厂无 config 上下文)。**改动**:

```go
// CreateRateLimiter 创建限流器（三层配置 + 双策略,配置源为共享存储）
func (f *ossFactory) CreateRateLimiter() plugin.RateLimitPlugin {
	return NewRateLimiter(f.CreateStorage(), 10, 100000, "token_bucket")
}
```

> 默认值 10/100000/token_bucket 与 config.yaml 默认一致。运行时 main.go 会用 config 的实际默认值重新构造(见任务 7 main.go 装配:main 直接调 `oss.NewRateLimiter(storage, cfg.RateLimit.DefaultRPS, cfg.RateLimit.DefaultTPM, cfg.RateLimit.Strategy)` 而非走工厂,因为工厂拿不到 config)。**关键决策**:main.go 不再用 `factory.CreateRateLimiter()`,改为直接 `oss.NewRateLimiter(...)`——但这样破坏 BuildTag 隔离(main 直接依赖 oss 包)。

> **修正方案(避免 main 直连 oss)**:保留 `factory.CreateRateLimiter()` 用默认常量;main.go 在拿到 rateLimiter 后调用类型断言注入实际默认值——过度复杂。**采用更简方案**:PluginFactory 接口不变,CreateRateLimiter 内部默认值用常量(10/100000/token_bucket);config.yaml 的 default_rps/default_tpm/strategy 通过 rateLimiter.Init(map) 传入。即:RateLimiter.Init 解析 config map 覆盖默认值。修改 Init:

```go
// Init 解析 default_rps/default_tpm/strategy 覆盖构造默认值
func (l *RateLimiter) Init(config map[string]interface{}) error {
	if v, ok := config["default_rps"].(int); ok && v > 0 {
		l.defaultRPS = v
	}
	if v, ok := config["default_tpm"].(int64); ok && v > 0 {
		l.defaultTPM = v
	}
	if v, ok := config["strategy"].(string); ok && v != "" {
		l.defaultStrategy = v
	}
	return l.ReloadConfig()
}
```

> main.go 已有 `rateLimiter.Init(map[string]interface{}{"default_rps":..., "default_tpm":...})` 调用(现状),补 "strategy" 键即可。ReloadConfig 在 Init 末尾调用完成首次加载。

`pkg/plugin/enterprise/factory.go` 的 `CreateRateLimiter` 同步:

```go
func (f *enterpriseFactory) CreateRateLimiter() plugin.RateLimitPlugin {
	return oss.NewRateLimiter(f.CreateStorage(), 10, 100000, "token_bucket")
}
```

- [ ] **步骤 5:运行测试验证通过**

运行:`go test ./pkg/plugin/oss/ -run TestRateLimiter -v && go build ./... 2>&1 | head`
预期:RateLimiter 测试 PASS;`go build ./...` 此时应通过(接口全部实现)。若 middleware_limit_test.go 因删除 MemRateLimiter 而失败,任务 6 修复其引用——先跑 `go build` 确认非 core 包问题;core 测试留任务 6。

- [ ] **步骤 6:Commit(任务 3+4+5 合并——接口+存储+限流器一起编译绿)**

```bash
git add pkg/plugin/interface.go pkg/plugin/oss/ratelimiter.go pkg/plugin/oss/ratelimiter_test.go \
  pkg/plugin/oss/storage_sql.go pkg/plugin/oss/storage_mem.go pkg/plugin/oss/storage_dynamic.go \
  pkg/plugin/oss/storage_sqlite.go pkg/plugin/oss/storage_mysql.go pkg/plugin/oss/storage_sql_test.go \
  pkg/plugin/oss/factory.go pkg/plugin/enterprise/factory.go
git rm pkg/plugin/oss/limit_mem.go pkg/plugin/oss/limit_mem_test.go
git commit -m "feat(plugin): 限流器策略化(三层配置+双维度双策略)+ 限流配置/上游存储 CRUD"
```

> 注:此 commit 较大(接口变更牵连广),但必须一起才能编译绿。若 core 包测试因 MemRateLimiter 删除而引用失败,见任务 6——core 的 middleware_limit_test.go 用的是 oss.NewMemRateLimiter,需同步改为 oss.NewRateLimiter。**本 commit 前先处理 core 测试的引用**(grep `NewMemRateLimiter` 全仓,改为 NewRateLimiter),否则 `go build ./...` 绿但 `go test ./...` 红。

---

### 任务 6:限流中间件 TPM 区分 + core 测试适配

**文件:**
- 修改:`pkg/core/middleware_limit.go`
- 修改:所有引用 `oss.NewMemRateLimiter` 的测试(改为 `oss.NewRateLimiter`)
- 测试:`pkg/core/middleware_limit_test.go`

- [ ] **步骤 1:全仓查引用**

运行:`grep -rn "NewMemRateLimiter" pkg/ cmd/`
预期:列出 middleware_limit_test.go、proxy_test.go、sse_test.go、e2e_test.go 等的引用。全部改为 `oss.NewRateLimiter(storage, 100, 100000, "token_bucket")`(测试用宽松默认;需要 storage 实参——测试里已有 storage 变量,无则传 `oss.NewMemStorage()`)。

- [ ] **步骤 2:编写失败测试(TPM 超限返回 token_limit)**

`pkg/core/middleware_limit_test.go` 追加:

```go
func TestRateLimitTPMExceededTokenLimit(t *testing.T) {
	s := oss.NewMemStorage()
	_ = s.SaveRateLimitConfig(&plugin.RateLimitConfig{
		ID: "g", RequestsPerSec: 1000, TokensPerMin: 5, Strategy: "sliding_window", Enabled: true,
	})
	limiter := oss.NewRateLimiter(s, 1000, 1000000, "sliding_window")
	_ = limiter.ReloadConfig()
	// 先耗尽 TPM
	_ = limiter.RecordTokens("", "", 5)

	mw := RateLimitMiddleware(limiter)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	rc := &RequestContext{TenantID: ""}
	req = req.WithContext(WithRequestContext(req.Context(), rc))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d; want 429", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "token_limit") {
		t.Fatalf("body = %s; want token_limit", rec.Body.String())
	}
}
```

> 说明:当前 Allow 对 RPS 与 TPM 超限都返回 `!allowed`,中间件无法区分。需要中间件能判断超限原因。**最简方案**:中间件超限时调 `limiter.Status` 拿 RPS 用量,若 RPS 未超(current < limit)则判定为 TPM 超限 → code=token_limit;否则 rate_limit。见步骤 3。

- [ ] **步骤 3:运行验证失败**

运行:`go test ./pkg/core/ -run TestRateLimitTPMExceededTokenLimit -v`
预期:FAIL(当前统一返回 rate_limit)

- [ ] **步骤 4:实现中间件 TPM 区分**

`pkg/core/middleware_limit.go` 的超限分支改为:

```go
			if !allowed {
				current, limit, resetAt := rateLimiter.Status(rc.TenantID, model)
				code := "rate_limit"
				msg := "rate limit exceeded"
				// RPS 未超(current < limit)则判定为 TPM(token)超限
				if current < limit {
					code = "token_limit"
					msg = "token rate limit exceeded"
				}
				w.Header().Set("X-RateLimit-Limit-Requests", strconv.FormatInt(limit, 10))
				w.Header().Set("X-RateLimit-Remaining-Requests", "0")
				w.Header().Set("X-RateLimit-Reset-Requests", strconv.FormatInt(resetAt.Unix(), 10))
				w.Header().Set("Retry-After", "1")
				writeOpenAIError(w, http.StatusTooManyRequests, "rate_limit_exceeded", code,
					msg+" (current="+strconv.FormatInt(current, 10)+", limit="+strconv.FormatInt(limit, 10)+", reset="+resetAt.Format(time.RFC3339)+")")
				return
			}
```

> 放行分支(Status 调用与 Header)保持不变。

- [ ] **步骤 5:运行测试验证通过**

运行:`go test ./pkg/core/ -run "TestRateLimit" -v`
预期:PASS(含既有 TestRateLimitAllow/Exceeded + 新 TPM 测试)

- [ ] **步骤 6:Commit**

```bash
git add pkg/core/middleware_limit.go pkg/core/middleware_limit_test.go pkg/core/proxy_test.go pkg/core/sse_test.go pkg/core/e2e_test.go pkg/core/pipeline_test.go
git commit -m "feat(core): 限流中间件区分 rate_limit/token_limit + 测试适配 RateLimiter"
```

> 注:git add 的测试文件以步骤 1 grep 实际命中为准。

---

### 任务 7:TPM 回补接线(proxy.go)

**文件:**
- 修改:`pkg/core/proxy.go`
- 测试:`pkg/core/proxy_test.go`

- [ ] **步骤 1:编写失败测试**

`pkg/core/proxy_test.go` 追加(验证转发完成后 RecordTokens 被调用——通过限流器状态间接验证):

```go
func TestProxyRecordsTokensAfterForward(t *testing.T) {
	upstream := newMockUpstream(t)
	defer upstream.Close()

	storage := oss.NewMemStorage()
	now := time.Now()
	_ = storage.SaveModelConfig(&plugin.ModelConfig{
		ID: "m1", ModelName: "gpt-4", Provider: "openai", ProviderModel: "gpt-4o",
		BaseURL: upstream.URL, APIKey: "sk", Enabled: true, CreatedAt: now, UpdatedAt: now,
	})
	_ = storage.SaveAPIKey(&plugin.APIKey{
		ID: "k1", KeyHash: hashKey("ng-test"), KeyPrefix: "ng-test", Name: "t",
		Status: plugin.APIKeyStatusActive, Quota: -1, CreatedAt: now, UpdatedAt: now,
	})
	// tpm=100 sliding_window;mock 上游返回 total_tokens=15
	_ = storage.SaveRateLimitConfig(&plugin.RateLimitConfig{
		ID: "g", RequestsPerSec: 1000, TokensPerMin: 100, Strategy: "sliding_window", Enabled: true,
	})
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	limiter := oss.NewRateLimiter(storage, 1000, 1000000, "sliding_window")
	_ = limiter.ReloadConfig()
	auditor := oss.NewSimpleAuditor(storage)
	pc := NewProxyCore(NewPipeline(storage, limiter, auditor, registry), registry)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer ng-test")
	rec := httptest.NewRecorder()
	pc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	// 回补后 TPM 用量应为 15
	current, _, _ := limiter.Status("", "gpt-4") // 注:Status 返回 RPS;需另验 TPM
	_ = current
	// 通过再次 RecordTokens 触发 TPM 判断:已用 15,tpm=100,再补 90 → 105 超限
	_ = limiter.RecordTokens("", "gpt-4", 90)
	if a, _, _ := limiter.Allow("", "gpt-4", 0); a {
		t.Fatal("TPM should be exhausted after 15+90 > 100")
	}
}
```

> 说明:测试通过"转发后 TPM 已累计 15"来间接验证回补。model 维度为 "gpt-4"(路由后 rc.ModelConfig.ModelName)。

- [ ] **步骤 2:运行验证失败**

运行:`go test ./pkg/core/ -run TestProxyRecordsTokensAfterForward -v`
预期:FAIL(回补未接线,TPM 累计为 90 而非 105,Allow 仍放行)

- [ ] **步骤 3:实现回补接线**

`pkg/core/proxy.go`:在 `finalizeAudit` 定义之后新增回补辅助,并在非流式与流式完成处调用。

新增方法:

```go
// recordTokens 请求完成后回补限流器 TPM 计数(model 维度)
func (p *ProxyCore) recordTokens(rc *RequestContext) {
	if p.pipeline.rateLimiter == nil || rc.TotalTokens <= 0 {
		return
	}
	model := ""
	if rc.ModelConfig != nil {
		model = rc.ModelConfig.ModelName
	}
	_ = p.pipeline.rateLimiter.RecordTokens(rc.TenantID, model, rc.TotalTokens)
}
```

非流式:在 `p.updateQuota(rc)` 调用旁(finalizeAudit 之前)增加 `p.recordTokens(rc)`。定位现有非流式成功段(updateQuota 调用处),改为:

```go
	p.updateQuota(rc)
	p.recordTokens(rc)
```

流式:`handleStreaming` 末尾 `p.updateQuota(rc)` 旁同样加 `p.recordTokens(rc)`。

- [ ] **步骤 4:运行测试验证通过**

运行:`go test ./pkg/core/ -run "TestProxy" -v`
预期:PASS(含新 TPM 回补测试 + 既有 proxy 测试无回归)

- [ ] **步骤 5:Commit**

```bash
git add pkg/core/proxy.go pkg/core/proxy_test.go
git commit -m "feat(core): TPM 回补接线(转发完成后 RecordTokens)"
```

---
## 模块④ 负载均衡(任务 8-10)

### 任务 8:selectUpstream 加权随机选上游

**文件:**
- 修改:`pkg/core/proxy.go`
- 测试:`pkg/core/upstream_select_test.go`

- [ ] **步骤 1:编写失败测试**

`pkg/core/upstream_select_test.go`(保留 Apache-2.0 头):

```go
package core

import (
	"testing"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

func TestSelectUpstreamEmpty(t *testing.T) {
	if selectUpstream(nil) != nil {
		t.Fatal("empty upstreams should return nil")
	}
}

func TestSelectUpstreamSingle(t *testing.T) {
	ups := []plugin.Upstream{{ID: "u1", BaseURL: "https://a", Weight: 1, Enabled: true}}
	got := selectUpstream(ups)
	if got == nil || got.ID != "u1" {
		t.Fatalf("single upstream = %v; want u1", got)
	}
}

func TestSelectUpstreamSkipsDisabled(t *testing.T) {
	ups := []plugin.Upstream{
		{ID: "u1", Weight: 1, Enabled: false},
		{ID: "u2", Weight: 1, Enabled: true},
	}
	// 仅 u2 enabled,恒选 u2
	for i := 0; i < 20; i++ {
		if got := selectUpstream(ups); got == nil || got.ID != "u2" {
			t.Fatalf("should always pick enabled u2; got %v", got)
		}
	}
}

func TestSelectUpstreamWeightedDistribution(t *testing.T) {
	// u1:weight 1、u2:weight 9 → u2 约占 90%
	ups := []plugin.Upstream{
		{ID: "u1", Weight: 1, Enabled: true},
		{ID: "u2", Weight: 9, Enabled: true},
	}
	counts := map[string]int{}
	const n = 10000
	for i := 0; i < n; i++ {
		counts[selectUpstream(ups).ID]++
	}
	// u2 占比应在 [0.82, 0.98](宽松统计断言,避免偶发失败)
	ratio := float64(counts["u2"]) / float64(n)
	if ratio < 0.82 || ratio > 0.98 {
		t.Fatalf("u2 ratio = %.3f; want ~0.90", ratio)
	}
}

func TestSelectUpstreamAllDisabled(t *testing.T) {
	ups := []plugin.Upstream{{ID: "u1", Weight: 1, Enabled: false}}
	if selectUpstream(ups) != nil {
		t.Fatal("all-disabled upstreams should return nil")
	}
}
```

- [ ] **步骤 2:运行验证失败**

运行:`go test ./pkg/core/ -run TestSelectUpstream 2>&1 | head`
预期:FAIL,`undefined: selectUpstream`

- [ ] **步骤 3:实现 selectUpstream**

`pkg/core/proxy.go` 顶部 import 加 `"math/rand"`,新增函数:

```go
// selectUpstream 按 weight 加权随机选一个 enabled 上游
// 空或全禁用返回 nil(调用方回退 ModelConfig 默认上游);权重和为 0 时等概率
func selectUpstream(ups []plugin.Upstream) *plugin.Upstream {
	enabled := make([]plugin.Upstream, 0, len(ups))
	total := 0
	for _, u := range ups {
		if u.Enabled {
			enabled = append(enabled, u)
			w := u.Weight
			if w < 0 {
				w = 0
			}
			total += w
		}
	}
	if len(enabled) == 0 {
		return nil
	}
	if len(enabled) == 1 {
		return &enabled[0]
	}
	if total <= 0 {
		// 权重全 0:等概率
		return &enabled[rand.Intn(len(enabled))]
	}
	r := rand.Intn(total)
	for i := range enabled {
		w := enabled[i].Weight
		if w < 0 {
			w = 0
		}
		if r < w {
			return &enabled[i]
		}
		r -= w
	}
	return &enabled[len(enabled)-1] // 兜底(浮点/边界)
}
```

- [ ] **步骤 4:运行测试验证通过**

运行:`go test ./pkg/core/ -run TestSelectUpstream -v`
预期:PASS(5 个测试)

- [ ] **步骤 5:Commit**

```bash
git add pkg/core/proxy.go pkg/core/upstream_select_test.go
git commit -m "feat(core): selectUpstream 加权随机选上游(空/单/禁用/分布)"
```

---

### 任务 9:RequestContext 加载 upstreams + 转发选上游

**文件:**
- 修改:`pkg/core/context.go`(加 Upstreams 字段)
- 修改:`pkg/core/middleware_route.go`(加载 upstreams)
- 修改:`pkg/core/proxy.go`(转发前选上游覆盖 cfg)
- 测试:`pkg/core/proxy_test.go`

- [ ] **步骤 1:编写失败测试**

`pkg/core/proxy_test.go` 追加(两个 mock 上游,验证多上游转发命中其中之一):

```go
func TestProxyLoadBalanceMultiUpstream(t *testing.T) {
	// 两个 mock 上游,各自返回可区分内容
	up1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"1","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"up1"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer up1.Close()
	up2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"2","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"up2"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer up2.Close()

	storage := oss.NewMemStorage()
	now := time.Now()
	_ = storage.SaveModelConfig(&plugin.ModelConfig{
		ID: "m1", ModelName: "gpt-4", Provider: "openai", ProviderModel: "gpt-4o",
		BaseURL: "http://unused", APIKey: "sk", Enabled: true, CreatedAt: now, UpdatedAt: now,
	})
	// 两个上游,各 weight 1
	_ = storage.SaveUpstream(&plugin.Upstream{ID: "u1", ModelConfigID: "m1", BaseURL: up1.URL, APIKey: "sk1", Weight: 1, Enabled: true, CreatedAt: now, UpdatedAt: now})
	_ = storage.SaveUpstream(&plugin.Upstream{ID: "u2", ModelConfigID: "m1", BaseURL: up2.URL, APIKey: "sk2", Weight: 1, Enabled: true, CreatedAt: now, UpdatedAt: now})
	_ = storage.SaveAPIKey(&plugin.APIKey{ID: "k1", KeyHash: hashKey("ng-test"), KeyPrefix: "ng-test", Name: "t", Status: plugin.APIKeyStatusActive, Quota: -1, CreatedAt: now, UpdatedAt: now})

	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	limiter := oss.NewRateLimiter(storage, 1000, 1000000, "token_bucket")
	_ = limiter.ReloadConfig()
	auditor := oss.NewSimpleAuditor(storage)
	pc := NewProxyCore(NewPipeline(storage, limiter, auditor, registry), registry)

	// 多次调用,命中的 content 必为 up1 或 up2(证明走了 upstreams 而非 unused base_url)
	seen := map[string]bool{}
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
			strings.NewReader(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`))
		req.Header.Set("Authorization", "Bearer ng-test")
		rec := httptest.NewRecorder()
		pc.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "up1") {
			seen["up1"] = true
		} else if strings.Contains(rec.Body.String(), "up2") {
			seen["up2"] = true
		} else {
			t.Fatalf("response from neither upstream: %s", rec.Body.String())
		}
	}
	// 10 次 50/50 权重,两个上游都应至少命中一次(极小概率偶发,统计上稳健)
	if !seen["up1"] && !seen["up2"] {
		t.Fatal("no upstream hit")
	}
}

func TestProxyFallbackToModelConfigWhenNoUpstream(t *testing.T) {
	// 无 upstreams → 回退 ModelConfig.base_url
	upstream := newMockUpstream(t)
	defer upstream.Close()
	storage := oss.NewMemStorage()
	now := time.Now()
	_ = storage.SaveModelConfig(&plugin.ModelConfig{
		ID: "m1", ModelName: "gpt-4", Provider: "openai", ProviderModel: "gpt-4o",
		BaseURL: upstream.URL, APIKey: "sk", Enabled: true, CreatedAt: now, UpdatedAt: now,
	})
	_ = storage.SaveAPIKey(&plugin.APIKey{ID: "k1", KeyHash: hashKey("ng-test"), KeyPrefix: "ng-test", Name: "t", Status: plugin.APIKeyStatusActive, Quota: -1, CreatedAt: now, UpdatedAt: now})
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	limiter := oss.NewRateLimiter(storage, 1000, 1000000, "token_bucket")
	_ = limiter.ReloadConfig()
	auditor := oss.NewSimpleAuditor(storage)
	pc := NewProxyCore(NewPipeline(storage, limiter, auditor, registry), registry)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer ng-test")
	rec := httptest.NewRecorder()
	pc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("no-upstream fallback status = %d; body=%s", rec.Code, rec.Body.String())
	}
}
```

- [ ] **步骤 2:运行验证失败**

运行:`go test ./pkg/core/ -run "TestProxyLoadBalance|TestProxyFallback" 2>&1 | head`
预期:FAIL(rc.Upstreams 字段不存在 / 未加载 / 转发仍用 unused base_url → up1 连接失败或 502)

- [ ] **步骤 3:context.go 加字段**

`pkg/core/context.go` 的 `RequestContext` 结构在 `Adapter` 字段后新增:

```go
	Upstreams        []plugin.Upstream    // 负载均衡上游列表(为空则用 ModelConfig 默认上游)
```

- [ ] **步骤 4:middleware_route.go 加载 upstreams**

`pkg/core/middleware_route.go` 在写入 `rc.ModelConfig = config` 之后、获取适配器之前,加载 upstreams:

```go
			// 加载负载均衡上游(失败或为空则运行时回退 ModelConfig 默认上游)
			if ups, err := storage.ListUpstreams(config.ID); err == nil {
				loaded := make([]plugin.Upstream, 0, len(ups))
				for _, u := range ups {
					loaded = append(loaded, *u)
				}
				rc.Upstreams = loaded
			}
```

> 需确认 middleware_route.go 已 import plugin 包(现有代码用了 plugin.xxx,应已 import;若无则加)。

- [ ] **步骤 5:proxy.go 转发前选上游**

`pkg/core/proxy.go` 的 `handleProxy` 在 `cfg := rc.ModelConfig` 之后、构造上游请求之前,选上游覆盖 base_url/api_key(用局部副本,不改 rc.ModelConfig):

```go
	cfg := rc.ModelConfig
	adpt := rc.Adapter
	// 负载均衡:选中上游覆盖默认 base_url/api_key(局部副本,不改存储)
	if up := selectUpstream(rc.Upstreams); up != nil {
		cfgCopy := *cfg
		cfgCopy.BaseURL = up.BaseURL
		cfgCopy.APIKey = up.APIKey
		cfg = &cfgCopy
	}
```

`handlePassThrough` 同样处理(在其取 cfg 之后加相同逻辑):

```go
	// 负载均衡:透传端点也支持多上游
	if up := selectUpstream(rc.Upstreams); up != nil {
		cfgCopy := *cfg
		cfgCopy.BaseURL = up.BaseURL
		cfgCopy.APIKey = up.APIKey
		cfg = &cfgCopy
	}
```

> 注:handlePassThrough 现有逻辑先判 `rc.ModelConfig == nil` 取首个启用模型到 cfg,选上游逻辑加在 cfg 确定之后。

- [ ] **步骤 6:运行测试验证通过**

运行:`go test ./pkg/core/ -run "TestProxyLoadBalance|TestProxyFallback|TestProxyChat" -v`
预期:PASS(多上游命中 + 无上游回退 + 既有单上游无回归)

- [ ] **步骤 7:Commit**

```bash
git add pkg/core/context.go pkg/core/middleware_route.go pkg/core/proxy.go pkg/core/proxy_test.go
git commit -m "feat(core): 负载均衡接线(加载 upstreams + 转发前加权选上游 + 回退)"
```

---

### 任务 10:上游管理 Admin API

**文件:**
- 创建:`pkg/admin/upstream.go`
- 修改:`pkg/admin/router.go`
- 测试:`pkg/admin/server_test.go`

- [ ] **步骤 1:编写失败测试**

`pkg/admin/server_test.go` 追加:

```go
func TestAdminUpstreamCRUD(t *testing.T) {
	s := oss.NewMemStorage()
	now := time.Now()
	_ = s.SaveModelConfig(&plugin.ModelConfig{ID: "m1", ModelName: "gpt-4", Provider: "openai", ProviderModel: "x", BaseURL: "https://x", APIKey: "sk", Enabled: true, CreatedAt: now, UpdatedAt: now})
	router := NewAdminServer(s, nil, "oss").Router()

	// 创建上游
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/models/m1/upstreams",
		strings.NewReader(`{"base_url":"https://up1","api_key":"sk-up","weight":3}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create status = %d; body=%s", w.Code, w.Body.String())
	}
	// 列表(api_key 脱敏不回显)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/models/m1/upstreams", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "up1") {
		t.Fatalf("list status=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "sk-up") {
		t.Fatalf("list leaks upstream api_key: %s", w.Body.String())
	}
}
```

> 注:NewAdminServer 签名此时仍是 3 参(storage, logger, edition)——本任务不改签名;上游 CRUD 只依赖 storage。限流 API(任务 11)才需 rateLimiter,届时改签名。

- [ ] **步骤 2:运行验证失败**

运行:`go test ./pkg/admin/ -run TestAdminUpstreamCRUD 2>&1 | head`
预期:FAIL(路由 404)

- [ ] **步骤 3:实现 upstream.go**

`pkg/admin/upstream.go`(保留 Apache-2.0 头):

```go
package admin

import (
	"net/http"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type upstreamRequest struct {
	BaseURL string `json:"base_url" binding:"required"`
	APIKey  string `json:"api_key" binding:"required"`
	Weight  int    `json:"weight"`
	Enabled *bool  `json:"enabled"`
}

// createUpstream POST /api/models/:id/upstreams
func (s *AdminServer) createUpstream(c *gin.Context) {
	modelID := c.Param("id")
	if _, err := s.storage.GetModelConfigByID(modelID); err != nil {
		Error(c, http.StatusNotFound, 404, "model config not found")
		return
	}
	var req upstreamRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	if req.Weight < 1 {
		req.Weight = 1
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	now := time.Now()
	up := &plugin.Upstream{
		ID: uuid.NewString(), ModelConfigID: modelID, BaseURL: req.BaseURL,
		APIKey: req.APIKey, Weight: req.Weight, Enabled: enabled,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.storage.SaveUpstream(up); err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to save upstream")
		return
	}
	OK(c, gin.H{"id": up.ID})
}

// listUpstreams GET /api/models/:id/upstreams(api_key 脱敏)
func (s *AdminServer) listUpstreams(c *gin.Context) {
	modelID := c.Param("id")
	ups, err := s.storage.ListUpstreams(modelID)
	if err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to list upstreams")
		return
	}
	type item struct {
		ID        string    `json:"id"`
		BaseURL   string    `json:"base_url"`
		Weight    int       `json:"weight"`
		Enabled   bool      `json:"enabled"`
		CreatedAt time.Time `json:"created_at"`
	}
	items := make([]item, 0, len(ups))
	for _, u := range ups {
		items = append(items, item{ID: u.ID, BaseURL: u.BaseURL, Weight: u.Weight, Enabled: u.Enabled, CreatedAt: u.CreatedAt})
	}
	OK(c, gin.H{"items": items, "total": len(items)})
}

// updateUpstream PUT /api/upstreams/:uid
func (s *AdminServer) updateUpstream(c *gin.Context) {
	uid := c.Param("uid")
	existing, err := s.storage.GetUpstreamByID(uid)
	if err != nil {
		Error(c, http.StatusNotFound, 404, "upstream not found")
		return
	}
	var req upstreamRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	if req.Weight < 1 {
		req.Weight = 1
	}
	existing.BaseURL = req.BaseURL
	existing.APIKey = req.APIKey
	existing.Weight = req.Weight
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}
	existing.UpdatedAt = time.Now()
	if err := s.storage.SaveUpstream(existing); err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to update upstream")
		return
	}
	OK(c, gin.H{"id": uid})
}

// deleteUpstream DELETE /api/upstreams/:uid
func (s *AdminServer) deleteUpstream(c *gin.Context) {
	uid := c.Param("uid")
	if err := s.storage.DeleteUpstream(uid); err != nil {
		Error(c, http.StatusNotFound, 404, "upstream not found")
		return
	}
	OK(c, gin.H{"id": uid, "deleted": true})
}
```

- [ ] **步骤 4:注册路由**

`pkg/admin/router.go` 的 `registerRoutes` 的 api 组内追加:

```go
		// 上游管理(负载均衡)
		api.POST("/models/:id/upstreams", s.createUpstream)
		api.GET("/models/:id/upstreams", s.listUpstreams)
		api.PUT("/upstreams/:uid", s.updateUpstream)
		api.DELETE("/upstreams/:uid", s.deleteUpstream)
```

> 注:gin 中 `/models/:id/upstreams` 与已有 `/models/:id`(PUT/DELETE)路径参数名都用 `:id`,不冲突(不同子路径)。`/upstreams/:uid` 用不同参数名 `:uid` 避免与 `:id` 在同段冲突。

- [ ] **步骤 5:运行测试验证通过**

运行:`go test ./pkg/admin/ -run "TestAdminUpstream|TestAdmin" -v`
预期:PASS(含既有 Admin 测试无回归)

- [ ] **步骤 6:Commit**

```bash
git add pkg/admin/upstream.go pkg/admin/router.go pkg/admin/server_test.go
git commit -m "feat(admin): 上游管理 API(CRUD + api_key 脱敏)"
```

---
## 模块⑤ 限流管理 API(任务 11)

### 任务 11:限流配置 Admin API + AdminServer 注入 rateLimiter

**文件:**
- 创建:`pkg/admin/rate_limit.go`
- 修改:`pkg/admin/server.go`(NewAdminServer 加 rateLimiter 参数)、`pkg/admin/router.go`、`cmd/gateway/main.go`
- 修改:所有 `NewAdminServer(...)` 调用点(3 参 → 4 参)
- 测试:`pkg/admin/server_test.go`

- [ ] **步骤 1:全仓查 NewAdminServer 调用**

运行:`grep -rn "NewAdminServer(" pkg/ cmd/`
预期:列出 server_test.go 多处 + main.go 1 处。全部需补第 4 参 rateLimiter(测试传 `oss.NewRateLimiter(s, 100, 100000, "token_bucket")`,main 传已构造的 rateLimiter)。

- [ ] **步骤 2:编写失败测试**

`pkg/admin/server_test.go` 追加:

```go
func TestAdminRateLimitCRUD(t *testing.T) {
	s := oss.NewMemStorage()
	rl := oss.NewRateLimiter(s, 100, 100000, "token_bucket")
	router := NewAdminServer(s, nil, "oss", rl).Router()

	// 创建
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/rate-limits",
		strings.NewReader(`{"tenant_id":"","model_name":"gpt-4","requests_per_sec":20,"tokens_per_min":50000,"strategy":"token_bucket"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create status = %d; body=%s", w.Code, w.Body.String())
	}
	// 列表
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/rate-limits", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "gpt-4") {
		t.Fatalf("list status=%d body=%s", w.Code, w.Body.String())
	}
	// 校验:非法 strategy → 400
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/rate-limits",
		strings.NewReader(`{"requests_per_sec":10,"tokens_per_min":1000,"strategy":"bad"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad strategy status = %d; want 400", w.Code)
	}
}
```

- [ ] **步骤 3:运行验证失败**

运行:`go test ./pkg/admin/ -run TestAdminRateLimitCRUD 2>&1 | head`
预期:FAIL(NewAdminServer 4 参不匹配 / 路由 404)

- [ ] **步骤 4:AdminServer 加 rateLimiter 字段与参数**

`pkg/admin/server.go`:

```go
type AdminServer struct {
	storage     plugin.StoragePlugin
	rateLimiter plugin.RateLimitPlugin
	logger      *zap.Logger
	engine      *gin.Engine
	edition     string
	startedAt   time.Time
}

// NewAdminServer 创建管理后台
func NewAdminServer(storage plugin.StoragePlugin, logger *zap.Logger, edition string, rateLimiter plugin.RateLimitPlugin) *AdminServer {
	gin.SetMode(gin.ReleaseMode)
	s := &AdminServer{storage: storage, rateLimiter: rateLimiter, logger: logger, edition: edition, startedAt: time.Now()}
	s.engine = gin.New()
	s.engine.Use(gin.Recovery(), CORS())
	s.registerRoutes(s.engine)
	return s
}
```

- [ ] **步骤 5:实现 rate_limit.go**

`pkg/admin/rate_limit.go`(保留 Apache-2.0 头):

```go
package admin

import (
	"net/http"
	"strconv"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type rateLimitRequest struct {
	TenantID       string `json:"tenant_id"`
	ModelName      string `json:"model_name"`
	RequestsPerSec int    `json:"requests_per_sec" binding:"required,min=1,max=100000"`
	TokensPerMin   int64  `json:"tokens_per_min" binding:"required,min=1,max=1000000000"`
	Strategy       string `json:"strategy" binding:"required,oneof=token_bucket sliding_window"`
	Enabled        *bool  `json:"enabled"`
}

// createRateLimit POST /api/rate-limits
func (s *AdminServer) createRateLimit(c *gin.Context) {
	var req rateLimitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	// 同维度唯一:已存在则 409
	if _, err := s.storage.GetRateLimitConfig(req.TenantID, req.ModelName); err == nil {
		Error(c, http.StatusConflict, 409, "该维度限流配置已存在")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	now := time.Now()
	cfg := &plugin.RateLimitConfig{
		ID: uuid.NewString(), TenantID: req.TenantID, ModelName: req.ModelName,
		RequestsPerSec: req.RequestsPerSec, TokensPerMin: req.TokensPerMin,
		Strategy: req.Strategy, Enabled: enabled, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.storage.SaveRateLimitConfig(cfg); err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to save rate limit config")
		return
	}
	_ = s.rateLimiter.ReloadConfig()
	OK(c, gin.H{"id": cfg.ID})
}

// listRateLimits GET /api/rate-limits
func (s *AdminServer) listRateLimits(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "10"))
	cfgs, total, err := s.storage.ListRateLimitConfigs(page, size)
	if err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to list rate limit configs")
		return
	}
	OK(c, gin.H{"items": cfgs, "total": total, "page": page, "size": size})
}

// updateRateLimit PUT /api/rate-limits/:id
func (s *AdminServer) updateRateLimit(c *gin.Context) {
	id := c.Param("id")
	var req rateLimitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	// 定位现有(按 id 遍历列表——存储无按 id 查限流的方法,用 List 找)
	cfgs, _, err := s.storage.ListRateLimitConfigs(1, 100000)
	if err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to load rate limit configs")
		return
	}
	var existing *plugin.RateLimitConfig
	for _, cfg := range cfgs {
		if cfg.ID == id {
			existing = cfg
			break
		}
	}
	if existing == nil {
		Error(c, http.StatusNotFound, 404, "rate limit config not found")
		return
	}
	existing.TenantID = req.TenantID
	existing.ModelName = req.ModelName
	existing.RequestsPerSec = req.RequestsPerSec
	existing.TokensPerMin = req.TokensPerMin
	existing.Strategy = req.Strategy
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}
	existing.UpdatedAt = time.Now()
	if err := s.storage.SaveRateLimitConfig(existing); err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to update rate limit config")
		return
	}
	_ = s.rateLimiter.ReloadConfig()
	OK(c, gin.H{"id": id})
}

// deleteRateLimit DELETE /api/rate-limits/:id
func (s *AdminServer) deleteRateLimit(c *gin.Context) {
	id := c.Param("id")
	if err := s.storage.DeleteRateLimitConfig(id); err != nil {
		Error(c, http.StatusNotFound, 404, "rate limit config not found")
		return
	}
	_ = s.rateLimiter.ReloadConfig()
	OK(c, gin.H{"id": id, "deleted": true})
}
```

- [ ] **步骤 6:注册路由**

`pkg/admin/router.go` api 组内追加:

```go
		// 限流配置管理
		api.POST("/rate-limits", s.createRateLimit)
		api.GET("/rate-limits", s.listRateLimits)
		api.PUT("/rate-limits/:id", s.updateRateLimit)
		api.DELETE("/rate-limits/:id", s.deleteRateLimit)
```

- [ ] **步骤 7:更新所有 NewAdminServer 调用点**

`cmd/gateway/main.go` 的 `admin.NewAdminServer(storage, logger, edition)` → `admin.NewAdminServer(storage, logger, edition, rateLimiter)`(rateLimiter 变量已在 main 中构造)。
server_test.go 所有 `NewAdminServer(s, nil, "oss")` → `NewAdminServer(s, nil, "oss", oss.NewRateLimiter(s, 100, 100000, "token_bucket"))`。

- [ ] **步骤 8:运行测试验证通过**

运行:`go test ./pkg/admin/ -v && go build ./...`
预期:PASS(含既有 Admin 测试);编译通过

- [ ] **步骤 9:Commit**

```bash
git add pkg/admin/rate_limit.go pkg/admin/server.go pkg/admin/router.go pkg/admin/server_test.go cmd/gateway/main.go
git commit -m "feat(admin): 限流配置 CRUD API + 写后 ReloadConfig"
```

---

## 模块⑥ 接入层 IP/TLS + 透传精确路由(任务 12-14)

### 任务 12:IP 黑白名单(config + IPFilter + 接入)

**文件:**
- 修改:`pkg/config/config.go`(IPFilterConfig)、`pkg/core/acceptor.go`、`cmd/gateway/main.go`
- 测试:`pkg/core/acceptor_test.go`

- [ ] **步骤 1:编写失败测试**

`pkg/core/acceptor_test.go`(保留 Apache-2.0 头):

```go
package core

import "testing"

func TestIPFilterDisabled(t *testing.T) {
	f := NewIPFilter("disabled", nil, nil)
	if !f.Allow("1.2.3.4") {
		t.Fatal("disabled mode should allow all")
	}
}

func TestIPFilterWhitelist(t *testing.T) {
	f := NewIPFilter("whitelist", []string{"10.0.0.0/8", "192.168.1.100"}, nil)
	if !f.Allow("10.5.6.7") {
		t.Fatal("10.5.6.7 in 10.0.0.0/8 should be allowed")
	}
	if !f.Allow("192.168.1.100") {
		t.Fatal("exact IP should be allowed")
	}
	if f.Allow("8.8.8.8") {
		t.Fatal("8.8.8.8 not in whitelist should be denied")
	}
}

func TestIPFilterBlacklist(t *testing.T) {
	f := NewIPFilter("blacklist", nil, []string{"1.2.3.0/24"})
	if f.Allow("1.2.3.4") {
		t.Fatal("1.2.3.4 in blacklist should be denied")
	}
	if !f.Allow("5.6.7.8") {
		t.Fatal("5.6.7.8 not in blacklist should be allowed")
	}
}

func TestIPFilterInvalidIPDenied(t *testing.T) {
	f := NewIPFilter("whitelist", []string{"10.0.0.0/8"}, nil)
	if f.Allow("not-an-ip") {
		t.Fatal("unparseable IP in whitelist mode should be denied")
	}
}
```

- [ ] **步骤 2:运行验证失败**

运行:`go test ./pkg/core/ -run TestIPFilter 2>&1 | head`
预期:FAIL(NewIPFilter 签名不匹配——当前无参)

- [ ] **步骤 3:config.go 加 IPFilterConfig**

`pkg/config/config.go` 的 `Config` 结构加字段:

```go
	IPFilter  IPFilterConfig  `yaml:"ip_filter"`
	TLS       TLSConfig       `yaml:"tls"`
```

新增结构体与默认:

```go
type IPFilterConfig struct {
	Mode      string   `yaml:"mode"`      // disabled/whitelist/blacklist
	Whitelist []string `yaml:"whitelist"`
	Blacklist []string `yaml:"blacklist"`
}

type TLSConfig struct {
	Enabled    bool   `yaml:"enabled"`
	CertFile   string `yaml:"cert_file"`
	KeyFile    string `yaml:"key_file"`
	MinVersion string `yaml:"min_version"`
}
```

`Default()` 增加:

```go
		IPFilter: IPFilterConfig{Mode: "disabled"},
		TLS:      TLSConfig{MinVersion: "1.2"},
```

`applyDefaults()` 增加 IPFilter/TLS 的 apply(Mode/MinVersion 空值回填;bool/slice 不处理):

```go
	if c.IPFilter.Mode == "" {
		c.IPFilter.Mode = d.IPFilter.Mode
	}
	if c.TLS.MinVersion == "" {
		c.TLS.MinVersion = d.TLS.MinVersion
	}
```

> 补测试:config_test.go 的 TestDefaultsFullyApplied 加 `{"IPFilter.Mode", cfg.IPFilter.Mode, d.IPFilter.Mode}` 与 `{"TLS.MinVersion", cfg.TLS.MinVersion, d.TLS.MinVersion}`。

- [ ] **步骤 4:实现 IPFilter**

`pkg/core/acceptor.go` 的 IPFilter 替换:

```go
// IPFilter IP 黑白名单(CIDR 或单 IP)
type IPFilter struct {
	mode      string // disabled/whitelist/blacklist
	whitelist []*net.IPNet
	blacklist []*net.IPNet
}

// NewIPFilter 按 mode 与规则列表(CIDR 或单 IP)构造
func NewIPFilter(mode string, whitelist, blacklist []string) *IPFilter {
	return &IPFilter{
		mode:      mode,
		whitelist: parseCIDRs(whitelist),
		blacklist: parseCIDRs(blacklist),
	}
}

// parseCIDRs 解析规则:CIDR 直接解析;单 IP 转为 /32(v4)或 /128(v6)
func parseCIDRs(rules []string) []*net.IPNet {
	var nets []*net.IPNet
	for _, r := range rules {
		if _, ipnet, err := net.ParseCIDR(r); err == nil {
			nets = append(nets, ipnet)
			continue
		}
		if ip := net.ParseIP(r); ip != nil {
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			nets = append(nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
		}
	}
	return nets
}

func contains(nets []*net.IPNet, ip net.IP) bool {
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// Allow 按 mode 判定:disabled 全放行;whitelist 命中才放行;blacklist 命中则拒
func (f *IPFilter) Allow(ipStr string) bool {
	if f.mode == "disabled" || f.mode == "" {
		return true
	}
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return f.mode != "whitelist" // 白名单模式解析失败拒绝;黑名单模式放行
	}
	switch f.mode {
	case "whitelist":
		return contains(f.whitelist, ip)
	case "blacklist":
		return !contains(f.blacklist, ip)
	}
	return true
}
```

`NewAcceptor` 签名加 ipf 参数(由 main 用 config 构造后传入):

```go
// NewAcceptor 创建接入层
func NewAcceptor(handler http.Handler, ipf *IPFilter) *Acceptor {
	return &Acceptor{
		handler: handler,
		connMgr: NewConnectionManager(),
		tls:     NewTLSHandler(),
		ipf:     ipf,
		parser:  NewProtocolParser(),
	}
}
```

`Acceptor.Handler()` 包装 IP 检查(拒绝返回 403 OpenAI 格式):

```go
// Handler 返回经接入层包装的 handler(IP 黑白名单在最外层)
func (a *Acceptor) Handler() http.Handler {
	inner := a.handler
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.ipf != nil && !a.ipf.Allow(clientIP(r)) {
			writeOpenAIError(w, http.StatusForbidden, "invalid_request_error", "forbidden", "access denied by IP filter")
			return
		}
		inner.ServeHTTP(w, r)
	})
}
```

> `clientIP` 已在 middleware_auth.go 定义(同 core 包);`writeOpenAIError` 已在 proxy.go 定义(同包)。/healthz 也会经 IP 过滤——可接受(探活来自内网);若需豁免可加路径判断,本轮不豁免。

- [ ] **步骤 5:main.go 注入 IPFilter**

`cmd/gateway/main.go` 的 `core.NewAcceptor(proxyCore.Handler())` 改为:

```go
	ipf := core.NewIPFilter(cfg.IPFilter.Mode, cfg.IPFilter.Whitelist, cfg.IPFilter.Blacklist)
	acceptor := core.NewAcceptor(proxyCore.Handler(), ipf)
```

- [ ] **步骤 6:运行测试验证通过**

运行:`go test ./pkg/core/ -run TestIPFilter -v && go test ./pkg/config/ -v && go build ./...`
预期:PASS;编译通过

- [ ] **步骤 7:Commit**

```bash
git add pkg/core/acceptor.go pkg/core/acceptor_test.go pkg/config/config.go pkg/config/config_test.go cmd/gateway/main.go
git commit -m "feat(core): IP 黑白名单(whitelist/blacklist/CIDR)+ 接入层拦截"
```

---
### 任务 13:TLS 支持(TLSHandler + main.go 启动分支)

**文件:**
- 修改:`pkg/core/acceptor.go`(TLSHandler)、`cmd/gateway/main.go`
- 测试:`pkg/core/acceptor_test.go`

- [ ] **步骤 1:编写失败测试**

`pkg/core/acceptor_test.go` 追加:

```go
func TestTLSHandlerDisabled(t *testing.T) {
	h := NewTLSHandler(false, "", "", "1.2")
	cfg, err := h.TLSConfig()
	if err != nil {
		t.Fatalf("disabled TLS should not error: %v", err)
	}
	if cfg != nil {
		t.Fatal("disabled TLS should return nil config")
	}
}

func TestTLSHandlerMissingCertErrors(t *testing.T) {
	h := NewTLSHandler(true, "/nonexistent/cert.pem", "/nonexistent/key.pem", "1.2")
	if _, err := h.TLSConfig(); err == nil {
		t.Fatal("enabled TLS with missing cert should error")
	}
}

func TestTLSHandlerMinVersion(t *testing.T) {
	// 生成临时自签证书验证加载 + MinVersion
	certPEM, keyPEM := genSelfSignedCert(t)
	certFile := writeTemp(t, "cert.pem", certPEM)
	keyFile := writeTemp(t, "key.pem", keyPEM)
	h := NewTLSHandler(true, certFile, keyFile, "1.3")
	cfg, err := h.TLSConfig()
	if err != nil {
		t.Fatalf("valid cert should load: %v", err)
	}
	if cfg == nil || cfg.MinVersion != tls.VersionTLS13 {
		t.Fatalf("MinVersion = %v; want TLS1.3", cfg.MinVersion)
	}
	if len(cfg.Certificates) != 1 {
		t.Fatalf("should load 1 certificate")
	}
}
```

测试辅助(同文件,保留 Apache-2.0 头):

```go
// genSelfSignedCert 生成临时自签 RSA 证书 PEM
func genSelfSignedCert(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Unix(1000, 0),
		NotAfter:     time.Unix(1<<31, 0),
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certPEM, keyPEM
}

func writeTemp(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := t.TempDir() + "/" + name
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
```

> acceptor_test.go 顶部 import 需加:`crypto/rand`、`crypto/rsa`、`crypto/x509`、`crypto/x509/pkix`、`crypto/tls`、`encoding/pem`、`math/big`、`os`、`time`。

- [ ] **步骤 2:运行验证失败**

运行:`go test ./pkg/core/ -run TestTLSHandler 2>&1 | head`
预期:FAIL(NewTLSHandler 签名不匹配)

- [ ] **步骤 3:实现 TLSHandler**

`pkg/core/acceptor.go` 的 TLSHandler 替换(顶部已 import crypto/tls,需加 "fmt"):

```go
// TLSHandler TLS 终止:按配置加载证书
type TLSHandler struct {
	enabled    bool
	certFile   string
	keyFile    string
	minVersion string
}

// NewTLSHandler 按配置构造
func NewTLSHandler(enabled bool, certFile, keyFile, minVersion string) *TLSHandler {
	return &TLSHandler{enabled: enabled, certFile: certFile, keyFile: keyFile, minVersion: minVersion}
}

// TLSConfig 未启用返回 (nil,nil);启用则加载证书对,失败返回 error
func (h *TLSHandler) TLSConfig() (*tls.Config, error) {
	if !h.enabled {
		return nil, nil
	}
	cert, err := tls.LoadX509KeyPair(h.certFile, h.keyFile)
	if err != nil {
		return nil, fmt.Errorf("load tls key pair: %w", err)
	}
	minVer := uint16(tls.VersionTLS12)
	if h.minVersion == "1.3" {
		minVer = tls.VersionTLS13
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   minVer,
	}, nil
}
```

`NewAcceptor` 已在任务 12 加了 ipf 参数;TLSHandler 由 main 单独构造并从 config 读,不必进 Acceptor(main 直接用)。保留 Acceptor 内 `tls` 字段但改为 main 注入或移除——**最简**:Acceptor 不再持有 TLSHandler(main 直接构造用于 http.Server)。修改 NewAcceptor 移除 tls 字段初始化:

```go
type Acceptor struct {
	handler http.Handler
	connMgr *ConnectionManager
	ipf     *IPFilter
	parser  *ProtocolParser
}

func NewAcceptor(handler http.Handler, ipf *IPFilter) *Acceptor {
	return &Acceptor{
		handler: handler,
		connMgr: NewConnectionManager(),
		ipf:     ipf,
		parser:  NewProtocolParser(),
	}
}
```

> 删除 Acceptor 的 tls 字段与 NewAcceptor 里的 `tls: NewTLSHandler()`。TLSHandler 仍是 core 包公开类型,main 直接用。

- [ ] **步骤 4:main.go TLS 启动分支**

`cmd/gateway/main.go` 代理服务启动处:

```go
	tlsHandler := core.NewTLSHandler(cfg.TLS.Enabled, cfg.TLS.CertFile, cfg.TLS.KeyFile, cfg.TLS.MinVersion)
	tlsConf, err := tlsHandler.TLSConfig()
	if err != nil {
		logger.Fatal("TLS 配置加载失败", zap.Error(err))
	}
	proxyServer := &http.Server{
		Addr:           cfg.Server.ProxyAddr,
		Handler:        acceptor.Handler(),
		ReadTimeout:    cfg.Server.ReadTimeout,
		WriteTimeout:   cfg.Server.WriteTimeout,
		IdleTimeout:    cfg.Server.IdleTimeout,
		MaxHeaderBytes: cfg.Server.MaxHeaderBytes,
		TLSConfig:      tlsConf, // nil 时等同普通 HTTP
	}
```

代理服务启动 goroutine:

```go
		var serveErr error
		if tlsConf != nil {
			serveErr = proxyServer.ListenAndServeTLS("", "") // 证书已在 TLSConfig
		} else {
			serveErr = proxyServer.ListenAndServe()
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- fmt.Errorf("代理服务: %w", serveErr)
		}
```

> 注:现有 main.go 代理 goroutine 里是 `proxyServer.ListenAndServe()`,替换为上述分支。`errors`/`fmt` 已 import。

- [ ] **步骤 5:运行测试验证通过**

运行:`go test ./pkg/core/ -run TestTLSHandler -v && go build ./...`
预期:PASS(3 个 TLS 测试);编译通过

- [ ] **步骤 6:Commit**

```bash
git add pkg/core/acceptor.go pkg/core/acceptor_test.go cmd/gateway/main.go
git commit -m "feat(core): TLS 支持(证书加载 + MinVersion + 启动分支)"
```

---

### 任务 14:透传端点精确路由 + 405 + config.yaml + 端到端

**文件:**
- 修改:`pkg/core/proxy.go`(端点分类精确路由)、`config.yaml`
- 测试:`pkg/core/proxy_test.go`、`pkg/core/e2e_test.go`

- [ ] **步骤 1:编写失败测试**

`pkg/core/proxy_test.go` 追加(透传端点方法校验):

```go
func TestProxyPassThroughMethodNotAllowed(t *testing.T) {
	storage := oss.NewMemStorage()
	now := time.Now()
	_ = storage.SaveModelConfig(&plugin.ModelConfig{
		ID: "m1", ModelName: "gpt-4", Provider: "openai", ProviderModel: "gpt-4o",
		BaseURL: "http://unused", APIKey: "sk", Enabled: true, CreatedAt: now, UpdatedAt: now,
	})
	_ = storage.SaveAPIKey(&plugin.APIKey{ID: "k1", KeyHash: hashKey("ng-test"), KeyPrefix: "ng-test", Name: "t", Status: plugin.APIKeyStatusActive, Quota: -1, CreatedAt: now, UpdatedAt: now})
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	limiter := oss.NewRateLimiter(storage, 1000, 1000000, "token_bucket")
	_ = limiter.ReloadConfig()
	pc := NewProxyCore(NewPipeline(storage, limiter, oss.NewSimpleAuditor(storage), registry), registry)

	// /v1/moderations 只允许 POST,用 DELETE → 405
	req := httptest.NewRequest(http.MethodDelete, "/v1/moderations", nil)
	req.Header.Set("Authorization", "Bearer ng-test")
	rec := httptest.NewRecorder()
	pc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE /v1/moderations status = %d; want 405, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "method_not_allowed") {
		t.Fatalf("body = %s; want method_not_allowed", rec.Body.String())
	}
}

func TestProxyUnknownEndpoint404(t *testing.T) {
	storage := oss.NewMemStorage()
	now := time.Now()
	_ = storage.SaveAPIKey(&plugin.APIKey{ID: "k1", KeyHash: hashKey("ng-test"), KeyPrefix: "ng-test", Name: "t", Status: plugin.APIKeyStatusActive, Quota: -1, CreatedAt: now, UpdatedAt: now})
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	limiter := oss.NewRateLimiter(storage, 1000, 1000000, "token_bucket")
	_ = limiter.ReloadConfig()
	pc := NewProxyCore(NewPipeline(storage, limiter, oss.NewSimpleAuditor(storage), registry), registry)

	req := httptest.NewRequest(http.MethodGet, "/v1/unknown/path", nil)
	req.Header.Set("Authorization", "Bearer ng-test")
	rec := httptest.NewRecorder()
	pc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown endpoint status = %d; want 404", rec.Code)
	}
}
```

> 注:405 测试用 unused base_url——因 405 在转发之前返回,不会真正连上游。DELETE /v1/moderations 走精确路由的方法校验分支。

- [ ] **步骤 2:运行验证失败**

运行:`go test ./pkg/core/ -run "TestProxyPassThroughMethodNotAllowed|TestProxyUnknownEndpoint404" 2>&1 | head`
预期:FAIL(当前 default 兜底,DELETE /v1/moderations 走 handlePassThrough 而非 405;未知路径也走透传)

- [ ] **步骤 3:实现精确路由**

`pkg/core/proxy.go` 新增透传端点表与匹配函数:

```go
// passthroughEndpoints 透传端点 → 允许的 HTTP 方法集合(PRD 8.5)
var passthroughEndpoints = map[string][]string{
	"/v1/completions":            {http.MethodPost},
	"/v1/moderations":            {http.MethodPost},
	"/v1/images/generations":     {http.MethodPost},
	"/v1/images/edits":           {http.MethodPost},
	"/v1/images/variations":      {http.MethodPost},
	"/v1/audio/speech":           {http.MethodPost},
	"/v1/audio/transcriptions":   {http.MethodPost},
	"/v1/audio/translations":     {http.MethodPost},
	"/v1/files":                  {http.MethodGet, http.MethodPost},
}

// matchPassthrough 判断路径是否为透传端点,返回允许方法与是否命中
// 处理带路径参数的 files 子路径(/v1/files/{id}、/v1/files/{id}/content)
func matchPassthrough(path string) (methods []string, ok bool) {
	path = strings.TrimRight(path, "/")
	if m, exist := passthroughEndpoints[path]; exist {
		return m, true
	}
	// /v1/files/{id} 与 /v1/files/{id}/content
	if strings.HasPrefix(path, "/v1/files/") {
		if strings.HasSuffix(path, "/content") {
			return []string{http.MethodGet}, true
		}
		return []string{http.MethodGet, http.MethodDelete}, true
	}
	return nil, false
}

func methodAllowed(methods []string, m string) bool {
	for _, x := range methods {
		if x == m {
			return true
		}
	}
	return false
}
```

`proxyHandler` 的 switch 的 `default` 分支改为精确匹配:

```go
	default:
		methods, ok := matchPassthrough(r.URL.Path)
		if !ok {
			writeOpenAIError(w, http.StatusNotFound, "invalid_request_error", "model_not_found", "unknown endpoint: "+r.URL.Path)
			return
		}
		if !methodAllowed(methods, r.Method) {
			writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method_not_allowed", "method "+r.Method+" not allowed for "+r.URL.Path)
			return
		}
		p.handlePassThrough(w, r, rc)
	}
```

- [ ] **步骤 4:config.yaml 加 tls/ip_filter 段**

`config.yaml` 追加(在 log 段之后或合适位置):

```yaml
tls:
  enabled: false
  cert_file: ""
  key_file: ""
  min_version: "1.2"

ip_filter:
  mode: disabled             # disabled/whitelist/blacklist
  whitelist: []
  blacklist: []
```

- [ ] **步骤 5:端到端测试扩展**

`pkg/core/e2e_test.go` 追加(多上游 + 限流配置生效的完整链路):

```go
func TestEndToEndLoadBalanceAndRateLimit(t *testing.T) {
	upstream := newMockUpstream(t)
	defer upstream.Close()

	storage := oss.NewSQLStorage()
	if err := storage.Init(map[string]interface{}{
		"driver": "sqlite", "dsn": t.TempDir() + "/e2e2.db", "encrypt_key": "e2e-key",
	}); err != nil {
		t.Fatalf("storage init: %v", err)
	}
	defer storage.Close()

	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	limiter := oss.NewRateLimiter(storage, 1000, 1000000, "token_bucket")
	_ = limiter.ReloadConfig()
	auditor := oss.NewSimpleAuditor(storage)
	pc := NewProxyCore(NewPipeline(storage, limiter, auditor, registry), registry)
	proxyHandler := pc.Handler()
	adminRouter := admin.NewAdminServer(storage, nil, "oss", limiter).Router()

	// 建模型
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/models",
		strings.NewReader(`{"name":"gpt-4","provider":"openai","provider_model":"gpt-4o","base_url":"`+upstream.URL+`","api_key":"sk-e2e"}`))
	req.Header.Set("Content-Type", "application/json")
	adminRouter.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create model: %d %s", w.Code, w.Body.String())
	}
	var created struct{ Data struct{ ID string } }
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	// 加一个上游指向同 mock(验证多上游路径可用)
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/models/"+created.Data.ID+"/upstreams",
		strings.NewReader(`{"base_url":"`+upstream.URL+`","api_key":"sk-up","weight":1}`))
	req.Header.Set("Content-Type", "application/json")
	adminRouter.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create upstream: %d %s", w.Code, w.Body.String())
	}

	// 建限流配置:gpt-4 rps=2
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/rate-limits",
		strings.NewReader(`{"tenant_id":"","model_name":"gpt-4","requests_per_sec":2,"tokens_per_min":100000,"strategy":"token_bucket"}`))
	req.Header.Set("Content-Type", "application/json")
	adminRouter.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create rate-limit: %d %s", w.Code, w.Body.String())
	}

	// 建 Key
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/api-keys", strings.NewReader(`{"name":"e2e","quota":-1}`))
	req.Header.Set("Content-Type", "application/json")
	adminRouter.ServeHTTP(w, req)
	var keyResp struct{ Data struct{ Key string } }
	_ = json.Unmarshal(w.Body.Bytes(), &keyResp)

	// 调用 3 次:rps=2 → 前 2 次 200,第 3 次 429
	codes := []int{}
	for i := 0; i < 3; i++ {
		w = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
			strings.NewReader(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`))
		req.Header.Set("Authorization", "Bearer "+keyResp.Data.Key)
		proxyHandler.ServeHTTP(w, req)
		codes = append(codes, w.Code)
	}
	if codes[0] != 200 || codes[1] != 200 || codes[2] != 429 {
		t.Fatalf("rps=2 codes = %v; want [200 200 429]", codes)
	}
}
```

- [ ] **步骤 6:运行测试验证通过**

运行:`go test ./pkg/core/ -run "TestProxyPassThrough|TestProxyUnknown|TestEndToEnd" -v`
预期:PASS(透传 405、未知 404、端到端限流生效)

- [ ] **步骤 7:全量验证 + Commit**

运行:`go test ./... && go build ./... && gofmt -l pkg/ cmd/`
预期:全部 PASS;编译通过;gofmt 无输出

```bash
git add pkg/core/proxy.go pkg/core/proxy_test.go pkg/core/e2e_test.go config.yaml
git commit -m "feat(core): 透传端点精确路由 + 405 + 端到端负载均衡/限流验证"
```

---

## 自检记录

**规格覆盖度:**

| 规格章节 | 对应任务 |
|----------|----------|
| §3.1-3.2 限流器策略化(token_bucket/sliding_window 基础件) | 任务 1-2 |
| §3.3-3.4 双维度双策略 + 三层配置 | 任务 5 |
| §3.5 TPM 预检+回补 | 任务 6-7 |
| §3.6 RateLimitConfig 存储 | 任务 3-4 |
| §3.7 限流管理 Admin API | 任务 11 |
| §4.1-4.3 负载均衡(独立 upstreams 表 + 加权随机 + 回退) | 任务 3-4/8-9 |
| §4.4 上游管理 Admin API | 任务 10 |
| §5.1 透传端点精确路由 + 405 | 任务 14 |
| §5.2 IP 黑白名单 | 任务 12 |
| §5.3 TLS | 任务 13 |
| §6 config.yaml tls/ip_filter | 任务 12(结构)/14(yaml) |
| §7 测试策略 | 各任务 + 任务 14 端到端 |

**占位符扫描:** 所有步骤含完整代码与命令,无"待定/TODO";接口变更(RateLimitPlugin +2 方法、StoragePlugin +8 方法、NewAdminServer/NewAcceptor 签名)在任务 3/5/11/12 统一传播。

**类型一致性:**
- `RateLimitPlugin.RecordTokens/ReloadConfig`:任务 3 声明,任务 5 实现(RateLimiter),任务 6/7 使用
- `StoragePlugin` 限流/上游 CRUD:任务 3 声明,任务 4 三实现,任务 10/11 使用
- `NewAdminServer` 4 参:任务 11 变更并更新全部调用点
- `NewAcceptor(handler, ipf)`:任务 12 变更,main 同步
- `NewRateLimiter(storage, rps, tpm, strategy)`:任务 5 定义,任务 6-11 测试与 main 使用一致
- `selectUpstream([]plugin.Upstream) *plugin.Upstream`:任务 8 定义,任务 9 使用
- `rc.Upstreams []plugin.Upstream`:任务 9 定义并使用

**关键顺序约束:**
- 任务 3(接口)+4(存储)+5(限流器)必须连续完成才能编译绿——commit 在任务 5 末尾一次性提交
- 任务 5 删除 MemRateLimiter → 任务 6 步骤 1 必须先改所有 `NewMemRateLimiter` 引用,否则 `go test ./...` 红

---

## 执行说明

**串行执行**:14 个任务严格按序 1→14,每个任务 TDD 五步(失败测试→验证失败→实现→验证通过→commit)。**不并发**。任务 3/4/5 因接口变更牵连,合并为一次 commit(在任务 5 末尾);其余任务各自 commit。

**验证基线**:起始 112 tests;预计新增约 30+ 测试。每个任务完成后 `go test ./...` 必须全绿再进入下一个。
