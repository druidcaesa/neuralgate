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

package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/druidcaesa/neuralgate/pkg/admin"
	"github.com/druidcaesa/neuralgate/pkg/config"
	"github.com/druidcaesa/neuralgate/pkg/machineid"
	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/enterprise"
	"go.uber.org/zap"
)

// licenseManager 校验并持久化上传的授权。校验用独立校验器实例,
// 不触碰运行中门控(激活在重启)——上传坏授权不会关停在线功能。
type licenseManager struct {
	validator *enterprise.EnterpriseLicenseValidator
	filePath  string
	logger    *zap.Logger
}

// LocalMachineID 返回本机机器码(供页面展示复制)
func (m *licenseManager) LocalMachineID() string {
	fp, err := machineid.Fingerprint()
	if err != nil {
		m.logger.Warn("获取本机机器码失败", zap.Error(err))
		return ""
	}
	return fp
}

// ApplyUpload 解析→验签+设备指纹+有效期→持久化;成功返回解析后的授权
func (m *licenseManager) ApplyUpload(raw []byte) (*plugin.LicenseInfo, error) {
	var info plugin.LicenseInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, fmt.Errorf("授权文件格式错误: %w", err)
	}
	if ok, err := m.validator.Validate(&info); !ok {
		return nil, err // 验签/过期/设备不匹配的具体原因
	}
	if err := os.WriteFile(m.filePath, raw, 0o600); err != nil {
		return nil, fmt.Errorf("持久化授权失败: %w", err)
	}
	m.logger.Info("授权上传成功,重启后生效",
		zap.String("customer", info.CustomerName), zap.String("path", m.filePath))
	return &info, nil
}

// setupLicenseManager 构造独立校验器并注入管理后台(enterprise 版)
func setupLicenseManager(cfg config.Config, adminServer *admin.AdminServer, logger *zap.Logger) {
	pub, err := enterprise.EmbeddedPublicKey()
	if err != nil {
		logger.Warn("内置公钥无效,授权上传不可用", zap.Error(err))
		return
	}
	v, err := enterprise.NewEnterpriseLicenseValidator(pub)
	if err != nil {
		logger.Warn("创建授权校验器失败,授权上传不可用", zap.Error(err))
		return
	}
	adminServer.SetLicenseManager(&licenseManager{
		validator: v, filePath: cfg.License.FilePath, logger: logger,
	})
}
