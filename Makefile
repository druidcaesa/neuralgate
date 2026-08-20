VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS := -X github.com/druidcaesa/neuralgate/pkg/core.Version=$(VERSION) \
           -X github.com/druidcaesa/neuralgate/pkg/core.BuildTime=$(BUILD_TIME) \
           -X github.com/druidcaesa/neuralgate/pkg/core.GitCommit=$(GIT_COMMIT)

.PHONY: build-oss build-enterprise run test vet

build-oss:
	go build -tags oss -ldflags "$(LDFLAGS)" -o neuralgate ./cmd/gateway/

build-enterprise:
	go build -tags enterprise -ldflags "$(LDFLAGS)" -o neuralgate-enterprise ./cmd/gateway/

run:
	go run -tags oss ./cmd/gateway/ -config config.yaml

test:
	go test -tags oss ./...

vet:
	go vet -tags oss ./...
