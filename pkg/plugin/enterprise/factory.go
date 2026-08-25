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
	"fmt"

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

// CreateStorage 创建存储（与 OSS 工厂同款：Init 时按 driver 选择 mem/sqlite/mysql）
func (f *enterpriseFactory) CreateStorage() plugin.StoragePlugin {
	if f.storage == nil {
		// 惰性创建：具体驱动由 Init 的 config 决定
		f.storage = oss.NewDynamicStorage()
	}
	return f.storage
}

// CreateAuditor 创建审计器（当前：简单审计）
func (f *enterpriseFactory) CreateAuditor() plugin.AuditPipeline {
	return oss.NewSimpleAuditor(f.CreateStorage())
}

// CreateRateLimiter 创建限流器（三层配置 + 双策略,配置源为共享存储）
func (f *enterpriseFactory) CreateRateLimiter() plugin.RateLimitPlugin {
	return oss.NewRateLimiter(f.CreateStorage(), 10, 100000, "token_bucket")
}

// CreateExporter 审计日志外推器（存储尾随；经 Init 配置目标后启动循环）
func (f *enterpriseFactory) CreateExporter() plugin.LogExporter {
	return NewTailExporter(f.CreateStorage())
}

// CreateLicenseValidator 创建企业版授权校验器（内置供应商公钥；
// 公钥常量非法属构建错误，启动即暴露）
func (f *enterpriseFactory) CreateLicenseValidator() plugin.LicenseValidator {
	pub, err := EmbeddedPublicKey()
	if err != nil {
		panic(fmt.Sprintf("内置授权公钥无效: %v", err))
	}
	v, err := NewEnterpriseLicenseValidator(pub)
	if err != nil {
		panic(fmt.Sprintf("创建授权校验器失败: %v", err))
	}
	return v
}
