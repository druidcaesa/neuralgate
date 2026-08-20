# NeuralGate

AI 大模型治理网关：LLM 流量代理 + 审计 + 限流 + 模型路由（双服务隔离架构）。

- 代理服务 `:8080`：纯 net/http，处理 LLM 流量（高并发/流式/SSE）
- 管理后台 `:8081`：Gin，配置管理与日志查询

## 快速开始（骨架阶段）

```bash
make build-oss        # 编译 OSS 版（产物 neuralgate）
./neuralgate -config config.yaml
curl :8080/healthz    # 代理健康检查
curl :8081/healthz    # 后台健康检查
```

## 双版本编译

| 版本 | 命令 | 说明 |
|------|------|------|
| OSS | `make build-oss` | 开源版 |
| Enterprise | `make build-enterprise` | 含商业插件（达梦/金仓/Redis/SIEM/授权） |

## 目录结构

- `cmd/gateway/` 程序入口（双服务启动）
- `pkg/core/` 内核四层（接入层/管道层/代理内核/断连处理）
- `pkg/adapter/` 模型适配器（OpenAI/通义/智谱/DeepSeek）
- `pkg/plugin/` 插件层（接口 + oss 共享实现 + enterprise 商业实现）
- `pkg/admin/` Gin 管理后台
- `pkg/config/` 配置加载

## 双仓库发布

- `./push-private.sh "msg"` 全量推送到私有仓库
- `./push-github-oss.sh "msg"` 过滤 enterprise 后推送到 GitHub
