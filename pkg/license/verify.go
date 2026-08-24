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

package license

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// 验签失败原因哨兵错误：调用方用 errors.Is 区分过期/无效等降级状态
var (
	ErrEmptyLicense      = errors.New("授权信息为空")
	ErrMissingSignature  = errors.New("签名缺失")
	ErrBadSignature      = errors.New("签名格式错误")
	ErrSignatureMismatch = errors.New("签名验证失败")
	ErrExpired           = errors.New("授权已过期")
	ErrNotYetValid       = errors.New("授权尚未生效")
)

// Verify 用供应商公钥校验授权信息：签名可解码 → Ed25519 验签 → 未过期 → 已生效，
// 任一步失败即返回对应哨兵错误，通过则返回 nil。纯函数，不读取任何文件。
func Verify(pubKey ed25519.PublicKey, info *plugin.LicenseInfo) error {
	if info == nil {
		return ErrEmptyLicense
	}
	if len(pubKey) != ed25519.PublicKeySize {
		return fmt.Errorf("公钥长度错误: %d", len(pubKey))
	}
	if info.Signature == "" {
		return ErrMissingSignature
	}
	sig, err := base64.StdEncoding.DecodeString(info.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("%w: 长度=%d", ErrBadSignature, len(sig))
	}
	if !ed25519.Verify(pubKey, CanonicalPayload(info), sig) {
		return ErrSignatureMismatch
	}
	now := time.Now()
	if now.After(info.ExpiresAt) {
		return ErrExpired
	}
	if now.Before(info.IssuedAt) {
		return ErrNotYetValid
	}
	return nil
}

// Sign 用私钥对授权信息签名，返回 base64 编码的签名值（签发工具与网关验签同源载荷）
func Sign(privKey ed25519.PrivateKey, info *plugin.LicenseInfo) (string, error) {
	if info == nil {
		return "", ErrEmptyLicense
	}
	if len(privKey) != ed25519.PrivateKeySize {
		return "", fmt.Errorf("私钥长度错误: %d", len(privKey))
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(privKey, CanonicalPayload(info))), nil
}
