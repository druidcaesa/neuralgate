// Copyright 2026 FanYaNan. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

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
