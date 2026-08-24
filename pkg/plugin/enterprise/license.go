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
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/druidcaesa/neuralgate/pkg/license"
	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// embeddedPublicKeyBase64 供应商签发公钥（base64），
// 由 licensegen keygen 生成后替换；对应私钥由供应商离线保管，绝不进入仓库。
const embeddedPublicKeyBase64 = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

// EmbeddedPublicKey 解码内置签发公钥；常量被错误修改时在装配阶段即报错
func EmbeddedPublicKey() (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(embeddedPublicKeyBase64)
	if err != nil {
		return nil, fmt.Errorf("解码内置公钥失败: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("内置公钥长度错误: %d", len(raw))
	}
	return ed25519.PublicKey(raw), nil
}

// EnterpriseLicenseValidator 企业版授权校验器：
// 加载 license.json → Ed25519 验签与有效期检查 → 缓存结论供门控与展示查询。
// 隐式满足 core.LicenseGate（HasFeature），无需反向依赖 pkg/core。
type EnterpriseLicenseValidator struct {
	pubKey ed25519.PublicKey

	mu    sync.RWMutex
	info  *plugin.LicenseInfo // 最近一次加载的授权（无论有效性，供后台尽力展示）
	valid bool                // 最近一次 Validate 的结论
}

// NewEnterpriseLicenseValidator 创建校验器；
// pub 为签发方公钥（生产用内置公钥，测试可注入自生成密钥）
func NewEnterpriseLicenseValidator(pub ed25519.PublicKey) (*EnterpriseLicenseValidator, error) {
	if len(pub) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("公钥长度错误: %d", len(pub))
	}
	return &EnterpriseLicenseValidator{pubKey: pub}, nil
}

// LoadLicense 读取并反序列化授权文件，成功后缓存原文
func (v *EnterpriseLicenseValidator) LoadLicense(filePath string) (*plugin.LicenseInfo, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("读取授权文件失败: %w", err)
	}
	var info plugin.LicenseInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("解析授权文件失败: %w", err)
	}
	v.mu.Lock()
	v.info = &info
	v.valid = false // 新加载的授权须重新 Validate 后门控才开启
	v.mu.Unlock()
	return &info, nil
}

// Validate 校验签名与有效期，并把结论缓存到校验器状态
func (v *EnterpriseLicenseValidator) Validate(lic *plugin.LicenseInfo) (bool, error) {
	err := license.Verify(v.pubKey, lic)
	v.mu.Lock()
	defer v.mu.Unlock()
	if err != nil {
		v.valid = false
		return false, err
	}
	v.info = lic
	v.valid = true
	return true, nil
}

// HasFeature 门控判断：仅在授权有效且功能在清单内时开启
func (v *EnterpriseLicenseValidator) HasFeature(feature string) bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if !v.valid || v.info == nil {
		return false
	}
	for _, f := range v.info.Features {
		if f == feature {
			return true
		}
	}
	return false
}

// CheckNodeLimit 判断节点数是否超出授权上限
func (v *EnterpriseLicenseValidator) CheckNodeLimit(currentNodes int) bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.valid && v.info != nil && currentNodes <= v.info.MaxNodes
}

// CheckTenantLimit 判断租户数是否超出授权上限
func (v *EnterpriseLicenseValidator) CheckTenantLimit(currentTenants int) bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.valid && v.info != nil && currentTenants <= v.info.MaxTenants
}

// GetLicenseInfo 返回最近一次加载的授权信息（未加载时为 nil；
// 授权无效时仍返回已加载字段，供管理后台尽力展示）
func (v *EnterpriseLicenseValidator) GetLicenseInfo() *plugin.LicenseInfo {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.info
}
