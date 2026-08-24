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
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/license"
	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// newTestValidator 生成测试密钥对并创建使用该公钥的校验器
func newTestValidator(t *testing.T) (*EnterpriseLicenseValidator, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成密钥对失败: %v", err)
	}
	v, err := NewEnterpriseLicenseValidator(pub)
	if err != nil {
		t.Fatalf("创建校验器失败: %v", err)
	}
	return v, priv
}

// signedInfo 构造一份已签名的有效授权
func signedInfo(t *testing.T, priv ed25519.PrivateKey) *plugin.LicenseInfo {
	t.Helper()
	info := &plugin.LicenseInfo{
		LicenseKey:   "NG-ENT-20260824-ab12",
		ProductName:  "NeuralGate Enterprise",
		CustomerName: "示例科技有限公司",
		MaxNodes:     3,
		MaxTenants:   50,
		IssuedAt:     time.Now().Add(-24 * time.Hour),
		ExpiresAt:    time.Now().Add(365 * 24 * time.Hour),
		Features:     []string{license.FeatureAuditStream, license.FeatureRBAC},
		IsOffline:    true,
	}
	sig, err := license.Sign(priv, info)
	if err != nil {
		t.Fatalf("签名失败: %v", err)
	}
	info.Signature = sig
	return info
}

// writeLicenseFile 将授权写入临时文件并返回路径
func writeLicenseFile(t *testing.T, info *plugin.LicenseInfo) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "license.json")
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("写入授权文件失败: %v", err)
	}
	return path
}

func TestEmbeddedPublicKeyDecodes(t *testing.T) {
	pub, err := EmbeddedPublicKey()
	if err != nil {
		t.Fatalf("内置公钥应可解码: %v", err)
	}
	if len(pub) != ed25519.PublicKeySize {
		t.Fatalf("内置公钥长度 = %d, want %d", len(pub), ed25519.PublicKeySize)
	}
}

func TestNewValidatorRejectsBadKey(t *testing.T) {
	if _, err := NewEnterpriseLicenseValidator(ed25519.PublicKey("short")); err == nil {
		t.Fatal("非法长度公钥应报错")
	}
}

func TestLoadLicenseMissingFile(t *testing.T) {
	v, _ := newTestValidator(t)
	if _, err := v.LoadLicense(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("授权文件缺失应报错")
	}
}

func TestLoadLicenseInvalidJSON(t *testing.T) {
	v, _ := newTestValidator(t)
	path := filepath.Join(t.TempDir(), "license.json")
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := v.LoadLicense(path); err == nil {
		t.Fatal("非法 JSON 应报错")
	}
}

func TestLoadValidateAndGateFlow(t *testing.T) {
	v, priv := newTestValidator(t)

	// 校验前门控恒关
	if v.HasFeature(license.FeatureRBAC) {
		t.Fatal("未通过校验前 HasFeature 应为 false")
	}
	if v.CheckNodeLimit(1) || v.CheckTenantLimit(1) {
		t.Fatal("未通过校验前限额检查应为 false")
	}

	info := signedInfo(t, priv)
	path := writeLicenseFile(t, info)
	loaded, err := v.LoadLicense(path)
	if err != nil {
		t.Fatalf("加载授权失败: %v", err)
	}
	ok, err := v.Validate(loaded)
	if !ok || err != nil {
		t.Fatalf("合法授权应校验通过: ok=%v err=%v", ok, err)
	}

	if !v.HasFeature(license.FeatureAuditStream) || !v.HasFeature(license.FeatureRBAC) {
		t.Error("已授权功能应为 true")
	}
	if v.HasFeature("not_granted") {
		t.Error("未授权功能应为 false")
	}
	if !v.CheckNodeLimit(1) || !v.CheckNodeLimit(3) {
		t.Error("节点数不超上限应为 true")
	}
	if v.CheckNodeLimit(4) {
		t.Error("节点数超上限应为 false")
	}
	if !v.CheckTenantLimit(50) {
		t.Error("租户数等于上限应为 true")
	}
	if v.CheckTenantLimit(51) {
		t.Error("租户数超上限应为 false")
	}
	got := v.GetLicenseInfo()
	if got == nil || got.CustomerName != info.CustomerName {
		t.Errorf("GetLicenseInfo 应返回缓存授权: %+v", got)
	}
}

func TestValidateTampered(t *testing.T) {
	v, priv := newTestValidator(t)
	info := signedInfo(t, priv)
	info.MaxNodes = 9999 // 篡改后与签名不符
	ok, err := v.Validate(info)
	if ok || !errors.Is(err, license.ErrSignatureMismatch) {
		t.Fatalf("篡改授权应返回签名验证失败: ok=%v err=%v", ok, err)
	}
	if v.HasFeature(license.FeatureRBAC) {
		t.Error("校验失败后 HasFeature 应为 false")
	}
}

func TestValidateExpired(t *testing.T) {
	v, priv := newTestValidator(t)
	info := signedInfo(t, priv)
	info.ExpiresAt = time.Now().Add(-time.Hour)
	sig, err := license.Sign(priv, info)
	if err != nil {
		t.Fatal(err)
	}
	info.Signature = sig
	ok, verr := v.Validate(info)
	if ok || !errors.Is(verr, license.ErrExpired) {
		t.Fatalf("过期授权应返回授权已过期: ok=%v err=%v", ok, verr)
	}
}

func TestValidateNil(t *testing.T) {
	v, _ := newTestValidator(t)
	ok, err := v.Validate(nil)
	if ok || !errors.Is(err, license.ErrEmptyLicense) {
		t.Fatalf("nil 授权应返回授权信息为空: ok=%v err=%v", ok, err)
	}
}

func TestGetLicenseInfoKeepsLoadedOnInvalid(t *testing.T) {
	v, priv := newTestValidator(t)
	info := signedInfo(t, priv)
	info.ExpiresAt = time.Now().Add(-time.Hour) // 过期但字段可读，供后台尽力展示
	sig, _ := license.Sign(priv, info)
	info.Signature = sig

	if _, err := v.LoadLicense(writeLicenseFile(t, info)); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Validate(v.GetLicenseInfo()); err == nil {
		t.Fatal("过期授权校验应失败")
	}
	if v.GetLicenseInfo() == nil {
		t.Fatal("校验失败后 GetLicenseInfo 仍应返回已加载字段供展示")
	}
	if !strings.Contains(v.GetLicenseInfo().CustomerName, "示例") {
		t.Fatal("缓存字段内容不符")
	}
}
