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

//go:build enterprise

package enterprise

import (
	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
)

// enterpriseFactory Enterprise 工厂：全部复用 OSS 实现
type enterpriseFactory struct {
	storage plugin.StoragePlugin
}

// NewPluginFactory 返回 Enterprise 版插件工厂
func NewPluginFactory() plugin.PluginFactory {
	return &enterpriseFactory{}
}

// CreateStorage 创建存储（当前：内存存储）
func (f *enterpriseFactory) CreateStorage() plugin.StoragePlugin {
	if f.storage == nil {
		f.storage = oss.NewMemStorage()
	}
	return f.storage
}

// CreateAuditor 创建审计器（当前：简单审计）
func (f *enterpriseFactory) CreateAuditor() plugin.AuditPipeline {
	return oss.NewSimpleAuditor(f.CreateStorage())
}

// CreateRateLimiter 创建限流器（当前：内存限流）
func (f *enterpriseFactory) CreateRateLimiter() plugin.RateLimitPlugin {
	return oss.NewMemRateLimiter()
}

// CreateExporter 日志外推（当前未实现，返回 nil）
func (f *enterpriseFactory) CreateExporter() plugin.LogExporter { return nil }

// CreateLicenseValidator 授权校验（当前未实现，返回 nil）
func (f *enterpriseFactory) CreateLicenseValidator() plugin.LicenseValidator { return nil }
