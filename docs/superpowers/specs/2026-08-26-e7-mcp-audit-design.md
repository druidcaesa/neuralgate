# Enterprise E7 MCP 智能体审计 设计文档

日期：2026-08-26
状态：已经用户逐节评审通过
对应 PRD：3.9 MCP智能体审计（Enterprise）；功能矩阵「MCP协议透传（OSS+）/ MCP全链路审计（Enterprise）」
前置：E1 授权门控（`license.FeatureMCPAudit = "mcp_audit"` 常量已预置）、E4 security_events 告警链路

## 1. 背景与范围

网关现状对 MCP 零支持（仅 license 常量存在）。PRD 将 MCP 能力拆为两条线：

- **MCP 协议透传（OSS+，无审计）**：本设计一并交付——完整 MCP 协议中继通道是审计的前提
- **MCP 全链路审计（Enterprise，mcp_audit 门控）**：工具调用参数/结果/行为全留存 + 异常调用告警

### 1.1 已裁决的范围决策

| # | 决策点 | 结论 |
|---|--------|------|
| D1 | 透传与审计的关系 | 一个里程碑一体交付（审计依赖通道存在） |
| D2 | 协议形态 | 完整 MCP 协议（JSON-RPC 2.0），真实 MCP 客户端可直连；不做 PRD 架构草图里的简化 `/v1/mcp/tools/call` 私有端点 |
| D3 | 传输层 | 仅 Streamable HTTP（现行规范单端点 POST + 可选 GET SSE）；不做旧版双端点 HTTP+SSE、不做 stdio 上游 |
| D4 | 多上游拓扑 | 每个 mcp_servers 记录一个上游，入口路径 `/v1/mcp/servers/{id}/mcp`，会话与工具集各自独立；不做聚合虚拟服务器 |
| D5 | tools/call 流式语义 | 上游回什么透传什么（json 或 SSE 流），SSE 时旁路累积帧至最终 JSON-RPC 响应再审计落库 |
| D6 | 协议层实现方式 | 自研窄协议层（手写 JSON-RPC 结构 + Streamable HTTP 处理），不引入官方 Go SDK |

### 1.2 YAGNI 明确排除

GET 反向通知流（对 GET 返回 405，规范允许 server 不提供）、`Last-Event-ID` 断线重连、batch JSON-RPC 帧、stdio 子进程上游、旧版 HTTP+SSE 传输、聚合虚拟服务器与工具级路由配置、prompts/resources 方法的深度解析（纯转发）。

## 2. 总体架构

```
MCP客户端 ──POST /v1/mcp/servers/{id}/mcp──▶ [AuthMiddleware(API Key) → RateLimiter] ─▶ MCPRelayHandler (pkg/core, OSS+)
                                                                                          │ 会话管理(内存 map)
                                                                                          ▼
                                                                                上游 MCP Server (Streamable HTTP)
                                                                                          │
                                                          MCPAuditHook 接口(OSS 定义) ◀── 旁路: 参数/结果/耗时/成败
                                                                                          ▼
                                                        enterprise/mcp_audit.go (mcp_audit 门控) → SaveMCPAuditLog
                                                                                          ▼ status=failed
                                                                              SaveSecurityEvent("mcp_call_failed")
```

### 2.1 组件划分

**`pkg/mcp`（新建包，无构建标签，OSS 可见）**
- JSON-RPC 2.0 消息结构：`RPCRequest{JSONRPC, Method, ID, Params}` / `RPCResponse{JSONRPC, ID, Result, Error}` / `RPCError{Code, Message, Data}`
- MCP 方法常量与最小协议类型：`initialize`(含 `clientInfo.name`)、`tools/list`、`tools/call`(`params.name`/`params.arguments`)、`ping`、`notifications/initialized`
- SSE 事件流解析器（`event: message\ndata: {...}` 帧 → RPC 消息流）与 SSE 写码器
- 错误码常量（`-32600/-32700/-32603` 等）
- 纯函数库，无 IO，两侧共用

**`pkg/core` MCP 中继 handler（OSS+）**
- 新 gin 路由组 `/v1/mcp/servers/:id/mcp`，挂现有 `AuthMiddleware` 与 `RateLimiter`；不走 chat 的 fixedChain（模型路由对 MCP 无意义）
- 职责：serverID 解析与会话校验、上游转发（复用代理层 http.Client 风格）、响应原样回写（按上游 Content-Type 透传 json 或 SSE 流式）、tools/call 旁路抓取、DELETE 会话终止
- 会话 map：网关生成 session id（UUID）→ `{caller_agent, serverID, apiKeyID}`；TTL 30 分钟惰性清理；进程重启丢失由客户端重新 initialize 自愈（协议允许）

**`pkg/plugin.MCPAuditHook`（接口定义在 OSS 侧，仿 E3 FingerprintHook 先例）**
- OSS 恒 nil：零开销直通，通道完整可用（PRD「透传无审计」语义）
- enterprise 实现经 `shouldStartMCPAudit(gate, cfg)` 注入；未启用时日志给出原因且零行为变化

**`pkg/plugin/enterprise/mcp_audit.go`（enterprise 编译）**
- hook 实现：组装 PRD 字段 → `SaveMCPAuditLog`；`status=failed` 时追加 `SaveSecurityEvent`
- 参数/结果超过 1MB 截断并在字段尾部标注 `[truncated]`

## 3. 数据模型

### 3.1 `mcp_servers` 表（mem/sqlite/mysql 同构，仿 upstreams 模式）

| 字段 | 类型 | 说明 |
|---|---|---|
| id | TEXT PK | UUID |
| name | TEXT UNIQUE NOT NULL | 展示名 |
| endpoint | TEXT NOT NULL | 上游 Streamable HTTP URL |
| headers | TEXT NOT NULL DEFAULT '{}' | 附加请求头 JSON（如上游要求 Bearer） |
| enabled | INTEGER NOT NULL DEFAULT 1 | 停用后该路径 404 |
| created_at / updated_at | INTEGER (ms) | |

### 3.2 `mcp_audit_logs` 表（PRD 3.9 十三字段原样落地）

| 字段 | 类型 | 说明 |
|---|---|---|
| id | TEXT PK | UUID |
| request_id | TEXT NOT NULL | 网关生成的请求追踪 ID；tools/call 同时向现有审计管道提交一条常规审计日志（request_path=/v1/mcp/servers/{id}/mcp，仿 E4 隐私中间件补审计先例），实现与 audit_logs 的两账串联 |
| tenant_id / api_key_id | TEXT | 来自 API Key 鉴权上下文 |
| tool_name | TEXT NOT NULL | `params.name` |
| tool_arguments | TEXT NOT NULL DEFAULT '' | `params.arguments` JSON 文本（≤1MB 截断标注） |
| tool_result | TEXT NOT NULL DEFAULT '' | 最终 result JSON 文本（同上） |
| caller_agent | TEXT NOT NULL DEFAULT '' | initialize 的 clientInfo.name，兜底 User-Agent |
| duration_ms | INTEGER | 从 handler 进入到最终响应写完 |
| status | TEXT NOT NULL | success / failed |
| error_message | TEXT NOT NULL DEFAULT '' | JSON-RPC error.message 或失败摘要 |
| client_ip | TEXT | |
| created_at | INTEGER (ms) | |

索引：`(tenant_id, created_at)`、`request_id`。

### 3.3 StoragePlugin 扩展

```go
// mcp_servers CRUD
SaveMCPServer(server *MCPServer) error            // UPSERT by id;name 冲突由调用方校验
GetMCPServer(id string) (*MCPServer, error)
ListMCPServers(page, size int) ([]*MCPServer, int64, error)
DeleteMCPServer(id string) error
// 审计留存与查询
SaveMCPAuditLog(entry *MCPAuditLog) error
ListMCPAuditLogs(filter MCPAuditLogFilter, page, size int) ([]*MCPAuditLog, int64, error)
```

`MCPAuditLogFilter{TenantID, RequestID, ToolName, Status, StartTime, EndTime}`（指针时间字段语义与 AuditLogFilter 一致：闭区间）。

## 4. 数据面流程

```
POST /v1/mcp/servers/{id}/mcp
 1. AuthMiddleware(API Key) → rc{tenant_id, api_key_id, client_ip}
 2. RateLimiter(api_key 维度；模型维度不适用)
 3. 解析 :id 查 mcp_servers → 不存在或停用 → 404
 4. 方法分支：
    a. initialize  → 转发上游；成功后生成网关 Mcp-Session-Id 回写响应头并登记会话
                     （caller_agent=clientInfo.name 兜底 User-Agent）
    b. 带 Mcp-Session-Id 的后续方法 → 校验存在且匹配 apiKey+serverID；
                     无效 → HTTP 404（规范语义：session 已终结需重新 initialize）
    c. tools/call  → 解析 params.name/params.arguments → 开始计时 → 转发
    d. 其他方法    → 纯转发（tools/list/ping/prompts/* 等）
 5. 上游响应处理：
    - application/json     → 原样回写；tools/call 旁路取 result/error 触发 hook
    - text/event-stream    → 流式透传客户端，同时 tee 累积帧；
                             出现匹配请求 id 的最终 JSONRPCResponse 后触发 hook（不阻塞客户端读流）
    - 上游不可达/超时       → 向客户端回 JSON-RPC -32603，hook 记 failed
    - 客户端帧非法          → -32700(Parse error)/-32600(Invalid Request)，不经上游
 6. DELETE（会话终止）→ 清本地 map + 转发上游 DELETE
 7. hook 非 nil 时组装 PRD 十三字段 → SaveMCPAuditLog；
    status=failed（JSON-RPC error 或 result.isError=true）→ SaveSecurityEvent(event_type="mcp_call_failed")
 8. tools/call 无论 hook 是否启用，均向现有审计管道 Submit 一条常规审计日志
    （request_id 同源、request_path=/v1/mcp/servers/{id}/mcp），保证 OSS 版 MCP 流量也有数据面审计可查
```

鉴权完全复用现有 API Key 链（tenant_id/api_key_id 字段由此而来）；无 key 的 MCP 端点不存在。限流沿用 api_key 维度。

## 5. 管理面

### 5.1 admin API

均挂 `RequirePermission` 与全局域守卫（globalOnlyGuard）：

- `GET/POST /api/mcp-servers`、`GET/PUT/DELETE /api/mcp-servers/:id` — system:read / system:write；create/update 校验 name 唯一与 endpoint URL 合法
- `GET /api/mcp-audit-logs?page&size&tool&status&tenant_id&request_id&start&end` + `GET /api/mcp-audit-logs/:id` — system:read

### 5.2 webui

- `MCPServers.vue`：列表 + 创建/编辑弹窗（name/endpoint/headers JSON/enabled）
- `MCPAuditLogs.vue`：筛选条（tool/status/时间范围）+ 表格 + 详情抽屉（arguments/result JSON 格式化展示）
- 两页菜单挂 `hasPerm('system:read')`；改 webui 同轮 `make build-webui` 并提交 dist（E5/E6 教训）

## 6. 门控与配置

```yaml
mcp_audit:                    # Enterprise only：MCP 智能体审计(mcp_audit 门控)
  enabled: false              # 默认 false；bool 不参与 applyDefaults；未启用零行为变化
```

- 通道本身 OSS+ 恒可用（无审计）；审计注入条件 = 授权含 mcp_audit && 配置 enabled
- 门控未满足时启动日志给出原因（仿 compliance：「配置未启用(mcp_audit.enabled=false)」/「授权未包含 mcp_audit 功能」）
- main.go 接线仿 setupCompliance 双 build-tag 文件模式；shutdown 序列无需新增组件（handler 无后台循环）

## 7. 测试策略

1. **pkg/mcp 单测**：JSON-RPC 帧编解码往返、SSE 解析（多帧/跨行 data/非法帧）、错误码映射
2. **core 中继集成测**（httptest 假上游）：initialize 发号会话、无/坏 session 404、tools/list 纯转发、tools/call json 抓取、tools/call SSE tee 至最终响应、上游宕机 -32603、DELETE 清会话、GET 405、停用 server 404
3. **enterprise hook 测**：字段映射完整性、SSE 累积后一次落库、失败调用产生 security_event、nil hook 零开销（-race）
4. **admin API 测**：CRUD 分支、审计筛选分页、租户内用户三接口 403（照 compliance 模式）
5. **双矩阵**：`go test -tags oss ./...` 与 `go test -race -tags enterprise ./...` 全绿；双 vet 干净；双版本二进制构建

## 8. 端到端验收

licensegen 签发含 mcp_audit 授权 → 企业版启动（mcp_audit.enabled=true）→ 配置一个 httptest/python 假 MCP 上游 → curl 以 JSON-RPC 走完 initialize → tools/list → tools/call（json 与 SSE 各一）→ `GET /api/mcp-audit-logs` 可见且字段正确（caller_agent/duration/status）→ 构造失败调用 → security_events 出现 mcp_call_failed → OSS 版或无授权时：通道可用、审计恒空、日志有门控原因。

## 9. 决策记录

| # | 决策 | 理由 |
|---|------|------|
| D1-D6 | 见 §1.1 | 用户逐项裁决 |
| A | 会话元数据存内存 map 而非存储表 | 会话生命周期短且可自愈（客户端重新 initialize），持久化收益为零 |
| B | 审计在收到最终 JSONRPCResponse 时同步落库 | 管理面低频写，换确定性与实现简单；失败仅告警不影响转发 |
| C | 异常告警复用 security_events 而非新告警表 | E4 已有事件模型/webui 页/查询 API，新增表属重复建设 |
| D | caller_agent 取 initialize clientInfo 并存会话 map | PRD 字段要求；User-Agent 仅兜底 |
