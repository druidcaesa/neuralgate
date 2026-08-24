# Enterprise E1 授权管理 — 设计文档

> **日期**: 2026-08-24
> **目标**: 实现 Enterprise 版授权体系(license 加载/Ed25519 验签/功能门控/过期软降级 OSS/后台展示/签发工具),作为后续所有 Enterprise 子项目(E2-E8)的运行时根与功能开关
> **前置**: OSS 全部完成;Enterprise 骨架仅 `pkg/plugin/enterprise/factory.go`(56 行,全部委托 OSS);`plugin.LicenseInfo`/`plugin.LicenseValidator` 接口已在 `pkg/plugin/interface.go` 定义;`config.LicenseConfig{FilePath, OfflineMode}` 已存在
> **版本**: V1.0

---

## 1. 背景与现状

### 1.1 现状

- `enterprise` 编译标签产出的二进制**行为与 OSS 完全一致**:`enterpriseFactory` 全部委托 OSS 实现,`CreateLicenseValidator` 返回 `nil`。
- 接口预留齐全但无实现:`plugin.LicenseValidator`(LoadLicense/Validate/HasFeature/CheckNodeLimit/CheckTenantLimit/GetLicenseInfo)、`plugin.LicenseInfo`(LicenseKey/ProductName/CustomerName/MaxNodes/MaxTenants/IssuedAt/ExpiresAt/Features/Signature/IsOffline)。
- `edition` 是编译期常量(`cmd/gateway/factory_oss.go`="oss",`factory_enterprise.go`="enterprise")。
- `main.go` 装配顺序:factory → storage → auditor → rateLimiter → pipeline → adminServer;`CreateLicenseValidator()` 返回值当前**无人使用**。
- `config.LicenseConfig{FilePath, OfflineMode}` 已存在;config.yaml 有 `license:` 段(标注 Enterprise only)。
- `pkg/admin/system.go` 的 `GET /api/system` 返回 version/edition/uptime/db_status/audit_queue_status/rate_limiter_status。

### 1.2 本次目标

1. Enterprise 二进制启动时加载并验证 license,决定运行在 enterprise 还是(软降级)oss 模式。
2. 提供功能门控 `LicenseGate`,供 E2-E8 判断某企业功能是否被授权。
3. 提供签发工具 `cmd/licensegen`(供应商侧生成密钥对与签发 license)。
4. 管理后台可查看授权状态(`GET /api/license` + `/api/system` 加 license 概览 + webui 展示)。

### 1.3 非目标(YAGNI,留后续)

- 集群多节点心跳/节点数动态统计 —— 单机部署 `currentNodes` 恒计 1。
- 在线授权服务器 / license 吊销列表(CRL)。
- 管理后台 8081 端口 TLS —— 内网假设。
- 具体企业功能(全量审计/防篡改/RBAC/SIEM/信创存储)—— 分别是 E2-E8,本项目只提供门控能力,不实现任何被门控的功能。
- 授权功能名(feature 字符串)与具体子项目的绑定 —— 本项目定义 feature 常量清单,但不在任何业务路径消费它们(消费在 E2-E8)。

---

## 2. 关键决策(已与用户确认)

| # | 决策 | 理由 |
|---|------|------|
| 1 | **Ed25519 非对称签名** | 公钥内置网关二进制(可公开),私钥仅供应商持有;客户拿不到私钥即无法伪造。标准库 `crypto/ed25519`,无外部依赖 |
| 2 | **过期/无效 → 软降级回退 OSS** | license 缺失/过期/验签失败时网关照常启动,仅企业功能关闭(`HasFeature` 全 false);对齐 PRD 3.10「降级为 OSS 模式运行」 |
| 3 | **节点数字段就位,单机计 1** | max_nodes 参与签名与展示,`CheckNodeLimit` 传 currentNodes=1 恒过;集群心跳留后续 |
| 4 | **门控用 `LicenseGate` 接口注入(非全局单例)** | 显式依赖、可测试(mock)、OSS 传 nop-gate 恒 false;与现有 factory 注入模式一致 |
| 5 | **内置 `cmd/licensegen` 签发工具** | Ed25519 需持私钥的签发端;keygen 生成密钥对、sign 签发 license,形成完整闭环 |
| 6 | **API + webui 展示授权** | 对齐 PRD 3.10 后台首页显示授权状态;`GET /api/license`(脱敏)+ system 概览 + webui 授权卡片 |

---

## 3. 架构

### 3.1 单元与文件

```
新增:
  cmd/licensegen/main.go              # 签发工具(无 BuildTag,独立二进制)
                                       #   keygen  → 生成 Ed25519 密钥对(私钥 *.pem 本地留存,公钥打印)
                                       #   sign    → 私钥签发 license.json(customer/max_nodes/features/expires...)
                                       #   verify  → (可选)本地用公钥自检签名
  pkg/license/                        # 无 BuildTag 共享包(签名载荷规范化 + 验签纯函数,两个版本+工具都可用)
    license.go                         #   CanonicalPayload(info) []byte:除 signature 外字段的确定性序列化
    verify.go                          #   Verify(pubKey, info) error:Ed25519 验签 + 过期检查(纯函数,不碰文件)
    verify_test.go
  pkg/plugin/enterprise/license.go    # //go:build enterprise
                                       #   EnterpriseLicenseValidator 实现 plugin.LicenseValidator + core.LicenseGate
                                       #   内置公钥常量 embeddedPublicKey;LoadLicense 读文件→反序列化→缓存
  pkg/plugin/enterprise/license_test.go
  pkg/core/license_gate.go            # 无 BuildTag:LicenseGate 接口 + nopGate(恒 false)
  pkg/admin/license.go                # GET /api/license(脱敏展示)
修改:
  pkg/plugin/enterprise/factory.go    # CreateLicenseValidator 返回真实 validator(OSS factory 仍 nil)
  cmd/gateway/main.go                 # 启动校验 license → effectiveEdition + gate 注入 pipeline/admin
  pkg/core/pipeline.go 或 proxy.go    # (仅接受 gate 注入;E1 不在任何路径消费,占位供 E2 用)
  pkg/admin/server.go                 # NewAdminServer 增 licenseInfo 展示源(或 validator 引用)
  pkg/admin/system.go                 # /api/system 加 license 概览(customer/expires/status/features 计数)
  pkg/admin/router.go                 # 注册 GET /api/license
  webui/src/...                       # 授权展示(SystemInfo 页加授权卡片 或 新增 License 视图)
  config.yaml                         # license 段补注释(file_path/offline_mode 已存在)
  .gitignore                          # 排除 *.pem / *.key / license.json(私钥与授权文件不进仓库)
```

### 3.2 信任模型与 Ed25519

- **密钥对**:`licensegen keygen` 用 `ed25519.GenerateKey` 生成。私钥写本地 `license_private.pem`(供应商保管,**绝不进仓库**);公钥以 base64/hex 打印,由开发者**硬编码**进 `pkg/plugin/enterprise/license.go` 的 `embeddedPublicKey` 常量。
- **签名载荷**:`pkg/license.CanonicalPayload(info)` 对 `LicenseInfo` 除 `Signature` 外的所有字段做**确定性序列化**(固定字段顺序,时间用 RFC3339 UTC,Features 原序)。签发与验签必须用同一函数,保证字节一致。
- **签发**:`sign` = `ed25519.Sign(privKey, CanonicalPayload(info))` → base64 → 填入 `info.Signature` → 写 license.json。
- **验签**:`Verify(pubKey, info)` = `ed25519.Verify(pubKey, CanonicalPayload(info), decode(info.Signature))`;失败返回 error。任何字段被篡改都导致载荷变化 → 验签失败。

### 3.3 LicenseGate 门控接口(方案 A)

```go
// pkg/core/license_gate.go — 无 BuildTag,两版本共用
package core

// LicenseGate 功能门控:企业功能在执行前询问是否被授权
type LicenseGate interface {
    HasFeature(feature string) bool
}

// nopGate 未授权门控:恒 false(OSS 编译 / license 无效时的兜底)
type nopGate struct{}
func (nopGate) HasFeature(string) bool { return false }

// NopGate 返回恒关闭的门控
func NopGate() LicenseGate { return nopGate{} }
```

- `EnterpriseLicenseValidator` 实现 `HasFeature`(license 有效且 feature 在 Features 列表→true;降级态→false),因此天然满足 `LicenseGate`。
- `main.go` 决策:enterprise 编译且 license 有效 → gate = validator;否则 gate = `core.NopGate()`。
- **E1 不消费 gate,故不预先注入 pipeline**(避免死代码)。`main.go` 计算出 `gate` 后仅用于日志与后台展示;`gate` 的持有由**首个消费者 E2** 引入(那时 pipeline/proxy 才加 gate 字段)。E1 交付的是 `LicenseGate` 接口 + `NopGate` + validator 实现三者,消费接线随 E2 落地。

### 3.4 启动数据流(cmd/gateway/main.go)

```
factory := newPluginFactory()
validator := factory.CreateLicenseValidator()   // enterprise→真实;oss→nil
effectiveEdition := edition                       // 编译常量作起点
var gate core.LicenseGate = core.NopGate()

if validator != nil {                             // enterprise 编译
    info, err := validator.LoadLicense(cfg.License.FilePath)
    if err != nil {
        logger.Warn("未检测到授权，以开源模式运行", zap.Error(err))
        effectiveEdition = "oss"                  // 软降级
    } else if ok, verr := validator.Validate(info); !ok {
        logger.Warn("授权已过期或无效，降级运行", zap.Error(verr))
        effectiveEdition = "oss"                  // 软降级
    } else {
        gate = validator                          // 企业模式
        logger.Info("授权校验通过",
            zap.String("customer", info.CustomerName),
            zap.Time("expires_at", info.ExpiresAt),
            zap.Strings("features", info.Features))
    }
}
// effectiveEdition 传 adminServer(展示);gate 计算出但 E1 暂不注入 pipeline(E2 消费时接线);
// validator(可能 nil)传 adminServer 供 /api/license 展示(nil→未授权响应)
```

> **注意**:`effectiveEdition` 取代原先直接用编译常量 `edition` 传给 adminServer。oss 编译时 validator=nil,effectiveEdition 保持 "oss",gate 保持 NopGate,行为与现在完全一致。
> **gate 去向**:E1 计算出 `gate` 仅为验证接口可用 + 打印授权功能;不改 pipeline/proxy 签名。首个消费 gate 的子项目(E2)再引入 pipeline 持有。

### 3.5 Validate 校验链

```
Validate(info):
  1. Signature 非空且 base64 可解码            否则 → (false, err "签名缺失/格式错误")
  2. license.Verify(embeddedPublicKey, info)   否则 → (false, err "签名验证失败")  ← 防篡改
  3. now ≤ ExpiresAt                            否则 → (false, err "授权已过期")
  4. (可选) now ≥ IssuedAt                       否则 → (false, err "授权尚未生效")
  → (true, nil)
```

时间比较用 `time.Now()`(真实运行环境,非 workflow 脚本)。

---

## 4. license.json 结构

```json
{
  "license_key":   "NG-ENT-20260824-XXXX",
  "product_name":  "NeuralGate Enterprise",
  "customer_name": "示例科技有限公司",
  "max_nodes":     3,
  "max_tenants":   50,
  "issued_at":     "2026-08-24T00:00:00Z",
  "expires_at":    "2027-08-24T00:00:00Z",
  "features":      ["audit_stream", "tamper_proof", "privacy", "rbac", "compliance", "mcp_audit", "domestic_db"],
  "is_offline":    true,
  "signature":     "base64(ed25519_sign(canonical_payload))"
}
```

- 签名载荷 = 除 `signature` 外全部字段的规范序列化。
- `features` 字符串常量(在 `pkg/license` 定义,供 E2-E8 引用):`audit_stream`(E2)、`tamper_proof`(E3)、`privacy`(E4)、`rbac`(E5)、`compliance`(E6)、`mcp_audit`(E7)、`domestic_db`(E8)。E1 仅定义,不消费。

---

## 5. 管理后台

### 5.1 GET /api/license(新增,pkg/admin/license.go)

- **enterprise + 有效**:返回 license_key(**脱敏**:前 8 位 + `****`)、product_name、customer_name、max_nodes、max_tenants、issued_at、expires_at、features、is_offline、`status:"valid"`、`days_remaining`。**不回显 signature 全文**(可返回布尔 `signed:true` 或 signature 前 16 位)。
- **enterprise + 降级(过期/无效/缺失)**:`status:"expired"|"invalid"|"missing"` + `message`,业务字段尽力展示(过期 license 仍可读字段;缺失则空)。
- **oss 编译**:`status:"oss"`、`message:"开源版本，无授权信息"`。

统一响应包装 `{code,message,data}`(与现有 admin API 一致)。

### 5.2 /api/system 增 license 概览

在现有 gin.H 追加:`"license": {"status":..., "customer":..., "expires_at":..., "features_count":N}`;`edition` 字段改为返回 `effectiveEdition`(降级后为 oss)。

### 5.3 webui 展示

SystemInfo 页新增「授权信息」el-descriptions 卡片(customer/edition/status/expires_at/days_remaining/features 标签),或独立 License 视图。降级态显著提示「未授权/已过期，以开源模式运行」。webui 改动最小化,复用现有 system API 或调 `/api/license`。

---

## 6. cmd/licensegen 签发工具

无 BuildTag,独立编译。子命令:

```
licensegen keygen -out ./                      # 生成 license_private.pem + 打印公钥(base64)供硬编码
licensegen sign   -key license_private.pem \   # 用私钥签发
    -customer "示例科技" -max-nodes 3 -max-tenants 50 \
    -features audit_stream,tamper_proof,rbac \
    -expires 2027-08-24 -out license.json
licensegen verify -pub <base64> -license license.json   # 本地自检(可选)
```

- keygen 私钥文件权限 0600;公钥打印到 stdout。
- sign 组装 `LicenseInfo` → `CanonicalPayload` → `ed25519.Sign` → 写 license.json(缩进 JSON)。
- 复用 `pkg/license.CanonicalPayload`/`Verify`,与网关验签**同源**,保证签发即可验。

---

## 7. 测试策略(TDD,全部 `go test -race`)

| 单元 | 测试点 |
|------|--------|
| `pkg/license` CanonicalPayload | 同一 info 多次序列化字节稳定;字段顺序/时间格式确定 |
| `pkg/license` Verify | 合法签名通过;篡改任一字段(customer/expires/features/max_nodes)→失败;错误公钥→失败;签名格式错误→失败 |
| Verify 过期 | now>expires→过期 err;now<issued→未生效 err;有效期内→通过 |
| licensegen(内部函数) | keygen 产出的私钥签发的 license 能被对应公钥 Verify 通过(端到端签发-验签闭环) |
| EnterpriseLicenseValidator | LoadLicense 读取/反序列化;文件缺失→err;Validate 各分支;HasFeature 授权 true/未授权 false;GetLicenseInfo 缓存 |
| CheckNodeLimit/CheckTenantLimit | currentNodes=1 恒过;超 max 边界(为未来集群留断言) |
| LicenseGate | NopGate.HasFeature 恒 false;validator 作为 gate 有效 license 对应 feature true |
| admin `/api/license` | enterprise 有效→脱敏字段齐全且无 signature 全文;降级→status 正确;oss 编译→status:"oss" |
| admin `/api/system` | 含 license 概览;降级后 edition=oss |
| 门控隔离 | oss build tag 下 gate 恒 false(用 `go test -tags ''` 与 `-tags enterprise` 双跑关键用例) |

**编译矩阵**:`go build ./... && go build -tags enterprise ./...` 均通过;`go test ./...`(oss)与 `go test -tags enterprise ./...` 均绿。

---

## 8. 涉及文件清单汇总

```
新增:
  cmd/licensegen/main.go
  pkg/license/license.go            # CanonicalPayload + feature 常量
  pkg/license/verify.go             # Verify(纯函数)
  pkg/license/verify_test.go
  pkg/core/license_gate.go          # LicenseGate 接口 + NopGate
  pkg/plugin/enterprise/license.go  # //go:build enterprise:validator + 内置公钥
  pkg/plugin/enterprise/license_test.go
  pkg/admin/license.go              # GET /api/license
修改:
  pkg/plugin/enterprise/factory.go  # CreateLicenseValidator 返回真实实现
  cmd/gateway/main.go               # 启动校验 + effectiveEdition + gate 计算(不注入 pipeline)
  pkg/admin/server.go               # NewAdminServer 加 license 展示源
  pkg/admin/system.go               # /api/system 加 license 概览
  pkg/admin/router.go               # 注册 /api/license
  webui/src/views/SystemInfo.vue(+types/api)  # 授权展示卡片
  webui/ 构建产物 pkg/admin/webui/dist          # 重新 build
  config.yaml                       # license 段注释完善
  .gitignore                        # *.pem *.key license.json
```

---

## 9. 决策记录

| # | 决策 | 理由 |
|---|------|------|
| 1 | Ed25519 非对称,公钥硬编码进二进制 | 防伪造根;私钥不进仓库 |
| 2 | 过期/无效软降级 OSS(不 fatal) | PRD 3.10;可用性优先 |
| 3 | 节点数字段就位单机计 1 | YAGNI;集群留后续 |
| 4 | LicenseGate 接口注入,非全局单例 | 可测试、显式依赖、OSS nop 兜底 |
| 5 | 内置 licensegen(keygen/sign/verify) | Ed25519 需签发端;签发-验签同源 `pkg/license` |
| 6 | `pkg/license` 无 BuildTag 共享 | 工具/oss/enterprise 三方复用规范化+验签,避免签发验签不一致 |
| 7 | effectiveEdition 取代编译常量传后台 | 降级后 edition 如实反映为 oss |
| 8 | GET /api/license 脱敏,不回显 signature 全文/私钥 | 后台展示安全 |
| 9 | E1 只建门控能力不消费 | feature 消费属于 E2-E8;E1 是根与开关 |
