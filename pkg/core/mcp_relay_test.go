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

package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
)

const mcpTestServerID = "srv-1"

// mcpRequestProbe 测试用最小请求形状
type mcpRequestProbe struct {
	Method string `json:"method"`
}

// hookRecorder 捕获审计旁路调用
type hookRecorder struct {
	mu      sync.Mutex
	entries []*plugin.MCPAuditLog
}

func (h *hookRecorder) OnToolCall(entry *plugin.MCPAuditLog) {
	h.mu.Lock()
	defer h.mu.Unlock()
	cp := *entry
	h.entries = append(h.entries, &cp)
}

func (h *hookRecorder) all() []*plugin.MCPAuditLog {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.entries
}

// recordingAuditor 只关心 Finalize 与 MarkDisconnect；其余方法嵌接口不触达
type recordingAuditor struct {
	plugin.AuditPipeline
	mu          sync.Mutex
	finalized   []string
	statuses    []int
	disconnects []string // MarkDisconnect 收到的 requestID（顺序记录）
}

func (a *recordingAuditor) Submit(event *plugin.AuditEvent) error { return nil }

func (a *recordingAuditor) Finalize(requestID string, meta *plugin.AuditMeta) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.finalized = append(a.finalized, requestID)
	a.statuses = append(a.statuses, meta.ResponseStatus)
	return nil
}

func (a *recordingAuditor) MarkDisconnect(requestID string, reason string, meta *plugin.AuditMeta) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.disconnects = append(a.disconnects, requestID)
	return nil
}

// mcpFixture 组装存储+假上游+中继；serverID 固定 srv-1，返回中继与记录器供断言
func mcpFixture(t *testing.T, upstream http.HandlerFunc, enabled bool) (*MCPRelay, *hookRecorder, *recordingAuditor) {
	t.Helper()
	upSrv := httptest.NewServer(upstream)
	t.Cleanup(upSrv.Close)
	storage := oss.NewMemStorage()
	srv := &plugin.MCPServer{ID: mcpTestServerID, Name: "u1", Endpoint: upSrv.URL,
		Enabled: enabled, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := storage.SaveMCPServer(srv); err != nil {
		t.Fatal(err)
	}
	hook := &hookRecorder{}
	auditor := &recordingAuditor{}
	relay := NewMCPRelay(storage, auditor, hook, upSrv.Client())
	return relay, hook, auditor
}

func postRPC(t *testing.T, h http.Handler, body, sessionID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/mcp/servers/"+mcpTestServerID+"/mcp", strings.NewReader(body))
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

const initRequestBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"test-agent","version":"1"}}}`
const toolsListBody = `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`

// initializeAndGetSession 走一次 initialize 换取网关会话 ID
func initializeAndGetSession(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := postRPC(t, h, initRequestBody, "")
	if rec.Code != 200 {
		t.Fatalf("initialize 应成功: %d %s", rec.Code, rec.Body.String())
	}
	sid := rec.Header().Get("Mcp-Session-Id")
	if sid == "" {
		t.Fatal("应回写 Mcp-Session-Id")
	}
	return sid
}

// TestMCPRelayInitializeIssuesSession 发号会话并携带 caller_agent；tools/list 凭会话放行且转发到上游
func TestMCPRelayInitializeIssuesSession(t *testing.T) {
	var mu sync.Mutex
	var seenMethods []string
	relay, _, _ := mcpFixture(t, func(w http.ResponseWriter, r *http.Request) {
		var probe mcpRequestProbe
		_ = json.NewDecoder(r.Body).Decode(&probe)
		mu.Lock()
		seenMethods = append(seenMethods, probe.Method)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if probe.Method == "initialize" {
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"serverInfo":{"name":"fake-mcp","version":"0"}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":2,"result":{"tools":[]}}`))
	}, true)

	sid := initializeAndGetSession(t, relay)
	rec := postRPC(t, relay, toolsListBody, sid)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"tools":[]`) {
		t.Errorf("tools/list 应透传: %d %s", rec.Code, rec.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seenMethods) != 2 || seenMethods[0] != "initialize" || seenMethods[1] != "tools/list" {
		t.Errorf("上游应收到两跳: %v", seenMethods)
	}
}

// TestMCPRelaySessionEnforced 无会话/坏会话一律 404
func TestMCPRelaySessionEnforced(t *testing.T) {
	relay, _, _ := mcpFixture(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":2,"result":{}}`))
	}, true)
	if rec := postRPC(t, relay, toolsListBody, ""); rec.Code != 404 {
		t.Errorf("无会话应 404, got %d", rec.Code)
	}
	if rec := postRPC(t, relay, toolsListBody, "bogus-session"); rec.Code != 404 {
		t.Errorf("坏会话应 404, got %d", rec.Code)
	}
}

// TestMCPRelayToolsCallJSONAudit json 结果抓取与审计字段组装
func TestMCPRelayToolsCallJSONAudit(t *testing.T) {
	relay, hook, auditor := mcpFixture(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":9,"result":{"content":[],"isError":false}}`))
	}, true)
	relay.elapsed = func(time.Time) int64 { return 150 } // 确定性耗时
	sid := initializeAndGetSession(t, relay)
	callBody := `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"search","arguments":{"q":"go"}}}`
	rec := postRPC(t, relay, callBody, sid)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"isError":false`) {
		t.Fatalf("结果应原样回写: %d %s", rec.Code, rec.Body.String())
	}
	entries := hook.all()
	if len(entries) != 1 {
		t.Fatalf("hook 应恰好一次, got %d", len(entries))
	}
	e := entries[0]
	if e.ToolName != "search" || !strings.Contains(e.ToolArguments, `"go"`) ||
		e.Status != plugin.MCPStatusSuccess || e.CallerAgent != "test-agent" ||
		e.DurationMS != 150 || e.RequestID == "" || e.ErrorMessage != "" {
		t.Errorf("审计字段不符(成功调用 error_message 应为空): %+v", e)
	}
	if !strings.Contains(e.ToolResult, "isError") {
		t.Errorf("tool_result 应含最终响应: %q", e.ToolResult)
	}
	auditor.mu.Lock()
	defer auditor.mu.Unlock()
	if len(auditor.finalized) != 1 || auditor.statuses[0] != 200 {
		t.Errorf("常规审计应 Finalize 一次且状态 200: %+v", auditor.finalized)
	}
}

// TestMCPRelayToolsCallFailure JSON-RPC error 判定 failed 并带错误信息
func TestMCPRelayToolsCallFailure(t *testing.T) {
	relay, hook, _ := mcpFixture(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":9,"error":{"code":-32000,"message":"tool exploded"}}`))
	}, true)
	sid := initializeAndGetSession(t, relay)
	rec := postRPC(t, relay, `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"boom"}}`, sid)
	if rec.Code != 200 {
		t.Fatalf("协议内错误仍是 HTTP 200: %d", rec.Code)
	}
	entries := hook.all()
	if len(entries) != 1 || entries[0].Status != plugin.MCPStatusFailed ||
		!strings.Contains(entries[0].ErrorMessage, "exploded") {
		t.Errorf("失败判定不符: %+v", entries)
	}
}

// TestMCPRelayServerStates 未知与停用上游均 404
func TestMCPRelayServerStates(t *testing.T) {
	relay, _, _ := mcpFixture(t, func(w http.ResponseWriter, r *http.Request) {}, false)
	req := httptest.NewRequest(http.MethodPost, "/v1/mcp/servers/nope/mcp", strings.NewReader(toolsListBody))
	rec := httptest.NewRecorder()
	relay.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Errorf("未知 server 应 404, got %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodPost, "/v1/mcp/servers/"+mcpTestServerID+"/mcp", strings.NewReader(initRequestBody))
	rec = httptest.NewRecorder()
	relay.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Errorf("停用 server 应 404, got %d", rec.Code)
	}
}

// TestMCPRelayMethodNotAllowedAndParseErrors GET 405;帧错误映射
func TestMCPRelayMethodNotAllowedAndParseErrors(t *testing.T) {
	relay, _, _ := mcpFixture(t, func(w http.ResponseWriter, r *http.Request) {}, true)
	get := httptest.NewRequest(http.MethodGet, "/v1/mcp/servers/"+mcpTestServerID+"/mcp", nil)
	rec := httptest.NewRecorder()
	relay.ServeHTTP(rec, get)
	if rec.Code != 405 {
		t.Errorf("GET 应 405, got %d", rec.Code)
	}
	if rec := postRPC(t, relay, `{bad`, ""); rec.Code != 400 || !strings.Contains(rec.Body.String(), "-32700") {
		t.Errorf("非法 JSON 应 400/-32700: %d %s", rec.Code, rec.Body.String())
	}
	if rec := postRPC(t, relay, `[1,2]`, ""); rec.Code != 400 || !strings.Contains(rec.Body.String(), "-32600") {
		t.Errorf("顶层数组应 400/-32600: %d %s", rec.Code, rec.Body.String())
	}
}

// TestMCPRelayUpstreamUnreachable 会话有效但上游中途宕掉 → 502/-32603 且记 failed
func TestMCPRelayUpstreamUnreachable(t *testing.T) {
	upSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	url := upSrv.URL
	storage := oss.NewMemStorage()
	_ = storage.SaveMCPServer(&plugin.MCPServer{ID: mcpTestServerID, Name: "dying",
		Endpoint: url, Enabled: true})
	hook := &hookRecorder{}
	relay := NewMCPRelay(storage, nil, hook, upSrv.Client())

	sid := initializeAndGetSession(t, relay) // 上游尚活着
	upSrv.Close()                            // 随后宕掉

	rec := postRPC(t, relay, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"x"}}`, sid)
	if rec.Code != 502 || !strings.Contains(rec.Body.String(), "-32603") {
		t.Fatalf("不可达应 502/-32603: %d %s", rec.Code, rec.Body.String())
	}
	entries := hook.all()
	if len(entries) != 1 || entries[0].Status != plugin.MCPStatusFailed {
		t.Errorf("传输层失败也应记 failed: %+v", entries)
	}
}

// TestMCPRelayDeleteTerminates DELETE 清会话并转发上游
func TestMCPRelayDeleteTerminates(t *testing.T) {
	upSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer upSrv.Close()
	storage := oss.NewMemStorage()
	_ = storage.SaveMCPServer(&plugin.MCPServer{ID: mcpTestServerID, Name: "u1",
		Endpoint: upSrv.URL, Enabled: true})
	relay := NewMCPRelay(storage, nil, nil, upSrv.Client())

	sid := initializeAndGetSession(t, relay)
	del := httptest.NewRequest(http.MethodDelete, "/v1/mcp/servers/"+mcpTestServerID+"/mcp", nil)
	del.Header.Set("Mcp-Session-Id", sid)
	rec := httptest.NewRecorder()
	relay.ServeHTTP(rec, del)
	if rec.Code != 204 {
		t.Fatalf("DELETE 应 204, got %d", rec.Code)
	}
	if rec := postRPC(t, relay, toolsListBody, sid); rec.Code != 404 {
		t.Errorf("DELETE 后同会话应 404, got %d", rec.Code)
	}
}

// TestPipelineMCPBranch 分支中间件按前缀分流；nil relay 恒放行零变化。
// 走完整管道需先过 Auth：种一个有效 Bearer Key
func TestPipelineMCPBranch(t *testing.T) {
	upSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer upSrv.Close()
	storage := oss.NewMemStorage()
	_ = storage.SaveMCPServer(&plugin.MCPServer{ID: mcpTestServerID, Name: "u1",
		Endpoint: upSrv.URL, Enabled: true})
	rawKey := "ng-mcp-test-key"
	keySum := sha256.Sum256([]byte(rawKey))
	_ = storage.SaveAPIKey(&plugin.APIKey{
		ID: "k-mcp", KeyHash: hex.EncodeToString(keySum[:]), Name: "tester",
		Status: plugin.APIKeyStatusActive, Quota: -1,
	})
	authedPost := func(h http.Handler, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost,
			"/v1/mcp/servers/"+mcpTestServerID+"/mcp", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+rawKey)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	limiter := oss.NewRateLimiter(storage, 1000, 1000000, "token_bucket")

	// relay==nil:/v1/mcp 路径走原链(此处下游为探针,不应到达 MCP 分支短路)
	pNil := NewPipeline(storage, limiter, nil, nil)
	reachedDownstream := false
	nilHandler := pNil.Build(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reachedDownstream = true
	}))
	req := httptest.NewRequest(http.MethodPost, "/v1/mcp/servers/x/mcp", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+rawKey)
	rec := httptest.NewRecorder()
	nilHandler.ServeHTTP(rec, req)
	if reachedDownstream {
		t.Error("nil relay 时 /v1/mcp 应由路由中间件处置(零变化), 不应到下游探针")
	}

	// relay 就绪:前缀命中走中继(initialize 成功),其余路径仍走原链
	p := NewPipeline(storage, limiter, nil, nil)
	p.SetMCPRelay(NewMCPRelay(storage, nil, nil, upSrv.Client()))
	h := p.Build(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("MCP 前缀请求不应落到 chat 下游")
	}))
	rec = authedPost(h, initRequestBody)
	if rec.Code != 200 {
		t.Errorf("经管道访问 MCP 应成功: %d %s", rec.Code, rec.Body.String())
	}
}

// stubLimiter 可编程限流桩：嵌接口仅实现 Allow，覆盖 mcpBranch 的分支判定
type stubLimiter struct {
	plugin.RateLimitPlugin
	err     error // 非 nil 时 Allow 返回该异常（限流器内部故障）
	allowed bool  // err 为 nil 时的放行判定
}

func (l *stubLimiter) Allow(tenantID string, model string, tokens int) (bool, int64, error) {
	if l.err != nil {
		return false, 0, l.err
	}
	return l.allowed, 10, nil
}

// TestPipelineMCPBranchLimiterDegrade 限流器内部异常时 MCP 分支降级放行
// （与 chat 链"可用性优先"口径一致，瞬时故障不得 429 全部 MCP 流量）；真超限仍 429
func TestPipelineMCPBranchLimiterDegrade(t *testing.T) {
	upSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"serverInfo":{"name":"fake-mcp","version":"0"}}}`))
	}))
	defer upSrv.Close()
	storage := oss.NewMemStorage()
	_ = storage.SaveMCPServer(&plugin.MCPServer{ID: mcpTestServerID, Name: "u1",
		Endpoint: upSrv.URL, Enabled: true})
	rawKey := "ng-mcp-test-key"
	keySum := sha256.Sum256([]byte(rawKey))
	_ = storage.SaveAPIKey(&plugin.APIKey{
		ID: "k-mcp", KeyHash: hex.EncodeToString(keySum[:]), Name: "tester",
		Status: plugin.APIKeyStatusActive, Quota: -1,
	})
	post := func(limiter plugin.RateLimitPlugin) *httptest.ResponseRecorder {
		p := NewPipeline(storage, limiter, nil, nil)
		p.SetMCPRelay(NewMCPRelay(storage, nil, nil, upSrv.Client()))
		h := p.Build(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		req := httptest.NewRequest(http.MethodPost,
			"/v1/mcp/servers/"+mcpTestServerID+"/mcp", strings.NewReader(initRequestBody))
		req.Header.Set("Authorization", "Bearer "+rawKey)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	rec := post(&stubLimiter{err: errors.New("limiter fault")})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "serverInfo") {
		t.Errorf("限流器异常应降级放行(initialize 成功): %d %s", rec.Code, rec.Body.String())
	}
	if rec := post(&stubLimiter{allowed: false}); rec.Code != http.StatusTooManyRequests {
		t.Errorf("真超限应 429: %d %s", rec.Code, rec.Body.String())
	}
}
