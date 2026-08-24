# Enterprise E1 授权管理 实施计划（已完成）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
> **状态**：全部任务已完成并通过验证（2026-08-24），本文档转为实施记录。

**Goal:** 实现 Enterprise 版授权体系（license 加载 / Ed25519 验签 / 功能门控 / 过期软降级 OSS / 后台展示 / 签发工具），作为后续企业子项目的运行时根与功能开关。

**Architecture:** Ed25519 非对称签名——公钥内置企业版二进制、私钥仅供应商持有；`pkg/license` 提供签发与验签同源的确定性序列化载荷；授权缺失/无效/过期时网关软降级为 OSS 模式运行（不中断服务）；`LicenseGate` 接口注入门控，OSS 兜底 `NopGate` 恒关闭。

**Tech Stack:** Go 标准库 `crypto/ed25519`（零新增依赖）、gin（既有）、Vue3 + Element Plus（既有 webui）。

## Global Constraints

- 所有 `.go` 文件带 Apache-20 许可证头（Copyright 2026 FanYaNan）
- 注释只写方案，不引用阶段编号/计划文档
- module `github.com/druidcaesa/neuralgate`（go 1.26.5），不新增第三方依赖
- 双编译矩阵必须全绿：`go build/test -tags oss ./...` 与 `-tags enterprise`
- 测试统一 `go test -race`
- 中文注释/日志/错误消息；commit 格式 `feat(scope): 中文描述`

---

### Task 1: pkg/license 载荷规范化 ✅ cc319bc

- [x] `pkg/plugin/interface.go`：`LicenseInfo` 补 snake_case json 标签
- [x] `pkg/license/license.go`：`CanonicalPayload(*plugin.LicenseInfo) []byte` 长度前缀确定性序列化（除 signature 外全字段，时间归一 UTC RFC3339）；七个 feature 常量（audit_stream/tamper_proof/privacy/rbac/compliance/mcp_audit/domestic_db）
- [x] `pkg/license/license_test.go`：确定性、字段敏感、时区归一、分隔符安全、json 标签（5 用例）
- [x] `.gitignore`：根锚定 `/license/` 修复误伤 `pkg/license/`，新增排除 `*.pem` `*.key` `license.json`

### Task 2: pkg/license 验签纯函数 ✅ d40a55e

- [x] `pkg/license/verify.go`：
  - 哨兵错误 `ErrEmptyLicense/ErrMissingSignature/ErrBadSignature/ErrSignatureMismatch/ErrExpired/ErrNotYetValid`（供降级状态判定）
  - `Verify(pubKey, info) error`：签名解码 → Ed25519 验签 → 未过期 → 已生效
  - `Sign(privKey, info) (string, error)`：与验签同源载荷
- [x] `pkg/license/verify_test.go`：往返、六字段篡改、错误公钥、签名格式、过期、未生效、nil 与非法密钥（7 用例）

### Task 3: pkg/core 门控接口 ✅ 11fb634

- [x] `pkg/core/license_gate.go`：`LicenseGate` 接口 + `nopGate` + `NopGate()`
- [x] `pkg/core/license_gate_test.go`：恒 false

### Task 4: 企业版校验器 ✅ 1dfb2ab

- [x] `pkg/plugin/enterprise/license.go`（`//go:build enterprise`）：
  - `embeddedPublicKeyBase64` 内置公钥常量 + `EmbeddedPublicKey()`
  - `EnterpriseLicenseValidator`：实现 `plugin.LicenseValidator` 全部方法；隐式满足 `core.LicenseGate`（无反向依赖 pkg/core，避免 import cycle）
  - RWMutex 保护 `info`/`valid` 缓存；授权无效时 `GetLicenseInfo` 仍返回已加载字段供展示
- [x] `factory.go`：`CreateLicenseValidator` 返回真实校验器（公钥常量非法则 panic 暴露构建错误）
- [x] `license_test.go`：9 用例（enterprise 标签下运行）

### Task 5: licensegen 签发工具 ✅ 3033547 + af47107

- [x] `cmd/licensegen/main.go`：子命令 `keygen`（PKCS8 PEM 私钥权限 0600 + 公钥打印）/ `sign`（授权码 NG-ENT-日期-随机hex，缩进 JSON）/ `verify`（公钥自检）
- [x] `main_test.go`：keygen 配对性、PEM 解析拒绝垃圾输入、签发-自检-篡改闭环（3 用例）
- [x] 真实密钥对生成于 `.keys/`（gitignored；注意 macOS 大小写不敏感，目录不可叫 `license`，会与根目录 `LICENSE` 文件冲突）；真实公钥已嵌入 `embeddedPublicKeyBase64`

### Task 6: admin 授权展示 ✅ 7c79f5a

- [x] `pkg/admin/license.go`：`LicenseOverview{Status,Message,Info}` 启动快照；`GET /api/license` 脱敏响应（授权码前 8 位+`****`、不回显签名全文、剩余天数实时计算）
- [x] `server.go`：`NewAdminServer` 增第 5 参 `license *LicenseOverview`（nil=OSS 未授权）
- [x] `router.go` 注册路由；`system.go` 追加 license 概览（status/customer/expires_at/features_count）
- [x] 全部调用点接线：测试文件用 `gofmt -r` 批量补参（带包名前缀的调用需手动改）
- [x] `license_test.go`：6 用例

### Task 7: 网关启动接线 ✅ 12312a5 + 9be38d8

- [x] `cmd/gateway/main.go` 步骤 7：LoadLicense → Validate → 通过则 `gate = validator` 且 `effectiveEdition = "enterprise"`；失败软降级 `effectiveEdition = "oss"` 仅告警；`gate` 计算后仅日志记录（消费接线由首个企业功能引入）
- [x] `licenseStatus(err)`：`errors.Is(ErrExpired)` → expired，否则 invalid
- [x] `config.yaml` license 段注释完善
- [x] 冒烟发现的三个缺陷修复：有效态残留降级 message、降级分支漏置 oss edition、降级态未回填业务字段

### Task 8: webui 授权卡片 ✅ 27a28f9

- [x] `types/index.ts`：`LicenseDetail` + SystemInfo.license 概览类型
- [x] `api/system.ts`：`getLicense()`
- [x] `views/SystemInfo.vue`：授权信息卡片（状态标签/客户/脱敏授权码/到期/剩余天数/上限/功能标签），非有效态 el-alert 显著提示降级
- [x] `vue-tsc --noEmit` 通过；`npm run build` 产物同步提交

### Task 9: 全量验证 ✅ d6a3650

- [x] `go vet ./...` + `-tags enterprise`（修复 e2e_test.go 两处漏改调用点）
- [x] `go build` 双标签通过
- [x] `go test -race`：oss 166 用例、enterprise 175 用例全绿
- [x] 端到端冒烟（真实密钥 + 企业版二进制）：

| 场景 | 日志 | /api/system | /api/license |
|------|------|-------------|--------------|
| 有效授权 | 授权校验通过 | edition=enterprise, status=valid | status=valid, message 空, days_remaining=364 |
| 缺失文件 | 未检测到授权文件 | edition=oss, status=missing | status=missing |
| 过期授权 | 授权无效或已过期 | edition=oss, status=expired | status=expired + 业务字段尽力展示 |
| 篡改字段 | 签名验证失败 | edition=oss, status=invalid | status=invalid |

## Self-Review 结论

- 规格覆盖：设计文档 §2 六项决策全部落地；§5 后台三态响应、§6 签发工具、§7 测试策略逐项对应任务
- 类型一致性：接口签名以 `plugin.LicenseValidator`/`core.LicenseGate` 为准，各任务引用一致
- 冒烟驱动的修正均已回归双矩阵测试
