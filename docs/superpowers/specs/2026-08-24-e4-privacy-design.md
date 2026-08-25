# Enterprise E4 数据隐私合规(privacy)— 设计文档

> **日期**: 2026-08-24
> **目标**: 实现 PRD 3.6 四项——PII 双向动态脱敏、Prompt 注入拦截(403+安全事件留痕)、脱敏/注入规则库 CRUD、风控白名单；输出内容风控延后(语义未定义)
> **前置**: E1 门控(`license.FeaturePrivacy`);E3 确立的接口扩展/setupXxx BuildTag 接线模式;Pipeline.Use() 中间件机制已就绪
> **版本**: V1.0

---

## 1. 背景与现状

PRD 3.6 五个子功能中「输出内容风控」无判定标准与命中动作定义，本期缓后。现有"脱敏"仅 Authorization 头移除；Pipeline 具备 fixedChain(Auth/Route/Limit)+Use() 追加机制，隐私中间件挂 auth 之后、proxy 之前。

## 2. 目标 / 非目标

**目标**：
1. PII 动态脱敏：请求侧替换后转发（上游收到脱敏后内容），审计记录脱敏后文本；响应侧按 scope 替换
2. Prompt 注入拦截：命中→403+安全事件入库+审计 status=403
3. 规则库：privacy_rules 表 CRUD（rule_type=pii/injection 统一存取），内置种子随建表写入
4. 白名单：命中白名单正则的请求整体跳过脱敏与注入检测

**非目标(YAGNI)**：
- 输出内容风控——待后续迭代定义敏感词集与命中动作后补齐
- 规则热更新回调耦合——采用 30s TTL 自动重载（CRUD 生效延迟 ≤30s）
- 按租户/模型维度的规则作用域——全局生效
- 流式响应跨分片 PII 匹配——单分片内完整匹配才替换，边界漏检为已知局限

## 3. 关键决策(已与用户确认)

| # | 决策 | 理由 |
|---|------|------|
| 1 | 范围四项,输出风控缓后 | PRD 无判定标准与命中动作,自行发明风险高 |
| 2 | 规则/白名单入库+CRUD | 对齐 PRD 字段约束的管理实体语义;运行时可调免重启 |
| 3 | 注入特征同表分类(rule_type)可配置 | 与 PII 共用 CRUD/白名单/TTL 重载机制;种子数据随建表写入 |
| 4 | privacy.enabled 默认 false | 企业版升级后流量行为零惊扰;需显式开启+授权双条件 |
| 5 | 引擎 30s TTL 重载 | 免 admin↔engine 回调耦合;延迟可接受 |
| 6 | 异常降级放行+Warn | PRD「脱敏异常→降级放行」;可用性优先 |

## 4. 架构

### 4.1 文件清单

```
修改 pkg/plugin/interface.go      # PrivacyRule/WhitelistEntry/SecurityEvent 结构;
                                   # StoragePlugin += rules/whitelist CRUD + events 写查
修改 oss storage_mem/sqlite/mysql/dynamic  # 两新表建表+种子写入(sqlite/mysql)+方法实现
新增 enterprise/privacy_engine.go # PrivacyEngine:缓存+TTL重载+Sanitize/DetectInjection/Whitelisted
新增 enterprise/privacy_middleware.go # core.Middleware 形态:白名单→注入403→request脱敏;
                                       # 响应侧包装(response scope,流式逐分片)
新增 enterprise/privacy_seed.go   # 内置规则种子(见 §5)
新增 cmd/gateway/privacy_oss.go / privacy_enterprise.go  # setupPrivacy 按 BuildTag 两版
修改 cmd/gateway/main.go          # 步骤10 setupPrivacy 接线;关闭无需收尾(无后台任务)
修改 config.yaml                  # privacy.enabled 开关注释
新增 pkg/admin/privacy.go         # rules/whitelist/events 三组 API(路由在 router.go 注册)
新增 webui views/PrivacyRules.vue / SecurityEvents.vue + api/privacy.ts + 路由菜单
各对应 _test.go
```

### 4.2 数据模型

```go
// PrivacyRule 脱敏/注入检测规则
type PrivacyRule struct {
    ID          string    `json:"id"`
    RuleType    string    `json:"rule_type"` // pii | injection
    Name        string    `json:"name"`      // 1-64字符
    Pattern     string    `json:"pattern"`   // 合法正则
    Replacement string    `json:"replacement"` // 1-128字符(injection 忽略)
    Scope       string    `json:"scope"`     // request|response|both(pii 有效,injection 恒 request)
    Enabled     bool      `json:"enabled"`
    CreatedAt   time.Time `json:"created_at"`
    UpdatedAt   time.Time `json:"updated_at"`
}

// PrivacyWhitelistEntry 白名单:请求内容命中 pattern 则跳过全部隐私检查
type PrivacyWhitelistEntry struct {
    ID string `json:"id"`; Pattern string `json:"pattern"`; Enabled bool `json:"enabled"`
    Note string `json:"note"`; CreatedAt time.Time `json:"created_at"`
}

// SecurityEvent 注入拦截安全事件
type SecurityEvent struct {
    ID string `json:"id"`; RequestID string `json:"request_id"`
    RuleName string `json:"rule_name"`; Snippet string `json:"snippet"` // 截断256字符
    ClientIP string `json:"client_ip"`; ModelName string `json:"model_name"`
    CreatedAt time.Time `json:"created_at"`
}
```

StoragePlugin 新增：SavePrivacyRule/DeletePrivacyRule/ListPrivacyRules(ruleType *string)/
SavePrivacyWhitelistEntry/DeletePrivacyWhitelistEntry/ListPrivacyWhitelistEntries/
SaveSecurityEvent/ListSecurityEvents(page,size)。种子写入仅在表首次创建(空表)时执行。

### 4.3 引擎与中间件

```
PrivacyEngine:
  缓存: piiRules/injectionRules/whitelist 三组编译后正则;lastLoaded 时间戳
  get(): TTL 超 30s → 重载(禁用条目与非法正则跳过+Warn)
  Whitelisted(body) bool → Sanitize(body, scope) (text, changed)
  DetectInjection(body) *命中规则
中间件(顺序):
  1. enabled/gate 未过 → 直接 next(零开销路径)
  2. Whitelisted(body) → next
  3. DetectInjection 命中 → SaveSecurityEvent 留痕 + 中间件自行 Submit+Finalize
     一条审计记录(status=403,确保短路路径留痕) + 返回
     403 JSON {"error":{"message":"请求被安全策略拦截","type":"prompt_injection_blocked"}}
  4. Sanitize(request scope) → 命中则替换请求体(Content-Length 同步修正)并继续
响应侧包装: scope 含 response 的规则对非流式响应体替换后回写;
  流式 SSE 逐分片扫描替换(单分片内完整匹配)
降级: 正则执行 panic/error → 放行原文 + Warn(PRD 降级放行)
```

### 4.4 门控与接线

```
shouldStartPrivacy(gate, enabled): enabled=false→"配置未启用(privacy.enabled=false)";
  缺 feature→"授权未包含 privacy 功能"
setupPrivacy(gate, cfg, pipeline, auditor?, storage, logger) — BuildTag 两版;
  enterprise 版构造 engine 并 pipeline.Use(mw);oss 空操作返回
main 步骤 10(防篡改之后): stopPrivacy 不需要(无后台任务),仅接线
```

### 4.5 管理后台

- `POST/GET/PUT/DELETE /api/privacy-rules`(创建校验:name/pattern/replacement/scope 枚举+正则可编译)
- `POST/GET/DELETE /api/privacy-whitelist`
- `GET /api/security-events?page=&size=`
- webui:PrivacyRules.vue(rule_type 切换/启停开关/编辑弹窗)、SecurityEvents.vue(只读表格分页)、路由菜单两项

## 5. 内置规则种子

**PII**(scope=both):

| name | pattern | replacement |
|------|---------|-------------|
| 手机号脱敏 | `1[3-9]\d{9}` | `1**********`(保留首位) |
| 身份证脱敏 | `\d{17}[\dXx]` | `******************` |
| 银行卡脱敏 | `\d{16,19}` | `****-****-****-****` |
| 邮箱脱敏 | `[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}` | `***@***.***` |

**injection**(恒 request,replacement 空):

| name | pattern |
|------|---------|
| 忽略指令(中) | `忽略(以上|之前|上面)(的)?(所有)?(指令|提示|设定)` |
| 忽略指令(英) | `(?i)ignore\s+(all\s+)?(previous|prior|above)\s+(instructions\|prompts\|rules)` |
| 系统提示窃取 | `(?i)(reveal\|show\|print)\s+(your\s+)?(system\s+)?(prompt\|instructions)` |
| 角色扮演越狱 | `(?i)(pretend\|act)\s+(to\s+be\|as)\s+(a\s+)?(DAN\|jailbreak)` |
| 开发者模式 | `(?i)developer\s+mode` |
| 越权指令(中) | `(你现在是\|扮演).*(不受(任何)?限制\|没有(任何)?规则)` |

种子以 Go 常量切片维护,空表初始化时批量插入。

## 6. 配置

```yaml
privacy:                      # Enterprise only：需 privacy 授权
  enabled: false              # 是否启用隐私防护(默认 false,显式开启)
```

`config.Config` 增加 `PrivacyConfig{Enabled bool}`(bool 不参与 applyDefaults)。

## 7. 测试策略(TDD,go test -race,双编译矩阵)

| 单元 | 测试点 |
|------|--------|
| 种子/建表 | 空表写入种子;重启不重复插入;非法正则跳过加载 |
| Sanitize | 四类 PII 各命中;误报边界(13 位数字不误判银行卡等);scope 过滤;changed 标记 |
| DetectInjection | 中英文样本命中;普通提问放行 |
| 白名单 | 命中跳过脱敏+注入;disabled 条目不豁免 |
| 中间件 | 403 分支+安全事件入库+审计 status=403;脱敏后转发 body 与审计一致;异常降级放行 |
| TTL 重载 | CRUD 后 ≤30s 生效(测试注入短 TTL);引擎并发读安全 |
| 存储 | 两表 CRUD+events 分页三存储一致性(mem/sqlite,mysql NG_MYSQL_DSN) |
| admin/webui | 校验分支(非法正则 400);页面构建 vue-tsc |
| 矩阵 | OSS 行为零变化;enterprise 全绿 |

## 8. 决策记录

| # | 决策 | 理由 |
|---|------|------|
| 1 | 输出风控延后 | 无判定标准;自行发明敏感词集风险高 |
| 2 | rule_type 同表 | 一套 CRUD/白名单/TTL 机制服务两类规则 |
| 3 | TTL 重载而非写回调 | 解耦 admin 与引擎;30s 延迟可接受 |
| 4 | 默认关闭 | 升级零惊扰;显式开启+授权双条件与 E2/E3 一致 |
| 5 | 请求侧替换即转发 | PRD 明确"脱敏替换→继续处理";防 PII 出境 |
| 6 | 流式逐分片替换 | 保 SSE 时序;跨分片漏检注明局限 |
