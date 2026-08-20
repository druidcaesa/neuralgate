package adapter

import (
	"errors"
	"net/http"
)

// TongyiAdapter 通义千问适配器（DashScope 协议，需转换）
type TongyiAdapter struct{}

// NewTongyiAdapter 创建通义千问适配器
func NewTongyiAdapter() *TongyiAdapter { return &TongyiAdapter{} }

func (a *TongyiAdapter) Name() string { return "tongyi" }

func (a *TongyiAdapter) SupportsNativeProxy() bool { return false }

func (a *TongyiAdapter) TransformRequest(req *UnifiedRequest, rawBody []byte) (*http.Request, error) {
	return nil, errors.New("not implemented")
}

func (a *TongyiAdapter) TransformResponse(resp *http.Response) (*UnifiedResponse, error) {
	return nil, errors.New("not implemented")
}

func (a *TongyiAdapter) TransformStreamChunk(chunk []byte) (*UnifiedSSEChunk, error) {
	return nil, errors.New("not implemented")
}

func (a *TongyiAdapter) ParseTokenUsage(resp *http.Response) (int, int, int) { return 0, 0, 0 }

func (a *TongyiAdapter) ParseStreamUsage(chunk []byte) (int, int, int) { return 0, 0, 0 }

func (a *TongyiAdapter) ParseError(resp *http.Response) (int, string) { return 0, "" }
