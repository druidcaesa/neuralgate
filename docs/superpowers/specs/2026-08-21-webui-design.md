# NeuralGate OSS webui 管理后台前端 — 设计文档

> **日期**: 2026-08-21
> **目标**: 为 OSS 版提供管理后台前端(Vue3 SPA),对接已有 admin API,以 go:embed 嵌入单二进制,闭环 OSS 部署体验
> **前置**: OSS 后端全部完成(112+ tests,MySQL 真库验证),admin API 完整(模型/上游/API Key/审计/限流/系统 6 组)
> **版本**: V1.0

---

## 1. 背景与定位

### 1.1 现状

- 后端 admin API 已完整:`/api/models`(含 upstreams)、`/api/api-keys`、`/api/audit-logs`(含 export)、`/api/rate-limits`、`/api/system`
- 管理后台 :8081 目前只有 JSON API,无 UI 页面
- 项目定位:单二进制部署、零外部依赖、信创环境兼容

### 1.2 目标

提供一个基于浏览器的管理后台 UI,覆盖模型配置、API Key、审计日志、限流配置、系统信息五大页面,通过 go:embed 嵌入单二进制,`go build` 产物开箱即用。

### 1.3 非目标

- 登录/RBAC(OSS 无多租户,后台无鉴权;Enterprise 迭代)
- 图表/可视化(首版 YAGNI)
- 中英文国际化(首版仅中文界面)
- 模型调用测试面板(通过测试连接按钮覆盖)

---

## 2. 技术栈

| 项 | 选择 | 理由 |
|----|------|------|
| 框架 | Vue 3(Composition API) | 组件化、生态成熟 |
| 构建 | Vite | 快、TS 开箱即用 |
| 语言 | TypeScript | 与后端字段类型对齐 |
| UI 库 | Element Plus | 后台表格/表单/弹窗/分页标准组件 |
| 路由 | vue-router | 5 页 SPA |
| HTTP | axios | 拦截器统一错误处理 |
| 服务 | go:embed 嵌入 dist | 单二进制闭环 |

---

## 3. 架构

### 3.1 总览

```
浏览器 (Vue3 SPA)
   │ /api/* (axios)
   ▼
Gin :8081
   ├─ /api/*   现有管理 API(不改动)
   ├─ /assets/* 静态资源(go:embed webui 产物)
   └─ SPA fallback(非 /api 且非静态 → index.html)
```

### 3.2 目录结构

```
webui/                          # 前端源码(开发根)
├── index.html
├── package.json / vite.config.ts / tsconfig.json
├── src/
│   ├── main.ts                 # Vue 入口 + ElementPlus + router
│   ├── App.vue                 # 根布局(侧边栏 + 顶栏 + 内容区)
│   ├── router.ts               # 5 页路由
│   ├── api/                    # 后端 API 封装
│   │   ├── client.ts           # axios 实例(baseURL=/api,响应拦截)
│   │   ├── model.ts            # 模型 + 上游
│   │   ├── apiKey.ts
│   │   ├── audit.ts
│   │   ├── rateLimit.ts
│   │   └── system.ts
│   ├── types/index.ts          # TS 类型(对齐后端字段)
│   └── views/
│       ├── ModelList.vue
│       ├── ApiKeyList.vue
│       ├── AuditLogList.vue
│       ├── RateLimitList.vue
│       └── SystemInfo.vue
└── dist/  → 构建产物输出到 pkg/admin/webui/dist(embed 位置)
```

### 3.3 embed 位置约束

Go `//go:embed` 路径不能 `..`,因此:
- 前端源码在 `webui/`(独立目录,开发清晰)
- `vite.config.ts` 设 `build.outDir: '../pkg/admin/webui/dist'`(构建产物直接进 admin 包)
- `pkg/admin/webui.go` 用 `//go:embed all:webui/dist` 嵌入
- `pkg/admin/webui/dist` 提交到 git(单二进制开箱即用;开发时 `npm run build` 重新生成)

---

## 4. 页面设计

### 4.1 路由

| 路由 | 视图 | 说明 |
|------|------|------|
| `/` | 重定向 `/models` | - |
| `/models` | ModelList | 模型配置 + 上游 |
| `/api-keys` | ApiKeyList | API Key |
| `/audit-logs` | AuditLogList | 审计日志 |
| `/rate-limits` | RateLimitList | 限流配置 |
| `/system` | SystemInfo | 系统信息 |

### 4.2 布局(App.vue)

- 左侧 el-menu 导航(5 项,图标)
- 顶部 el-header:标题 + 系统版本(从 /api/system)
- 内容区 el-main:router-view

### 4.3 模型配置页(ModelList)

主表格列:name / provider / provider_model / base_url / timeout / max_retries / weight / enabled(开关)/ 操作

操作:
- 新增/编辑:el-dialog 表单(name 唯一、provider 下拉、base_url 必填 URL 校验、api_key、timeout 1-300、max_retries 0-5、retry_interval、weight、enabled)
- 删除:ElMessageBox.confirm
- 测试连接:行内按钮 → `POST /api/models/:id/test` → ElMessage(成功含延迟/失败含错误)
- 启用/禁用:el-switch → `PUT /api/models/:id`

上游(行展开 el-table):
- 子表列:base_url / weight / enabled / 操作
- 新增/编辑:弹窗(base_url/api_key/weight)
- 删除:confirm
- 对接 `GET/POST /api/models/:id/upstreams`、`PUT/DELETE /api/upstreams/:uid`

### 4.4 API Key 页(ApiKeyList)

主表格列:key_prefix(脱敏)/ name / status / quota / used_quota / rate_limit / allowed_models / expires_at / created_at / 操作

操作:
- 创建:弹窗(name/quota/rate_limit/allowed_models/expires_at)→ 提交 → **明文 Key 展示弹窗**(高亮 + 复制按钮 + "仅显示一次"警示,关闭前 ElMessageBox.confirm)
- 禁用/启用:el-switch → `PATCH /api/api-keys/:id`
- 删除:confirm(软删除)

### 4.5 审计日志页(AuditLogList)

- 筛选栏:model_name(下拉)/ response_status / is_stream / keyword / 时间范围(el-date-picker)
- 表格:created_at / model_name / response_status / total_tokens / duration_ms / is_stream / request_id
- 分页:page/size
- 详情:点击行 → el-drawer,含基础信息、request_body/response_body(JSON 格式化 + 超长折叠 500 字符)、SSE 分片表(流式)、重组文本
- 导出:按钮 → window.open 或 a 标签下载 `/api/audit-logs/export?format=json|csv`

### 4.6 限流配置页(RateLimitList)

表格列:tenant_id / model_name / requests_per_sec / tokens_per_min / strategy / enabled / 操作
- 新增/编辑:弹窗(tenant/model/rps 1-100000/tpm 1-1e9/strategy 下拉/disabled)
- 删除:confirm
- 对接 `POST/GET/PUT/DELETE /api/rate-limits`

### 4.7 系统信息页(SystemInfo)

el-descriptions 卡片:
- version / build_time / git_commit / edition / uptime
- db_status / audit_queue_status / rate_limiter_status

---

## 5. 错误处理

| 场景 | 处理 |
|------|------|
| HTTP 非 2xx | axios 拦截器读 `{error.message}` 或 `{message}` → ElMessage.error |
| 业务 `code!==0` | 拦截器 → ElMessage.error(message) |
| 网络错误 | 拦截器 → "无法连接管理后台" |
| 表单校验 | Element Plus 规则(必填/URL/数值范围对齐后端) |
| 明文 Key 关前 | confirm 二次确认 |
| 删除 | ElMessageBox.confirm |
| 超长正文 | 折叠展开 |

axios 拦截器统一:

```ts
client.interceptors.response.use(
  (resp) => resp,
  (err) => {
    const msg = err.response?.data?.message || err.response?.data?.error?.message || "请求失败";
    ElMessage.error(msg);
    return Promise.reject(err);
  }
);
```

> 成功响应(code===0)在各 api 模块解包 `data`。

---

## 6. 测试策略

| 层级 | 方式 | 覆盖 |
|------|------|------|
| 后端 embed | Go httptest:`GET /` 返回 index.html、`GET /models` SPA fallback、`GET /api/unknown` 404 JSON | `pkg/admin/server_test.go` 追加 |
| 前端类型 | `vue-tsc --noEmit` | 全量 TS 类型 |
| 手动 | vite dev 联调 + 生产构建 embed | 5 页面走通 |

首版不做前端单测(YAGNI),靠类型检查 + 后端 embed 测试 + 手动验证。

---

## 7. 双模式运行

| 模式 | 命令 | 访问 |
|------|------|------|
| 开发 | `cd webui && npm run dev` + `go run ./cmd/gateway` | http://localhost:5173 |
| 生产 | `cd webui && npm run build` → `go build ./...` | http://localhost:8081 |

vite.config.ts:

```ts
server: {
  proxy: { '/api': 'http://localhost:8081' }
}
build: {
  outDir: '../pkg/admin/webui/dist',
  emptyOutDir: true
}
```

---

## 8. 后端改动清单

```
新增:
  pkg/admin/webui.go            # go:embed 静态服务 + SPA fallback
  pkg/admin/webui/dist/...      # 构建产物(提交到 git)
修改:
  pkg/admin/server.go           # registerRoutes 末尾调 registerWebUI
  push-github-oss.sh            # 确认 webui 已纳入清单(文档已列,核对)
  .gitignore                    # 确认不忽略 pkg/admin/webui/dist
```

> main.go 无需改动——AdminServer 内完成静态服务注册。

---

## 9. 前端依赖

```json
dependencies: {
  "vue": "^3.4",
  "vue-router": "^4.3",
  "element-plus": "^2.7",
  "@element-plus/icons-vue": "^2.3",
  "axios": "^1.7"
}
devDependencies: {
  "typescript": "^5.4",
  "vite": "^5.2",
  "@vitejs/plugin-vue": "^5.0",
  "vue-tsc": "^2.0"
}
```

---

## 10. 决策记录

| # | 决策 | 理由 |
|---|------|------|
| 1 | Vue3 + Vite + TS + Element Plus | 后台管理事实标准组合,组件覆盖全需求 |
| 2 | go:embed 嵌入 dist,单二进制 | 契合"单二进制/零依赖"定位;push-github-oss.sh 已含 webui |
| 3 | 前端源码 webui/ + 产物 pkg/admin/webui/dist | embed 路径不能 `..`,产物需在 admin 包内 |
| 4 | dist 提交到 git | 单二进制开箱即用,免 CI 构建前端 |
| 5 | 无登录 | OSS 无 RBAC,与后端一致;Enterprise 迭代 |
| 6 | 首版无前端单测/图表/i18n | YAGNI |
| 7 | axios 统一拦截错误 | 一处处理 HTTP/业务/网络三类错误 |
