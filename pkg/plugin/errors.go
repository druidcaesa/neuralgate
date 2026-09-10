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

package plugin

import "errors"

// ErrAPIKeyUnreadable 存储实现无法用当前 encrypt_key 解密该行 api_key(通常因密钥轮换)。
// 属接口契约:实现(oss/enterprise)在 GetModelConfig 命中不可解密行时返回,
// 调用方用 errors.Is 判定并据此走降级/拒绝/告警路径。
var ErrAPIKeyUnreadable = errors.New("api key 无法解密(encrypt_key 可能已轮换),请在模型管理中重新填写该模型的密钥")
