# Enterprise E5 权限体系(RBAC) — 设计文档

> **日期**: 2026-08-25
> **目标**: 实现 PRD 3.7 六项——多租户隔离、租户管理、RBAC 角色管理、用户管理、操作审计、功能权限；验收对齐 PRD 6.6(跨租户返回空/无权限 403/操作日志可查)
> **前置**: E1 门控(`license.FeatureRBAC`);E3/E4 确立的 TTL 缓存与 BuildTag 接线模式;现有 admin 认证体系(bcrypt+HMAC 会话,admin_users 表)
> **版本**: V1.0

---

## 1. 背景与现状

现有管理面为单管理员体系:admin_users 表(bcrypt 密码+HMAC 会话 token 绑口令摘要),认证默认开启 fail-closed。数据面 API Key/审计日志/限流配置三实体表结构已含 tenant_id 字段但无隔离语义。PRD 3.7 要求多租户+角色+用户+操作审计完整权限体系。

## 2. 目标 / 非目标

**目标**:
1. 多租户隔离:API Key 列表、审计日志查询、限流配置三实体按租户隔离;跨租户写操作 404
2. 租户管理:tenants 表 CRUD,禁用租户数据面 Key ≤30s 失效
3. RBAC 角色:roles 表 CRUD,权限编码勾选
4. 用户管理:后台账号分配角色与租户
5. 操作审计:管理面写操作全量落 admin_operation_logs 并可查
6. 功能权限:`RequirePermission` 中间件按会话权限放行/403

**非目标(YAGNI)**:
- 数据面模型配置按租户隔离(PRD 无要求,路由热路径改动大)
- 权限运行时热生效(登录时固化进会话,改权限后重登录生效——与改密失效机制一致)
- 租户级配额/计费、租户自助门户
- 数据面 API 的细粒度权限(权限体系仅管管理面)

## 3. 关键决策(已与用户确认)

| # | 决策 | 理由 |
|---|------|------|
| 1 | 隔离范围 = Key+审计+限流 | 三者表已含 tenant_id;满足 PRD 6.6 第一条且范围可控 |
| 2 | 扩展现有认证体系,非独立子系统 | admin_users 加列迁移+复用会话/密码机制;独立重构违背 YAGNI |
| 3 | 超管角色自动种子,bootstrap 自动挂载 | 升级零惊扰;现有账号升级后即为超管 |
| 4 | rbac.enabled 默认 false + FeatureRBAC 双条件 | 与 E2/E3/E4 一致;管理面行为变更须显式开启 |
| 5 | 权限登录时固化进会话 | 免每请求查库;改权限重登录生效可接受 |
| 6 | 租户状态 30s TTL 缓存检查 | 热路径不加裸查询;复用 E4 引擎缓存模式 |
| 7 | 扩展权限码 rate_limit/privacy 两组 | PRD 十码未覆盖限流与 E4 隐私 API;按资源前缀自然延伸 |
| 8 | 跨租户写操作返回 404 | 不暴露资源存在性 |

## 4. 架构

### 4.1 文件清单

```
修改 pkg/plugin/interface.go      # Tenant/Role/AdminOperationLog 结构;
                                   # StoragePlugin += tenants/roles/oplogs CRUD + admin_users 新列字段
修改 oss storage_mem/sql/sqlite/mysql/dynamic  # 两新表+一操作日志表建表;admin_users 增量加列;方法实现
新增 pkg/admin/rbac.go            # 租户/角色/用户/操作日志四组 API handler
修改 pkg/admin/auth.go            # 登录时会话固化 permissions+tenant_id+is_super;RequirePermission 中间件
                                    # EnableRBAC(bool) setter;OperationAudit 写操作落库中间件
修改 pkg/admin/router.go          # authz 组路由标注权限;新四组路由注册
修改 pkg/core/middleware_auth.go  # GetAPIKey 成功后查租户状态(TenantGate 30s TTL 缓存)
新增 pkg/core/tenant_gate.go      # TenantGate:tenants 空表跳过+TTL 缓存禁用判定
新增 enterprise 侧无需独立包       # RBAC 全在 admin/core 层,按 bool 开关而非 BuildTag 分叉
                                   # (oss 版 gate 恒 false → RequirePermission 恒放行,行为零变化)
修改 cmd/gateway/main.go          # shouldStartRBAC + adminServer.EnableRBAC(enabled) 接线
修改 config.yaml                  # rbac.enabled 开关注释
新增 webui views Tenants.vue / Roles.vue / Users.vue / OperationLogs.vue + api/rbac.ts
                                  # 菜单按登录返回的 permissions 显隐
各对应 _test.go
```

### 4.2 数据模型

```go
// Tenant 租户
type Tenant struct {
    ID        string            `json:"id"`
    Name      string            `json:"name"`      // 1-64 字符
    Code      string            `json:"code"`      // 1-32 字母数字,唯一
    Status    string            `json:"status"`    // active|disabled
    Config    map[string]string `json:"config"`
    CreatedAt time.Time         `json:"created_at"`
    UpdatedAt time.Time         `json:"updated_at"`
}

// Role 角色(tenant_id 空 = 全局角色,仅超管可配)
type Role struct {
    ID          string    `json:"id"`
    Name        string    `json:"name"`       // 1-64 字符
    TenantID    string    `json:"tenant_id"`  // 空=全局
    Permissions []string  `json:"permissions"` // 权限编码列表
    CreatedAt   time.Time `json:"created_at"`
    UpdatedAt   time.Time `json:"updated_at"`
}

// AdminOperationLog 管理面操作审计
type AdminOperationLog struct {
    ID        string    `json:"id"`
    UserID    string    `json:"user_id"`
    Username  string    `json:"username"`
    Method    string    `json:"method"`
    Path      string    `json:"path"`
    TargetID  string    `json:"target_id"` // :id 参数,无则空
    StatusCode int      `json:"status_code"`
    ClientIP  string    `json:"client_ip"`
    CreatedAt time.Time `json:"created_at"`
}
```

AdminUser 结构体加 `TenantID string` / `RoleID string` 两字段(json tag 同名下划线)。

StoragePlugin 新增：
GetTenantByID/GetTenantByCode/ListTenants(page,size)/SaveTenant/DeleteTenant/
GetRoleByID/ListRoles/SaveRole/DeleteRole/
SaveAdminOperationLog/ListAdminOperationLogs(filter AdminOpLogFilter, page, size)/
CountTenants()(供门控判断空表)。种子写入仅 roles 空表时插入超级管理员。

### 4.3 权限编码

```go
// pkg/admin/permissions.go —— PRD 十码
const (
    PermAPIKeyRead    = "api_key:read"
    PermAPIKeyWrite   = "api_key:write"
    PermModelRead     = "model:read"
    PermModelWrite    = "model:write"
    PermAuditRead     = "audit:read"
    PermAuditExport   = "audit:export"
    PermTenantRead    = "tenant:read"
    PermTenantWrite   = "tenant:write"
    PermRBACRead      = "rbac:read"
    PermRBACWrite     = "rbac:write"
    // 扩展码(PRD 未覆盖资源的自然延伸)
    PermSystemRead    = "system:read"
    PermSystemWrite   = "system:write"
    PermRateLimitRead  = "rate_limit:read"
    PermRateLimitWrite = "rate_limit:write"
    PermPrivacyRead    = "privacy:read"
    PermPrivacyWrite   = "privacy:write"
)
var AllPermissions = []string{/* 上列全部 */} // 超管种子与前端勾选清单共用
```

超管角色 permissions = AllPermissions 全集。

**租户内权限边界**：tenant_id 非空的用户即使持有 rbac:write/tenant:write，
也只能在本租户内建角色/用户（角色 tenant_id 强制取自身租户）；tenant:write 仅对全局用户有意义，
租户内用户携带亦不生效（handler 层校验）。

### 4.4 会话与中间件

```
SessionManager 登录成功时固化: user_id, username, tenant_id, role_id,
  permissions []string, is_super
  实现:登录时查 user 的 role,is_super = (role.TenantID=="") &&
  (role.Permissions 覆盖 AllPermissions 全集)

RequirePermission(perm):
  !rbacEnabled → next(零开销路径)
  is_super 或 permissions 含 perm → next
  否则 403 {"code":403,"message":"无权限"}

OperationAudit:
  authz 组 POST/PUT/PATCH/DELETE 响应后(c.Next 之后)异步落库;
  user 取自会话,target 取 c.Param("id")

租户过滤(admin handler 内):
  非超管且 tenant_id 非空 → 强制注入:
    listAPIKeys(参数已有 tenantID)、QueryAuditLogs(filter.TenantID)、
    ListRateLimitConfigs(接口加 tenantID *string 参数,四实现同步)
  写操作前查目标记录 tenant_id ≠ 自己 → 404
```

### 4.5 数据面联动

```
core.TenantGate:
  storage.CountTenants()==0 → 恒允许(OSS/未用租户零变化)
  缓存: disabled tenant_id 集合,TTL 30s(同 E4 引擎重载模式)
AuthMiddleware: GetAPIKey 成功且 key.TenantID != "" → tenantGate.Allow(tenantID)
  禁用 → 401 api_key_disabled 语义(响应 403 tenant_disabled 更准确,取 401 保持 OpenAI 错误形态)
```

### 4.6 门控与接线

```
shouldStartRBAC(gate, enabled): enabled=false→"配置未启用(rbac.enabled=false)";
  缺 feature→"授权未包含 rbac 功能"
main 步骤 11: start,reason := shouldStartRBAC(gate,cfg.RBAC.Enabled);
  start 则 adminServer.EnableRBAC(true);日志记录原因
AdminServer.EnableRBAC(bool): setter 存 rbacEnabled,router 注册期已固定 →
  改为构造后立即调用(在 Router() 使用前),中间件闭包读 rbacEnabled 即可动态生效
```

### 4.7 管理后台 API

- `POST/GET/PUT/DELETE /api/tenants`(tenant:write/read;code 唯一 409;内置保留 code 如 "default")
- `POST/GET/PUT/DELETE /api/roles`(rbac:write/read;超管角色禁改禁删 409)
- `POST/GET/PUT/DELETE /api/admin-users`(rbac:write/read;密码哈希存储;不可删自己/最后一个超管 409)
- `GET /api/operation-logs?page&size&user_id`(system:read)
- 登录响应 data 增加 permissions/is_super/tenant_id(webui 菜单显隐依据)

### 4.8 webui

- Tenants.vue:表格+新建/编辑弹窗(status 开关)
- Roles.vue:表格+编辑弹窗(权限分组勾选 checkbox)
- Users.vue:表格+新建/编辑(角色下拉+租户下拉+重置密码)
- OperationLogs.vue:只读分页(user 过滤)
- 菜单四项,按登录返回 permissions 显隐;api/rbac.ts + types

## 5. 配置

```yaml
rbac:                         # Enterprise only：需 rbac 授权
  enabled: false              # 是否启用权限体系(默认 false,显式开启)
```

`config.Config` 增加 `RBACConfig{Enabled bool}`(bool 不参与 applyDefaults)。

## 6. 测试策略(TDD,go test -race,双编译矩阵)

| 单元 | 测试点 |
|------|--------|
| 迁移/种子 | admin_users 加列幂等;roles 空表插超管;重启不重复 |
| 权限中间件 | 未启用恒放行;有权限放行;无权限 403;超管全通 |
| 会话固化 | 登录响应含 permissions/is_super;改权限后旧会话不变、重新登录生效 |
| 租户过滤 | 非超管列 Key/审计/限流只见本租户;跨租户改删 404 |
| 操作审计 | 写操作落库(method/path/user/status);读操作不落;分页过滤 |
| 租户联动 | 禁用租户 Key ≤TTL 401;恢复 ≤TTL 放行;空表恒放行;-race 并发 |
| 存储 | 四表 CRUD 往返三存储一致性(mem/sqlite,mysql NG_MYSQL_DSN) |
| admin/webui | 校验分支(code 重名 409/删自己 409/改超管 409);页面 vue-tsc |
| 矩阵 | OSS 行为零变化(rbac 关闭全部现状测试不动);enterprise 全绿 |
| 冒烟 | 建租户→只读角色→用户→登录→隔离与 403→操作日志可查 |

## 7. 决策记录

| # | 决策 | 理由 |
|---|------|------|
| 1 | RBAC 用 bool 开关而非 BuildTag 分叉 | admin 包不依赖 core/gate;oss 版开关恒关即零变化,E4 引擎类才需要 BuildTag(oss 无对应实现) |
| 2 | is_super 判定 = 全局角色且权限全覆盖 | 免维护特殊角色 ID;语义清晰 |
| 3 | 租户禁用数据面响应 401 | 保持 OpenAI 错误形态,客户端重试逻辑兼容 |
| 4 | 操作审计异步落库 | 不阻塞管理面请求;丢一条可接受(非合规强制项,防篡改仅覆盖数据面审计) |
