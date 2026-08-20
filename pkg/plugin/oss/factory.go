package oss

import "github.com/druidcaesa/neuralgate/pkg/plugin"

// ossFactory OSS 工厂（照设计文档 6.2：仅注册 OSS 实现）
type ossFactory struct {
	storage plugin.StoragePlugin
}

// NewPluginFactory 返回 OSS 版插件工厂
func NewPluginFactory() plugin.PluginFactory {
	return &ossFactory{}
}

// CreateStorage 创建内存存储（骨架期；Phase 3 按 config.driver 分发 mysql/sqlite）
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
