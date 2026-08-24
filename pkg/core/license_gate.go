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

package core

// LicenseGate 功能门控：企业功能在执行前询问是否被授权。
// 企业版由授权校验器实现（license 有效且功能在清单内才开启）；
// OSS 版本或授权无效时使用 NopGate 兜底。
type LicenseGate interface {
	HasFeature(feature string) bool
}

// nopGate 未授权门控：对任何功能恒返回 false
type nopGate struct{}

func (nopGate) HasFeature(string) bool { return false }

// NopGate 返回恒关闭的门控实例
func NopGate() LicenseGate { return nopGate{} }
