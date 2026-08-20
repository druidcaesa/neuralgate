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

// DeepSeekAdapter DeepSeek 适配器（原生兼容,协议与 OpenAI 完全一致）
type DeepSeekAdapter struct {
	OpenAIAdapter // 复用 OpenAI 格式解析
}

// NewDeepSeekAdapter 创建 DeepSeek 适配器
func NewDeepSeekAdapter() *DeepSeekAdapter { return &DeepSeekAdapter{} }

func (a *DeepSeekAdapter) Name() string { return "deepseek" }

func (a *DeepSeekAdapter) SupportsNativeProxy() bool { return true }
