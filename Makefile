VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS := -X github.com/druidcaesa/neuralgate/pkg/core.Version=$(VERSION) \
           -X github.com/druidcaesa/neuralgate/pkg/core.BuildTime=$(BUILD_TIME) \
           -X github.com/druidcaesa/neuralgate/pkg/core.GitCommit=$(GIT_COMMIT)

.PHONY: build-webui build-oss build-enterprise run test vet dev-webui test-webui

# 构建 webui 前端产物(vite build → pkg/admin/webui/dist,go:embed 使用)
build-webui:
	cd webui && npm run build

# 一键打包:先构建最新前端,再编译单二进制(避免遗漏前端变更用旧产物)
build-oss: build-webui
	go build -tags oss -ldflags "$(LDFLAGS)" -o neuralgate ./cmd/gateway/

build-enterprise: build-webui
	go build -tags enterprise -ldflags "$(LDFLAGS)" -o neuralgate-enterprise ./cmd/gateway/

run:
	go run -tags oss ./cmd/gateway/ -config config.yaml

test:
	go test -tags oss ./...

vet:
	go vet -tags oss ./...

# 前端开发模式(vite dev,proxy /api → :8081)
dev-webui:
	cd webui && npm run dev

# 前端类型检查
test-webui:
	cd webui && npx vue-tsc --noEmit
