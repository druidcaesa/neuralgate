# Enterprise E2 审计日志外推 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 企业版将运行期新增审计日志准实时外推到 SIEM(HTTP JSON) 或 Syslog(RFC5424)，失败重试+有界缓冲，由 LicenseGate 门控。

**Architecture:** `TailExporter` 后台循环按时间游标从存储尾随拉取新增审计日志，批量交给可插拔的 `ExportTarget`（SIEM/Syslog）；推送失败整批入 10000 条环形缓冲按 1s→30s 指数退避补投；`Init(map)` 即启动、`Close` 收尾，不改 `plugin.LogExporter` 接口。OSS 版 `CreateExporter` 保持 nil，行为零变化。

**Tech Stack:** Go 标准库（net/http、net、encoding/json），零新增第三方依赖。

## Global Constraints

- 所有 `.go` 文件带 Apache-2.0 许可证头（Copyright 2026 FanYaNan）
- 注释只写方案，不引用阶段编号/计划文档
- module `github.com/druidcaesa/neuralgate`（go 1.26.5），零新增依赖
- 双编译矩阵全绿：`go build/test -race -tags oss ./...` 与 `-tags enterprise ./...`
- enterprise 包新文件一律带 `//go:build enterprise`
- 中文注释/日志/错误消息；commit 格式 `feat(scope): 中文描述`
- `AuditLog` 无 json 标签：外推 JSON 字段名为 Go 导出名（ID/RequestID/…，与 webui 现消费的大写字段一致），这是预期行为不要"修复"
- 存储审计查询固定 `CreatedAt DESC` 序、`StartTime` 为闭区间(`>=`)——游标逻辑依赖此事实

---

### Task 1: ExportTarget 接口与 SIEMTarget

**Files:**
- Create: `pkg/plugin/enterprise/export_target.go`
- Test: `pkg/plugin/enterprise/export_target_test.go`

**Interfaces:**
- Consumes: `plugin.AuditLog`（既有）
- Produces:
  ```go
  type ExportTarget interface {
      Send(logs []*plugin.AuditLog) error
      TestConnection() error
      Close() error
  }
  func NewExportTarget(exportType, endpoint, apiKey string) (ExportTarget, error) // type: siem/syslog
  func NewSIEMTarget(endpoint, apiKey string) *SIEMTarget
  ```

- [x] **Step 1: 写失败测试**

```go
// 标准头 + //go:build enterprise + package enterprise
// imports: encoding/json, io, net/http, net/http/httptest, strings, testing
//          github.com/druidcaesa/neuralgate/pkg/plugin

func sampleLogs(n int) []*plugin.AuditLog {
	logs := make([]*plugin.AuditLog, n)
	for i := range logs {
		logs[i] = &plugin.AuditLog{
			ID: fmt.Sprintf("id-%d", i), RequestID: fmt.Sprintf("req-%d", i),
			ModelName: "gpt-x", RequestMethod: "POST", RequestPath: "/v1/chat",
			ResponseStatus: 200, TotalTokens: 42, ClientIP: "10.0.0.1",
			CreatedAt: time.Now().UTC(),
		}
	}
	return logs
}

func TestNewExportTargetUnknownType(t *testing.T) {
	if _, err := NewExportTarget("kafka", "x", ""); err == nil {
		t.Fatal("未知类型应报错")
	}
}

func TestSIEMSendPostsJSONArray(t *testing.T) {
	var gotBody []byte
	var gotAuth, gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	target := NewSIEMTarget(srv.URL, "secret-key")
	logs := sampleLogs(2)
	if err := target.Send(logs); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotAuth != "Bearer secret-key" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if !strings.HasPrefix(gotCT, "application/json") {
		t.Errorf("Content-Type = %q", gotCT)
	}
	var parsed []map[string]any
	if err := json.Unmarshal(gotBody, &parsed); err != nil {
		t.Fatalf("body 不是 JSON 数组: %v", err)
	}
	if len(parsed) != 2 || parsed[0]["RequestID"] != "req-0" {
		t.Errorf("数组内容不符: %s", gotBody)
	}
}

func TestSIEMSendNon2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if err := NewSIEMTarget(srv.URL, "").Send(sampleLogs(1)); err == nil {
		t.Fatal("500 应返回 error")
	}
}

func TestSIEMTestConnection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	if err := NewSIEMTarget(srv.URL, "").TestConnection(); err != nil {
		t.Fatalf("可达端点应通过: %v", err)
	}
	if err := NewSIEMTarget("http://127.0.0.1:1/nope", "").TestConnection(); err == nil {
		t.Fatal("不可达端点应报错")
	}
}
```

注意：测试文件需要 `fmt` 与 `time` import（sampleLogs 用到）。

- [x] **Step 2: 运行确认失败**

Run: `go test -tags enterprise ./pkg/plugin/enterprise/ -run 'TestSIEM|TestNewExportTarget'`
Expected: FAIL `undefined: NewExportTarget`

- [x] **Step 3: 最小实现**

```go
// export_target.go：标准许可证头 + //go:build enterprise + package enterprise
// Package 内职责：定义外推目标接口并实现 SIEM/Syslog 两种目标

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// ExportTarget 外推目标：把一批审计日志推送到外部系统
type ExportTarget interface {
	// Send 批量推送；返回 error 表示本批失败需重试
	Send(logs []*plugin.AuditLog) error
	// TestConnection 连通性自检
	TestConnection() error
	// Close 释放底层资源
	Close() error
}

// NewExportTarget 按类型构造外推目标（siem/syslog）
func NewExportTarget(exportType, endpoint, apiKey string) (ExportTarget, error) {
	switch strings.ToLower(exportType) {
	case "siem":
		return NewSIEMTarget(endpoint, apiKey), nil
	case "syslog":
		return NewSyslogTarget(endpoint)
	default:
		return nil, fmt.Errorf("不支持的外推类型: %s", exportType)
	}
}

// ===== SIEM =====

// SIEMTarget SIEM 外推：HTTP POST 全量 JSON 数组，Bearer 认证
type SIEMTarget struct {
	endpoint string
	apiKey   string
	client   *http.Client
}

// NewSIEMTarget 创建 SIEM 目标
func NewSIEMTarget(endpoint, apiKey string) *SIEMTarget {
	return &SIEMTarget{endpoint: endpoint, apiKey: apiKey, client: &http.Client{Timeout: 10 * time.Second}}
}

// Send 整批单次 POST；2xx 视为成功
func (t *SIEMTarget) Send(logs []*plugin.AuditLog) error {
	body, err := json.Marshal(logs)
	if err != nil {
		return fmt.Errorf("序列化审计日志失败: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, t.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if t.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+t.apiKey)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("推送 SIEM 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("SIEM 返回非 2xx: %d", resp.StatusCode)
	}
	return nil
}

// TestConnection 以空批次探测连通性（只关心传输层可达）
func (t *SIEMTarget) TestConnection() error {
	req, err := http.NewRequest(http.MethodPost, t.endpoint, bytes.NewReader([]byte("[]")))
	if err != nil {
		return fmt.Errorf("构造请求失败: %w", err)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("SIEM 不可达: %w", err)
	}
	resp.Body.Close()
	return nil
}

// Close HTTP 客户端无需释放
func (t *SIEMTarget) Close() error { return nil }
```

- [x] **Step 4: 运行确认通过**

Run: `go test -race -tags enterprise ./pkg/plugin/enterprise/ -run 'TestSIEM|TestNewExportTarget'`
Expected: PASS

- [x] **Step 5: Commit**

```bash
git add pkg/plugin/enterprise/export_target.go pkg/plugin/enterprise/export_target_test.go
git commit -m "feat(enterprise): ExportTarget 接口与 SIEM HTTP 外推目标"
```

---

### Task 2: SyslogTarget（RFC5424，UDP/TCP）

**Files:**
- Modify: `pkg/plugin/enterprise/export_target.go`（追加 Syslog 部分）
- Test: `pkg/plugin/enterprise/export_target_test.go`（追加用例）

**Interfaces:**
- Consumes: Task 1 的 `ExportTarget`
- Produces:
  ```go
  func NewSyslogTarget(endpoint string) (*SyslogTarget, error) // endpoint: udp://host:port | tcp://host:port | host:port(默认udp)
  ```

- [x] **Step 1: 写失败测试（追加到 export_target_test.go）**

```go
// 追加 imports: bufio, net, strconv, strings(已), time(已)

func TestSyslogUDPSend(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	go func() {
		buf := make([]byte, 65536)
		for {
			_, _, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
		}
	}()
	// 实际断言走下方回环读取：改用独立 goroutine 回读两条
	...
}
```

完整测试（替换上面骨架的歧义——直接采用以下最终版本）：

```go
func dialableUDPAddr(t *testing.T) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	return pc.LocalAddr().String()
}

func TestSyslogUDPSendRFC5424(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	frames := make(chan string, 4)
	go func() {
		buf := make([]byte, 65536)
		for {
			n, _, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			frames <- string(buf[:n])
		}
	}()

	target, err := NewSyslogTarget("udp://" + pc.LocalAddr().String())
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	ok := sampleLogs(1)[0]
	bad := sampleLogs(1)[0]
	bad.ResponseStatus = 502
	if err := target.Send([]*plugin.AuditLog{ok, bad}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	infoFrame := <-frames
	errFrame := <-frames
	if !strings.HasPrefix(infoFrame, "<14>1 ") { // user(1)*8+info(6)=14
		t.Errorf("info 报文 PRI 不符: %q", infoFrame[:12])
	}
	if !strings.Contains(infoFrame, " NeuralGate - req-0 - ") || !strings.Contains(infoFrame, `"RequestID":"req-0"`) {
		t.Errorf("info 报文头/MSG 不符: %s", infoFrame)
	}
	if !strings.HasPrefix(errFrame, "<11>1 ") { // user(1)*8+err(3)=11
		t.Errorf("5xx 应映射 err severity: %q", errFrame[:12])
	}
}

func TestSyslogTCPOctetFraming(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	type received struct {
		raw string
	}
	rec := make(chan received, 2)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		reader := bufio.NewReader(conn)
		for {
			// RFC6587 octet-count 分帧："LEN MSG"
			head, err := reader.ReadString(' ')
			if err != nil {
				return
			}
			n, err := strconv.Atoi(strings.TrimSpace(head))
			if err != nil {
				return
			}
			body := make([]byte, n)
			if _, err := io.ReadFull(reader, body); err != nil {
				return
			}
			rec <- received{raw: string(body)}
		}
	}()

	target, err := NewSyslogTarget("tcp://" + ln.Addr().String())
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	logs := sampleLogs(2)
	if err := target.Send(logs); err != nil {
		t.Fatalf("Send: %v", err)
	}
	frame := <-rec
	if !strings.HasPrefix(frame.raw, "<14>1 ") || !strings.Contains(frame.raw, `"RequestID":"req-0"`) {
		t.Errorf("首帧内容不符: %s", frame.raw)
	}
}

func TestSyslogRejectsBadScheme(t *testing.T) {
	if _, err := NewSyslogTarget("amqp://127.0.0.1:514"); err == nil {
		t.Fatal("非法协议应报错")
	}
}
```

测试文件需追加 imports：`bufio`、`io`、`net`、`strconv`。

- [x] **Step 2: 运行确认失败**

Run: `go test -tags enterprise ./pkg/plugin/enterprise/ -run TestSyslog`
Expected: FAIL `undefined: NewSyslogTarget`

- [x] **Step 3: 实现（追加到 export_target.go）**

```go
// ===== Syslog =====

// SyslogTarget Syslog 外推：RFC5424 报文；TCP 采用 RFC6587 octet-count 分帧并维持常连接
type SyslogTarget struct {
	network string // udp / tcp
	addr    string
	conn    net.Conn // 仅 tcp 使用
}

// NewSyslogTarget 创建 Syslog 目标；endpoint 支持 udp://host:port、tcp://host:port，
// 无前缀默认 udp
func NewSyslogTarget(endpoint string) (*SyslogTarget, error) {
	network, addr := "udp", endpoint
	if i := strings.Index(endpoint, "://"); i >= 0 {
		network = strings.ToLower(endpoint[:i])
		addr = endpoint[i+3:]
	}
	switch network {
	case "udp", "tcp":
	default:
		return nil, fmt.Errorf("不支持的 syslog 协议: %s", network)
	}
	t := &SyslogTarget{network: network, addr: addr}
	if network == "tcp" {
		if err := t.dial(); err != nil {
			return nil, err
		}
	}
	return t, nil
}

func (t *SyslogTarget) dial() error {
	conn, err := net.DialTimeout(t.network, t.addr, 5*time.Second)
	if err != nil {
		return fmt.Errorf("连接 syslog(%s) 失败: %w", t.addr, err)
	}
	t.conn = conn
	return nil
}

// Send 每条日志一帧；TCP 任一帧失败即中断返回，由上层重试整批
func (t *SyslogTarget) Send(logs []*plugin.AuditLog) error {
	for _, log := range logs {
		msg, err := syslogFrame(log)
		if err != nil {
			return err
		}
		if t.network == "tcp" {
			frame := append([]byte(strconv.Itoa(len(msg))+" "), msg...)
			if err := t.tcpWrite(frame); err != nil {
				return err
			}
			continue
		}
		if err := t.udpWrite(msg); err != nil {
			return err
		}
	}
	return nil
}

func (t *SyslogTarget) tcpWrite(frame []byte) error {
	if t.conn == nil {
		if err := t.dial(); err != nil {
			return err
		}
	}
	if _, err := t.conn.Write(frame); err != nil {
		t.conn.Close()
		t.conn = nil // 置空触发下次重连
		return fmt.Errorf("syslog 发送失败: %w", err)
	}
	return nil
}

func (t *SyslogTarget) udpWrite(msg []byte) error {
	conn, err := net.DialTimeout("udp", t.addr, 5*time.Second)
	if err != nil {
		return fmt.Errorf("syslog 不可达: %w", err)
	}
	defer conn.Close()
	if _, err := conn.Write(msg); err != nil {
		return fmt.Errorf("syslog 发送失败: %w", err)
	}
	return nil
}

// syslogFrame 组装 RFC5424 报文：
// <PRI>1 TIMESTAMP HOSTNAME APP-NAME PROCID MSGID STRUCTURED-DATA MSG
// facility=user(1)；severity 按 5xx/断连取 err(3)，否则 info(6)；SD 固定 "-"
func syslogFrame(log *plugin.AuditLog) ([]byte, error) {
	payload, err := json.Marshal(log)
	if err != nil {
		return nil, fmt.Errorf("序列化审计日志失败: %w", err)
	}
	severity := 6
	if log.ResponseStatus >= 500 || log.Disconnected {
		severity = 3
	}
	pri := 1*8 + severity
	header := fmt.Sprintf("<%d>1 %s - NeuralGate NeuralGate - %s - ",
		pri, log.CreatedAt.UTC().Format(time.RFC3339), log.RequestID)
	return append([]byte(header), payload...), nil
}

// TestConnection TCP 探测建连；UDP 探测地址可拨
func (t *SyslogTarget) TestConnection() error {
	conn, err := net.DialTimeout(t.network, t.addr, 5*time.Second)
	if err != nil {
		return fmt.Errorf("syslog 不可达: %w", err)
	}
	conn.Close()
	return nil
}

// Close 关闭 TCP 常连接（UDP 无资源）
func (t *SyslogTarget) Close() error {
	if t.conn != nil {
		err := t.conn.Close()
		t.conn = nil
		return err
	}
	return nil
}
```

- [x] **Step 4: 运行确认通过**

Run: `go test -race -tags enterprise ./pkg/plugin/enterprise/ -run TestSyslog`
Expected: PASS

- [x] **Step 5: Commit**

```bash
git add pkg/plugin/enterprise/
git commit -m "feat(enterprise): Syslog RFC5424 外推目标(UDP/TCP octet-count)"
```

### Task 3: TailExporter（游标拉取 + 有界缓冲 + 退避补投）

**Files:**
- Create: `pkg/plugin/enterprise/export_tail.go`
- Test: `pkg/plugin/enterprise/export_tail_test.go`

**Interfaces:**
- Consumes: Task 1 的 `ExportTarget`/`NewExportTarget`；`plugin.StoragePlugin.QueryAuditLogs(filter,page,size)`（CreatedAt **DESC** 序、StartTime 闭区间）
- Produces:
  ```go
  func NewTailExporter(storage plugin.StoragePlugin) *TailExporter  // 实现 plugin.LogExporter 全部方法
  // Init(map{"type","endpoint","api_key","batch_size"(int),"flush_interval"(time.Duration),"logger"(*zap.Logger)}) error —— 成功即启动后台循环
  // Close() error —— 停循环→最终拉取→无视退避清空缓冲→target.Close()；未 Init 时安全空操作
  ```

- [x] **Step 1: 写失败测试**

```go
// 标准头 + //go:build enterprise + package enterprise
// imports: errors, sync, testing, time
//          github.com/druidcaesa/neuralgate/pkg/plugin
//          github.com/druidcaesa/neuralgate/pkg/plugin/oss

// collectingTarget 测试用收集目标：可控前 N 次失败
type collectingTarget struct {
	mu       sync.Mutex
	received []*plugin.AuditLog
	failNext int
	sends    int
}

func (c *collectingTarget) Send(logs []*plugin.AuditLog) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sends++
	if c.failNext > 0 {
		c.failNext--
		return errors.New("模拟故障")
	}
	c.received = append(c.received, logs...)
	return nil
}
func (c *collectingTarget) TestConnection() error { return nil }
func (c *collectingTarget) Close() error          { return nil }

func (c *collectingTarget) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.received)
}

// newManualExporter 构造不经 Init 的手工装配导出器(不起后台循环,测试直接调 drain)
func newManualExporter(target *collectingTarget) *TailExporter {
	e := NewTailExporter(oss.NewMemStorage())
	e.target = target
	e.cursor = time.Now().Add(-time.Hour)
	return e
}

func seedLogs(t *testing.T, storage plugin.StoragePlugin, ids []string, created time.Time) {
	t.Helper()
	for i, id := range ids {
		if err := storage.SaveAuditLog(&plugin.AuditLog{
			ID: id, RequestID: id, ModelName: "gpt-x",
			ResponseStatus: 200, TotalTokens: i,
			CreatedAt: created,
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPullSendRoundTripNoDuplication(t *testing.T) {
	storage := oss.NewMemStorage()
	base := time.Now().Add(-time.Minute)
	seedLogs(t, storage, []string{"a", "b", "c"}, base)

	e := NewTailExporter(storage)
	e.cursor = base.Add(-time.Minute)
	target := &collectingTarget{}
	e.target = target

	e.drain()
	if got := target.count(); got != 3 {
		t.Fatalf("首轮应推出 3 条, got %d", got)
	}
	second := &collectingTarget{}
	e.target = second
	e.drain()
	if second.count() != 0 {
		t.Fatalf("游标推进后不应重复拉取, got %d", second.count())
	}
}

func TestSameTimestampDedupedBySeen(t *testing.T) {
	storage := oss.NewMemStorage()
	same := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC) // 同一时刻三条
	seedLogs(t, storage, []string{"x1", "x2", "x3"}, same)

	e := NewTailExporter(storage)
	e.cursor = same
	target := &collectingTarget{}
	e.target = target

	e.drain() // StartTime=cursor 闭区间会重复带回同刻日志,靠 seen 去重
	e.drain()
	if got := target.count(); got != 3 {
		t.Fatalf("同刻日志应恰好推出一次, got %d", got)
	}
}

func TestFailureBuffersThenRetries(t *testing.T) {
	storage := oss.NewMemStorage()
	seedLogs(t, storage, []string{"r1"}, time.Now().Add(-time.Minute))

	e := NewTailExporter(storage)
	e.cursor = time.Now().Add(-time.Hour)
	target := &collectingTarget{failNext: 1}
	e.target = target

	e.drain() // Send 失败 → 入缓冲
	if got := target.count(); got != 0 {
		t.Fatalf("失败时不应送达, got %d", got)
	}
	e.mu.Lock()
	e.nextAttempt = time.Time{} // 测试跳过退避等待
	e.mu.Unlock()
	e.drain()
	if got := target.count(); got != 1 {
		t.Fatalf("恢复后应补投, got %d", got)
	}
}

func TestBackoffDelayCapsAt30s(t *testing.T) {
	cases := map[int]time.Duration{1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second, 20: maxBackoff}
	for failures, want := range cases {
		if got := backoffDelay(failures); got != want {
			t.Errorf("backoffDelay(%d) = %v, want %v", failures, got, want)
		}
	}
}

func TestBufferOverflowDropsOldest(t *testing.T) {
	e := NewTailExporter(oss.NewMemStorage())
	e.bufferLimit = 3
	for i := 0; i < 5; i++ {
		e.Export(&plugin.AuditLog{ID: fmt.Sprintf("id-%d", i), RequestID: fmt.Sprintf("id-%d", i)})
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.buffer) != 3 {
		t.Fatalf("缓冲应为 3, got %d", len(e.buffer))
	}
	if e.buffer[0].ID != "id-2" || e.buffer[2].ID != "id-4" {
		t.Errorf("应丢弃最旧保留最新: 首=%s 尾=%s", e.buffer[0].ID, e.buffer[2].ID)
	}
}

func TestCloseDrainsRemainingAndSafeOnUninit(t *testing.T) {
	// 未 Init 直接 Close 安全
	NewTailExporter(oss.NewMemStorage()).Close()

	storage := oss.NewMemStorage()
	seedLogs(t, storage, []string{"z1"}, time.Now().Add(-time.Minute))
	e := NewTailExporter(storage)
	e.cursor = time.Now().Add(-time.Hour)
	target := &collectingTarget{failNext: 1}
	e.target = target
	e.drain() // 缓冲 1 条未送
	target.failNext = 0
	if err := e.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := target.count(); got != 1 {
		t.Fatalf("Close 应无视退避清空缓冲, got %d", got)
	}
}
```

测试文件需追加 import：`fmt`。

- [x] **Step 2: 运行确认失败**

Run: `go test -tags enterprise ./pkg/plugin/enterprise/ -run 'TestPull|TestSame|TestFailure|TestBackoff|TestBuffer|TestClose'`
Expected: FAIL `undefined: NewTailExporter`

- [x] **Step 3: 最小实现**

```go
// export_tail.go：标准许可证头 + //go:build enterprise + package enterprise

import (
	"errors"
	"sync"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"go.uber.org/zap"
)

const (
	exportBufferLimit      = 10000            // 待重试缓冲上限
	pullPageSize           = 1000             // 游标查询单页上限
	baseBackoff            = time.Second      // 退避基准
	maxBackoff             = 30 * time.Second // 退避封顶
	cursorOverlap          = time.Second      // 游标向前重叠窗口(配合 seen 去重)
	defaultExportBatchSize = 50
	defaultFlushInterval   = 10 * time.Second
)

// backoffDelay 第 failures 次连续失败后的重试间隔：1s 起倍增，30s 封顶
func backoffDelay(failures int) time.Duration {
	if failures <= 0 {
		return 0
	}
	d := baseBackoff
	for i := 1; i < failures; i++ {
		d *= 2
		if d >= maxBackoff {
			return maxBackoff
		}
	}
	return d
}

// TailExporter 存储尾随外推器：时间游标拉取新增审计日志批量推送，
// 失败整批入有界缓冲按指数退避补投。实现 plugin.LogExporter：
// Init 配置并启动后台循环，Close 收尾；Export/BatchExport 为兼容接口直入缓冲。
// target/cursor/seen/buffer 等状态均由 mu 保护；target.Send 必须在锁外调用。
type TailExporter struct {
type TailExporter struct {
	storage plugin.StoragePlugin
	target  ExportTarget
	logger  *zap.Logger

	batchSize     int
	flushInterval time.Duration
	bufferLimit   int

	mu          sync.Mutex
	cursor      time.Time
	seen        map[string]time.Time
	buffer      []*plugin.AuditLog
	failures    int
	nextAttempt time.Time
	stopCh      chan struct{}
	doneCh      chan struct{}
	stopped     bool
}

// NewTailExporter 创建未初始化的外推器；须经 Init 后才开始工作
func NewTailExporter(storage plugin.StoragePlugin) *TailExporter {
	return &TailExporter{
		storage:       storage,
		logger:        zap.NewNop(),
		batchSize:     defaultExportBatchSize,
		flushInterval: defaultFlushInterval,
		bufferLimit:   exportBufferLimit,
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
}

// Init 解析配置、构造目标并启动后台循环；cursor 从当前时刻起（只外推运行期新增）
func (e *TailExporter) Init(config map[string]interface{}) error {
	e.mu.Lock()
	if e.target != nil || e.stopped {
		e.mu.Unlock()
		return errors.New("外推器已初始化或已关闭")
	}
	e.mu.Unlock()

	endpoint, _ := config["endpoint"].(string)
	if endpoint == "" {
		return errors.New("外推 endpoint 不能为空")
	}
	exportType, _ := config["type"].(string)
	apiKey, _ := config["api_key"].(string)
	target, err := NewExportTarget(exportType, endpoint, apiKey)
	if err != nil {
		return err
	}
	if v, ok := config["batch_size"].(int); ok && v > 0 {
		e.batchSize = v
	}
	if v, ok := config["flush_interval"].(time.Duration); ok && v > 0 {
		e.flushInterval = v
	}
	if l, ok := config["logger"].(*zap.Logger); ok && l != nil {
		e.logger = l
	}

	e.mu.Lock()
	e.target = target
	e.cursor = time.Now()
	e.seen = make(map[string]time.Time)
	e.stopCh = make(chan struct{})
	e.doneCh = make(chan struct{})
	e.mu.Unlock()

	go e.run()
	e.logger.Info("审计外推器已启动", zap.String("type", exportType), zap.Duration("interval", e.flushInterval))
	return nil
}

func (e *TailExporter) run() {
	defer close(e.doneCh)
	ticker := time.NewTicker(e.flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-e.stopCh:
			return
		case <-ticker.C:
			e.drain()
		}
	}
}

// drain 一轮「拉取入缓冲 → 按退避节奏推送一批」
func (e *TailExporter) drain() {
	e.pullNew()
	e.flushOnce()
}

// pullNew 以游标前移重叠窗查询新增日志；存储固定 DESC 序故倒序遍历得升序；
// seen 去重后入缓冲并推进游标，最后清理窗口外的 seen 记录
func (e *TailExporter) pullNew() {
	e.mu.Lock()
	defer e.mu.Unlock()
	from := e.cursor.Add(-cursorOverlap)
	newest := e.cursor
	for page := 1; ; page++ {
		logs, _, err := e.storage.QueryAuditLogs(plugin.AuditLogFilter{StartTime: &from}, page, pullPageSize)
		if err != nil {
			e.logger.Warn("外推拉取审计日志失败", zap.Error(err))
			return
		}
		for i := len(logs) - 1; i >= 0; i-- {
			l := logs[i]
			if _, dup := e.seen[l.ID]; dup {
				continue
			}
			e.seen[l.ID] = l.CreatedAt
			e.enqueueLocked(l)
			if l.CreatedAt.After(newest) {
				newest = l.CreatedAt
			}
		}
		if len(logs) < pullPageSize {
			break
		}
	}
	e.cursor = newest
	for id, ts := range e.seen {
		if ts.Before(e.cursor.Add(-cursorOverlap)) {
			delete(e.seen, id)
		}
	}
}

// enqueueLocked 入缓冲，满则丢最旧并告警（调用方须持锁）
func (e *TailExporter) enqueueLocked(log *plugin.AuditLog) {
	if len(e.buffer) >= e.bufferLimit {
		dropped := e.buffer[0]
		e.buffer = e.buffer[1:]
		e.logger.Warn("外推缓冲已满，丢弃最旧日志",
			zap.String("dropped_request_id", dropped.RequestID), zap.Int("limit", e.bufferLimit))
	}
	e.buffer = append(e.buffer, log)
}

// flushOnce 推送一批；成功则连续清空至缓冲空或再次失败，失败设置退避
func (e *TailExporter) flushOnce() {
	for {
		e.mu.Lock()
		if len(e.buffer) == 0 || time.Now().Before(e.nextAttempt) {
			e.mu.Unlock()
			return
		}
		n := e.batchSize
		if n > len(e.buffer) {
			n = len(e.buffer)
		}
		batch := make([]*plugin.AuditLog, n)
		copy(batch, e.buffer[:n])
		target := e.target
		e.mu.Unlock()

		err := target.Send(batch)
		e.mu.Lock()
		if err != nil {
			e.failures++
			e.nextAttempt = time.Now().Add(backoffDelay(e.failures))
			e.logger.Warn("外推推送失败，进入退避重试",
				zap.Error(err), zap.Int("buffered", len(e.buffer)), zap.Duration("delay", backoffDelay(e.failures)))
			e.mu.Unlock()
			return
		}
		e.buffer = e.buffer[n:]
		e.failures = 0
		e.mu.Unlock()
	}
}

// Close 停止循环 → 最终一轮拉取（兜住 auditor.Shutdown 落库的尾部日志）→
// 无视退避尽力清空缓冲 → 关闭目标。未 Init 过为安全空操作。
func (e *TailExporter) Close() error {
	e.mu.Lock()
	if e.stopped {
		e.mu.Unlock()
		return nil
	}
	e.stopped = true
	started := e.target != nil
	close(e.stopCh)
	interval := e.flushInterval
	e.mu.Unlock()

	if !started {
		return nil
	}
	select {
	case <-e.doneCh:
	case <-time.After(3 * interval):
	}
	e.pullNew()
	e.flushAll()
	e.mu.Lock()
	target := e.target
	e.mu.Unlock()
	return target.Close()
}

// flushAll 无视退避连续推送直到缓冲清空或失败
func (e *TailExporter) flushAll() {
	for {
		e.mu.Lock()
		if len(e.buffer) == 0 {
			e.mu.Unlock()
			return
		}
		n := e.batchSize
		if n > len(e.buffer) {
			n = len(e.buffer)
		}
		batch := make([]*plugin.AuditLog, n)
		copy(batch, e.buffer[:n])
		target := e.target
		e.mu.Unlock()

		if err := target.Send(batch); err != nil {
			e.logger.Warn("关闭前外推未完成", zap.Error(err))
			return
		}
		e.mu.Lock()
		e.buffer = e.buffer[n:]
		e.mu.Unlock()
	}
}

// ===== plugin.LogExporter 兼容方法 =====

// Export 单条直入缓冲（主路径为存储尾随拉取）
func (e *TailExporter) Export(log *plugin.AuditLog) error {
	if log == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.enqueueLocked(log)
	return nil
}

// BatchExport 批量直入缓冲
func (e *TailExporter) BatchExport(logs []*plugin.AuditLog) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, l := range logs {
		if l != nil {
			e.enqueueLocked(l)
		}
	}
	return nil
}

// TestConnection 透传目标的连通性检查
func (e *TailExporter) TestConnection() error {
	e.mu.Lock()
	target := e.target
	e.mu.Unlock()
	if target == nil {
		return errors.New("外推器尚未初始化")
	}
	return target.TestConnection()
}
```

注意一处实现要点：
- `flushOnce`/`flushAll` 中 `copy(batch, e.buffer[:n])` 必须在解锁前完成快照；`e.buffer = e.buffer[n:]` 只能由本方法在重新持锁后执行。

- [x] **Step 4: 运行确认通过**

Run: `go test -race -tags enterprise ./pkg/plugin/enterprise/`
Expected: PASS（Task 1/2 用例一并回归）

- [x] **Step 5: Commit**

```bash
git add pkg/plugin/enterprise/export_tail.go pkg/plugin/enterprise/export_tail_test.go
git commit -m "feat(enterprise): TailExporter 游标拉取与有界缓冲退避补投"
```

---

### Task 4: 工厂接线 + 门控启动 + 配置开关

**Files:**
- Modify: `pkg/config/config.go`（ExportConfig 增加 Enabled）
- Modify: `pkg/plugin/enterprise/factory.go`（CreateExporter 返回真实实现）
- Modify: `cmd/gateway/main.go`（步骤 8 外推启动；shutdown 序列插入 exporter.Close）
- Create: `cmd/gateway/main_test.go`（门控决策纯函数测试）
- Modify: `config.yaml`（export 段注释）

**Interfaces:**
- Consumes: Task 3 `NewTailExporter`；E1 的 `core.LicenseGate` 与 `license.FeatureAuditStream`
- Produces:
  ```go
  // cmd/gateway
  func shouldStartExport(gate core.LicenseGate, enabled bool) (bool, string)
  // pkg/config
  type ExportConfig struct { Enabled bool; Type, Endpoint, APIKey string; BatchSize int; FlushInterval time.Duration } // 均 yaml 标签
  ```

- [x] **Step 1: 写失败测试（cmd/gateway/main_test.go 新建）**

```go
// 标准许可证头；package main；无 BuildTag（双矩阵都要跑）
// imports: testing
//          github.com/druidcaesa/neuralgate/pkg/core
//          github.com/druidcaesa/neuralgate/pkg/license

// featureGate 测试用固定清单门控
type featureGate map[string]bool

func (f featureGate) HasFeature(feature string) bool { return f[feature] }

func TestShouldStartExport(t *testing.T) {
	if ok, reason := shouldStartExport(core.NopGate(), false); ok || reason == "" {
		t.Errorf("未启用应不启动且给出原因: ok=%v reason=%q", ok, reason)
	}
	ok, reason := shouldStartExport(core.NopGate(), true)
	if ok || reason != "授权未包含 audit_stream 功能" {
		t.Errorf("NopGate 应因缺 feature 不启动: ok=%v reason=%q", ok, reason)
	}
	licensed := featureGate{license.FeatureAuditStream: true}
	if ok, _ := shouldStartExport(licensed, true); !ok {
		t.Error("启用且授权含 audit_stream 应启动")
	}
	other := featureGate{license.FeatureRBAC: true}
	if ok, _ := shouldStartExport(other, true); ok {
		t.Error("仅含其他功能不应启动")
	}
}
```

- [x] **Step 2: 运行确认失败**

Run: `go test ./cmd/gateway/ -run TestShouldStartExport`
Expected: FAIL `undefined: shouldStartExport`

- [x] **Step 3: 实现（三处修改一次完成）**

`pkg/config/config.go`：

```go
type ExportConfig struct {
	Enabled       bool          `yaml:"enabled"` // 是否启用外推(bool 不参与 applyDefaults)
	Type          string        `yaml:"type"`
	Endpoint      string        `yaml:"endpoint"`
	APIKey        string        `yaml:"api_key"`
	BatchSize     int           `yaml:"batch_size"`
	FlushInterval time.Duration `yaml:"flush_interval"`
}
```

`pkg/plugin/enterprise/factory.go`（替换返回 nil 的一行）：

```go
// CreateExporter 审计日志外推器（存储尾随；经 Init 配置目标后启动循环）
func (f *enterpriseFactory) CreateExporter() plugin.LogExporter {
	return NewTailExporter(f.CreateStorage())
}
```

`cmd/gateway/main.go` 步骤 7 之后插入步骤 8，并在 shutdown 区插入收尾：

```go
	// 8. 审计日志外推（audit_stream 门控）
	exporter := factory.CreateExporter()
	exportStarted := false
	if exporter != nil {
		if start, reason := shouldStartExport(gate, cfg.Export.Enabled); !start {
			logger.Info("审计日志外推未启用", zap.String("reason", reason))
		} else if err := exporter.Init(map[string]interface{}{
			"type":           cfg.Export.Type,
			"endpoint":       cfg.Export.Endpoint,
			"api_key":        cfg.Export.APIKey,
			"batch_size":     cfg.Export.BatchSize,
			"flush_interval": cfg.Export.FlushInterval,
			"logger":         logger,
		}); err != nil {
			logger.Warn("审计日志外推启动失败", zap.Error(err))
		} else {
			exportStarted = true
			logger.Info("审计日志外推已启用",
				zap.String("type", cfg.Export.Type),
				zap.Duration("flush_interval", cfg.Export.FlushInterval))
		}
	}
```

```go
// shouldStartExport 判断外推启动条件（配置启用 + 授权含 audit_stream）；不满足给出原因
func shouldStartExport(gate core.LicenseGate, enabled bool) (bool, string) {
	if !enabled {
		return false, "配置未启用(enabled=false)"
	}
	if !gate.HasFeature(license.FeatureAuditStream) {
		return false, "授权未包含 audit_stream 功能"
	}
	return true, ""
}
```

shutdown 序列（在 `auditor.Shutdown()` 代码块之后、`storage.Close()` 之前插入）：

```go
	if exportStarted {
		exporter.Close() // 最终一轮拉取兜住 auditor.Shutdown 落库的尾部日志
	}
```

`config.yaml` export 段替换为：

```yaml
export:                       # Enterprise only：审计日志外推(audit_stream 门控)
  enabled: false              # 是否启用外推
  type: siem                  # siem/syslog
  endpoint: "https://siem.example.com/api"   # syslog 形如 udp://10.0.0.1:514 或 tcp://...
  api_key: ""                 # SIEM 认证密钥(syslog 忽略)
  batch_size: 50              # 批量大小 1-1000
  flush_interval: 10s         # 拉取/推送节奏 1-60s
```

- [x] **Step 4: 运行确认通过**

Run: `go test ./cmd/gateway/ -run TestShouldStartExport && go build ./... && go build -tags enterprise ./...`
Expected: PASS + 双编译无错误

- [x] **Step 5: Commit**

```bash
git add pkg/config/config.go pkg/plugin/enterprise/factory.go cmd/gateway/main.go cmd/gateway/main_test.go config.yaml
git commit -m "feat(gateway): 审计外推门控启动接线与 export.enabled 开关"
```

---

### Task 5: 全量验证矩阵 + 端到端冒烟

**Files:** 无新改动（验证任务；发现问题则修复后单独 commit）

- [x] **Step 1: 格式与静态检查**

Run: `gofmt -l cmd pkg | grep . && echo NEED_FMT || echo FMT_OK; go vet ./... && go vet -tags enterprise ./...`
Expected: FMT_OK 且 vet 无输出

- [x] **Step 2: 双矩阵构建与测试**

Run: `go build ./... && go build -tags enterprise ./... && go test -race -tags oss ./... && go test -race -tags enterprise ./...`
Expected: oss 全绿（数量与 E1 基线一致，证明零行为变化）；enterprise 全绿（新增 export 用例）

- [x] **Step 3: 端到端冒烟（真实密钥 + 企业版二进制）**

```bash
# 签发含 audit_stream 的真实 license
go run ./cmd/licensegen sign -key .keys/license_private.pem \
  -customer "冒烟客户" -max-nodes 3 -max-tenants 50 \
  -features audit_stream -expires 2027-08-24 -out .keys/license.json

# 本地 Syslog UDP 接收器（终端 B）
nc -k -u -l 5514 > /tmp/syslog-received.txt &

# 本地假 SIEM（终端 C）：python3 -m http.server 不支持 POST 会回 501，
# 改用：python3 /tmp/fake_siem.py（打印 POST body 并回 200）
cat > /tmp/fake_siem.py <<'EOF'
from http.server import BaseHTTPRequestHandler, HTTPServer
class H(BaseHTTPRequestHandler):
    def do_POST(self):
        body = self.rfile.read(int(self.headers.get('Content-Length', 0)))
        print(body.decode(), flush=True)
        self.send_response(200); self.end_headers()
    def log_message(self, *a): pass
HTTPServer(('127.0.0.1', 5500), H).serve_forever()
EOF
python3 /tmp/fake_siem.py &

# 冒烟配置：复制 config.yaml，license.file_path=.keys/license.json，
# export.enabled=true、endpoint 分别指向上述两个接收端，storage.driver=mem
# 启动企业版并打一条会产生审计的请求（或直接观察启动后手动造一条审计）
go build -tags enterprise -o /tmp/neuralgate-e2 ./cmd/gateway/
/tmp/neuralgate-e2 -config smoke-config.yaml &
sleep 3 && curl -s localhost:8081/api/system | jq '.data.edition'   # enterprise
grep -o '"msg":"[^"]*外推[^"]*"' /tmp/e2-smoke.log                   # 已启用日志
# 触发一条代理请求后：/tmp/syslog-received.txt 出现 <14>1 帧 或 fake_siem 打印 JSON 数组

# 反向验证：enabled:false 或去掉 features 里 audit_stream 重签 → 日志出现「未启用」及原因
```

- [x] **Step 4: 收尾提交（如有修复）**

```bash
git add -A && git commit -m "fix(enterprise): E2 验证问题修复"  # 仅当有修复
```

---

## Self-Review 结论（编写者已核对）

- **规格覆盖**：§4.1 组件五文件全部对应 Task；§4.4 游标/缓冲/退避细节落 Task 3；§5 enabled 开关、§6 接线与关闭顺序落 Task 4；§7 测试策略逐条映射到各 Task 测试步
- **占位符**：Task 3 Step 1 的 `newManualExporter` 骨架已在 Step 3 注明删除、用例内联构造；无 TBD/TODO
- **类型一致性**：`ExportTarget.Send/TestConnection/Close` 在 Task 1 定义、Task 2/3 实现与消费一致；`shouldStartExport` 签名在 Task 4 测试与实现一致


---

## 实施结果（2026-08-24 完成）

| 提交 | 内容 |
|------|------|
| 00067d4 | ExportTarget 接口与 SIEM/Syslog 外推目标（T1+T2 合并提交，两者编译互依无法拆分） |
| 3c1276c | TailExporter 游标拉取与有界缓冲退避补投（构造器补 seen 初始化修复测试装配路径） |
| 52eaa75 | 门控启动接线与 export.enabled 开关 |

**验证矩阵**：gofmt/vet 干净；`go test -race` oss 167 / enterprise 189 全绿。

**端到端冒烟**（真实密钥授权 + 企业版二进制 + 本地 UDP Syslog 接收器）：
- 授权含 audit_stream 且 enabled=true → 「审计日志外推已启动」，edition=enterprise
- 触发代理请求（上游不可达 504）→ 1s 内接收器收到 RFC5424 帧 `<11>1 ... NeuralGate NeuralGate - <requestID> - {全量审计JSON}`（504→err severity 映射正确）
- 反向：授权仅含 rbac → 「审计日志外推未启用 reason=授权未包含 audit_stream 功能」
