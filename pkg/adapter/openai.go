package adapter

import (
	"errors"
	"net/http"
)

// OpenAIAdapter OpenAI 适配器（原生兼容：入口协议与上游一致，原样透传）
type OpenAIAdapter struct{}

// NewOpenAIAdapter 创建 OpenAI 适配器
func NewOpenAIAdapter() *OpenAIAdapter { return &OpenAIAdapter{} }

func (a *OpenAIAdapter) Name() string { return "openai" }

func (a *OpenAIAdapter) SupportsNativeProxy() bool { return true }

// 以下转换方法为骨架期 stub，Phase 5 填充实现
func (a *OpenAIAdapter) TransformRequest(req *UnifiedRequest, rawBody []byte) (*http.Request, error) {
	return nil, errors.New("not implemented")
}

func (a *OpenAIAdapter) TransformResponse(resp *http.Response) (*UnifiedResponse, error) {
	return nil, errors.New("not implemented")
}

func (a *OpenAIAdapter) TransformStreamChunk(chunk []byte) (*UnifiedSSEChunk, error) {
	return nil, errors.New("not implemented")
}

func (a *OpenAIAdapter) ParseTokenUsage(resp *http.Response) (int, int, int) { return 0, 0, 0 }

func (a *OpenAIAdapter) ParseStreamUsage(chunk []byte) (int, int, int) { return 0, 0, 0 }

func (a *OpenAIAdapter) ParseError(resp *http.Response) (int, string) { return 0, "" }
