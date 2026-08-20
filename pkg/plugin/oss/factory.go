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

package oss

import "github.com/druidcaesa/neuralgate/pkg/plugin"

// ossFactory OSS 工厂：仅注册 OSS 实现
type ossFactory struct {
	storage plugin.StoragePlugin
}

// NewPluginFactory 返回 OSS 版插件工厂
func NewPluginFactory() plugin.PluginFactory {
	return &ossFactory{}
}

// CreateStorage 创建内存存储
func (f *ossFactory) CreateStorage() plugin.StoragePlugin {
	if f.storage == nil {
		f.storage = NewMemStorage()
	}
	return f.storage
}

// CreateAuditor 创建简单审计器（与 CreateStorage 共享同一存储实例）
func (f *ossFactory) CreateAuditor() plugin.AuditPipeline {
	return NewSimpleAuditor(f.CreateStorage())
}

// CreateRateLimiter 创建内存限流器
func (f *ossFactory) CreateRateLimiter() plugin.RateLimitPlugin {
	return NewMemRateLimiter()
}

// CreateExporter OSS 版无日志外推
func (f *ossFactory) CreateExporter() plugin.LogExporter { return nil }

// CreateLicenseValidator OSS 版无授权校验
func (f *ossFactory) CreateLicenseValidator() plugin.LicenseValidator { return nil }
