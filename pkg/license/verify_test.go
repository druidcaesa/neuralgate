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
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// testKeypair 生成测试用 Ed25519 密钥对
func testKeypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成密钥对失败: %v", err)
	}
	return pub, priv
}

// signInto 用私钥签名并回填 info.Signature
func signInto(t *testing.T, priv ed25519.PrivateKey, info *plugin.LicenseInfo) {
	t.Helper()
	sig, err := Sign(priv, info)
	if err != nil {
		t.Fatalf("签名失败: %v", err)
	}
	info.Signature = sig
}

func TestSignAndVerifyRoundTrip(t *testing.T) {
	pub, priv := testKeypair(t)
	info := baseInfo()
	signInto(t, priv, &info)
	if err := Verify(pub, &info); err != nil {
		t.Fatalf("合法签名应验签通过: %v", err)
	}
}

func TestVerifyTampered(t *testing.T) {
	pub, priv := testKeypair(t)
	mutations := map[string]func(*plugin.LicenseInfo){
		"license_key":   func(i *plugin.LicenseInfo) { i.LicenseKey = "NG-ENT-FAKE" },
		"customer_name": func(i *plugin.LicenseInfo) { i.CustomerName = "伪造客户" },
		"max_nodes":     func(i *plugin.LicenseInfo) { i.MaxNodes = 9999 },
		"expires_at":    func(i *plugin.LicenseInfo) { i.ExpiresAt = i.ExpiresAt.AddDate(10, 0, 0) },
		"features":      func(i *plugin.LicenseInfo) { i.Features = append(i.Features, FeatureRBAC) },
		"is_offline":    func(i *plugin.LicenseInfo) { i.IsOffline = !i.IsOffline },
	}
	for name, mutate := range mutations {
		info := baseInfo()
		signInto(t, priv, &info)
		mutate(&info)
		if err := Verify(pub, &info); !errors.Is(err, ErrSignatureMismatch) {
			t.Errorf("篡改 %s 后应返回签名验证失败，得到: %v", name, err)
		}
	}
}

func TestVerifyWrongPublicKey(t *testing.T) {
	_, priv := testKeypair(t)
	otherPub, _ := testKeypair(t)
	info := baseInfo()
	signInto(t, priv, &info)
	if err := Verify(otherPub, &info); !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("错误公钥应返回签名验证失败: %v", err)
	}
}

func TestVerifyBadSignatureFormat(t *testing.T) {
	pub, priv := testKeypair(t)
	info := baseInfo()
	signInto(t, priv, &info)

	missing := info
	missing.Signature = ""
	if err := Verify(pub, &missing); !errors.Is(err, ErrMissingSignature) {
		t.Errorf("空签名应返回签名缺失: %v", err)
	}
	badFmt := info
	badFmt.Signature = "!!!not-base64!!!"
	if err := Verify(pub, &badFmt); !errors.Is(err, ErrBadSignature) {
		t.Errorf("非 base64 签名应返回签名格式错误: %v", err)
	}
	tooShort := info
	tooShort.Signature = "AAAA"
	if err := Verify(pub, &tooShort); !errors.Is(err, ErrBadSignature) {
		t.Errorf("长度不足的签名应返回签名格式错误: %v", err)
	}
}

func TestVerifyExpired(t *testing.T) {
	pub, priv := testKeypair(t)
	info := baseInfo()
	info.ExpiresAt = time.Now().Add(-time.Hour)
	signInto(t, priv, &info)
	if err := Verify(pub, &info); !errors.Is(err, ErrExpired) {
		t.Fatalf("过期授权应返回授权已过期: %v", err)
	}
}

func TestVerifyNotYetValid(t *testing.T) {
	pub, priv := testKeypair(t)
	info := baseInfo()
	info.IssuedAt = time.Now().Add(time.Hour)
	signInto(t, priv, &info)
	if err := Verify(pub, &info); !errors.Is(err, ErrNotYetValid) {
		t.Fatalf("未生效授权应返回授权尚未生效: %v", err)
	}
}

func TestVerifyNilAndBadKeys(t *testing.T) {
	pub, priv := testKeypair(t)
	if err := Verify(pub, nil); !errors.Is(err, ErrEmptyLicense) {
		t.Fatalf("nil 授权信息应返回授权信息为空: %v", err)
	}
	info := baseInfo()
	signInto(t, priv, &info)
	if err := Verify(ed25519.PublicKey("short"), &info); err == nil {
		t.Fatal("非法长度公钥应报错")
	}
	if _, err := Sign(ed25519.PrivateKey("short"), &info); err == nil {
		t.Fatal("非法长度私钥应报错")
	}
}
