package adapter

import (
	"errors"
	"net/http"
)

// DeepSeekAdapter DeepSeek 适配器（原生兼容：入口协议与上游一致，原样透传）
type DeepSeekAdapter struct{}

// NewDeepSeekAdapter 创建 DeepSeek 适配器
func NewDeepSeekAdapter() *DeepSeekAdapter { return &DeepSeekAdapter{} }

func (a *DeepSeekAdapter) Name() string { return "deepseek" }

func (a *DeepSeekAdapter) SupportsNativeProxy() bool { return true }

// 以下转换方法为骨架期 stub，Phase 5 填充实现
func (a *DeepSeekAdapter) TransformRequest(req *UnifiedRequest, rawBody []byte) (*http.Request, error) {
	return nil, errors.New("not implemented")
}

func (a *DeepSeekAdapter) TransformResponse(resp *http.Response) (*UnifiedResponse, error) {
	return nil, errors.New("not implemented")
}

func (a *DeepSeekAdapter) TransformStreamChunk(chunk []byte) (*UnifiedSSEChunk, error) {
	return nil, errors.New("not implemented")
}

func (a *DeepSeekAdapter) ParseTokenUsage(resp *http.Response) (int, int, int) { return 0, 0, 0 }

func (a *DeepSeekAdapter) ParseStreamUsage(chunk []byte) (int, int, int) { return 0, 0, 0 }

func (a *DeepSeekAdapter) ParseError(resp *http.Response) (int, string) { return 0, "" }
