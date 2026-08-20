# OSS 端到端打通 实现计划

> **面向 AI 代理的工作者:** 必需子技能:使用 superpowers:subagent-driven-development(推荐)或 superpowers:executing-plans 逐任务实现此计划。步骤使用复选框(`- [ ]`)语法来跟踪进度。

**目标:** 按设计文档 `docs/superpowers/specs/2026-08-20-oss-end-to-end-design.md` 完成 OSS 版核心实现:存储(MySQL/SQLite)、中间件真实逻辑、代理内核转发、SSE 审计接线、适配器实现、管理后台 CRUD,使 OSS 版端到端可用。

**架构:** 四层固定分层(接入层→管道中间件→代理内核→插件扩展)。本次实现第 2-4 层真实逻辑与第 4 层存储插件,全部走既有接口契约。OpenAI/DeepSeek 原生透传(仅替换 model 字段),通义/智谱做协议转换。

**技术栈:** Go 1.26、net/http(代理服务)、Gin(管理后台)、database/sql + go-sql-driver/mysql + modernc.org/sqlite、zap、uuid、yaml.v3

**规格:** `docs/superpowers/specs/2026-08-20-oss-end-to-end-design.md`

---

## 文件结构

```
新增:
  pkg/plugin/oss/crypto.go            # AES-GCM 加解密(上游 API Key 加密)
  pkg/plugin/oss/storage_sql.go       # 共享 SQL CRUD(MySQL/SQLite 共用,基于 database/sql)
  pkg/plugin/oss/storage_mysql.go     # MySQL 建表 DDL(driver="mysql")
  pkg/plugin/oss/storage_sqlite.go    # SQLite 建表 DDL(driver="sqlite")
  pkg/admin/response.go               # 统一响应格式 {code,message,data}
  pkg/admin/api_key.go                # API Key CRUD(创建明文仅一次/列表脱敏/禁用/软删)
  pkg/admin/model_config.go           # 模型配置 CRUD + 测试连接
  pkg/admin/audit_api.go              # 审计查询/详情/导出
  pkg/admin/system.go                 # 系统信息
  pkg/core/e2e_test.go                # 端到端集成测试(全链路)

修改:
  go.mod                               # 新增 mysql/sqlite 驱动
  config.yaml                          # 默认 driver: sqlite + encrypt_key
  pkg/config/config.go                 # StorageConfig 增加 EncryptKey 字段
  pkg/plugin/interface.go              # 增加 GetAPIKeyByID/GetModelConfigByID;AuditLogFilter.RequestID
  pkg/plugin/oss/factory.go            # CreateStorage 按 driver 分发
  pkg/plugin/oss/storage_mem.go        # 同步新增接口方法
  pkg/plugin/oss/audit_simple.go       # MarkDisconnect 竞态修复(已 Finalize 则忽略)
  pkg/adapter/openai.go                # ParseTokenUsage/ParseStreamUsage/ParseError 真实解析
  pkg/adapter/deepseek.go              # 同上(复用 OpenAI 格式)
  pkg/adapter/tongyi.go                # TransformRequest/TransformResponse/ParseError 基础转换
  pkg/adapter/zhipu.go                 # TransformRequest/TransformResponse/ParseError 基础转换
  pkg/core/middleware_auth.go          # 真实鉴权(查存储/状态/过期/额度)
  pkg/core/middleware_route.go         # 真实路由(读body解析model/404/403)
  pkg/core/middleware_limit.go         # 真实限流(429 + X-RateLimit-* Header)
  pkg/core/proxy.go                    # 端点分类 + 核心代理转发 + /v1/models 本地响应
  pkg/core/sse_writer.go               # Write 时分片解析与审计投递 + Chunks()
  pkg/core/sse_reassembler.go          # Reassemble 实现
  pkg/core/disconnect_handler.go       # Watch 增加 done 通道防误标断连
  pkg/admin/router.go                  # 注册全部 CRUD 路由
  cmd/gateway/main.go                  # storage.Init 传 dsn/encrypt_key;admin 传 edition
```

---

### 任务 1:新增依赖与配置字段

**文件:**
- 修改:`go.mod`、`config.yaml`
- 修改:`pkg/config/config.go`(StorageConfig 增加 EncryptKey)

- [ ] **步骤 1:添加依赖**

运行:
```bash
go get github.com/go-sql-driver/mysql@latest
go get modernc.org/sqlite@latest
```
预期:go.mod 增加两个 require 项,go.sum 更新,无错误。

- [ ] **步骤 2:config.go 增加 EncryptKey 字段**

在 `pkg/config/config.go` 的 `StorageConfig` 中新增字段:

```go
type StorageConfig struct {
	Driver       string `yaml:"driver"`
	DSN          string `yaml:"dsn"`
	EncryptKey   string `yaml:"encrypt_key"` // AES-GCM 加密密钥(上游 API Key 加密)
	MaxOpenConns int    `yaml:"max_open_conns"`
	MaxIdleConns int    `yaml:"max_idle_conns"`
}
```

在 `Default()` 的 Storage 段补充默认值:

```go
Storage: StorageConfig{
	Driver:     "sqlite",
	DSN:        "neuralgate.db",
	EncryptKey: "neuralgate-default-encrypt-key",
	MaxOpenConns: 20,
	MaxIdleConns: 10,
},
```

在 `applyDefaults()` 的 StorageConfig.apply 中补充 EncryptKey 空值回填:

```go
func (s *StorageConfig) apply(d StorageConfig) {
	if s.Driver == "" {
		s.Driver = d.Driver
	}
	if s.DSN == "" {
		s.DSN = d.DSN
	}
	if s.EncryptKey == "" {
		s.EncryptKey = d.EncryptKey
	}
	...
}
```

- [ ] **步骤 3:更新 config.yaml 默认配置**

```yaml
storage:
  # 可选值: sqlite(默认) / mysql / mem(内存,开发调试用)
  # 注: 达梦/金仓需 Enterprise 编译版
  driver: sqlite
  dsn: "neuralgate.db"
  encrypt_key: "neuralgate-default-encrypt-key"
  max_open_conns: 20
  max_idle_conns: 10
  # ----- MySQL (可选) -----
  # driver: mysql
  # dsn: "user:pass@tcp(host:3306)/neuralgate?charset=utf8mb4&parseTime=true"
```

- [ ] **步骤 4:验证**

运行:`go build ./...`
预期:编译成功。

- [ ] **步骤 5:Commit**

```bash
git add go.mod go.sum config.yaml pkg/config/config.go
git commit -m "feat(config): 新增 SQLite/MySQL 存储配置(encrypt_key)与依赖"
```

---

### 任务 2:AES-GCM 加解密工具

**文件:**
- 创建:`pkg/plugin/oss/crypto.go`
- 测试:`pkg/plugin/oss/crypto_test.go`

- [ ] **步骤 1:编写失败测试**

`pkg/plugin/oss/crypto_test.go`:

```go
package oss

import "testing"

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := "test-key"
	plain := "sk-abc123"
	enc, err := Encrypt(plain, key)
	if err != nil {
		t.Fatalf("Encrypt error: %v", err)
	}
	if enc == plain {
		t.Fatal("ciphertext must differ from plaintext")
	}
	dec, err := Decrypt(enc, key)
	if err != nil {
		t.Fatalf("Decrypt error: %v", err)
	}
	if dec != plain {
		t.Fatalf("roundtrip mismatch: got %q want %q", dec, plain)
	}
}

func TestDecryptWrongKeyFails(t *testing.T) {
	enc, err := Encrypt("secret", "key-a")
	if err != nil {
		t.Fatalf("Encrypt error: %v", err)
	}
	if _, err := Decrypt(enc, "key-b"); err == nil {
		t.Fatal("decrypt with wrong key must fail")
	}
}

func TestDecryptInvalidCiphertextFails(t *testing.T) {
	if _, err := Decrypt("not-valid-ciphertext", "key"); err == nil {
		t.Fatal("decrypt invalid input must fail")
	}
}
```

- [ ] **步骤 2:运行测试验证失败**

运行:`go test ./pkg/plugin/oss/ -run TestEncryptDecrypt -v`
预期:FAIL,`undefined: Encrypt`。

- [ ] **步骤 3:实现 crypto.go**

`pkg/plugin/oss/crypto.go`(保留 Apache-2.0 头,下同):

```go
package oss

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
)

// keyBytes 通过 SHA256 派生固定 32 字节密钥(任意长度输入均可)
func keyBytes(key string) []byte {
	sum := sha256.Sum256([]byte(key))
	return sum[:]
}

// Encrypt AES-GCM 加密,输出 base64 字符串;每次加密使用随机 nonce,密文可安全重复加密
func Encrypt(plaintext, key string) (string, error) {
	block, err := aes.NewCipher(keyBytes(key))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt AES-GCM 解密,输入为 Encrypt 输出的 base64 字符串
func Decrypt(ciphertext, key string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(keyBytes(key))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(data) < gcm.NonceSize() {
		return "", errors.New("ciphertext too short")
	}
	nonce, sealed := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}
```

- [ ] **步骤 4:运行测试验证通过**

运行:`go test ./pkg/plugin/oss/ -run TestEncryptDecrypt -v`
预期:PASS,3 个测试通过。

- [ ] **步骤 5:Commit**

```bash
git add pkg/plugin/oss/crypto.go pkg/plugin/oss/crypto_test.go
git commit -m "feat(plugin): 新增 AES-GCM 加解密工具(crypto.go)"
```

---

### 任务 3:接口扩展(按 ID 查询 + 审计详情过滤)

管理后台 CRUD 需要按 ID 查询记录,审计详情需要按 RequestID 过滤,现有接口缺这两个能力。同步实现到内存存储。

**文件:**
- 修改:`pkg/plugin/interface.go`
- 修改:`pkg/plugin/oss/storage_mem.go`
- 测试:`pkg/plugin/oss/storage_mem_test.go`

- [ ] **步骤 1:编写失败测试**

`pkg/plugin/oss/storage_mem_test.go` 追加:

```go
func TestMemStorageGetAPIKeyByID(t *testing.T) {
	s := NewMemStorage()
	key := &plugin.APIKey{ID: "k1", KeyHash: "h1", Name: "test"}
	if err := s.SaveAPIKey(key); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetAPIKeyByID("k1")
	if err != nil || got.ID != "k1" {
		t.Fatalf("GetAPIKeyByID(k1) = %v, %v; want k1, nil", got, err)
	}
	if _, err := s.GetAPIKeyByID("nope"); err != ErrNotFound {
		t.Fatalf("GetAPIKeyByID(nope) err = %v; want ErrNotFound", err)
	}
}

func TestMemStorageGetModelConfigByID(t *testing.T) {
	s := NewMemStorage()
	cfg := &plugin.ModelConfig{ID: "m1", ModelName: "gpt-4"}
	if err := s.SaveModelConfig(cfg); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetModelConfigByID("m1")
	if err != nil || got.ModelName != "gpt-4" {
		t.Fatalf("GetModelConfigByID(m1) = %v, %v", got, err)
	}
	if _, err := s.GetModelConfigByID("nope"); err != ErrNotFound {
		t.Fatalf("GetModelConfigByID(nope) err = %v; want ErrNotFound", err)
	}
}

func TestMemStorageQueryAuditLogsByRequestID(t *testing.T) {
	s := NewMemStorage()
	l1 := &plugin.AuditLog{ID: "a1", RequestID: "r1", ModelName: "gpt-4"}
	if err := s.SaveAuditLog(l1); err != nil {
		t.Fatal(err)
	}
	logs, total, err := s.QueryAuditLogs(plugin.AuditLogFilter{RequestID: "r1"}, 1, 10)
	if err != nil || total != 1 || logs[0].ID != "a1" {
		t.Fatalf("QueryAuditLogs by requestID = %v,%d,%v", logs, total, err)
	}
}
```

- [ ] **步骤 2:运行测试验证失败**

运行:`go test ./pkg/plugin/oss/ -run "TestMemStorageGet|TestMemStorageQueryAuditLogsByRequestID" -v`
预期:FAIL,编译错误 `s.GetAPIKeyByID undefined`。

- [ ] **步骤 3:扩展接口定义**

`pkg/plugin/interface.go` 的 `StoragePlugin` 接口新增两个方法(放在 GetAPIKey 之后):

```go
	// API Key 管理
	GetAPIKey(keyHash string) (*APIKey, error)
	GetAPIKeyByID(id string) (*APIKey, error) // 按主键查询(管理后台用)
	SaveAPIKey(key *APIKey) error
```

`ModelConfig` 查询段新增:

```go
	// 模型配置管理
	GetModelConfig(modelName string) (*ModelConfig, error)
	GetModelConfigByID(id string) (*ModelConfig, error) // 按主键查询(管理后台用)
	ListModelConfigs(page, size int) ([]*ModelConfig, int64, error)
```

`AuditLogFilter` 新增 RequestID 字段:

```go
type AuditLogFilter struct {
	TenantID  string
	APIKeyID  string
	ModelName string
	RequestID string // 按请求 ID 精查(审计详情)
	StartTime *time.Time
	EndTime   *time.Time
	Status    int    // 响应状态码过滤
	IsStream  *bool  // 是否流式
	Keyword   string // 全文搜索关键词
}
```

- [ ] **步骤 4:实现到内存存储**

`pkg/plugin/oss/storage_mem.go` 追加(GetAPIKey 方法之后):

```go
func (s *MemStorage) GetAPIKeyByID(id string) (*plugin.APIKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, k := range s.apiKeys {
		if k.ID == id {
			return k, nil
		}
	}
	return nil, ErrNotFound
}
```

GetModelConfig 之后追加:

```go
func (s *MemStorage) GetModelConfigByID(id string) (*plugin.ModelConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, c := range s.modelConfigs {
		if c.ID == id {
			return c, nil
		}
	}
	return nil, ErrNotFound
}
```

`matchAuditLog` 开头追加:

```go
	if f.RequestID != "" && l.RequestID != f.RequestID {
		return false
	}
```

- [ ] **步骤 5:运行测试验证通过**

运行:`go test ./pkg/plugin/oss/ -run "TestMemStorage" -v`
预期:PASS。

- [ ] **步骤 6:Commit**

```bash
git add pkg/plugin/interface.go pkg/plugin/oss/storage_mem.go pkg/plugin/oss/storage_mem_test.go
git commit -m "feat(plugin): 接口新增按ID查询与审计详情过滤(GetAPIKeyByID/GetModelConfigByID/RequestID)"
```

---

### 任务 4:共享 SQL 存储 — APIKey CRUD

**文件:**
- 创建:`pkg/plugin/oss/storage_sql.go`
- 测试:`pkg/plugin/oss/storage_sql_test.go`

- [ ] **步骤 1:编写失败测试**

`pkg/plugin/oss/storage_sql_test.go`(SQLite `:memory:` 测试全量 CRUD;本任务先测 APIKey 段):

```go
package oss

import (
	"database/sql"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	_ "modernc.org/sqlite"
)

// newTestSQLStorage 创建基于内存 SQLite 的 SQLStorage(建表)
func newTestSQLStorage(t *testing.T) *SQLStorage {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := sqliteCreateTables(db); err != nil {
		t.Fatal(err)
	}
	return &SQLStorage{db: db, encryptKey: "test-key"}
}

func TestSQLStorageAPIKeyCRUD(t *testing.T) {
	s := newTestSQLStorage(t)
	now := time.Now()
	key := &plugin.APIKey{
		ID: "k1", KeyHash: "hash-1", KeyPrefix: "ng-abcdef12",
		TenantID: "t1", Name: "测试Key", Status: plugin.APIKeyStatusActive,
		Quota: -1, RateLimit: 10, AllowedModels: []string{"gpt-4"},
		ExpiresAt: nil, CreatedAt: now, UpdatedAt: now, CreatedBy: "admin",
	}
	if err := s.SaveAPIKey(key); err != nil {
		t.Fatalf("SaveAPIKey: %v", err)
	}

	// 按哈希查
	got, err := s.GetAPIKey("hash-1")
	if err != nil || got.Name != "测试Key" || got.TenantID != "t1" {
		t.Fatalf("GetAPIKey = %v, %v", got, err)
	}
	if len(got.AllowedModels) != 1 || got.AllowedModels[0] != "gpt-4" {
		t.Fatalf("AllowedModels mismatch: %v", got.AllowedModels)
	}

	// 按 ID 查
	byID, err := s.GetAPIKeyByID("k1")
	if err != nil || byID.KeyHash != "hash-1" {
		t.Fatalf("GetAPIKeyByID = %v, %v", byID, err)
	}

	// 更新额度
	if err := s.UpdateAPIKeyQuota("k1", 100); err != nil {
		t.Fatalf("UpdateAPIKeyQuota: %v", err)
	}
	got, _ = s.GetAPIKey("hash-1")
	if got.UsedQuota != 100 {
		t.Fatalf("UsedQuota = %d; want 100", got.UsedQuota)
	}

	// 列表(含租户过滤)
	if _, total, err := s.ListAPIKeys("t1", 1, 10); err != nil || total != 1 {
		t.Fatalf("ListAPIKeys = total %d, err %v", total, err)
	}

	// 软删除后查不到
	if err := s.DeleteAPIKey("k1"); err != nil {
		t.Fatalf("DeleteAPIKey: %v", err)
	}
	if _, err := s.GetAPIKey("hash-1"); err != ErrNotFound {
		t.Fatalf("GetAPIKey after delete err = %v; want ErrNotFound", err)
	}
	if _, total, _ := s.ListAPIKeys("", 1, 10); total != 0 {
		t.Fatalf("ListAPIKeys after delete total = %d; want 0", total)
	}
}
```

- [ ] **步骤 2:运行测试验证失败**

运行:`go test ./pkg/plugin/oss/ -run TestSQLStorageAPIKeyCRUD -v`
预期:FAIL,`undefined: SQLStorage` / `sqliteCreateTables`。

- [ ] **步骤 3:实现 SQLStorage 骨架 + APIKey CRUD**

`pkg/plugin/oss/storage_sql.go`:

```go
package oss

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// SQLStorage 共享 SQL 存储实现(MySQL/SQLite 共用 CRUD 逻辑)
type SQLStorage struct {
	db         *sql.DB
	encryptKey string
}

// NewSQLStorage 创建 SQL 存储(不含连接,连接由 Init 建立)
func NewSQLStorage() *SQLStorage { return &SQLStorage{} }

// Init 按 driver 打开连接并建表: driver ∈ {mysql, sqlite}
func (s *SQLStorage) Init(config map[string]interface{}) error {
	driver, _ := config["driver"].(string)
	dsn, _ := config["dsn"].(string)
	s.encryptKey, _ = config["encrypt_key"].(string)
	if driver != "mysql" && driver != "sqlite" {
		return fmt.Errorf("unsupported sql driver: %s", driver)
	}
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return fmt.Errorf("open %s: %w", driver, err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return fmt.Errorf("ping %s: %w", driver, err)
	}
	s.db = db
	if driver == "mysql" {
		return mysqlCreateTables(db)
	}
	return sqliteCreateTables(db)
}

// ===== 时间与 JSON 转换 =====

func timeToMS(t time.Time) int64        { return t.UnixMilli() }
func msToTime(ms int64) time.Time       { return time.UnixMilli(ms) }
func timePtrToMS(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	v := t.UnixMilli()
	return &v
}
func msToTimePtr(ms *int64) *time.Time {
	if ms == nil {
		return nil
	}
	v := time.UnixMilli(*ms)
	return &v
}

func marshalJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// ===== API Key 管理 =====

const apiKeyCols = "id, key_hash, key_prefix, tenant_id, name, status, quota, used_quota, rate_limit, allowed_models, expires_at, created_at, updated_at, created_by, deleted"

func scanAPIKey(row interface{ Scan(...interface{}) error }) (*plugin.APIKey, error) {
	var k plugin.APIKey
	var allowedModels, expiresAt, createdAt, updatedAt string
	var deleted int
	if err := row.Scan(&k.ID, &k.KeyHash, &k.KeyPrefix, &k.TenantID, &k.Name,
		&k.Status, &k.Quota, &k.UsedQuota, &k.RateLimit, &allowedModels,
		&expiresAt, &createdAt, &updatedAt, &k.CreatedBy, &deleted); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	_ = json.Unmarshal([]byte(allowedModels), &k.AllowedModels)
	if expiresAt != "" {
		var ms int64
		fmt.Sscanf(expiresAt, "%d", &ms)
		k.ExpiresAt = msToTimePtr(&ms)
	}
	var cms, ums int64
	fmt.Sscanf(createdAt, "%d", &cms)
	fmt.Sscanf(updatedAt, "%d", &ums)
	k.CreatedAt = msToTime(cms)
	k.UpdatedAt = msToTime(ums)
	return &k, nil
}

func (s *SQLStorage) GetAPIKey(keyHash string) (*plugin.APIKey, error) {
	row := s.db.QueryRow("SELECT "+apiKeyCols+" FROM api_keys WHERE key_hash = ? AND deleted = 0", keyHash)
	return scanAPIKey(row)
}

func (s *SQLStorage) GetAPIKeyByID(id string) (*plugin.APIKey, error) {
	row := s.db.QueryRow("SELECT "+apiKeyCols+" FROM api_keys WHERE id = ? AND deleted = 0", id)
	return scanAPIKey(row)
}

func (s *SQLStorage) SaveAPIKey(key *plugin.APIKey) error {
	allowed := marshalJSON(key.AllowedModels)
	expiresAt := timePtrToMS(key.ExpiresAt)
	created := timeToMS(key.CreatedAt)
	updated := timeToMS(key.UpdatedAt)
	_, err := s.db.Exec(
		`INSERT INTO api_keys (id, key_hash, key_prefix, tenant_id, name, status, quota, used_quota, rate_limit, allowed_models, expires_at, created_at, updated_at, created_by, deleted)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,0)
		 ON CONFLICT(id) DO UPDATE SET key_hash=excluded.key_hash, key_prefix=excluded.key_prefix, tenant_id=excluded.tenant_id,
		   name=excluded.name, status=excluded.status, quota=excluded.quota, used_quota=excluded.used_quota,
		   rate_limit=excluded.rate_limit, allowed_models=excluded.allowed_models, expires_at=excluded.expires_at,
		   updated_at=excluded.updated_at, created_by=excluded.created_by`,
		key.ID, key.KeyHash, key.KeyPrefix, key.TenantID, key.Name, string(key.Status),
		key.Quota, key.UsedQuota, key.RateLimit, allowed, expiresAt, created, updated, key.CreatedBy)
	return err
}

func (s *SQLStorage) UpdateAPIKeyQuota(keyID string, usedQuota int64) error {
	_, err := s.db.Exec("UPDATE api_keys SET used_quota = ? WHERE id = ? AND deleted = 0", usedQuota, keyID)
	return err
}

func (s *SQLStorage) ListAPIKeys(tenantID string, page, size int) ([]*plugin.APIKey, int64, error) {
	page, size = normalizePage(page, size)
	where := "deleted = 0"
	args := []interface{}{}
	if tenantID != "" {
		where += " AND tenant_id = ?"
		args = append(args, tenantID)
	}
	var total int64
	if err := s.db.QueryRow("SELECT COUNT(*) FROM api_keys WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query("SELECT "+apiKeyCols+" FROM api_keys WHERE "+where+
		" ORDER BY created_at DESC LIMIT ? OFFSET ?", append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var keys []*plugin.APIKey
	for rows.Next() {
		k, err := scanAPIKey(rows)
		if err != nil {
			return nil, 0, err
		}
		keys = append(keys, k)
	}
	return keys, total, rows.Err()
}

func (s *SQLStorage) DeleteAPIKey(keyID string) error {
	res, err := s.db.Exec("UPDATE api_keys SET deleted = 1 WHERE id = ?", keyID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
```

> 注意:`ON CONFLICT(id)` 是 SQLite 语法;MySQL 将在任务 6 中通过驱动分支处理(见 storage_mysql.go 的 saveSQL 差异说明)。

- [ ] **步骤 4:运行测试验证通过**

运行:`go test ./pkg/plugin/oss/ -run TestSQLStorageAPIKeyCRUD -v`
预期:PASS。

- [ ] **步骤 5:Commit**

```bash
git add pkg/plugin/oss/storage_sql.go pkg/plugin/oss/storage_sql_test.go
git commit -m "feat(plugin): 共享 SQL 存储 APIKey CRUD(软删除/额度更新/分页)"
```

---

### 任务 5:共享 SQL 存储 — ModelConfig(加密)与 AuditLog

**文件:**
- 修改:`pkg/plugin/oss/storage_sql.go`
- 修改:`pkg/plugin/oss/storage_sql_test.go`

- [ ] **步骤 1:编写失败测试**

`pkg/plugin/oss/storage_sql_test.go` 追加:

```go
func TestSQLStorageModelConfigCRUD(t *testing.T) {
	s := newTestSQLStorage(t)
	now := time.Now()
	cfg := &plugin.ModelConfig{
		ID: "m1", ModelName: "gpt-4", Provider: "openai", ProviderModel: "gpt-4o",
		BaseURL: "https://api.openai.com", APIKey: "sk-upstream-secret",
		Timeout: 60, MaxRetries: 2, RetryInterval: 3, Weight: 1, Enabled: true,
		Tags: map[string]string{"env": "prod"}, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.SaveModelConfig(cfg); err != nil {
		t.Fatalf("SaveModelConfig: %v", err)
	}

	// 读回后 api_key 已解密还原
	got, err := s.GetModelConfig("gpt-4")
	if err != nil || got.APIKey != "sk-upstream-secret" {
		t.Fatalf("GetModelConfig = %v, %v", got, err)
	}
	if got.Tags["env"] != "prod" {
		t.Fatalf("Tags mismatch: %v", got.Tags)
	}
	// 按 ID 查
	byID, err := s.GetModelConfigByID("m1")
	if err != nil || byID.ModelName != "gpt-4" {
		t.Fatalf("GetModelConfigByID = %v, %v", byID, err)
	}
	// 列表
	if _, total, err := s.ListModelConfigs(1, 10); err != nil || total != 1 {
		t.Fatalf("ListModelConfigs total = %d, err %v", total, err)
	}
	// 删除
	if err := s.DeleteModelConfig("m1"); err != nil {
		t.Fatalf("DeleteModelConfig: %v", err)
	}
	if _, err := s.GetModelConfig("gpt-4"); err != ErrNotFound {
		t.Fatalf("GetModelConfig after delete err = %v; want ErrNotFound", err)
	}
}

func TestSQLStorageAuditLogCRUD(t *testing.T) {
	s := newTestSQLStorage(t)
	now := time.Now()
	log := &plugin.AuditLog{
		ID: "a1", RequestID: "r1", TenantID: "t1", APIKeyID: "k1",
		ModelName: "gpt-4", Provider: "openai", RequestMethod: "POST",
		RequestPath: "/v1/chat/completions", RequestHeaders: map[string]string{"Content-Type": "application/json"},
		RequestBody: `{"model":"gpt-4"}`, ResponseStatus: 200, ResponseBody: `{"choices":[]}`,
		SSEChunks: []plugin.SSEChunk{{Index: 0, Data: `{"choices":[]}`, Timestamp: now}},
		PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, Duration: 120,
		ClientIP: "127.0.0.1", IsStream: true, CreatedAt: now,
	}
	if err := s.SaveAuditLog(log); err != nil {
		t.Fatalf("SaveAuditLog: %v", err)
	}
	// 批量
	if err := s.BatchSaveAuditLogs([]*plugin.AuditLog{
		{ID: "a2", RequestID: "r2", ModelName: "qwen", CreatedAt: now},
	}); err != nil {
		t.Fatalf("BatchSaveAuditLogs: %v", err)
	}

	// 按 RequestID 精查
	logs, total, err := s.QueryAuditLogs(plugin.AuditLogFilter{RequestID: "r1"}, 1, 10)
	if err != nil || total != 1 {
		t.Fatalf("Query by requestID = %d, %v", total, err)
	}
	if logs[0].SSEChunks[0].Data != `{"choices":[]}` {
		t.Fatalf("SSEChunks roundtrip mismatch: %+v", logs[0].SSEChunks)
	}
	if logs[0].RequestHeaders["Content-Type"] != "application/json" {
		t.Fatalf("headers roundtrip mismatch: %v", logs[0].RequestHeaders)
	}

	// 组合过滤: 租户 + 模型 + 状态 + 流式 + 关键词
	f := plugin.AuditLogFilter{TenantID: "t1", ModelName: "gpt-4", Status: 200, IsStream: boolPtr(true), Keyword: "choices"}
	if _, total, err := s.QueryAuditLogs(f, 1, 10); err != nil || total != 1 {
		t.Fatalf("Query combined = %d, %v", total, err)
	}
	// 时间过滤
	start := now.Add(-time.Hour)
	end := now.Add(time.Hour)
	f2 := plugin.AuditLogFilter{StartTime: &start, EndTime: &end}
	if _, total, err := s.QueryAuditLogs(f2, 1, 10); err != nil || total != 2 {
		t.Fatalf("Query time range = %d, %v", total, err)
	}
}

func boolPtr(b bool) *bool { return &b }

func TestSQLStoragePingClose(t *testing.T) {
	s := newTestSQLStorage(t)
	if err := s.Ping(); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
```

- [ ] **步骤 2:运行测试验证失败**

运行:`go test ./pkg/plugin/oss/ -run "TestSQLStorageModelConfig|TestSQLStorageAuditLog|TestSQLStoragePing" -v`
预期:FAIL,`s.SaveModelConfig undefined`。

- [ ] **步骤 3:实现 ModelConfig CRUD(加密)**

`pkg/plugin/oss/storage_sql.go` 追加:

```go
// ===== 模型配置管理 =====

const modelConfigCols = "id, model_name, provider, provider_model, base_url, api_key, encrypted, timeout, max_retries, retry_interval, weight, enabled, tags, created_at, updated_at"

func scanModelConfig(row interface{ Scan(...interface{}) error }) (*plugin.ModelConfig, error) {
	var c plugin.ModelConfig
	var apiKey, tags, createdAt, updatedAt string
	var encrypted, enabled int
	if err := row.Scan(&c.ID, &c.ModelName, &c.Provider, &c.ProviderModel, &c.BaseURL,
		&apiKey, &encrypted, &c.Timeout, &c.MaxRetries, &c.RetryInterval, &c.Weight,
		&enabled, &tags, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	// api_key 解密(encrypted=1 表示密文)
	if encrypted == 1 {
		if plain, err := Decrypt(apiKey, s2encryptKey()); err == nil {
			apiKey = plain
		}
	}
	c.APIKey = apiKey
	c.Enabled = enabled == 1
	_ = json.Unmarshal([]byte(tags), &c.Tags)
	var cms, ums int64
	fmt.Sscanf(createdAt, "%d", &cms)
	fmt.Sscanf(updatedAt, "%d", &ums)
	c.CreatedAt = msToTime(cms)
	c.UpdatedAt = msToTime(ums)
	return &c, nil
}

// s2encryptKey 占位,由 SQLStorage 持有密钥,见下方实际实现
```

> ⚠️ 修正:scanModelConfig 需要访问 `s.encryptKey`,改为方法(而非包函数):

```go
func (s *SQLStorage) scanModelConfig(row interface{ Scan(...interface{}) error }) (*plugin.ModelConfig, error) {
	var c plugin.ModelConfig
	var apiKey, tags, createdAt, updatedAt string
	var encrypted, enabled int
	if err := row.Scan(&c.ID, &c.ModelName, &c.Provider, &c.ProviderModel, &c.BaseURL,
		&apiKey, &encrypted, &c.Timeout, &c.MaxRetries, &c.RetryInterval, &c.Weight,
		&enabled, &tags, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if encrypted == 1 {
		if plain, err := Decrypt(apiKey, s.encryptKey); err == nil {
			apiKey = plain
		}
	}
	c.APIKey = apiKey
	c.Enabled = enabled == 1
	_ = json.Unmarshal([]byte(tags), &c.Tags)
	var cms, ums int64
	fmt.Sscanf(createdAt, "%d", &cms)
	fmt.Sscanf(updatedAt, "%d", &ums)
	c.CreatedAt = msToTime(cms)
	c.UpdatedAt = msToTime(ums)
	return &c, nil
}

func (s *SQLStorage) GetModelConfig(modelName string) (*plugin.ModelConfig, error) {
	row := s.db.QueryRow("SELECT "+modelConfigCols+" FROM model_configs WHERE model_name = ?", modelName)
	return s.scanModelConfig(row)
}

func (s *SQLStorage) GetModelConfigByID(id string) (*plugin.ModelConfig, error) {
	row := s.db.QueryRow("SELECT "+modelConfigCols+" FROM model_configs WHERE id = ?", id)
	return s.scanModelConfig(row)
}

func (s *SQLStorage) SaveModelConfig(config *plugin.ModelConfig) error {
	encrypted, err := Encrypt(config.APIKey, s.encryptKey)
	if err != nil {
		return fmt.Errorf("encrypt api key: %w", err)
	}
	created := timeToMS(config.CreatedAt)
	updated := timeToMS(config.UpdatedAt)
	_, err = s.db.Exec(
		`INSERT INTO model_configs (id, model_name, provider, provider_model, base_url, api_key, encrypted, timeout, max_retries, retry_interval, weight, enabled, tags, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,1,?,?,?,?,?,?,?,?)
		 ON CONFLICT(id) DO UPDATE SET model_name=excluded.model_name, provider=excluded.provider,
		   provider_model=excluded.provider_model, base_url=excluded.base_url, api_key=excluded.api_key,
		   encrypted=excluded.encrypted, timeout=excluded.timeout, max_retries=excluded.max_retries,
		   retry_interval=excluded.retry_interval, weight=excluded.weight, enabled=excluded.enabled,
		   tags=excluded.tags, updated_at=excluded.updated_at`,
		config.ID, config.ModelName, config.Provider, config.ProviderModel, config.BaseURL,
		encrypted, config.Timeout, config.MaxRetries, config.RetryInterval, config.Weight,
		config.Enabled, marshalJSON(config.Tags), created, updated)
	return err
}

func (s *SQLStorage) ListModelConfigs(page, size int) ([]*plugin.ModelConfig, int64, error) {
	page, size = normalizePage(page, size)
	var total int64
	if err := s.db.QueryRow("SELECT COUNT(*) FROM model_configs").Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query("SELECT "+modelConfigCols+" FROM model_configs ORDER BY created_at DESC LIMIT ? OFFSET ?", size, (page-1)*size)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var configs []*plugin.ModelConfig
	for rows.Next() {
		c, err := s.scanModelConfig(rows)
		if err != nil {
			return nil, 0, err
		}
		configs = append(configs, c)
	}
	return configs, total, rows.Err()
}

func (s *SQLStorage) DeleteModelConfig(id string) error {
	res, err := s.db.Exec("DELETE FROM model_configs WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
```

- [ ] **步骤 4:实现 AuditLog CRUD**

`pkg/plugin/oss/storage_sql.go` 追加:

```go
// ===== 审计日志 =====

const auditLogCols = "id, request_id, tenant_id, api_key_id, model_name, provider, request_method, request_path, request_headers, request_body, response_status, response_body, sse_chunks, prompt_tokens, completion_tokens, total_tokens, duration_ms, client_ip, is_stream, disconnected, disconnect_reason, sha256_fingerprint, created_at"

func (s *SQLStorage) scanAuditLog(row interface{ Scan(...interface{}) error }) (*plugin.AuditLog, error) {
	var l plugin.AuditLog
	var headers, chunks, createdAt string
	var isStream, disconnected int
	if err := row.Scan(&l.ID, &l.RequestID, &l.TenantID, &l.APIKeyID, &l.ModelName,
		&l.Provider, &l.RequestMethod, &l.RequestPath, &headers, &l.RequestBody,
		&l.ResponseStatus, &l.ResponseBody, &chunks, &l.PromptTokens, &l.CompletionTokens,
		&l.TotalTokens, &l.Duration, &l.ClientIP, &isStream, &disconnected,
		&l.DisconnectReason, &l.SHA256Fingerprint, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	_ = json.Unmarshal([]byte(headers), &l.RequestHeaders)
	_ = json.Unmarshal([]byte(chunks), &l.SSEChunks)
	l.IsStream = isStream == 1
	l.Disconnected = disconnected == 1
	var cms int64
	fmt.Sscanf(createdAt, "%d", &cms)
	l.CreatedAt = msToTime(cms)
	return &l, nil
}

func (s *SQLStorage) SaveAuditLog(log *plugin.AuditLog) error {
	_, err := s.db.Exec(
		`INSERT INTO audit_logs (id, request_id, tenant_id, api_key_id, model_name, provider, request_method, request_path, request_headers, request_body, response_status, response_body, sse_chunks, prompt_tokens, completion_tokens, total_tokens, duration_ms, client_ip, is_stream, disconnected, disconnect_reason, sha256_fingerprint, created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		log.ID, log.RequestID, log.TenantID, log.APIKeyID, log.ModelName, log.Provider,
		log.RequestMethod, log.RequestPath, marshalJSON(log.RequestHeaders), log.RequestBody,
		log.ResponseStatus, log.ResponseBody, marshalJSON(log.SSEChunks),
		log.PromptTokens, log.CompletionTokens, log.TotalTokens, log.Duration,
		log.ClientIP, log.IsStream, log.Disconnected, log.DisconnectReason,
		log.SHA256Fingerprint, timeToMS(log.CreatedAt))
	return err
}

func (s *SQLStorage) BatchSaveAuditLogs(logs []*plugin.AuditLog) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	for _, l := range logs {
		if err := s.SaveAuditLog(l); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// buildAuditWhere 构建过滤 WHERE 子句与参数(与 MemStorage.matchAuditLog 语义一致)
func buildAuditWhere(filter plugin.AuditLogFilter) (string, []interface{}) {
	conds := []string{}
	args := []interface{}{}
	add := func(cond string, arg interface{}) {
		conds = append(conds, cond)
		args = append(args, arg)
	}
	if filter.TenantID != "" {
		add("tenant_id = ?", filter.TenantID)
	}
	if filter.APIKeyID != "" {
		add("api_key_id = ?", filter.APIKeyID)
	}
	if filter.ModelName != "" {
		add("model_name = ?", filter.ModelName)
	}
	if filter.RequestID != "" {
		add("request_id = ?", filter.RequestID)
	}
	if filter.Status != 0 {
		add("response_status = ?", filter.Status)
	}
	if filter.IsStream != nil {
		add("is_stream = ?", boolToInt(*filter.IsStream))
	}
	if filter.StartTime != nil {
		add("created_at >= ?", timeToMS(*filter.StartTime))
	}
	if filter.EndTime != nil {
		add("created_at <= ?", timeToMS(*filter.EndTime))
	}
	if filter.Keyword != "" {
		kw := "%" + filter.Keyword + "%"
		add("(request_id LIKE ? OR model_name LIKE ? OR request_body LIKE ? OR response_body LIKE ?)", kw, kw, kw, kw)
	}
	if len(conds) == 0 {
		return "1=1", nil
	}
	return strings.Join(conds, " AND "), args
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *SQLStorage) QueryAuditLogs(filter plugin.AuditLogFilter, page, size int) ([]*plugin.AuditLog, int64, error) {
	page, size = normalizePage(page, size)
	where, args := buildAuditWhere(filter)
	var total int64
	if err := s.db.QueryRow("SELECT COUNT(*) FROM audit_logs WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query("SELECT "+auditLogCols+" FROM audit_logs WHERE "+where+
		" ORDER BY created_at DESC LIMIT ? OFFSET ?", append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var logs []*plugin.AuditLog
	for rows.Next() {
		l, err := s.scanAuditLog(rows)
		if err != nil {
			return nil, 0, err
		}
		logs = append(logs, l)
	}
	return logs, total, rows.Err()
}

// ===== 健康检查 =====

func (s *SQLStorage) Ping() error { return s.db.Ping() }

func (s *SQLStorage) Close() error { return s.db.Close() }
```

- [ ] **步骤 5:运行测试验证通过**

运行:`go test ./pkg/plugin/oss/ -run "TestSQLStorage" -v`
预期:PASS(注意 `sort` 与 `strings` 已 import;若 `sort` 未使用请移除)。

- [ ] **步骤 6:Commit**

```bash
git add pkg/plugin/oss/storage_sql.go pkg/plugin/oss/storage_sql_test.go
git commit -m "feat(plugin): SQL 存储 ModelConfig(加密)与 AuditLog CRUD + 组合过滤查询"
```

---

### 任务 6:MySQL/SQLite 建表 DDL 与驱动适配

**文件:**
- 创建:`pkg/plugin/oss/storage_mysql.go`
- 创建:`pkg/plugin/oss/storage_sqlite.go`
- 修改:`pkg/plugin/oss/storage_sql.go`(SaveAPIKey/SaveModelConfig 的 UPSERT 方言分支)

- [ ] **步骤 1:编写失败测试**

`pkg/plugin/oss/storage_sql_test.go` 追加:

```go
func TestSQLiteCreateTables(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := sqliteCreateTables(db); err != nil {
		t.Fatalf("sqliteCreateTables: %v", err)
	}
	// 三张表均应存在
	for _, table := range []string{"api_keys", "model_configs", "audit_logs"} {
		var name string
		if err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name); err != nil {
			t.Fatalf("table %s missing: %v", table, err)
		}
	}
}

func TestMySQLCreateTables_SkipWithoutDB(t *testing.T) {
	// MySQL 需要真实环境;本地无 MySQL 时跳过
	db, err := sql.Open("mysql", "root:pass@tcp(127.0.0.1:3306)/neuralgate?charset=utf8mb4")
	if err != nil {
		t.Skipf("mysql driver unavailable: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Skipf("mysql not reachable: %v", err)
	}
	defer db.Close()
	if err := mysqlCreateTables(db); err != nil {
		t.Fatalf("mysqlCreateTables: %v", err)
	}
}
```

- [ ] **步骤 2:运行测试验证失败**

运行:`go test ./pkg/plugin/oss/ -run "TestSQLiteCreateTables|TestMySQLCreateTables" -v`
预期:FAIL,`undefined: sqliteCreateTables` / `undefined: mysqlCreateTables`。

- [ ] **步骤 3:实现建表 DDL**

`pkg/plugin/oss/storage_sqlite.go`:

```go
package oss

import "database/sql"

// sqliteCreateTables SQLite 建表(与 storage_sql.go 中 CRUD 列完全对应)
func sqliteCreateTables(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS api_keys (
			id TEXT PRIMARY KEY,
			key_hash TEXT NOT NULL UNIQUE,
			key_prefix TEXT NOT NULL DEFAULT '',
			tenant_id TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'active',
			quota INTEGER NOT NULL DEFAULT -1,
			used_quota INTEGER NOT NULL DEFAULT 0,
			rate_limit INTEGER NOT NULL DEFAULT 10,
			allowed_models TEXT NOT NULL DEFAULT '[]',
			expires_at INTEGER,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			created_by TEXT NOT NULL DEFAULT '',
			deleted INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS model_configs (
			id TEXT PRIMARY KEY,
			model_name TEXT NOT NULL UNIQUE,
			provider TEXT NOT NULL,
			provider_model TEXT NOT NULL,
			base_url TEXT NOT NULL,
			api_key TEXT NOT NULL,
			encrypted INTEGER NOT NULL DEFAULT 1,
			timeout INTEGER NOT NULL DEFAULT 60,
			max_retries INTEGER NOT NULL DEFAULT 2,
			retry_interval INTEGER NOT NULL DEFAULT 3,
			weight INTEGER NOT NULL DEFAULT 1,
			enabled INTEGER NOT NULL DEFAULT 1,
			tags TEXT NOT NULL DEFAULT '{}',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS audit_logs (
			id TEXT PRIMARY KEY,
			request_id TEXT NOT NULL,
			tenant_id TEXT NOT NULL DEFAULT '',
			api_key_id TEXT NOT NULL DEFAULT '',
			model_name TEXT NOT NULL DEFAULT '',
			provider TEXT NOT NULL DEFAULT '',
			request_method TEXT NOT NULL DEFAULT '',
			request_path TEXT NOT NULL DEFAULT '',
			request_headers TEXT NOT NULL DEFAULT '{}',
			request_body TEXT NOT NULL DEFAULT '',
			response_status INTEGER NOT NULL DEFAULT 0,
			response_body TEXT NOT NULL DEFAULT '',
			sse_chunks TEXT NOT NULL DEFAULT '[]',
			prompt_tokens INTEGER NOT NULL DEFAULT 0,
			completion_tokens INTEGER NOT NULL DEFAULT 0,
			total_tokens INTEGER NOT NULL DEFAULT 0,
			duration_ms INTEGER NOT NULL DEFAULT 0,
			client_ip TEXT NOT NULL DEFAULT '',
			is_stream INTEGER NOT NULL DEFAULT 0,
			disconnected INTEGER NOT NULL DEFAULT 0,
			disconnect_reason TEXT NOT NULL DEFAULT '',
			sha256_fingerprint TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_created ON audit_logs(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_tenant ON audit_logs(tenant_id)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_model ON audit_logs(model_name)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}
```

`pkg/plugin/oss/storage_mysql.go`:

```go
package oss

import "database/sql"

// mysqlCreateTables MySQL 建表(与 storage_sql.go 中 CRUD 列完全对应)
func mysqlCreateTables(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS api_keys (
			id VARCHAR(64) PRIMARY KEY,
			key_hash VARCHAR(64) NOT NULL UNIQUE,
			key_prefix VARCHAR(32) NOT NULL DEFAULT '',
			tenant_id VARCHAR(64) NOT NULL DEFAULT '',
			name VARCHAR(64) NOT NULL DEFAULT '',
			status VARCHAR(16) NOT NULL DEFAULT 'active',
			quota BIGINT NOT NULL DEFAULT -1,
			used_quota BIGINT NOT NULL DEFAULT 0,
			rate_limit INT NOT NULL DEFAULT 10,
			allowed_models TEXT NOT NULL,
			expires_at BIGINT NULL,
			created_at BIGINT NOT NULL,
			updated_at BIGINT NOT NULL,
			created_by VARCHAR(64) NOT NULL DEFAULT '',
			deleted TINYINT NOT NULL DEFAULT 0
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS model_configs (
			id VARCHAR(64) PRIMARY KEY,
			model_name VARCHAR(64) NOT NULL UNIQUE,
			provider VARCHAR(32) NOT NULL,
			provider_model VARCHAR(128) NOT NULL,
			base_url VARCHAR(512) NOT NULL,
			api_key VARCHAR(1024) NOT NULL,
			encrypted TINYINT NOT NULL DEFAULT 1,
			timeout INT NOT NULL DEFAULT 60,
			max_retries INT NOT NULL DEFAULT 2,
			retry_interval INT NOT NULL DEFAULT 3,
			weight INT NOT NULL DEFAULT 1,
			enabled TINYINT NOT NULL DEFAULT 1,
			tags TEXT NOT NULL,
			created_at BIGINT NOT NULL,
			updated_at BIGINT NOT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS audit_logs (
			id VARCHAR(64) PRIMARY KEY,
			request_id VARCHAR(64) NOT NULL,
			tenant_id VARCHAR(64) NOT NULL DEFAULT '',
			api_key_id VARCHAR(64) NOT NULL DEFAULT '',
			model_name VARCHAR(64) NOT NULL DEFAULT '',
			provider VARCHAR(32) NOT NULL DEFAULT '',
			request_method VARCHAR(16) NOT NULL DEFAULT '',
			request_path VARCHAR(255) NOT NULL DEFAULT '',
			request_headers TEXT NOT NULL,
			request_body TEXT NOT NULL,
			response_status INT NOT NULL DEFAULT 0,
			response_body TEXT NOT NULL,
			sse_chunks TEXT NOT NULL,
			prompt_tokens INT NOT NULL DEFAULT 0,
			completion_tokens INT NOT NULL DEFAULT 0,
			total_tokens INT NOT NULL DEFAULT 0,
			duration_ms BIGINT NOT NULL DEFAULT 0,
			client_ip VARCHAR(64) NOT NULL DEFAULT '',
			is_stream TINYINT NOT NULL DEFAULT 0,
			disconnected TINYINT NOT NULL DEFAULT 0,
			disconnect_reason VARCHAR(255) NOT NULL DEFAULT '',
			sha256_fingerprint VARCHAR(64) NOT NULL DEFAULT '',
			created_at BIGINT NOT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE INDEX idx_audit_created ON audit_logs(created_at)`,
		`CREATE INDEX idx_audit_tenant ON audit_logs(tenant_id)`,
		`CREATE INDEX idx_audit_model ON audit_logs(model_name)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **步骤 4:UPSERT 方言分支**

SQLite 用 `ON CONFLICT(id) DO UPDATE`,MySQL 用 `ON DUPLICATE KEY UPDATE`。修改 `pkg/plugin/oss/storage_sql.go` 的 SaveAPIKey / SaveModelConfig:

在 `SQLStorage` 增加:

```go
// upsertClause 返回 UPSERT 冲突更新子句(MySQL/SQLite 方言不同)
func (s *SQLStorage) upsertClause() string {
	if s.isMySQL() {
		return "ON DUPLICATE KEY UPDATE"
	}
	return "ON CONFLICT(id) DO UPDATE SET"
}

// isMySQL 判断当前驱动
func (s *SQLStorage) isMySQL() bool {
	return s.db != nil && strings.HasPrefix(reflectType(s.db), "*sql") // 简化实现见下方
}
```

> ⚠️ 简化:在 `Init` 中记录 driver 字段,`isMySQL` 直接判断字段:

```go
type SQLStorage struct {
	db         *sql.DB
	driver     string // mysql / sqlite
	encryptKey string
}
```

`Init` 中增加 `s.driver = driver`。然后:

```go
func (s *SQLStorage) isMySQL() bool { return s.driver == "mysql" }
```

SaveAPIKey 的 Exec 语句改为拼接(两个方言的更新列集相同,只差关键词):

```go
func (s *SQLStorage) SaveAPIKey(key *plugin.APIKey) error {
	allowed := marshalJSON(key.AllowedModels)
	expiresAt := timePtrToMS(key.ExpiresAt)
	created := timeToMS(key.CreatedAt)
	updated := timeToMS(key.UpdatedAt)
	upsert := ""
	if s.isMySQL() {
		upsert = " ON DUPLICATE KEY UPDATE key_hash=VALUES(key_hash), key_prefix=VALUES(key_prefix), tenant_id=VALUES(tenant_id), name=VALUES(name), status=VALUES(status), quota=VALUES(quota), used_quota=VALUES(used_quota), rate_limit=VALUES(rate_limit), allowed_models=VALUES(allowed_models), expires_at=VALUES(expires_at), updated_at=VALUES(updated_at), created_by=VALUES(created_by)"
	} else {
		upsert = " ON CONFLICT(id) DO UPDATE SET key_hash=excluded.key_hash, key_prefix=excluded.key_prefix, tenant_id=excluded.tenant_id, name=excluded.name, status=excluded.status, quota=excluded.quota, used_quota=excluded.used_quota, rate_limit=excluded.rate_limit, allowed_models=excluded.allowed_models, expires_at=excluded.expires_at, updated_at=excluded.updated_at, created_by=excluded.created_by"
	}
	_, err := s.db.Exec(
		`INSERT INTO api_keys (id, key_hash, key_prefix, tenant_id, name, status, quota, used_quota, rate_limit, allowed_models, expires_at, created_at, updated_at, created_by, deleted)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,0)`+upsert,
		key.ID, key.KeyHash, key.KeyPrefix, key.TenantID, key.Name, string(key.Status),
		key.Quota, key.UsedQuota, key.RateLimit, allowed, expiresAt, created, updated, key.CreatedBy)
	return err
}
```

SaveModelConfig 同理,替换原有 `ON CONFLICT(id) DO UPDATE` 为 `s.upsertClause()` + 对应列集(MySQL 用 `VALUES(col)`,SQLite 用 `excluded.col`):

```go
func (s *SQLStorage) SaveModelConfig(config *plugin.ModelConfig) error {
	encrypted, err := Encrypt(config.APIKey, s.encryptKey)
	if err != nil {
		return fmt.Errorf("encrypt api key: %w", err)
	}
	created := timeToMS(config.CreatedAt)
	updated := timeToMS(config.UpdatedAt)
	col := "model_name, provider, provider_model, base_url, api_key, encrypted, timeout, max_retries, retry_interval, weight, enabled, tags, updated_at"
	var upsert string
	if s.isMySQL() {
		upsert = " ON DUPLICATE KEY UPDATE model_name=VALUES(model_name), provider=VALUES(provider), provider_model=VALUES(provider_model), base_url=VALUES(base_url), api_key=VALUES(api_key), encrypted=VALUES(encrypted), timeout=VALUES(timeout), max_retries=VALUES(max_retries), retry_interval=VALUES(retry_interval), weight=VALUES(weight), enabled=VALUES(enabled), tags=VALUES(tags), updated_at=VALUES(updated_at)"
	} else {
		upsert = " ON CONFLICT(id) DO UPDATE SET model_name=excluded.model_name, provider=excluded.provider, provider_model=excluded.provider_model, base_url=excluded.base_url, api_key=excluded.api_key, encrypted=excluded.encrypted, timeout=excluded.timeout, max_retries=excluded.max_retries, retry_interval=excluded.retry_interval, weight=excluded.weight, enabled=excluded.enabled, tags=excluded.tags, updated_at=excluded.updated_at"
	}
	_, err = s.db.Exec(
		`INSERT INTO model_configs (id, model_name, provider, provider_model, base_url, api_key, encrypted, timeout, max_retries, retry_interval, weight, enabled, tags, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,1,?,?,?,?,?,?,?,?)`+upsert,
		config.ID, config.ModelName, config.Provider, config.ProviderModel, config.BaseURL,
		encrypted, config.Timeout, config.MaxRetries, config.RetryInterval, config.Weight,
		config.Enabled, marshalJSON(config.Tags), created, updated)
	return err
}
```

- [ ] **步骤 5:运行全部测试验证通过**

运行:`go test ./pkg/plugin/oss/ -v`
预期:全部 PASS(MySQL 无环境时 TestMySQLCreateTables 输出 SKIP)。

- [ ] **步骤 6:Commit**

```bash
git add pkg/plugin/oss/storage_mysql.go pkg/plugin/oss/storage_sqlite.go pkg/plugin/oss/storage_sql.go
git commit -m "feat(plugin): MySQL/SQLite 建表 DDL 与 UPSERT 方言适配"
```

---

### 任务 7:工厂 driver 分发与启动接线

**文件:**
- 修改:`pkg/plugin/oss/factory.go`
- 修改:`cmd/gateway/main.go`
- 测试:`pkg/plugin/oss/storage_factory_test.go`

- [ ] **步骤 1:编写失败测试**

`pkg/plugin/oss/storage_factory_test.go`:

```go
package oss

import (
	"testing"
)

func TestFactoryCreateStorageByDriver(t *testing.T) {
	f := NewPluginFactory()

	mem := f.CreateStorage()
	if err := mem.Init(map[string]interface{}{"driver": "mem"}); err != nil {
		t.Fatalf("mem init: %v", err)
	}
	if _, ok := mem.(*MemStorage); !ok {
		t.Fatalf("driver=mem got %T; want *MemStorage", mem)
	}

	sqlite := f.CreateStorage()
	if err := sqlite.Init(map[string]interface{}{
		"driver": "sqlite", "dsn": ":memory:", "encrypt_key": "test",
	}); err != nil {
		t.Fatalf("sqlite init: %v", err)
	}
	if _, ok := sqlite.(*SQLStorage); !ok {
		t.Fatalf("driver=sqlite got %T; want *SQLStorage", sqlite)
	}

	// 未知驱动报错
	bad := f.CreateStorage()
	if err := bad.Init(map[string]interface{}{"driver": "oracle"}); err == nil {
		t.Fatal("unknown driver must error")
	}
}
```

- [ ] **步骤 2:运行测试验证失败**

运行:`go test ./pkg/plugin/oss/ -run TestFactoryCreateStorageByDriver -v`
预期:FAIL,当前 CreateStorage 恒返回 MemStorage。

- [ ] **步骤 3:改造 ossFactory**

`pkg/plugin/oss/factory.go` 修改:

```go
// ossFactory OSS 工厂:仅注册 OSS 实现
type ossFactory struct {
	storage plugin.StoragePlugin
}

// NewPluginFactory 返回 OSS 版插件工厂
func NewPluginFactory() plugin.PluginFactory {
	return &ossFactory{}
}

// CreateStorage 创建存储:Init 时按 driver 选择 mem/sqlite/mysql
func (f *ossFactory) CreateStorage() plugin.StoragePlugin {
	if f.storage == nil {
		// 惰性创建:具体驱动由 Init 的 config 决定
		f.storage = &dynamicStorage{}
	}
	return f.storage
}
```

新增文件 `pkg/plugin/oss/storage_dynamic.go`:

```go
package oss

import (
	"fmt"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// dynamicStorage 按 Init 传入的 driver 分发到 mem/sqlite/mysql 实现
type dynamicStorage struct {
	impl plugin.StoragePlugin
}

func (d *dynamicStorage) Init(config map[string]interface{}) error {
	driver, _ := config["driver"].(string)
	switch driver {
	case "", "mem":
		d.impl = NewMemStorage()
	case "sqlite", "mysql":
		d.impl = NewSQLStorage()
	default:
		return fmt.Errorf("unsupported storage driver: %s", driver)
	}
	return d.impl.Init(config)
}

func (d *dynamicStorage) GetAPIKey(keyHash string) (*plugin.APIKey, error) {
	return d.impl.GetAPIKey(keyHash)
}
func (d *dynamicStorage) GetAPIKeyByID(id string) (*plugin.APIKey, error) {
	return d.impl.GetAPIKeyByID(id)
}
func (d *dynamicStorage) SaveAPIKey(key *plugin.APIKey) error { return d.impl.SaveAPIKey(key) }
func (d *dynamicStorage) UpdateAPIKeyQuota(keyID string, usedQuota int64) error {
	return d.impl.UpdateAPIKeyQuota(keyID, usedQuota)
}
func (d *dynamicStorage) ListAPIKeys(tenantID string, page, size int) ([]*plugin.APIKey, int64, error) {
	return d.impl.ListAPIKeys(tenantID, page, size)
}
func (d *dynamicStorage) DeleteAPIKey(keyID string) error { return d.impl.DeleteAPIKey(keyID) }
func (d *dynamicStorage) GetModelConfig(modelName string) (*plugin.ModelConfig, error) {
	return d.impl.GetModelConfig(modelName)
}
func (d *dynamicStorage) GetModelConfigByID(id string) (*plugin.ModelConfig, error) {
	return d.impl.GetModelConfigByID(id)
}
func (d *dynamicStorage) ListModelConfigs(page, size int) ([]*plugin.ModelConfig, int64, error) {
	return d.impl.ListModelConfigs(page, size)
}
func (d *dynamicStorage) SaveModelConfig(config *plugin.ModelConfig) error {
	return d.impl.SaveModelConfig(config)
}
func (d *dynamicStorage) DeleteModelConfig(id string) error { return d.impl.DeleteModelConfig(id) }
func (d *dynamicStorage) SaveAuditLog(log *plugin.AuditLog) error {
	return d.impl.SaveAuditLog(log)
}
func (d *dynamicStorage) BatchSaveAuditLogs(logs []*plugin.AuditLog) error {
	return d.impl.BatchSaveAuditLogs(logs)
}
func (d *dynamicStorage) QueryAuditLogs(filter plugin.AuditLogFilter, page, size int) ([]*plugin.AuditLog, int64, error) {
	return d.impl.QueryAuditLogs(filter, page, size)
}
func (d *dynamicStorage) Ping() error   { return d.impl.Ping() }
func (d *dynamicStorage) Close() error  { return d.impl.Close() }
```

- [ ] **步骤 4:修改 main.go 传参**

`cmd/gateway/main.go` 中存储初始化改为传完整配置:

```go
	// 3. 初始化插件工厂（BuildTag 决定实现）
	factory := newPluginFactory()
	storage := factory.CreateStorage()
	if err := storage.Init(map[string]interface{}{
		"driver":      cfg.Storage.Driver,
		"dsn":         cfg.Storage.DSN,
		"encrypt_key": cfg.Storage.EncryptKey,
	}); err != nil {
		logger.Fatal("存储初始化失败", zap.Error(err))
	}
```

- [ ] **步骤 5:运行测试验证通过**

运行:`go test ./pkg/plugin/oss/ -run TestFactoryCreateStorageByDriver -v && go build ./...`
预期:PASS,编译成功。

- [ ] **步骤 6:Commit**

```bash
git add pkg/plugin/oss/factory.go pkg/plugin/oss/storage_dynamic.go pkg/plugin/oss/storage_factory_test.go cmd/gateway/main.go
git commit -m "feat(plugin): 工厂按 driver 分发存储实现 + 启动传 dsn/encrypt_key"
```

---

### 任务 8:OpenAI/DeepSeek 适配器 — Token 用量与错误解析

**文件:**
- 修改:`pkg/adapter/openai.go`
- 修改:`pkg/adapter/deepseek.go`
- 测试:`pkg/adapter/adapters_test.go`

- [ ] **步骤 1:编写失败测试**

`pkg/adapter/adapters_test.go` 追加:

```go
func TestOpenAIParseTokenUsage(t *testing.T) {
	body := `{"id":"chatcmpl-1","object":"chat.completion","created":1700000000,"model":"gpt-4",
	  "choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],
	  "usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}
	a := NewOpenAIAdapter()
	p, c, total := a.ParseTokenUsage(resp)
	if p != 10 || c != 5 || total != 15 {
		t.Fatalf("ParseTokenUsage = %d,%d,%d; want 10,5,15", p, c, total)
	}
}

func TestOpenAIParseStreamUsage(t *testing.T) {
	chunk := `{"id":"chatcmpl-1","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4",
	  "choices":[{"index":0,"delta":{},"finish_reason":"stop"}],
	  "usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`
	a := NewOpenAIAdapter()
	p, c, total := a.ParseStreamUsage([]byte(chunk))
	if p != 10 || c != 5 || total != 15 {
		t.Fatalf("ParseStreamUsage = %d,%d,%d; want 10,5,15", p, c, total)
	}
	// 无 usage 的分片返回 0
	if p, c, total := a.ParseStreamUsage([]byte(`{"choices":[{"delta":{"content":"x"}}]}`)); p != 0 || c != 0 || total != 0 {
		t.Fatalf("ParseStreamUsage no-usage = %d,%d,%d; want 0,0,0", p, c, total)
	}
}

func TestOpenAIParseError(t *testing.T) {
	body := `{"error":{"message":"Incorrect API key","type":"invalid_request_error","param":null,"code":"invalid_api_key"}}`
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}
	a := NewOpenAIAdapter()
	code, msg := a.ParseError(resp)
	if code != 401 || msg != "Incorrect API key" {
		t.Fatalf("ParseError = %d,%q", code, msg)
	}
}

func TestDeepSeekParseTokenUsage(t *testing.T) {
	// DeepSeek 与 OpenAI 格式一致
	body := `{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}
	a := NewDeepSeekAdapter()
	p, c, total := a.ParseTokenUsage(resp)
	if p != 2 || c != 1 || total != 3 {
		t.Fatalf("DeepSeek ParseTokenUsage = %d,%d,%d", p, c, total)
	}
}
```

- [ ] **步骤 2:运行测试验证失败**

运行:`go test ./pkg/adapter/ -run "TestOpenAIParse|TestDeepSeekParse" -v`
预期:FAIL,返回 0,0,0 / 0,""。

- [ ] **步骤 3:实现 OpenAI 解析**

`pkg/adapter/openai.go` 实现(保留 SupportsNativeProxy):

```go
package adapter

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// OpenAIAdapter OpenAI 适配器（原生兼容：入口协议与上游一致，原样透传）
type OpenAIAdapter struct{}

// NewOpenAIAdapter 创建 OpenAI 适配器
func NewOpenAIAdapter() *OpenAIAdapter { return &OpenAIAdapter{} }

func (a *OpenAIAdapter) Name() string { return "openai" }

func (a *OpenAIAdapter) SupportsNativeProxy() bool { return true }

// TransformRequest 原生透传模式不调用;保留接口签名
func (a *OpenAIAdapter) TransformRequest(req *UnifiedRequest, rawBody []byte) (*http.Request, error) {
	return nil, errors.New("native proxy only")
}

// TransformResponse 原生透传不调用
func (a *OpenAIAdapter) TransformResponse(resp *http.Response) (*UnifiedResponse, error) {
	return nil, errors.New("native proxy only")
}

// TransformStreamChunk 原生透传不调用
func (a *OpenAIAdapter) TransformStreamChunk(chunk []byte) (*UnifiedSSEChunk, error) {
	return nil, errors.New("native proxy only")
}

// usageBody 非流式响应体(仅取 usage 字段)
type usageBody struct {
	Usage *TokenUsage `json:"usage"`
}

// ParseTokenUsage 从非流式响应体解析 Token 用量(OpenAI 格式)
func (a *OpenAIAdapter) ParseTokenUsage(resp *http.Response) (int, int, int) {
	if resp == nil || resp.Body == nil {
		return 0, 0, 0
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, 0, 0
	}
	resp.Body = io.NopCloser(io.Reader(bytes.NewReader(body)))
	var ub usageBody
	if err := json.Unmarshal(body, &ub); err != nil || ub.Usage == nil {
		return 0, 0, 0
	}
	return ub.Usage.PromptTokens, ub.Usage.CompletionTokens, ub.Usage.TotalTokens
}

// ParseStreamUsage 从流式最后一个分片解析 Token 用量(含 usage 字段的分片)
func (a *OpenAIAdapter) ParseStreamUsage(chunk []byte) (int, int, int) {
	var ub usageBody
	if err := json.Unmarshal(chunk, &ub); err != nil || ub.Usage == nil {
		return 0, 0, 0
	}
	return ub.Usage.PromptTokens, ub.Usage.CompletionTokens, ub.Usage.TotalTokens
}

// errorBody OpenAI 错误响应体
type errorBody struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

// ParseError 解析错误状态码与消息;无法解析时返回 (0, "")
func (a *OpenAIAdapter) ParseError(resp *http.Response) (int, string) {
	if resp == nil || resp.Body == nil {
		return 0, ""
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, ""
	}
	resp.Body = io.NopCloser(io.Reader(bytes.NewReader(body)))
	var eb errorBody
	if err := json.Unmarshal(body, &eb); err != nil || eb.Error.Message == "" {
		return 0, ""
	}
	return resp.StatusCode, eb.Error.Message
}
```

> 注意:需在 import 增加 `bytes`;`io.Reader(...)` 转写简化为 `bytes.NewReader(body)`。

- [ ] **步骤 4:实现 DeepSeek(复用 OpenAI 逻辑)**

`pkg/adapter/deepseek.go` 改为嵌入 OpenAIAdapter:

```go
package adapter

import "net/http"

// DeepSeekAdapter DeepSeek 适配器（原生兼容,协议与 OpenAI 完全一致）
type DeepSeekAdapter struct {
	OpenAIAdapter // 复用 OpenAI 格式解析
}

// NewDeepSeekAdapter 创建 DeepSeek 适配器
func NewDeepSeekAdapter() *DeepSeekAdapter { return &DeepSeekAdapter{} }

func (a *DeepSeekAdapter) Name() string { return "deepseek" }

func (a *DeepSeekAdapter) SupportsNativeProxy() bool { return true }

var _ http.ResponseWriter // 无意义占位,防止未使用 import;直接删除本行
```

> ⚠️ 删除上述 `var _ http.ResponseWriter` 行(不需要)。DeepSeekAdapter 通过嵌入复用 `TransformRequest/TransformResponse/ParseTokenUsage/ParseStreamUsage/ParseError`;`Name()` 覆盖。

- [ ] **步骤 5:运行测试验证通过**

运行:`go test ./pkg/adapter/ -run "TestOpenAIParse|TestDeepSeekParse" -v`
预期:PASS。

- [ ] **步骤 6:Commit**

```bash
git add pkg/adapter/openai.go pkg/adapter/deepseek.go pkg/adapter/adapters_test.go
git commit -m "feat(adapter): OpenAI/DeepSeek Token 用量与错误解析实现"
```

---

### 任务 9:通义/智谱适配器 — 协议转换

**文件:**
- 修改:`pkg/adapter/tongyi.go`
- 修改:`pkg/adapter/zhipu.go`
- 测试:`pkg/adapter/adapters_test.go`

- [ ] **步骤 1:编写失败测试**

`pkg/adapter/adapters_test.go` 追加:

```go
func TestTongyiTransformRequest(t *testing.T) {
	a := NewTongyiAdapter()
	req := &UnifiedRequest{
		Model: "qwen-max", Messages: []Message{{Role: "user", Content: "你好"}},
		Temperature: float64Ptr(0.7), Stream: true,
	}
	httpReq, err := a.TransformRequest(req, nil)
	if err != nil {
		t.Fatalf("TransformRequest: %v", err)
	}
	var body map[string]interface{}
	if err := json.Unmarshal([]byte(mustRead(httpReq.Body)), &body); err != nil {
		t.Fatalf("body not json: %v", err)
	}
	if body["model"] != "qwen-max" {
		t.Fatalf("model = %v; want qwen-max", body["model"])
	}
	input := body["input"].(map[string]interface{})
	msgs := input["messages"].([]interface{})
	first := msgs[0].(map[string]interface{})
	if first["role"] != "user" || first["content"] != "你好" {
		t.Fatalf("message = %v", first)
	}
	params := body["parameters"].(map[string]interface{})
	if params["temperature"] != 0.7 {
		t.Fatalf("temperature = %v; want 0.7", params["temperature"])
	}
}

func TestTongyiTransformResponse(t *testing.T) {
	a := NewTongyiAdapter()
	body := `{"output":{"choices":[{"message":{"role":"assistant","content":"你好，世界"}}],
	  "usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}},"request_id":"r1"}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}
	ur, err := a.TransformResponse(resp)
	if err != nil {
		t.Fatalf("TransformResponse: %v", err)
	}
	if len(ur.Choices) != 1 || ur.Choices[0].Message.Content != "你好，世界" {
		t.Fatalf("choices = %+v", ur.Choices)
	}
	if ur.Usage == nil || ur.Usage.PromptTokens != 5 || ur.Usage.CompletionTokens != 3 || ur.Usage.TotalTokens != 8 {
		t.Fatalf("usage = %+v", ur.Usage)
	}
	if ur.Object != "chat.completion" {
		t.Fatalf("object = %q", ur.Object)
	}
}

func TestZhipuTransformRequest(t *testing.T) {
	a := NewZhipuAdapter()
	req := &UnifiedRequest{Model: "glm-4", Messages: []Message{{Role: "user", Content: "hi"}}}
	httpReq, err := a.TransformRequest(req, nil)
	if err != nil {
		t.Fatalf("TransformRequest: %v", err)
	}
	var body map[string]interface{}
	_ = json.Unmarshal([]byte(mustRead(httpReq.Body)), &body)
	if body["model"] != "glm-4" {
		t.Fatalf("model = %v; want glm-4", body["model"])
	}
	msgs := body["messages"].([]interface{})
	if msgs[0].(map[string]interface{})["role"] != "user" {
		t.Fatalf("messages = %v", msgs)
	}
}

func TestZhipuTransformResponse(t *testing.T) {
	a := NewZhipuAdapter()
	body := `{"choices":[{"message":{"role":"assistant","content":"hi there"},"finish_reason":"stop"}],
	  "usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}
	ur, err := a.TransformResponse(resp)
	if err != nil {
		t.Fatalf("TransformResponse: %v", err)
	}
	if ur.Choices[0].Message.Content != "hi there" {
		t.Fatalf("content = %+v", ur.Choices[0].Message)
	}
	if ur.Usage.TotalTokens != 6 {
		t.Fatalf("usage = %+v", ur.Usage)
	}
}

func float64Ptr(f float64) *float64 { return &f }

func mustRead(r io.Reader) string {
	b, _ := io.ReadAll(r)
	return string(b)
}
```

- [ ] **步骤 2:运行测试验证失败**

运行:`go test ./pkg/adapter/ -run "TestTongyi|TestZhipu" -v`
预期:FAIL,`not implemented`。

- [ ] **步骤 3:实现通义(DashScope)转换**

`pkg/adapter/tongyi.go`:

```go
package adapter

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// TongyiAdapter 通义千问适配器（DashScope 协议,需转换）
type TongyiAdapter struct{}

// NewTongyiAdapter 创建通义千问适配器
func NewTongyiAdapter() *TongyiAdapter { return &TongyiAdapter{} }

func (a *TongyiAdapter) Name() string { return "tongyi" }

func (a *TongyiAdapter) SupportsNativeProxy() bool { return false }

// dashScopeMessage DashScope 消息
type dashScopeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// dashScopeRequest DashScope chat 请求体
type dashScopeRequest struct {
	Model      string            `json:"model"`
	Input      map[string]any    `json:"input"`
	Parameters map[string]any    `json:"parameters,omitempty"`
}

// TransformRequest OpenAI 格式 → DashScope 格式
func (a *TongyiAdapter) TransformRequest(req *UnifiedRequest, rawBody []byte) (*http.Request, error) {
	dash := dashScopeRequest{
		Model: req.Model,
		Input: map[string]any{"messages": toDashMessages(req.Messages)},
	}
	params := map[string]any{}
	if req.Temperature != nil {
		params["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		params["top_p"] = *req.TopP
	}
	if req.MaxTokens != nil {
		params["max_tokens"] = *req.MaxTokens
	}
	if len(params) > 0 {
		dash.Parameters = params
	}
	body, err := json.Marshal(dash)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequest(http.MethodPost, "", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	return httpReq, nil
}

func toDashMessages(msgs []Message) []dashScopeMessage {
	out := make([]dashScopeMessage, 0, len(msgs))
	for _, m := range msgs {
		content := ""
		switch c := m.Content.(type) {
		case string:
			content = c
		case []ContentPart:
			var sb bytes.Buffer
			for _, p := range c {
				if p.Type == "text" || p.Text != "" {
					sb.WriteString(p.Text)
				} else if p.ImageURL != nil {
					sb.WriteString("[image]")
				} else if p.InputAudio != nil {
					sb.WriteString("[audio]")
				}
			}
			content = sb.String()
		}
		out = append(out, dashScopeMessage{Role: m.Role, Content: content})
	}
	return out
}

// dashScopeResponse DashScope 非流式响应体
type dashScopeResponse struct {
	Output struct {
		Choices []struct {
			Message dashScopeMessage `json:"message"`
		} `json:"choices"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
	} `json:"output"`
}

// TransformResponse DashScope → OpenAI 格式(非流式)
func (a *TongyiAdapter) TransformResponse(resp *http.Response) (*UnifiedResponse, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	var ds dashScopeResponse
	if err := json.Unmarshal(body, &ds); err != nil {
		return nil, err
	}
	ur := &UnifiedResponse{
		ID:     "chatcmpl-" + resp.Request.Header.Get("X-Request-Id"),
		Object: "chat.completion",
		Model:  resp.Request.Header.Get("X-Model"),
	}
	for i, c := range ds.Output.Choices {
		ur.Choices = append(ur.Choices, Choice{
			Index: i,
			Message: Message{Role: c.Message.Role, Content: c.Message.Content},
			FinishReason: "stop",
		})
	}
	ur.Usage = &TokenUsage{
		PromptTokens:     ds.Output.Usage.InputTokens,
		CompletionTokens: ds.Output.Usage.OutputTokens,
		TotalTokens:      ds.Output.Usage.TotalTokens,
	}
	return ur, nil
}

// TransformStreamChunk DashScope 流式分片 → OpenAI 格式
func (a *TongyiAdapter) TransformStreamChunk(chunk []byte) (*UnifiedSSEChunk, error) {
	var ds struct {
		Output struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
					Role    string `json:"role"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		} `json:"output"`
	}
	if err := json.Unmarshal(chunk, &ds); err != nil {
		return nil, err
	}
	usc := &UnifiedSSEChunk{Object: "chat.completion.chunk"}
	for i, c := range ds.Output.Choices {
		delta := Message{}
		if c.Delta.Role != "" {
			delta.Role = c.Delta.Role
		}
		if c.Delta.Content != "" {
			delta.Content = c.Delta.Content
		}
		var fr *string
		if c.FinishReason != "" {
			fr = &c.FinishReason
		}
		usc.Choices = append(usc.Choices, SSEChoice{Index: i, Delta: delta, FinishReason: fr})
	}
	return usc, nil
}

// ParseTokenUsage 从已读 body 解析 DashScope 用量
func (a *TongyiAdapter) ParseTokenUsage(resp *http.Response) (int, int, int) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, 0, 0
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	var ds dashScopeResponse
	if err := json.Unmarshal(body, &ds); err != nil {
		return 0, 0, 0
	}
	return ds.Output.Usage.InputTokens, ds.Output.Usage.OutputTokens, ds.Output.Usage.TotalTokens
}

// ParseStreamUsage DashScope 流式分片暂无用量,返回 0
func (a *TongyiAdapter) ParseStreamUsage(chunk []byte) (int, int, int) { return 0, 0, 0 }

// ParseError DashScope 错误格式: {"code":"...","message":"..."}
func (a *TongyiAdapter) ParseError(resp *http.Response) (int, string) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, ""
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	var eb struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &eb); err != nil || eb.Message == "" {
		return 0, ""
	}
	return resp.StatusCode, eb.Message
}

// 保留未用依赖占位
var _ = errors.New
```

> ⚠️ 移除 `var _ = errors.New`(不需要 errors import 就删掉 import)。

- [ ] **步骤 4:实现智谱(GLM)转换**

`pkg/adapter/zhipu.go`:

```go
package adapter

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
)

// ZhipuAdapter 智谱 AI 适配器（异构上游,需转换）
type ZhipuAdapter struct{}

// NewZhipuAdapter 创建智谱 AI 适配器
func NewZhipuAdapter() *ZhipuAdapter { return &ZhipuAdapter{} }

func (a *ZhipuAdapter) Name() string { return "zhipu" }

func (a *ZhipuAdapter) SupportsNativeProxy() bool { return false }

// zhipuMessage GLM 消息(内容扁平化为 string)
type zhipuMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// zhipuRequest GLM chat 请求体(结构接近 OpenAI)
type zhipuRequest struct {
	Model    string          `json:"model"`
	Messages []zhipuMessage  `json:"messages"`
	Stream   bool            `json:"stream,omitempty"`
	Temperature *float64     `json:"temperature,omitempty"`
	TopP        *float64     `json:"top_p,omitempty"`
	MaxTokens   *int         `json:"max_tokens,omitempty"`
}

// TransformRequest OpenAI 格式 → GLM 格式
func (a *ZhipuAdapter) TransformRequest(req *UnifiedRequest, rawBody []byte) (*http.Request, error) {
	zr := zhipuRequest{
		Model:    req.Model,
		Messages: toZhipuMessages(req.Messages),
		Stream:   req.Stream,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		MaxTokens:   req.MaxTokens,
	}
	body, err := json.Marshal(zr)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequest(http.MethodPost, "", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	return httpReq, nil
}

func toZhipuMessages(msgs []Message) []zhipuMessage {
	out := make([]zhipuMessage, 0, len(msgs))
	for _, m := range msgs {
		content := ""
		switch c := m.Content.(type) {
		case string:
			content = c
		case []ContentPart:
			for _, p := range c {
				if p.Text != "" {
					content += p.Text
				} else if p.ImageURL != nil {
					content += "[image]"
				} else if p.InputAudio != nil {
					content += "[audio]"
				}
			}
		}
		out = append(out, zhipuMessage{Role: m.Role, Content: content})
	}
	return out
}

// zhipuResponse GLM 非流式响应体
type zhipuResponse struct {
	Choices []struct {
		Message      struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// TransformResponse GLM → OpenAI 格式(非流式)
func (a *ZhipuAdapter) TransformResponse(resp *http.Response) (*UnifiedResponse, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	var zr zhipuResponse
	if err := json.Unmarshal(body, &zr); err != nil {
		return nil, err
	}
	ur := &UnifiedResponse{Object: "chat.completion"}
	for i, c := range zr.Choices {
		ur.Choices = append(ur.Choices, Choice{
			Index: i,
			Message: Message{Role: c.Message.Role, Content: c.Message.Content},
			FinishReason: c.FinishReason,
		})
	}
	ur.Usage = &TokenUsage{
		PromptTokens:     zr.Usage.PromptTokens,
		CompletionTokens: zr.Usage.CompletionTokens,
		TotalTokens:      zr.Usage.TotalTokens,
	}
	return ur, nil
}

// TransformStreamChunk GLM 流式分片 → OpenAI 格式(choices[].delta 与 OpenAI 一致)
func (a *ZhipuAdapter) TransformStreamChunk(chunk []byte) (*UnifiedSSEChunk, error) {
	var usc UnifiedSSEChunk
	if err := json.Unmarshal(chunk, &usc); err != nil {
		return nil, err
	}
	usc.Object = "chat.completion.chunk"
	return &usc, nil
}

// ParseTokenUsage 解析 GLM 用量
func (a *ZhipuAdapter) ParseTokenUsage(resp *http.Response) (int, int, int) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, 0, 0
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	var zr zhipuResponse
	if err := json.Unmarshal(body, &zr); err != nil {
		return 0, 0, 0
	}
	return zr.Usage.PromptTokens, zr.Usage.CompletionTokens, zr.Usage.TotalTokens
}

// ParseStreamUsage 从含 usage 的流式分片解析
func (a *ZhipuAdapter) ParseStreamUsage(chunk []byte) (int, int, int) {
	var zr zhipuResponse
	if err := json.Unmarshal(chunk, &zr); err != nil {
		return 0, 0, 0
	}
	return zr.Usage.PromptTokens, zr.Usage.CompletionTokens, zr.Usage.TotalTokens
}

// ParseError GLM 错误格式: {"error":{"message":"..."}}
func (a *ZhipuAdapter) ParseError(resp *http.Response) (int, string) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, ""
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	var eb struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &eb); err != nil || eb.Error.Message == "" {
		return 0, ""
	}
	return resp.StatusCode, eb.Error.Message
}
```

- [ ] **步骤 5:运行测试验证通过**

运行:`go test ./pkg/adapter/ -v`
预期:全部 PASS(含既有测试)。

- [ ] **步骤 6:Commit**

```bash
git add pkg/adapter/tongyi.go pkg/adapter/zhipu.go pkg/adapter/adapters_test.go
git commit -m "feat(adapter): 通义/智谱协议转换实现(请求/响应/用量/错误)"
```

---

### 任务 10:鉴权中间件真实逻辑

**文件:**
- 修改:`pkg/core/middleware_auth.go`
- 测试:`pkg/core/middleware_auth_test.go`

- [ ] **步骤 1:编写失败测试**

`pkg/core/middleware_auth_test.go`:

```go
package core

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
)

func hashKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func newTestStorage() *oss.MemStorage {
	s := oss.NewMemStorage()
	now := time.Now()
	_ = s.SaveAPIKey(&plugin.APIKey{
		ID: "k1", KeyHash: hashKey("ng-goodkey"), KeyPrefix: "ng-goodkey",
		Name: "test", Status: plugin.APIKeyStatusActive, Quota: -1,
		CreatedAt: now, UpdatedAt: now,
	})
	_ = s.SaveAPIKey(&plugin.APIKey{
		ID: "k2", KeyHash: hashKey("ng-disabled"), KeyPrefix: "ng-disabled",
		Name: "disabled", Status: plugin.APIKeyStatusDisabled,
		CreatedAt: now, UpdatedAt: now,
	})
	exp := now.Add(-time.Hour)
	_ = s.SaveAPIKey(&plugin.APIKey{
		ID: "k3", KeyHash: hashKey("ng-expired"), KeyPrefix: "ng-expired",
		Name: "expired", Status: plugin.APIKeyStatusActive, ExpiresAt: &exp,
		CreatedAt: now, UpdatedAt: now,
	})
	_ = s.SaveAPIKey(&plugin.APIKey{
		ID: "k4", KeyHash: hashKey("ng-quota"), KeyPrefix: "ng-quota",
		Name: "quota", Status: plugin.APIKeyStatusActive, Quota: 10, UsedQuota: 10,
		CreatedAt: now, UpdatedAt: now,
	})
	return s
}

func doAuthRequest(storage plugin.StoragePlugin, bearer string) *httptest.ResponseRecorder {
	mw := AuthMiddleware(storage)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc, ok := RequestContextFrom(r.Context())
		if !ok {
			http.Error(w, "no context", 500)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(rc.APIKeyID + "|" + rc.TenantID))
	}))
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestAuthValidKey(t *testing.T) {
	rec := doAuthRequest(newTestStorage(), "ng-goodkey")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.HasPrefix(rec.Body.String(), "k1") {
		t.Fatalf("body = %s; want prefix k1", rec.Body.String())
	}
}

func TestAuthMissingKey(t *testing.T) {
	rec := doAuthRequest(newTestStorage(), "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invalid_api_key") {
		t.Fatalf("body = %s; want invalid_api_key", rec.Body.String())
	}
}

func TestAuthInvalidKey(t *testing.T) {
	rec := doAuthRequest(newTestStorage(), "ng-unknown")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401", rec.Code)
	}
}

func TestAuthDisabledKey(t *testing.T) {
	rec := doAuthRequest(newTestStorage(), "ng-disabled")
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "api_key_disabled") {
		t.Fatalf("status=%d body=%s; want 401 api_key_disabled", rec.Code, rec.Body.String())
	}
}

func TestAuthExpiredKey(t *testing.T) {
	rec := doAuthRequest(newTestStorage(), "ng-expired")
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "api_key_expired") {
		t.Fatalf("status=%d body=%s; want 401 api_key_expired", rec.Code, rec.Body.String())
	}
}

func TestAuthQuotaExceeded(t *testing.T) {
	rec := doAuthRequest(newTestStorage(), "ng-quota")
	if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), "quota_exceeded") {
		t.Fatalf("status=%d body=%s; want 429 quota_exceeded", rec.Code, rec.Body.String())
	}
}
```

- [ ] **步骤 2:运行测试验证失败**

运行:`go test ./pkg/core/ -run "TestAuth" -v`
预期:FAIL(当前中间件放行所有请求)。

- [ ] **步骤 3:实现真实鉴权**

`pkg/core/middleware_auth.go` 替换实现(保留 RequestContext 初始化与 headerMap):

```go
package core

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/google/uuid"
)

// AuthMiddleware 鉴权中间件:提取 Bearer API Key → 查存储校验 → 写入 RequestContext
func AuthMiddleware(storage plugin.StoragePlugin) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rc := &RequestContext{
				RequestID:      uuid.NewString(),
				StartTime:      time.Now(),
				ClientIP:       clientIP(r),
				RequestMethod:  r.Method,
				RequestPath:    r.URL.Path,
				RequestHeaders: headerMap(r.Header),
			}
			// 脱敏:移除 Authorization 头,避免 API Key 明文进入审计日志(PRD 5.4)
			delete(rc.RequestHeaders, "Authorization")

			// 提取 Bearer Key
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") || len(strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))) == 0 {
				writeOpenAIError(w, http.StatusUnauthorized, "invalid_request_error", "invalid_api_key", "Incorrect API key provided")
				return
			}
			rawKey := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))

			// SHA256 查存储
			sum := sha256.Sum256([]byte(rawKey))
			key, err := storage.GetAPIKey(hex.EncodeToString(sum[:]))
			if err != nil || key == nil {
				writeOpenAIError(w, http.StatusUnauthorized, "invalid_request_error", "invalid_api_key", "Incorrect API key provided")
				return
			}
			rc.APIKeyID = key.ID
			rc.TenantID = key.TenantID

			// 状态校验
			switch key.Status {
			case plugin.APIKeyStatusDisabled:
				writeOpenAIError(w, http.StatusUnauthorized, "invalid_request_error", "api_key_disabled", "API key is disabled")
				return
			}
			if key.ExpiresAt != nil && time.Now().After(*key.ExpiresAt) {
				writeOpenAIError(w, http.StatusUnauthorized, "invalid_request_error", "api_key_expired", "API key has expired")
				return
			}
			// 额度校验(quota != -1 表示有限额)
			if key.Quota >= 0 && key.UsedQuota >= key.Quota {
				writeOpenAIError(w, http.StatusTooManyRequests, "rate_limit_exceeded", "quota_exceeded", "API key quota exceeded")
				return
			}

			ctx := WithRequestContext(r.Context(), rc)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// clientIP 提取客户端 IP(优先 X-Forwarded-For)
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
```

> 需要 import `net`。

- [ ] **步骤 4:运行测试验证通过**

运行:`go test ./pkg/core/ -run "TestAuth" -v`
预期:全部 PASS。

- [ ] **步骤 5:Commit**

```bash
git add pkg/core/middleware_auth.go pkg/core/middleware_auth_test.go
git commit -m "feat(core): 鉴权中间件真实逻辑(Key哈希校验/禁用/过期/额度)"
```

---

### 任务 11:路由匹配中间件真实逻辑

**文件:**
- 修改:`pkg/core/middleware_route.go`
- 测试:`pkg/core/middleware_route_test.go`

- [ ] **步骤 1:编写失败测试**

`pkg/core/middleware_route_test.go`:

```go
package core

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/adapter"
	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
)

func routeTestStorage() *oss.MemStorage {
	s := oss.NewMemStorage()
	now := time.Now()
	_ = s.SaveModelConfig(&plugin.ModelConfig{
		ID: "m1", ModelName: "gpt-4", Provider: "openai", ProviderModel: "gpt-4o",
		BaseURL: "https://upstream", APIKey: "sk", Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	})
	_ = s.SaveModelConfig(&plugin.ModelConfig{
		ID: "m2", ModelName: "disabled-model", Provider: "openai", ProviderModel: "x",
		BaseURL: "https://upstream", APIKey: "sk", Enabled: false,
		CreatedAt: now, UpdatedAt: now,
	})
	_ = s.SaveModelConfig(&plugin.ModelConfig{
		ID: "m3", ModelName: "deepseek-chat", Provider: "deepseek", ProviderModel: "deepseek-chat",
		BaseURL: "https://upstream", APIKey: "sk", Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	})
	// Key 限制模型:仅允许 gpt-4
	_ = s.SaveAPIKey(&plugin.APIKey{
		ID: "k1", KeyHash: hashKey("ng-restricted"), KeyPrefix: "ng-restricted",
		Name: "restricted", Status: plugin.APIKeyStatusActive,
		AllowedModels: []string{"gpt-4"}, CreatedAt: now, UpdatedAt: now,
	})
	_ = s.SaveAPIKey(&plugin.APIKey{
		ID: "k2", KeyHash: hashKey("ng-open"), KeyPrefix: "ng-open",
		Name: "open", Status: plugin.APIKeyStatusActive, AllowedModels: nil,
		CreatedAt: now, UpdatedAt: now,
	})
	return s
}

func doRouteRequest(storage plugin.StoragePlugin, registry *adapter.AdapterRegistry, keyID string, body string) *httptest.ResponseRecorder {
	rc := &RequestContext{APIKeyID: keyID, TenantID: "t1"}
	ctx := WithRequestContext(nil, rc)
	mw := RouteMatchMiddleware(storage, registry)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc, _ := RequestContextFrom(r.Context())
		// 恢复的 body 可再次读取
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Model", rc.ModelConfig.ModelName)
		w.Header().Set("X-Provider", rc.Adapter.Name())
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader([]byte(body)))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestRouteValidModel(t *testing.T) {
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	rec := doRouteRequest(routeTestStorage(), registry, "k2", `{"model":"gpt-4","messages":[]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-Model") != "gpt-4" || rec.Header().Get("X-Provider") != "openai" {
		t.Fatalf("headers = %s,%s", rec.Header().Get("X-Model"), rec.Header().Get("X-Provider"))
	}
	// body 恢复后可读
	if !strings.Contains(rec.Body.String(), "gpt-4") {
		t.Fatalf("restored body = %s", rec.Body.String())
	}
}

func TestRouteModelNotFound(t *testing.T) {
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	rec := doRouteRequest(routeTestStorage(), registry, "k2", `{"model":"nope","messages":[]}`)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "model_not_found") {
		t.Fatalf("status=%d body=%s; want 404 model_not_found", rec.Code, rec.Body.String())
	}
}

func TestRouteDisabledModel(t *testing.T) {
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	rec := doRouteRequest(routeTestStorage(), registry, "k2", `{"model":"disabled-model","messages":[]}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", rec.Code)
	}
}

func TestRouteModelAccessDenied(t *testing.T) {
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	rec := doRouteRequest(routeTestStorage(), registry, "k1", `{"model":"deepseek-chat","messages":[]}`)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "model_access_denied") {
		t.Fatalf("status=%d body=%s; want 403 model_access_denied", rec.Code, rec.Body.String())
	}
}

func TestRouteBadJSON(t *testing.T) {
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	rec := doRouteRequest(routeTestStorage(), registry, "k2", `{invalid`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", rec.Code)
	}
}
```

- [ ] **步骤 2:运行测试验证失败**

运行:`go test ./pkg/core/ -run "TestRoute" -v`
预期:FAIL(当前中间件直接放行,无适配器写入)。

- [ ] **步骤 3:实现真实路由匹配**

`pkg/core/middleware_route.go` 替换实现(签名需要 registry):

```go
package core

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/druidcaesa/neuralgate/pkg/adapter"
	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// RouteMatchMiddleware 路由匹配中间件:解析 model 字段 → 查模型配置 → 校验权限 → 写 RequestContext
func RouteMatchMiddleware(storage plugin.StoragePlugin, registry *adapter.AdapterRegistry) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rc, ok := RequestContextFrom(r.Context())
			if !ok {
				writeOpenAIError(w, http.StatusInternalServerError, "api_error", "internal_error", "internal error")
				return
			}

			// 读取请求体(上限 1MB),缓存后恢复
			body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err != nil {
				writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "bad_request", "failed to read request body")
				return
			}
		r.Body = io.NopCloser(bytes.NewReader(body))
			rc.RequestBody = body

			// 解析 model 字段(chat/completions 与 embeddings 都含 model)
			var reqBody struct {
				Model string `json:"model"`
			}
			if err := json.Unmarshal(body, &reqBody); err != nil {
				writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "bad_request", "invalid JSON body")
				return
			}
			if reqBody.Model == "" {
				writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "bad_request", "model field is required")
				return
			}

			// 查模型配置
			config, err := storage.GetModelConfig(reqBody.Model)
			if err != nil || config == nil || !config.Enabled {
				writeOpenAIError(w, http.StatusNotFound, "invalid_request_error", "model_not_found", "model not found: "+reqBody.Model)
				return
			}
			rc.ModelConfig = config

			// Key 模型权限校验(allowed_models 非空且不含 → 403)
			if key, err := storage.GetAPIKeyByID(rc.APIKeyID); err == nil && len(key.AllowedModels) > 0 {
				allowed := false
				for _, m := range key.AllowedModels {
					if m == config.ModelName {
						allowed = true
						break
					}
				}
				if !allowed {
					writeOpenAIError(w, http.StatusForbidden, "invalid_request_error", "model_access_denied", "model not allowed for this API key")
					return
				}
			}

			// 获取适配器
			adpt, err := registry.Get(config.Provider)
			if err != nil {
				writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "unsupported_model", "unsupported provider: "+config.Provider)
				return
			}
			rc.Adapter = adpt

			next.ServeHTTP(w, r.WithContext(WithRequestContext(r.Context(), rc)))
		})
	}
}
```

> 需要 import `bytes`。同时更新 `pkg/core/pipeline.go` 的 `fixedChain()` 调用:

```go
	return []Middleware{
		AuthMiddleware(p.storage),
		RateLimitMiddleware(p.rateLimiter),
		RouteMatchMiddleware(p.storage, registryOf(p)),
	}
```

> ⚠️ Pipeline 目前不持有 registry。将 `AdapterRegistry` 传入 `NewPipeline`:

```go
func NewPipeline(storage plugin.StoragePlugin, rateLimiter plugin.RateLimitPlugin, auditor plugin.AuditPipeline, registry *adapter.AdapterRegistry) *Pipeline {
	return &Pipeline{storage: storage, rateLimiter: rateLimiter, auditor: auditor, registry: registry}
}
```

Pipeline 增加字段 `registry *adapter.AdapterRegistry`;fixedChain 中使用 `RouteMatchMiddleware(p.storage, p.registry)`。更新 `cmd/gateway/main.go` 与 `pkg/core/proxy.go` 的 NewPipeline 调用(传 registry),并同步 `pkg/core/pipeline_test.go` / `proxy_test.go` 中 NewPipeline 调用(补第 4 个参数)。

- [ ] **步骤 4:运行测试验证通过**

运行:`go test ./pkg/core/ -run "TestRoute" -v && go test ./pkg/core/ -v && go build ./...`
预期:PASS;既有 pipeline/proxy 测试因 NewPipeline 签名变更同步修复后 PASS;编译成功。

- [ ] **步骤 5:Commit**

```bash
git add pkg/core/middleware_route.go pkg/core/middleware_route_test.go pkg/core/pipeline.go pkg/core/pipeline_test.go pkg/core/proxy.go pkg/core/proxy_test.go cmd/gateway/main.go
git commit -m "feat(core): 路由匹配中间件真实逻辑(模型解析/404/403/适配器注入)"
```

---

### 任务 12:限流中间件真实逻辑

**文件:**
- 修改:`pkg/core/middleware_limit.go`
- 测试:`pkg/core/middleware_limit_test.go`

- [ ] **步骤 1:编写失败测试**

`pkg/core/middleware_limit_test.go`:

```go
package core

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
)

func TestRateLimitAllow(t *testing.T) {
	limiter := oss.NewMemRateLimiter()
	_ = limiter.Init(map[string]interface{}{"default_rps": 10, "default_tpm": 100000})
	mw := RateLimitMiddleware(limiter)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	// 限流 Header 应存在
	if rec.Header().Get("X-RateLimit-Limit-Requests") == "" {
		t.Fatalf("missing X-RateLimit-Limit-Requests header: %v", rec.Header())
	}
}

func TestRateLimitExceeded(t *testing.T) {
	// 构造 rps=1 的限流器
	limiter := oss.NewMemRateLimiter()
	_ = limiter.Init(map[string]interface{}{"default_rps": 1, "default_tpm": 100000})
	mw := RateLimitMiddleware(limiter)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	do := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	// 第一次通过
	if rec := do(); rec.Code != http.StatusOK {
		t.Fatalf("first request status = %d; want 200", rec.Code)
	}
	// 第二次 429
	rec := do()
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d; want 429", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "rate_limit") {
		t.Fatalf("body = %s; want rate_limit", rec.Body.String())
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatalf("missing Retry-After header")
	}
}
```

- [ ] **步骤 2:运行测试验证失败**

运行:`go test ./pkg/core/ -run "TestRateLimit" -v`
预期:FAIL(当前直接放行)。

- [ ] **步骤 3:实现真实限流**

`pkg/core/middleware_limit.go`:

```go
package core

import (
	"net/http"
	"strconv"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// RateLimitMiddleware 限流中间件:调用 RateLimiter.Allow,超限返回 429 + X-RateLimit-* Header
func RateLimitMiddleware(rateLimiter plugin.RateLimitPlugin) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rc, ok := RequestContextFrom(r.Context())
			if !ok {
				writeOpenAIError(w, http.StatusInternalServerError, "api_error", "internal_error", "internal error")
				return
			}
			model := ""
			if rc.ModelConfig != nil {
				model = rc.ModelConfig.ModelName
			}
			allowed, _, err := rateLimiter.Allow(rc.TenantID, model, 0)
			if err != nil {
				// 限流器异常:降级放行(可用性优先)+ 记录错误日志
				next.ServeHTTP(w, r)
				return
			}
			if !allowed {
				current, limit, resetAt := rateLimiter.Status(rc.TenantID, model)
				w.Header().Set("X-RateLimit-Limit-Requests", strconv.FormatInt(limit, 10))
				w.Header().Set("X-RateLimit-Remaining-Requests", "0")
				w.Header().Set("X-RateLimit-Reset-Requests", "1s")
				w.Header().Set("Retry-After", "1")
				writeOpenAIError(w, http.StatusTooManyRequests, "rate_limit_exceeded", "rate_limit",
					"rate limit exceeded (current="+strconv.FormatInt(current, 10)+", limit="+strconv.FormatInt(limit, 10)+", reset="+resetAt.Format(time.RFC3339)+")")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
```

- [ ] **步骤 4:运行测试验证通过**

运行:`go test ./pkg/core/ -run "TestRateLimit" -v`
预期:PASS。

- [ ] **步骤 5:Commit**

```bash
git add pkg/core/middleware_limit.go pkg/core/middleware_limit_test.go
git commit -m "feat(core): 限流中间件真实逻辑(429 + X-RateLimit-* Header + 降级放行)"
```

---

### 任务 13:代理内核 — 端点分类与 /v1/models

**文件:**
- 修改:`pkg/core/proxy.go`
- 测试:`pkg/core/proxy_test.go`

- [ ] **步骤 1:编写失败测试**

`pkg/core/proxy_test.go` 追加:

```go
func TestProxyModelsList(t *testing.T) {
	storage := routeTestStorage()
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	limiter := oss.NewMemRateLimiter()
	auditor := oss.NewSimpleAuditor(storage)
	pc := NewProxyCore(NewPipeline(storage, limiter, auditor, registry), registry)

	// /v1/models 也走鉴权中间件(路由中间件对 GET 请求跳过 body 解析)
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer ng-open")
	rec := httptest.NewRecorder()
	pc.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var list struct {
		Object string `json:"object"`
		Data   []struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if list.Object != "list" {
		t.Fatalf("object = %q; want list", list.Object)
	}
	// routeTestStorage 中有 3 个模型配置(1 个 disabled)
	if len(list.Data) != 2 {
		t.Fatalf("data len = %d; want 2 (enabled only)", len(list.Data))
	}
	found := false
	for _, m := range list.Data {
		if m.ID == "gpt-4" && m.OwnedBy == "neuralgate" {
			found = true
		}
	}
	if !found {
		t.Fatalf("gpt-4 missing: %+v", list.Data)
	}
}

func TestProxyModelsDetail(t *testing.T) {
	storage := routeTestStorage()
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	limiter := oss.NewMemRateLimiter()
	auditor := oss.NewSimpleAuditor(storage)
	pc := NewProxyCore(NewPipeline(storage, limiter, auditor, registry), registry)

	req := httptest.NewRequest(http.MethodGet, "/v1/models/gpt-4", nil)
	req.Header.Set("Authorization", "Bearer ng-open")
	rec := httptest.NewRecorder()
	pc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/models/nope", nil)
	req.Header.Set("Authorization", "Bearer ng-open")
	rec = httptest.NewRecorder()
	pc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("detail missing model status = %d; want 404", rec.Code)
	}
}
```

> 注意:所有端点都走完整 Pipeline(鉴权→限流→路由)。GET 请求无 body,RouteMatchMiddleware 需对 GET 跳过 body 解析与 model 校验(见任务 17 步骤 3 的修复点)。

- [ ] **步骤 2:运行测试验证失败**

运行:`go test ./pkg/core/ -run "TestProxyModels" -v`
预期:FAIL(当前 503)。

- [ ] **步骤 3:实现端点分类 + 本地 /v1/models**

`pkg/core/proxy.go` 替换(保留 writeOpenAIError 等):

```go
package core

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/adapter"
)

// ProxyCore 代理内核层:端点分类 → 本地响应或核心代理转发
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

// proxyHandler 代理处理入口:端点分类
func (p *ProxyCore) proxyHandler(w http.ResponseWriter, r *http.Request) {
	// 健康检查
	if r.URL.Path == "/healthz" {
		writeHealthz(w)
		return
	}
	rc, ok := RequestContextFrom(r.Context())
	if !ok {
		writeOpenAIError(w, http.StatusInternalServerError, "api_error", "internal_error", "internal error")
		return
	}

	switch {
	case r.URL.Path == "/v1/models":
		p.handleModelsList(w, rc)
	case strings.HasPrefix(r.URL.Path, "/v1/models/"):
		p.handleModelDetail(w, r, rc)
	case r.URL.Path == "/v1/chat/completions" || r.URL.Path == "/v1/embeddings":
		p.handleProxy(w, r, rc)
	default:
		// 透传端点(completions/moderations/images/audio/files 等)
		p.handlePassThrough(w, r, rc)
	}
}

// handleModelsList GET /v1/models:返回启用模型列表(本地响应)
func (p *ProxyCore) handleModelsList(w http.ResponseWriter, rc *RequestContext) {
	models, _, err := p.pipeline.storage.ListModelConfigs(1, 1000)
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "api_error", "internal_error", "failed to list models")
		return
	}
	type modelItem struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		OwnedBy string `json:"owned_by"`
	}
	data := make([]modelItem, 0, len(models))
	for _, m := range models {
		if !m.Enabled {
			continue
		}
		data = append(data, modelItem{
			ID:      m.ModelName,
			Object:  "model",
			Created: m.CreatedAt.Unix(),
			OwnedBy: "neuralgate",
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"object": "list", "data": data})
}

// handleModelDetail GET /v1/models/{model}
func (p *ProxyCore) handleModelDetail(w http.ResponseWriter, r *http.Request, rc *RequestContext) {
	name := strings.TrimPrefix(r.URL.Path, "/v1/models/")
	config, err := p.pipeline.storage.GetModelConfig(name)
	if err != nil || !config.Enabled {
		writeOpenAIError(w, http.StatusNotFound, "invalid_request_error", "model_not_found", "model not found: "+name)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"id": config.ModelName, "object": "model",
		"created": config.CreatedAt.Unix(), "owned_by": "neuralgate",
	})
}
```

`handleProxy` 与 `handlePassThrough` 在任务 14 实现(先留最小透传错误):

```go
// handleProxy 核心代理(chat/completions、embeddings) — 任务 14 实现
func (p *ProxyCore) handleProxy(w http.ResponseWriter, r *http.Request, rc *RequestContext) {
	writeOpenAIError(w, http.StatusServiceUnavailable, "api_error", "service_unavailable", "proxy not implemented yet")
}

// handlePassThrough 透传端点 — 任务 14 实现
func (p *ProxyCore) handlePassThrough(w http.ResponseWriter, r *http.Request, rc *RequestContext) {
	writeOpenAIError(w, http.StatusServiceUnavailable, "api_error", "service_unavailable", "proxy not implemented yet")
}
```

- [ ] **步骤 4:运行测试验证通过**

运行:`go test ./pkg/core/ -run "TestProxyModels" -v`
预期:PASS(GET /v1/models 带 Bearer 通过鉴权;列表返回 2 个启用模型)。

- [ ] **步骤 5:Commit**

```bash
git add pkg/core/proxy.go pkg/core/proxy_test.go
git commit -m "feat(core): 代理内核端点分类 + /v1/models 本地响应"
```

---

### 任务 14:代理内核 — 核心转发(非流式 + 透传端点)

**文件:**
- 修改:`pkg/core/proxy.go`
- 测试:`pkg/core/proxy_test.go`

- [ ] **步骤 1:编写失败测试**

`pkg/core/proxy_test.go` 追加(mock 上游):

```go
// newMockUpstream 构造 OpenAI 兼容 mock 上游
func newMockUpstream(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var reqBody struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(body, &reqBody)
		if reqBody.Model != "gpt-4o" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"model must be replaced to gpt-4o","type":"invalid_request_error","code":"bad_request"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","object":"chat.completion","created":1700000000,"model":"gpt-4o",
		  "choices":[{"index":0,"message":{"role":"assistant","content":"hello from upstream"},"finish_reason":"stop"}],
		  "usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`))
	}))
}

func TestProxyChatCompletion(t *testing.T) {
	upstream := newMockUpstream(t)
	defer upstream.Close()

	storage := oss.NewMemStorage()
	now := time.Now()
	_ = storage.SaveModelConfig(&plugin.ModelConfig{
		ID: "m1", ModelName: "gpt-4", Provider: "openai", ProviderModel: "gpt-4o",
		BaseURL: upstream.URL, APIKey: "sk-upstream", Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	})
	_ = storage.SaveAPIKey(&plugin.APIKey{
		ID: "k1", KeyHash: hashKey("ng-test"), KeyPrefix: "ng-test", Name: "t",
		Status: plugin.APIKeyStatusActive, Quota: -1, CreatedAt: now, UpdatedAt: now,
	})
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	limiter := oss.NewMemRateLimiter()
	_ = limiter.Init(map[string]interface{}{"default_rps": 100, "default_tpm": 100000})
	auditor := oss.NewSimpleAuditor(storage)
	pc := NewProxyCore(NewPipeline(storage, limiter, auditor, registry), registry)

	body := `{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer ng-test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	pc.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["object"] != "chat.completion" {
		t.Fatalf("object = %v", resp["object"])
	}
	// 上游收到的是替换后的 model(ProviderModel)
	// 审计已落库
	logs, total, err := storage.QueryAuditLogs(plugin.AuditLogFilter{RequestID: ""}, 1, 10)
	if err != nil || total != 1 {
		t.Fatalf("audit total = %d, err %v", total, err)
	}
	if logs[0].ModelName != "gpt-4" || logs[0].Provider != "openai" {
		t.Fatalf("audit log = %+v", logs[0])
	}
	if logs[0].TotalTokens != 15 || logs[0].PromptTokens != 10 {
		t.Fatalf("audit tokens = %+v", logs[0])
	}
	if logs[0].ResponseStatus != 200 {
		t.Fatalf("audit status = %d", logs[0].ResponseStatus)
	}
}

func TestProxyUpstreamError(t *testing.T) {
	// 上游返回 500 → 网关 502 upstream_error
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"upstream boom","type":"api_error","code":"server_error"}}`))
	}))
	defer upstream.Close()

	storage := oss.NewMemStorage()
	now := time.Now()
	_ = storage.SaveModelConfig(&plugin.ModelConfig{
		ID: "m1", ModelName: "gpt-4", Provider: "openai", ProviderModel: "gpt-4o",
		BaseURL: upstream.URL, APIKey: "sk", Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	})
	_ = storage.SaveAPIKey(&plugin.APIKey{
		ID: "k1", KeyHash: hashKey("ng-test"), KeyPrefix: "ng-test", Name: "t",
		Status: plugin.APIKeyStatusActive, Quota: -1, CreatedAt: now, UpdatedAt: now,
	})
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	limiter := oss.NewMemRateLimiter()
	_ = limiter.Init(map[string]interface{}{"default_rps": 100, "default_tpm": 100000})
	auditor := oss.NewSimpleAuditor(storage)
	pc := NewProxyCore(NewPipeline(storage, limiter, auditor, registry), registry)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4","messages":[{"role":"user","content":"x"}]}`))
	req.Header.Set("Authorization", "Bearer ng-test")
	rec := httptest.NewRecorder()
	pc.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d; want 502, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "upstream_error") {
		t.Fatalf("body = %s; want upstream_error", rec.Body.String())
	}
}
```

- [ ] **步骤 2:运行测试验证失败**

运行:`go test ./pkg/core/ -run "TestProxyChat|TestProxyUpstreamError" -v`
预期:FAIL,503 `proxy not implemented yet`。

- [ ] **步骤 3:实现核心代理转发**

`pkg/core/proxy.go` 实现 handleProxy(替换占位):

```go
// handleProxy 核心代理:chat/completions 与 embeddings
// 流程: 原生透传(替换model) 或 适配器转换 → 超时重试转发 → 非流式写回+审计
func (p *ProxyCore) handleProxy(w http.ResponseWriter, r *http.Request, rc *RequestContext) {
	if rc.ModelConfig == nil || rc.Adapter == nil {
		writeOpenAIError(w, http.StatusInternalServerError, "api_error", "internal_error", "routing context missing")
		return
	}
	cfg := rc.ModelConfig
	adpt := rc.Adapter

	// 1. 构造上游请求
	upstreamURL := strings.TrimRight(cfg.BaseURL, "/") + r.URL.Path
	var outbound *http.Request
	var err error
	if adpt.SupportsNativeProxy() {
		outbound, err = p.buildNativeRequest(r, upstreamURL, cfg)
	} else {
		outbound, err = p.buildConvertedRequest(r, upstreamURL, cfg, adpt)
	}
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "bad_request", err.Error())
		return
	}

	// 2. 转发(重试)
	resp, err := p.doWithRetry(outbound, cfg)
	if err != nil {
		writeOpenAIError(w, http.StatusGatewayTimeout, "api_error", "upstream_timeout", "upstream timeout or unreachable: "+err.Error())
		return
	}
	defer resp.Body.Close()

	// 3. 上游错误(4xx/5xx) → 502 透传错误信息
	if resp.StatusCode >= 400 {
		code, msg := adpt.ParseError(resp)
		if code == 0 {
			code = resp.StatusCode
			msg = "upstream returned " + http.StatusText(resp.StatusCode)
		}
		rc.ResponseStatus = resp.StatusCode
		rc.EndTime = time.Now()
		p.finalizeAudit(rc, 0, 0, 0)
		writeOpenAIError(w, http.StatusBadGateway, "api_error", "upstream_error", msg)
		return
	}

	// 4. 非流式响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "api_error", "upstream_error", "failed to read upstream response")
		return
	}
	rc.ResponseBody = string(body)
	rc.ResponseStatus = resp.StatusCode

	// Token 用量
	prompt, completion, total := adpt.ParseTokenUsage(resp)
	rc.PromptTokens, rc.CompletionTokens, rc.TotalTokens = prompt, completion, total
	p.updateQuota(rc)

	// 写回客户端(透传上游响应头)
	copyResponseHeaders(w.Header(), resp.Header)
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
	rc.EndTime = time.Now()
	p.finalizeAudit(rc, prompt, completion, total)
}

// buildNativeRequest 原生透传:仅替换 model 字段,raw body 原样转发
func (p *ProxyCore) buildNativeRequest(r *http.Request, upstreamURL string, cfg *plugin.ModelConfig) (*http.Request, error) {
	raw := make([]byte, len(rcBody(r)))
	copy(raw, rcBody(r))
	// 替换 model 字段
	var bodyMap map[string]interface{}
	if err := json.Unmarshal(raw, &bodyMap); err != nil {
		return nil, err
	}
	bodyMap["model"] = cfg.ProviderModel
	newBody, err := json.Marshal(bodyMap)
	if err != nil {
		return nil, err
	}
	return p.newUpstreamRequest(r, upstreamURL, cfg, newBody)
}

// buildConvertedRequest 非原生适配器:TransformRequest 转换
func (p *ProxyCore) buildConvertedRequest(r *http.Request, upstreamURL string, cfg *plugin.ModelConfig, adpt adapter.ModelAdapter) (*http.Request, error) {
	var unified adapter.UnifiedRequest
	if err := json.Unmarshal(rcBody(r), &unified); err != nil {
		return nil, err
	}
	outbound, err := adpt.TransformRequest(&unified, rcBody(r))
	if err != nil {
		return nil, err
	}
	outbound.URL = upstreamURL // 覆盖为上游地址
	return p.attachUpstreamAuth(outbound, cfg)
}

// newUpstreamRequest 组装上游请求(URL/方法/头/上游Key)
func (p *ProxyCore) newUpstreamRequest(r *http.Request, upstreamURL string, cfg *plugin.ModelConfig, body []byte) (*http.Request, error) {
	outbound, err := http.NewRequest(r.Method, upstreamURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	return p.attachUpstreamAuth(outbound, cfg)
}

// attachUpstreamAuth 设置上游鉴权与 Content-Type
func (p *ProxyCore) attachUpstreamAuth(req *http.Request, cfg *plugin.ModelConfig) (*http.Request, error) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	return req, nil
}

// rcBody 从 RequestContext 取请求体(路由中间件已缓存)
func rcBody(r *http.Request) []byte {
	if rc, ok := RequestContextFrom(r.Context()); ok {
		return rc.RequestBody
	}
	return nil
}

// doWithRetry 转发并重试(连接错误/5xx 重试 MaxRetries 次,4xx 不重试)
func (p *ProxyCore) doWithRetry(req *http.Request, cfg *plugin.ModelConfig) (*http.Response, error) {
	timeout := time.Duration(cfg.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	client := &http.Client{Timeout: timeout}
	bodyBytes, _ := io.ReadAll(req.Body) // 预先读出,供每次重试
	var lastErr error
	attempts := cfg.MaxRetries + 1
	if attempts < 1 {
		attempts = 1
	}
	for i := 0; i < attempts; i++ {
		if i > 0 {
			time.Sleep(time.Duration(cfg.RetryInterval) * time.Second)
		}
		attempt := req.Clone(req.Context())
		attempt.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		resp, err := client.Do(attempt)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("upstream status %d", resp.StatusCode)
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			continue
		}
		return resp, nil
	}
	if lastErr == nil {
		lastErr = errors.New("upstream request failed")
	}
	return nil, lastErr
}

// updateQuota 回补 API Key 已用额度
func (p *ProxyCore) updateQuota(rc *RequestContext) {
	if rc.APIKeyID == "" {
		return
	}
	if key, err := p.pipeline.storage.GetAPIKeyByID(rc.APIKeyID); err == nil && key.Quota >= 0 {
		_ = p.pipeline.storage.UpdateAPIKeyQuota(key.ID, key.UsedQuota+int64(rc.TotalTokens))
	}
}

// finalizeAudit 审计 Finalize
func (p *ProxyCore) finalizeAudit(rc *RequestContext, prompt, completion, total int) {
	if p.pipeline.auditor == nil {
		return
	}
	_ = p.pipeline.auditor.Finalize(rc.RequestID, &plugin.AuditMeta{
		ResponseStatus:   rc.ResponseStatus,
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      total,
		Duration:         rc.EndTime.Sub(rc.StartTime).Milliseconds(),
	})
}

// handlePassThrough 透传端点:原样转发(不解析 body,仅替换上游 Key)
func (p *ProxyCore) handlePassThrough(w http.ResponseWriter, r *http.Request, rc *RequestContext) {
	if rc.ModelConfig == nil {
		writeOpenAIError(w, http.StatusNotFound, "invalid_request_error", "model_not_found", "model not found")
		return
	}
	cfg := rc.ModelConfig
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "bad_request", "failed to read body")
		return
	}
	upstreamURL := strings.TrimRight(cfg.BaseURL, "/") + r.URL.Path
	outbound, err := http.NewRequest(r.Method, upstreamURL, bytes.NewReader(body))
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "api_error", "internal_error", err.Error())
		return
	}
	// 复制请求头(Content-Type 等),替换鉴权
	for k, vv := range r.Header {
		if k == "Authorization" {
			continue
		}
		for _, v := range vv {
			outbound.Header.Add(k, v)
		}
	}
	outbound.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	resp, err := p.doWithRetry(outbound, cfg)
	if err != nil {
		writeOpenAIError(w, http.StatusGatewayTimeout, "api_error", "upstream_timeout", "upstream timeout: "+err.Error())
		return
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "api_error", "upstream_error", "failed to read upstream response")
		return
	}
	rc.ResponseStatus = resp.StatusCode
	copyResponseHeaders(w.Header(), resp.Header)
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(respBody)
}

// copyResponseHeaders 复制上游响应头到客户端
func copyResponseHeaders(dst, src http.Header) {
	for k, vv := range src {
		if k == "Content-Length" {
			continue
		}
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}
```

> ⚠️ `doWithRetry` 中 body 处理有冗余,简化实现(最终版):

```go
// doWithRetry 转发并重试(连接错误/5xx 重试 MaxRetries 次,4xx 不重试)
func (p *ProxyCore) doWithRetry(req *http.Request, cfg *plugin.ModelConfig) (*http.Response, error) {
	timeout := time.Duration(cfg.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	client := &http.Client{Timeout: timeout}
	bodyBytes, _ := io.ReadAll(req.Body) // 预先读出,供每次重试
	var lastErr error
	attempts := cfg.MaxRetries + 1
	if attempts < 1 {
		attempts = 1
	}
	for i := 0; i < attempts; i++ {
		if i > 0 {
			time.Sleep(time.Duration(cfg.RetryInterval) * time.Second)
		}
		attempt := req.Clone(req.Context())
		attempt.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		resp, err := client.Do(attempt)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("upstream status %d", resp.StatusCode)
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			continue
		}
		return resp, nil
	}
	if lastErr == nil {
		lastErr = errors.New("upstream request failed")
	}
	return nil, lastErr
}
```

同时审计 Submit 请求开始事件(在 handleProxy 开头):

```go
	// 0. 审计:请求开始(携带基础元数据)
	if p.pipeline.auditor != nil {
		_ = p.pipeline.auditor.Submit(&plugin.AuditEvent{
			RequestID: rc.RequestID,
			EventType: plugin.AuditEventRequestStart,
			Timestamp: rc.StartTime,
			Data: &plugin.AuditLog{
				ID: rc.RequestID, RequestID: rc.RequestID,
				TenantID: rc.TenantID, APIKeyID: rc.APIKeyID,
				ModelName: cfg.ModelName, Provider: cfg.Provider,
				RequestMethod: rc.RequestMethod, RequestPath: rc.RequestPath,
				RequestHeaders: rc.RequestHeaders, RequestBody: string(rc.RequestBody),
				ClientIP: rc.ClientIP, IsStream: rc.IsStream,
				CreatedAt: rc.StartTime,
			},
		})
	}
```

并在 `pkg/plugin/oss/audit_simple.go` 的 `Submit` 支持 Data 携带 AuditLog:

```go
// Submit 提交审计事件;Data 可携带 *AuditLog 作为基础元数据(请求开始事件)
func (a *SimpleAuditor) Submit(event *plugin.AuditEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	log, ok := a.pending[event.RequestID]
	if !ok {
		log = &plugin.AuditLog{ID: event.RequestID, RequestID: event.RequestID, CreatedAt: event.Timestamp}
		if base, isLog := event.Data.(*plugin.AuditLog); isLog && base != nil {
			log = base
		}
		a.pending[event.RequestID] = log
	}
	return nil
}
```

- [ ] **步骤 4:运行测试验证通过**

运行:`go test ./pkg/core/ -run "TestProxyChat|TestProxyUpstreamError" -v`
预期:PASS。mock 上游校验收到的 model 已替换为 gpt-4o;审计日志包含完整元数据与 Token 用量。

- [ ] **步骤 5:Commit**

```bash
git add pkg/core/proxy.go pkg/core/proxy_test.go pkg/plugin/oss/audit_simple.go
git commit -m "feat(core): 代理内核核心转发(原生透传/适配器转换/重试/审计落库)"
```

---

### 任务 15:SSE 流式代理与审计接线

**文件:**
- 修改:`pkg/core/sse_writer.go`
- 修改:`pkg/core/sse_reassembler.go`
- 修改:`pkg/core/disconnect_handler.go`
- 修改:`pkg/core/proxy.go`
- 测试:`pkg/core/sse_test.go`

- [ ] **步骤 1:编写失败测试**

`pkg/core/sse_test.go`:

```go
package core

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/adapter"
	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
)

// newMockSSEUpstream 返回 SSE 流式上游(含 usage 结尾分片)
func newMockSSEUpstream(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		chunks := []string{
			`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
			`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`,
			`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`,
			`data: [DONE]`,
		}
		for _, c := range chunks {
			_, _ = w.Write([]byte(c + "\n\n"))
			flusher.Flush()
		}
	}))
}

func TestProxySSEStream(t *testing.T) {
	upstream := newMockSSEUpstream(t)
	defer upstream.Close()

	storage := oss.NewMemStorage()
	now := time.Now()
	_ = storage.SaveModelConfig(&plugin.ModelConfig{
		ID: "m1", ModelName: "gpt-4", Provider: "openai", ProviderModel: "gpt-4o",
		BaseURL: upstream.URL, APIKey: "sk", Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	})
	_ = storage.SaveAPIKey(&plugin.APIKey{
		ID: "k1", KeyHash: hashKey("ng-test"), KeyPrefix: "ng-test", Name: "t",
		Status: plugin.APIKeyStatusActive, Quota: -1, CreatedAt: now, UpdatedAt: now,
	})
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	limiter := oss.NewMemRateLimiter()
	_ = limiter.Init(map[string]interface{}{"default_rps": 100, "default_tpm": 100000})
	auditor := oss.NewSimpleAuditor(storage)
	pc := NewProxyCore(NewPipeline(storage, limiter, auditor, registry), registry)

	body := `{"model":"gpt-4","messages":[{"role":"user","content":"hi"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer ng-test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	pc.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type = %q; want text/event-stream", ct)
	}
	raw := rec.Body.String()
	if !strings.Contains(raw, "data: [DONE]") {
		t.Fatalf("missing [DONE]: %s", raw)
	}
	// 分片内容透传:包含 Hello
	if !strings.Contains(raw, "Hello") {
		t.Fatalf("missing chunk content: %s", raw)
	}

	// 审计:分片已捕获 + usage 解析 + 状态
	logs, total, err := storage.QueryAuditLogs(plugin.AuditLogFilter{}, 1, 10)
	if err != nil || total != 1 {
		t.Fatalf("audit total = %d, err %v", total, err)
	}
	log := logs[0]
	if !log.IsStream {
		t.Fatalf("audit is_stream = false; want true")
	}
	if len(log.SSEChunks) != 4 {
		t.Fatalf("sse chunks = %d; want 4, got %+v", len(log.SSEChunks), log.SSEChunks)
	}
	if log.TotalTokens != 12 || log.PromptTokens != 10 || log.CompletionTokens != 2 {
		t.Fatalf("audit tokens = %+v", log)
	}
	if log.ResponseStatus != 200 {
		t.Fatalf("audit status = %d", log.ResponseStatus)
	}
}

func TestStreamReassembler(t *testing.T) {
	now := time.Now()
	chunks := []plugin.SSEChunk{
		{Index: 0, Data: `{"choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`},
		{Index: 1, Data: `{"choices":[{"index":0,"delta":{"content":"Hello"}}]}`},
		{Index: 2, Data: `{"choices":[{"index":0,"delta":{"content":" world"}}]}`},
		{Index: 3, Data: `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`},
	}
	r := NewStreamReassembler()
	out, err := r.Reassemble(chunks)
	if err != nil {
		t.Fatalf("Reassemble: %v", err)
	}
	if !strings.Contains(out, "Hello world") {
		t.Fatalf("reassembled = %q; want contains 'Hello world'", out)
	}
	if out != "Hello world" {
		t.Fatalf("reassembled = %q; want 'Hello world'", out)
	}
}
```

- [ ] **步骤 2:运行测试验证失败**

运行:`go test ./pkg/core/ -run "TestProxySSE|TestStreamReassembler" -v`
预期:FAIL(handleProxy 未实现流式分支;Reassembler 返回 not implemented)。

- [ ] **步骤 3:实现 SSEResponseWriter 分片捕获**

`pkg/core/sse_writer.go` 修改(Write 时捕获分片):

```go
// SSEResponseWriter 劫持 SSE 流量:原样写入客户端,同时解析分片投递审计
type SSEResponseWriter struct {
	http.ResponseWriter // 嵌入原始 Writer
	requestID           string
	auditor             plugin.AuditPipeline
	mu                  sync.Mutex
	startWrite          time.Time
	headerWritten       bool
	chunks              []plugin.SSEChunk // 已捕获分片
	lineBuf             string            // 跨 Write 的未完成行
	chunkIndex          int
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

// Write 写入原始 Writer(推送客户端),同时捕获 SSE 分片并投递审计
func (w *SSEResponseWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.headerWritten = true
	n, err := w.ResponseWriter.Write(data)
	w.capture(data)
	return n, err
}

// capture 按行解析 data: 事件(跨 Write 缓冲),投递审计
func (w *SSEResponseWriter) capture(data []byte) {
	if w.auditor == nil {
		return
	}
	w.lineBuf += string(data)
	for {
		idx := strings.IndexByte(w.lineBuf, '\n')
		if idx < 0 {
			return
		}
		line := strings.TrimSuffix(w.lineBuf[:idx], "\r")
		w.lineBuf = w.lineBuf[idx+1:]
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		chunk := plugin.SSEChunk{
			Index:     w.chunkIndex,
			Data:      payload,
			Timestamp: time.Now(),
			EventType: "data",
		}
		w.chunkIndex++
		w.chunks = append(w.chunks, chunk)
		_ = w.auditor.SubmitSSEChunk(w.requestID, &chunk)
	}
}

// Chunks 返回已捕获分片(供 Finalize 用量解析)
func (w *SSEResponseWriter) Chunks() []plugin.SSEChunk {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]plugin.SSEChunk, len(w.chunks))
	copy(out, w.chunks)
	return out
}
```

- [ ] **步骤 4:实现 Reassembler 与 DisconnectHandler**

`pkg/core/sse_reassembler.go`:

```go
package core

import (
	"encoding/json"
	"strings"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// StreamReassembler 分片重组器:解析每个分片的 delta.content,拼接为完整应答
type StreamReassembler struct{}

// NewStreamReassembler 创建重组器
func NewStreamReassembler() *StreamReassembler { return &StreamReassembler{} }

// Reassemble 重组:提取每分片 choices[0].delta.content(OpenAI 格式)拼接
func (r *StreamReassembler) Reassemble(chunks []plugin.SSEChunk) (string, error) {
	var sb strings.Builder
	for _, c := range chunks {
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(c.Data), &chunk); err != nil {
			continue // 跳过无法解析的分片
		}
		if len(chunk.Choices) > 0 {
			sb.WriteString(chunk.Choices[0].Delta.Content)
		}
	}
	return sb.String(), nil
}
```

`pkg/core/disconnect_handler.go`:

```go
package core

import (
	"context"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// DisconnectHandler 断连检测与补全
type DisconnectHandler struct {
	auditor plugin.AuditPipeline
}

// NewDisconnectHandler 创建断连处理器
func NewDisconnectHandler(auditor plugin.AuditPipeline) *DisconnectHandler {
	return &DisconnectHandler{auditor: auditor}
}

// Watch 监听请求取消;done 通道在流正常结束时关闭(防止正常结束误标断连)
func (h *DisconnectHandler) Watch(ctx context.Context, requestID string, done <-chan struct{}) {
	select {
	case <-ctx.Done():
		if h.auditor != nil {
			_ = h.auditor.MarkDisconnect(requestID, "client_disconnected")
		}
	case <-done:
	}
}
```

`pkg/plugin/oss/audit_simple.go` 的 MarkDisconnect 防竞态(已 Finalize 则忽略):

```go
// MarkDisconnect 标记客户端断连,保存已收集内容;已 Finalize 的记录忽略(正常结束竞态)
func (a *SimpleAuditor) MarkDisconnect(requestID string, reason string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	log, ok := a.pending[requestID]
	if !ok {
		return nil
	}
	log.Disconnected = true
	log.DisconnectReason = reason
	if err := a.storage.SaveAuditLog(log); err != nil {
		return err
	}
	delete(a.pending, requestID)
	return nil
}
```

- [ ] **步骤 5:实现流式转发分支(handleProxy)**

`pkg/core/proxy.go` 的 handleProxy 在第 4 步前增加流式检测:

```go
	// 3.5 流式响应:劫持 SSE
	if resp.StatusCode == http.StatusOK && isStreamRequest(r) {
		p.handleStreaming(w, r, rc, resp, cfg, adpt)
		return
	}
```

新增方法:

```go
// isStreamRequest 判断流式请求(请求体 stream=true 或响应为 text/event-stream)
func isStreamRequest(r *http.Request) bool {
	rc, ok := RequestContextFrom(r.Context())
	if ok && rc.IsStream {
		return true
	}
	var body struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(rcBody(r), &body)
	return body.Stream
}

// handleStreaming 流式转发:劫持分片写客户端 + 投递审计 + Finalize
func (p *ProxyCore) handleStreaming(w http.ResponseWriter, r *http.Request, rc *RequestContext, upstreamResp *http.Response, cfg *plugin.ModelConfig, adpt adapter.ModelAdapter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	done := make(chan struct{})
	defer close(done)
	disconnect := NewDisconnectHandler(p.pipeline.auditor)
	go disconnect.Watch(r.Context(), rc.RequestID, done)

	scanner := bufio.NewScanner(upstreamResp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	rc.ResponseStatus = http.StatusOK

	for scanner.Scan() {
		line := scanner.Text()
		// 写客户端
		if _, err := w.Write([]byte(line + "\n")); err != nil {
			break // 客户端断开
		}
		// 捕获分片(行级,与 SSEResponseWriter 逻辑一致)
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "data:") {
			payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
			if payload != "" && payload != "[DONE]" {
				chunk := plugin.SSEChunk{
					Index:     len(rc.SSEChunks),
					Data:      payload,
					Timestamp: time.Now(),
					EventType: "data",
				}
				rc.SSEChunks = append(rc.SSEChunks, chunk)
				_ = p.pipeline.auditor.SubmitSSEChunk(rc.RequestID, &chunk)
				// 解析 usage
				if prompt, completion, total := adpt.ParseStreamUsage([]byte(payload)); total > 0 {
					rc.PromptTokens, rc.CompletionTokens, rc.TotalTokens = prompt, completion, total
				}
			}
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	rc.EndTime = time.Now()
	p.updateQuota(rc)
	p.finalizeAudit(rc, rc.PromptTokens, rc.CompletionTokens, rc.TotalTokens)
}
```

> `rc.SSELen()` 不存在,直接 `len(rc.SSEChunks)`:

```go
			chunk := plugin.SSEChunk{
				Index:     len(rc.SSEChunks),
				Data:      payload,
				Timestamp: time.Now(),
				EventType: "data",
			}
```

`RequestContext.IsStream` 由 handleProxy 开始时设置:

```go
	// 0. 审计:请求开始(携带基础元数据)
	rc.IsStream = isStreamRequest(r)
```

- [ ] **步骤 6:运行测试验证通过**

运行:`go test ./pkg/core/ -run "TestProxySSE|TestStreamReassembler" -v`
预期:PASS(4 个分片捕获、usage=12、重组 "Hello world")。

- [ ] **步骤 7:Commit**

```bash
git add pkg/core/sse_writer.go pkg/core/sse_reassembler.go pkg/core/disconnect_handler.go pkg/core/proxy.go pkg/plugin/oss/audit_simple.go pkg/core/sse_test.go
git commit -m "feat(core): SSE 流式代理与审计接线(分片捕获/重组/断连检测)"
```

---

### 任务 16:管理后台 CRUD

**文件:**
- 创建:`pkg/admin/response.go`、`pkg/admin/api_key.go`、`pkg/admin/model_config.go`、`pkg/admin/audit_api.go`、`pkg/admin/system.go`
- 修改:`pkg/admin/router.go`
- 测试:`pkg/admin/server_test.go`(扩展)

- [ ] **步骤 1:编写失败测试**

`pkg/admin/server_test.go` 追加:

```go
func TestAdminAPIKeyCRUD(t *testing.T) {
	s := oss.NewMemStorage()
	router := NewAdminServer(s, nil, "oss").Router()

	// 创建
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/api-keys",
		strings.NewReader(`{"name":"测试Key","quota":-1,"rate_limit":10}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create status = %d; body=%s", w.Code, w.Body.String())
	}
	var created struct {
		Data struct {
			ID      string `json:"id"`
			Key     string `json:"key"`
			KeyHash string `json:"key_hash"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created.Data.Key, "ng-") {
		t.Fatalf("key = %q; want ng- prefix", created.Data.Key)
	}
	// 密文明文在响应中,哈希已入库
	if _, err := s.GetAPIKey(created.Data.KeyHash); err != nil {
		t.Fatalf("key hash not stored: %v", err)
	}

	// 列表
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/api-keys?page=1&size=10", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), created.Data.ID) {
		t.Fatalf("list status=%d body=%s", w.Code, w.Body.String())
	}
	// 列表不返回明文 key_hash
	if strings.Contains(w.Body.String(), "hash-") {
		t.Fatalf("list leaks key hash: %s", w.Body.String())
	}

	// 禁用
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPatch, "/api/api-keys/"+created.Data.ID,
		strings.NewReader(`{"status":"disabled"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("disable status = %d", w.Code)
	}
	key, _ := s.GetAPIKeyByID(created.Data.ID)
	if key.Status != plugin.APIKeyStatusDisabled {
		t.Fatalf("status = %s; want disabled", key.Status)
	}

	// 删除(软删除)
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/api/api-keys/"+created.Data.ID, nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete status = %d", w.Code)
	}
	if _, err := s.GetAPIKeyByID(created.Data.ID); err == nil {
		t.Fatal("key should be soft-deleted")
	}
}

func TestAdminModelConfigCRUD(t *testing.T) {
	s := oss.NewMemStorage()
	router := NewAdminServer(s, nil, "oss").Router()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/models",
		strings.NewReader(`{"name":"gpt-4","provider":"openai","provider_model":"gpt-4o","base_url":"https://api.openai.com","api_key":"sk-test"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create status = %d; body=%s", w.Code, w.Body.String())
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	// 名称唯一冲突
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/models",
		strings.NewReader(`{"name":"gpt-4","provider":"openai","provider_model":"x","base_url":"https://x","api_key":"y"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate name status = %d; want 409", w.Code)
	}

	// 列表
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/models", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "gpt-4") {
		t.Fatalf("list status=%d body=%s", w.Code, w.Body.String())
	}
	// 列表不回显上游 api_key
	if strings.Contains(w.Body.String(), "sk-test") {
		t.Fatalf("list leaks api_key: %s", w.Body.String())
	}
}

func TestAdminAuditQuery(t *testing.T) {
	s := oss.NewMemStorage()
	now := time.Now()
	_ = s.SaveAuditLog(&plugin.AuditLog{
		ID: "a1", RequestID: "r1", ModelName: "gpt-4", ResponseStatus: 200,
		TotalTokens: 15, CreatedAt: now,
	})
	router := NewAdminServer(s, nil, "oss").Router()

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/audit-logs?model_name=gpt-4&page=1&size=10", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "r1") {
		t.Fatalf("query status=%d body=%s", w.Code, w.Body.String())
	}

	// 详情
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/audit-logs/a1", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "gpt-4") {
		t.Fatalf("detail status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestAdminSystem(t *testing.T) {
	s := oss.NewMemStorage()
	router := NewAdminServer(s, nil, "oss").Router()
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/system", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "version") {
		t.Fatalf("body = %s; want version field", w.Body.String())
	}
}
```

- [ ] **步骤 2:运行测试验证失败**

运行:`go test ./pkg/admin/ -run "TestAdmin" -v`
预期:FAIL(路由未注册,404)。

- [ ] **步骤 3:实现统一响应与 API Key CRUD**

`pkg/admin/response.go`:

```go
package admin

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Response 统一响应格式
type Response struct {
	Code    int         `json:"code"`    // 0=成功,非0=错误码
	Message string      `json:"message"` // 成功或错误描述
	Data    interface{} `json:"data,omitempty"`
}

// OK 成功响应
func OK(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, Response{Code: 0, Message: "ok", Data: data})
}

// Error 错误响应(status 为 HTTP 状态码,code 为业务错误码)
func Error(c *gin.Context, status, code int, message string) {
	c.JSON(status, Response{Code: code, Message: message})
}
```

`pkg/admin/api_key.go`:

```go
package admin

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// apiKeyCreateRequest 创建 API Key 请求体(字段校验按 PRD 3.2)
type apiKeyCreateRequest struct {
	Name          string    `json:"name" binding:"required,min=1,max=64"`
	TenantID      string    `json:"tenant_id"`
	Quota         int64     `json:"quota"`          // -1 为无限
	RateLimit     int       `json:"rate_limit"`     // 1-10000,默认 10
	AllowedModels []string  `json:"allowed_models"`
	ExpiresAt     *time.Time `json:"expires_at"`
}

// apiKeyUpdateRequest 更新 Key 请求体(禁用/启用)
type apiKeyUpdateRequest struct {
	Status string `json:"status" binding:"required,oneof=active disabled"`
}

// createAPIKey POST /api/api-keys:创建并返回明文(仅一次)
func (s *AdminServer) createAPIKey(c *gin.Context) {
	var req apiKeyCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	if req.RateLimit < 1 || req.RateLimit > 10000 {
		req.RateLimit = 10
	}
	if req.ExpiresAt != nil && req.ExpiresAt.Before(time.Now()) {
		Error(c, http.StatusBadRequest, 400, "expires_at must be in the future")
		return
	}

	// 生成随机 Key:ng- + 32 hex
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to generate key")
		return
	}
	rawKey := "ng-" + hex.EncodeToString(buf)
	sum := sha256.Sum256([]byte(rawKey))

	now := time.Now()
	key := &plugin.APIKey{
		ID:            uuid.NewString(),
		KeyHash:       hex.EncodeToString(sum[:]),
		KeyPrefix:     rawKey[:11], // ng- + 8 hex
		TenantID:      req.TenantID,
		Name:          req.Name,
		Status:        plugin.APIKeyStatusActive,
		Quota:         req.Quota,
		RateLimit:     req.RateLimit,
		AllowedModels: req.AllowedModels,
		ExpiresAt:     req.ExpiresAt,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.storage.SaveAPIKey(key); err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to save api key")
		return
	}
	OK(c, gin.H{
		"id": key.ID, "key": rawKey, "key_hash": key.KeyHash, "key_prefix": key.KeyPrefix,
		"name": key.Name, "quota": key.Quota, "rate_limit": key.RateLimit,
		"allowed_models": key.AllowedModels, "expires_at": key.ExpiresAt,
	})
}

// listAPIKeys GET /api/api-keys:分页列表(脱敏,不返回哈希)
func (s *AdminServer) listAPIKeys(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "10"))
	tenantID := c.Query("tenant_id")
	keys, total, err := s.storage.ListAPIKeys(tenantID, page, size)
	if err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to list api keys")
		return
	}
	type item struct {
		ID            string     `json:"id"`
		KeyPrefix     string     `json:"key_prefix"`
		Name          string     `json:"name"`
		Status        string     `json:"status"`
		Quota         int64      `json:"quota"`
		UsedQuota     int64      `json:"used_quota"`
		RateLimit     int        `json:"rate_limit"`
		AllowedModels []string   `json:"allowed_models"`
		ExpiresAt     *time.Time `json:"expires_at"`
		CreatedAt     time.Time  `json:"created_at"`
	}
	items := make([]item, 0, len(keys))
	for _, k := range keys {
		items = append(items, item{
			ID: k.ID, KeyPrefix: k.KeyPrefix + "****", Name: k.Name,
			Status: string(k.Status), Quota: k.Quota, UsedQuota: k.UsedQuota,
			RateLimit: k.RateLimit, AllowedModels: k.AllowedModels,
			ExpiresAt: k.ExpiresAt, CreatedAt: k.CreatedAt,
		})
	}
	OK(c, gin.H{"items": items, "total": total, "page": page, "size": size})
}

// updateAPIKey PATCH /api/api-keys/:id:禁用/启用
func (s *AdminServer) updateAPIKey(c *gin.Context) {
	id := c.Param("id")
	var req apiKeyUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	key, err := s.storage.GetAPIKeyByID(id)
	if err != nil {
		Error(c, http.StatusNotFound, 404, "api key not found")
		return
	}
	key.Status = plugin.APIKeyStatus(req.Status)
	key.UpdatedAt = time.Now()
	if err := s.storage.SaveAPIKey(key); err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to update api key")
		return
	}
	OK(c, gin.H{"id": id, "status": key.Status})
}

// deleteAPIKey DELETE /api/api-keys/:id:软删除
func (s *AdminServer) deleteAPIKey(c *gin.Context) {
	id := c.Param("id")
	if err := s.storage.DeleteAPIKey(id); err != nil {
		Error(c, http.StatusNotFound, 404, "api key not found")
		return
	}
	OK(c, gin.H{"id": id, "deleted": true})
}
```

- [ ] **步骤 4:实现模型配置 CRUD**

`pkg/admin/model_config.go`:

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

// modelConfigRequest 模型配置请求体(字段校验按 PRD 3.1)
type modelConfigRequest struct {
	Name          string            `json:"name" binding:"required,min=1,max=64"`
	Provider      string            `json:"provider" binding:"required,oneof=openai tongyi zhipu deepseek"`
	ProviderModel string            `json:"provider_model" binding:"required,min=1,max=128"`
	BaseURL       string            `json:"base_url" binding:"required"`
	APIKey        string            `json:"api_key" binding:"required,min=1,max=256"`
	Timeout       int               `json:"timeout"`        // 1-300,默认 60
	MaxRetries    int               `json:"max_retries"`    // 0-5,默认 2
	RetryInterval int               `json:"retry_interval"` // 1-30,默认 3
	Weight        int               `json:"weight"`         // 1-100,默认 1
	Enabled       *bool             `json:"enabled"`        // 默认 true
	Tags          map[string]string `json:"tags"`
}

func (req *modelConfigRequest) normalize() {
	if req.Timeout < 1 || req.Timeout > 300 {
		req.Timeout = 60
	}
	if req.MaxRetries < 0 || req.MaxRetries > 5 {
		req.MaxRetries = 2
	}
	if req.RetryInterval < 1 || req.RetryInterval > 30 {
		req.RetryInterval = 3
	}
	if req.Weight < 1 || req.Weight > 100 {
		req.Weight = 1
	}
	if req.Enabled == nil {
		t := true
		req.Enabled = &t
	}
	if req.Tags == nil {
		req.Tags = map[string]string{}
	}
}

// createModelConfig POST /api/models
func (s *AdminServer) createModelConfig(c *gin.Context) {
	var req modelConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	req.normalize()
	// 名称唯一校验
	if _, err := s.storage.GetModelConfig(req.Name); err == nil {
		Error(c, http.StatusConflict, 409, "模型名称已存在")
		return
	}
	now := time.Now()
	config := &plugin.ModelConfig{
		ID: uuid.NewString(), ModelName: req.Name, Provider: req.Provider,
		ProviderModel: req.ProviderModel, BaseURL: req.BaseURL, APIKey: req.APIKey,
		Timeout: req.Timeout, MaxRetries: req.MaxRetries, RetryInterval: req.RetryInterval,
		Weight: req.Weight, Enabled: *req.Enabled, Tags: req.Tags,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.storage.SaveModelConfig(config); err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to save model config")
		return
	}
	OK(c, gin.H{"id": config.ID, "name": config.ModelName})
}

// listModelConfigs GET /api/models(不回显上游 api_key)
func (s *AdminServer) listModelConfigs(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "10"))
	configs, total, err := s.storage.ListModelConfigs(page, size)
	if err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to list model configs")
		return
	}
	type item struct {
		ID            string            `json:"id"`
		Name          string            `json:"name"`
		Provider      string            `json:"provider"`
		ProviderModel string            `json:"provider_model"`
		BaseURL       string            `json:"base_url"`
		Timeout       int               `json:"timeout"`
		MaxRetries    int               `json:"max_retries"`
		RetryInterval int               `json:"retry_interval"`
		Weight        int               `json:"weight"`
		Enabled       bool              `json:"enabled"`
		Tags          map[string]string `json:"tags"`
		CreatedAt     time.Time         `json:"created_at"`
	}
	items := make([]item, 0, len(configs))
	for _, cfg := range configs {
		items = append(items, item{
			ID: cfg.ID, Name: cfg.ModelName, Provider: cfg.Provider,
			ProviderModel: cfg.ProviderModel, BaseURL: cfg.BaseURL,
			Timeout: cfg.Timeout, MaxRetries: cfg.MaxRetries, RetryInterval: cfg.RetryInterval,
			Weight: cfg.Weight, Enabled: cfg.Enabled, Tags: cfg.Tags, CreatedAt: cfg.CreatedAt,
		})
	}
	OK(c, gin.H{"items": items, "total": total, "page": page, "size": size})
}

// updateModelConfig PUT /api/models/:id
func (s *AdminServer) updateModelConfig(c *gin.Context) {
	id := c.Param("id")
	existing, err := s.storage.GetModelConfigByID(id)
	if err != nil {
		Error(c, http.StatusNotFound, 404, "model config not found")
		return
	}
	var req modelConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	req.normalize()
	existing.ModelName = req.Name
	existing.Provider = req.Provider
	existing.ProviderModel = req.ProviderModel
	existing.BaseURL = req.BaseURL
	existing.APIKey = req.APIKey
	existing.Timeout = req.Timeout
	existing.MaxRetries = req.MaxRetries
	existing.RetryInterval = req.RetryInterval
	existing.Weight = req.Weight
	existing.Enabled = *req.Enabled
	existing.Tags = req.Tags
	existing.UpdatedAt = time.Now()
	if err := s.storage.SaveModelConfig(existing); err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to update model config")
		return
	}
	OK(c, gin.H{"id": id})
}

// deleteModelConfig DELETE /api/models/:id
func (s *AdminServer) deleteModelConfig(c *gin.Context) {
	id := c.Param("id")
	if err := s.storage.DeleteModelConfig(id); err != nil {
		Error(c, http.StatusNotFound, 404, "model config not found")
		return
	}
	OK(c, gin.H{"id": id, "deleted": true})
}

// testModelConfig POST /api/models/:id/test:测试连接(轻量请求,返回延迟)
func (s *AdminServer) testModelConfig(c *gin.Context) {
	id := c.Param("id")
	config, err := s.storage.GetModelConfigByID(id)
	if err != nil {
		Error(c, http.StatusNotFound, 404, "model config not found")
		return
	}
	url := strings.TrimRight(config.BaseURL, "/") + "/v1/models"
	client := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+config.APIKey)
	start := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		OK(c, gin.H{"ok": false, "latency_ms": latency, "error": err.Error()})
		return
	}
	defer resp.Body.Close()
	OK(c, gin.H{"ok": resp.StatusCode < 500, "latency_ms": latency, "status": resp.StatusCode})
}
```

> 需要 import `strings`。

- [ ] **步骤 5:实现审计查询与系统信息**

`pkg/admin/audit_api.go`:

```go
package admin

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/core"
	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/gin-gonic/gin"
)

// queryAuditLogs GET /api/audit-logs:分页查询(过滤参数见 AuditLogFilter)
func (s *AdminServer) queryAuditLogs(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "10"))
	filter := plugin.AuditLogFilter{
		TenantID:  c.Query("tenant_id"),
		APIKeyID:  c.Query("api_key_id"),
		ModelName: c.Query("model_name"),
		RequestID: c.Query("request_id"),
		Status:    parseInt(c.Query("response_status")),
		Keyword:   c.Query("keyword"),
	}
	if v := c.Query("start_time"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			filter.StartTime = &t
		}
	}
	if v := c.Query("end_time"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			filter.EndTime = &t
		}
	}
	if v := c.Query("is_stream"); v == "true" {
		t := true
		filter.IsStream = &t
	} else if v == "false" {
		f := false
		filter.IsStream = &f
	}
	logs, total, err := s.storage.QueryAuditLogs(filter, page, size)
	if err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to query audit logs")
		return
	}
	OK(c, gin.H{"items": logs, "total": total, "page": page, "size": size})
}

// getAuditLog GET /api/audit-logs/:id:详情(含分片重组)
func (s *AdminServer) getAuditLog(c *gin.Context) {
	id := c.Param("id")
	logs, total, err := s.storage.QueryAuditLogs(plugin.AuditLogFilter{RequestID: id}, 1, 1)
	if err != nil || total == 0 {
		Error(c, http.StatusNotFound, 404, "audit log not found")
		return
	}
	log := logs[0]
	resp := gin.H{
		"id": log.ID, "request_id": log.RequestID, "tenant_id": log.TenantID,
		"model_name": log.ModelName, "provider": log.Provider,
		"request_body": log.RequestBody, "response_body": log.ResponseBody,
		"response_status": log.ResponseStatus, "sse_chunks": log.SSEChunks,
		"prompt_tokens": log.PromptTokens, "completion_tokens": log.CompletionTokens,
		"total_tokens": log.TotalTokens, "duration_ms": log.Duration,
		"is_stream": log.IsStream, "disconnected": log.Disconnected,
		"disconnect_reason": log.DisconnectReason, "created_at": log.CreatedAt,
	}
	if log.IsStream {
		reassembled, _ := core.NewStreamReassembler().Reassemble(log.SSEChunks)
		resp["reassembled"] = reassembled
	}
	OK(c, resp)
}

// exportAuditLogs GET /api/audit-logs/export?format=csv|json
func (s *AdminServer) exportAuditLogs(c *gin.Context) {
	format := c.DefaultQuery("format", "json")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "1000"))
	filter := plugin.AuditLogFilter{Keyword: c.Query("keyword")}
	logs, _, err := s.storage.QueryAuditLogs(filter, page, size)
	if err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to export audit logs")
		return
	}
	if format == "csv" {
		c.Header("Content-Type", "text/csv")
		c.Header("Content-Disposition", "attachment; filename=audit-logs.csv")
		cw := csv.NewWriter(c.Writer)
		_ = cw.Write([]string{"id", "request_id", "tenant_id", "model_name", "response_status", "total_tokens", "duration_ms", "is_stream", "created_at"})
		for _, l := range logs {
			_ = cw.Write([]string{
				l.ID, l.RequestID, l.TenantID, l.ModelName,
				strconv.Itoa(l.ResponseStatus), strconv.Itoa(l.TotalTokens),
				strconv.FormatInt(l.Duration, 10), strconv.FormatBool(l.IsStream),
				l.CreatedAt.Format(time.RFC3339),
			})
		}
		cw.Flush()
		return
	}
	OK(c, gin.H{"items": logs})
}

func parseInt(v string) int {
	n, _ := strconv.Atoi(v)
	return n
}
```

`pkg/admin/system.go`:

```go
package admin

import (
	"net/http"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/core"
	"github.com/gin-gonic/gin"
)

// systemInfo GET /api/system:版本、DB 状态、审计与限流状态
func (s *AdminServer) systemInfo(c *gin.Context) {
	uptime := time.Since(s.startedAt).Round(time.Second).String()
	dbStatus := "ok"
	if err := s.storage.Ping(); err != nil {
		dbStatus = "error: " + err.Error()
	}
	OK(c, gin.H{
		"version":             core.Version,
		"build_time":          core.BuildTime,
		"git_commit":          core.GitCommit,
		"edition":             s.edition,
		"uptime":              uptime,
		"db_status":           dbStatus,
		"audit_queue_status":  gin.H{"status": "ok"},
		"rate_limiter_status": gin.H{"status": "ok"},
	})
}
```

`pkg/admin/server.go` 修改:增加 edition 与 startedAt 字段,注册路由:

```go
// AdminServer 管理后台（Gin）:低并发短连接,提供 CRUD 接口、配置管理、日志查询、授权校验
type AdminServer struct {
	storage   plugin.StoragePlugin
	logger    *zap.Logger
	engine    *gin.Engine
	edition   string
	startedAt time.Time
}

// NewAdminServer 创建管理后台
func NewAdminServer(storage plugin.StoragePlugin, logger *zap.Logger, edition string) *AdminServer {
	gin.SetMode(gin.ReleaseMode)
	s := &AdminServer{storage: storage, logger: logger, edition: edition, startedAt: time.Now()}
	s.engine = gin.New()
	s.engine.Use(gin.Recovery(), CORS())
	s.registerRoutes(s.engine)
	return s
}
```

> 需 `time` import;`cmd/gateway/main.go` 调用处改为 `admin.NewAdminServer(storage, logger, edition)`(edition 变量已由 factory 文件定义)。

- [ ] **步骤 6:注册路由**

`pkg/admin/router.go` 的 registerRoutes 扩展:

```go
// registerRoutes 注册路由
func (s *AdminServer) registerRoutes(r *gin.Engine) {
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	api := r.Group("/api")
	{
		api.GET("/ping", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"message": "pong"})
		})

		// API Key 管理
		api.POST("/api-keys", s.createAPIKey)
		api.GET("/api-keys", s.listAPIKeys)
		api.PATCH("/api-keys/:id", s.updateAPIKey)
		api.DELETE("/api-keys/:id", s.deleteAPIKey)

		// 模型配置
		api.POST("/models", s.createModelConfig)
		api.GET("/models", s.listModelConfigs)
		api.PUT("/models/:id", s.updateModelConfig)
		api.DELETE("/models/:id", s.deleteModelConfig)
		api.POST("/models/:id/test", s.testModelConfig)

		// 审计日志
		api.GET("/audit-logs", s.queryAuditLogs)
		api.GET("/audit-logs/export", s.exportAuditLogs)
		api.GET("/audit-logs/:id", s.getAuditLog)

		// 系统信息
		api.GET("/system", s.systemInfo)
	}
}
```

> 路由顺序注意:gin 中 `/audit-logs/export` 需在 `/audit-logs/:id` 之前注册,否则 `:id` 会捕获 export。按上面顺序(export 在前)已正确。

- [ ] **步骤 7:运行测试验证通过**

运行:`go test ./pkg/admin/ -run "TestAdmin" -v && go test ./... && go build ./...`
预期:PASS;全量测试与编译通过(server_test.go 既有测试的 NewAdminServer 调用需同步加第 3 个参数 `"oss"`)。

- [ ] **步骤 8:Commit**

```bash
git add pkg/admin/ cmd/gateway/main.go
git commit -m "feat(admin): 管理后台 CRUD(API Key/模型配置/审计查询导出/系统信息)"
```

---

### 任务 17:端到端集成测试与启动验证

**文件:**
- 创建:`pkg/core/e2e_test.go`

- [ ] **步骤 1:编写端到端测试**

`pkg/core/e2e_test.go`(完整链路:SQLite 存储 + mock 上游 + 管理后台配置 + 代理调用 + 审计查询):

```go
package core

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/adapter"
	"github.com/druidcaesa/neuralgate/pkg/admin"
	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
)

// TestEndToEnd 全链路:SQLite 存储 + mock 上游 + Admin 配置模型/Key + 代理调用 + 审计查询
func TestEndToEnd(t *testing.T) {
	// 1. mock 上游
	upstream := newMockUpstream(t)
	defer upstream.Close()

	// 2. SQLite 存储(临时文件)
	storage := oss.NewSQLStorage()
	if err := storage.Init(map[string]interface{}{
		"driver": "sqlite", "dsn": t.TempDir() + "/e2e.db", "encrypt_key": "e2e-key",
	}); err != nil {
		t.Fatalf("storage init: %v", err)
	}
	defer storage.Close()

	// 3. 组件装配(与 cmd/gateway/main.go 一致)
	registry := adapter.NewAdapterRegistry()
	registry.Register(adapter.NewOpenAIAdapter())
	registry.Register(adapter.NewTongyiAdapter())
	registry.Register(adapter.NewZhipuAdapter())
	registry.Register(adapter.NewDeepSeekAdapter())
	limiter := oss.NewMemRateLimiter()
	_ = limiter.Init(map[string]interface{}{"default_rps": 100, "default_tpm": 100000})
	auditor := oss.NewSimpleAuditor(storage)
	pc := NewProxyCore(NewPipeline(storage, limiter, auditor, registry), registry)
	proxyHandler := pc.Handler()
	adminRouter := admin.NewAdminServer(storage, nil, "oss").Router()

	// 4. 通过管理后台创建模型配置(指向 mock 上游)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/models",
		strings.NewReader(`{"name":"gpt-4","provider":"openai","provider_model":"gpt-4o","base_url":"`+upstream.URL+`","api_key":"sk-e2e"}`))
	req.Header.Set("Content-Type", "application/json")
	adminRouter.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("admin create model: %d %s", w.Code, w.Body.String())
	}

	// 5. 创建 API Key
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/api-keys",
		strings.NewReader(`{"name":"e2e-key","quota":-1}`))
	req.Header.Set("Content-Type", "application/json")
	adminRouter.ServeHTTP(w, req)
	var created struct {
		Data struct {
			Key string `json:"key"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || !strings.HasPrefix(created.Data.Key, "ng-") {
		t.Fatalf("admin create key: %s", w.Body.String())
	}

	// 6. 通过代理服务调用(非流式)
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer "+created.Data.Key)
	req.Header.Set("Content-Type", "application/json")
	proxyHandler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("proxy chat: %d %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["object"] != "chat.completion" || !strings.Contains(w.Body.String(), "hello from upstream") {
		t.Fatalf("proxy response: %s", w.Body.String())
	}

	// 7. 审计查询:管理后台可查到该请求
	w = httptest.NewRecorder()
	adminRouter.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/audit-logs?model_name=gpt-4", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("audit query: %d", w.Code)
	}
	var audit struct {
		Data struct {
			Total int `json:"total"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &audit)
	if audit.Data.Total < 1 {
		t.Fatalf("audit total = %d; want >=1, body=%s", audit.Data.Total, w.Body.String())
	}

	// 8. /v1/models 列表可见模型
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+created.Data.Key)
	proxyHandler.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "gpt-4") {
		t.Fatalf("models list: %d %s", w.Code, w.Body.String())
	}
}
```

> 移除文件末尾两个 `var _ =` 占位;如 bytes/io 未使用则删 import。

- [ ] **步骤 2:运行测试验证失败**

运行:`go test ./pkg/core/ -run TestEndToEnd -v`
预期:首次运行可能因接线遗漏 FAIL(如 admin 路由冲突、审计查询过滤),修复直到 PASS。

- [ ] **步骤 3:修复直至通过**

预期修复点(如出现):
- Admin 路由顺序:`/audit-logs/export` 必须在 `/:id` 前
- GET 请求(如 `/v1/models`)无 body,RouteMatchMiddleware 解析 body 会读到空→400。**修复**:RouteMatchMiddleware 对 GET 请求跳过 body 解析与 model 校验,直接放行(路由匹配仅对 POST 核心代理/透传端点生效):

```go
	// GET 请求(如 /v1/models)无需 body 解析与模型路由
	if r.Method == http.MethodGet {
		next.ServeHTTP(w, r)
		return
	}
	// 读取请求体(上限 1MB),缓存后恢复
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil { ... }
	r.Body = io.NopCloser(bytes.NewReader(body))
	rc.RequestBody = body
	// 解析 model 字段 ...
```

- [ ] **步骤 4:运行全量测试与构建**

运行:`go test ./... && go build ./...`
预期:全部 PASS,编译成功。

- [ ] **步骤 5:手动启动验证**

运行:
```bash
go run ./cmd/gateway -config config.yaml &
sleep 2
curl -s http://127.0.0.1:8081/healthz
curl -s http://127.0.0.1:8080/healthz
# 创建模型与 Key,调用代理(见 e2e 测试)
kill %1
```
预期:双服务启动,健康检查返回 `{"status":"ok"}`;SQLite 文件 `neuralgate.db` 生成。

- [ ] **步骤 6:Commit**

```bash
git add pkg/core/e2e_test.go
git commit -m "test(core): 端到端集成测试(SQLite+Admin+代理转发+审计查询)"
```

---

## 自检记录

**规格覆盖度:**

| 规格章节 | 对应任务 |
|----------|----------|
| §2 存储层(表结构/加密/软删除/工厂分发/配置) | 任务 1-7 |
| §3 中间件(鉴权/路由/限流) | 任务 10-12 |
| §4 代理内核(端点分类/转发/限流Header/用量回补) | 任务 13-14 |
| §5 SSE 审计接线(分片捕获/重组/断连) | 任务 15 |
| §6 适配器(OpenAI/DeepSeek 解析,通义/智谱转换) | 任务 8-9 |
| §7 Admin CRUD(Key/模型/审计/系统) | 任务 16 |
| §8 测试策略(存储/中间件/内核/适配器/Admin/E2E) | 各任务 + 任务 17 |
| §9 文件清单 | 全部任务 |
| §10 决策(默认 sqlite/加密/软删除/OSS 无鉴权) | 任务 1/2/6/16 |

**占位符扫描:** 所有步骤含完整代码与命令,无"待定/TODO";任务间引用类型与签名一致(NewPipeline 签名变更在任务 11 统一传播)。

**类型一致性:** RouteMatchMiddleware 签名变更(加 registry)在任务 11 同步更新 pipeline.go/proxy.go/main.go;NewAdminServer 签名变更(加 edition)在任务 16 同步更新 main.go 与既有测试。
