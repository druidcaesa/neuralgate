package adapter

import (
	"errors"
	"net/http"
)

// ZhipuAdapter 智谱 AI 适配器（异构上游，需转换）
type ZhipuAdapter struct{}

// NewZhipuAdapter 创建智谱 AI 适配器
func NewZhipuAdapter() *ZhipuAdapter { return &ZhipuAdapter{} }

func (a *ZhipuAdapter) Name() string { return "zhipu" }

func (a *ZhipuAdapter) SupportsNativeProxy() bool { return false }

func (a *ZhipuAdapter) TransformRequest(req *UnifiedRequest, rawBody []byte) (*http.Request, error) {
	return nil, errors.New("not implemented")
}

func (a *ZhipuAdapter) TransformResponse(resp *http.Response) (*UnifiedResponse, error) {
	return nil, errors.New("not implemented")
}

func (a *ZhipuAdapter) TransformStreamChunk(chunk []byte) (*UnifiedSSEChunk, error) {
	return nil, errors.New("not implemented")
}

func (a *ZhipuAdapter) ParseTokenUsage(resp *http.Response) (int, int, int) { return 0, 0, 0 }

func (a *ZhipuAdapter) ParseStreamUsage(chunk []byte) (int, int, int) { return 0, 0, 0 }

func (a *ZhipuAdapter) ParseError(resp *http.Response) (int, string) { return 0, "" }
