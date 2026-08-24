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

package main

import (
	"crypto/ed25519"
	"encoding/base64"
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

func TestKeygenWritesPrivateKeyAndReturnsPublicKey(t *testing.T) {
	dir := t.TempDir()
	pub, err := keygen(dir)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	if len(pub) != ed25519.PublicKeySize {
		t.Fatalf("公钥长度 = %d, want %d", len(pub), ed25519.PublicKeySize)
	}

	priv, err := readPrivateKey(filepath.Join(dir, privateKeyName))
	if err != nil {
		t.Fatalf("读取私钥失败: %v", err)
	}
	if !pub.Equal(priv.Public()) {
		t.Fatal("私钥与 keygen 返回的公钥不配对")
	}
}

func TestReadPrivateKeyRejectsGarbage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fake.pem")
	if err := os.WriteFile(path, []byte("not a pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivateKey(path); err == nil {
		t.Fatal("非法 PEM 应报错")
	}
}

func TestSignVerifyRoundTrip(t *testing.T) {
	dir := t.TempDir()
	pub, err := keygen(dir)
	if err != nil {
		t.Fatal(err)
	}
	pubB64 := base64.StdEncoding.EncodeToString(pub)
	out := filepath.Join(dir, "license.json")

	info, err := signLicense(signOptions{
		KeyPath:    filepath.Join(dir, privateKeyName),
		Customer:   "示例科技有限公司",
		MaxNodes:   3,
		MaxTenants: 50,
		Features:   []string{license.FeatureAuditStream, license.FeatureRBAC},
		ExpiresAt:  time.Now().Add(30 * 24 * time.Hour).UTC(),
		OutPath:    out,
	})
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}
	if info.Signature == "" {
		t.Fatal("签名未回填")
	}
	if !strings.HasPrefix(info.LicenseKey, "NG-ENT-") {
		t.Errorf("授权码格式不符: %s", info.LicenseKey)
	}
	if !info.IsOffline {
		t.Error("签发的授权应为离线授权")
	}
	if err := verifyLicenseFile(pubB64, out); err != nil {
		t.Fatalf("自检应通过: %v", err)
	}

	// 篡改任一字段后自检必须失败
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var tampered plugin.LicenseInfo
	if err := json.Unmarshal(data, &tampered); err != nil {
		t.Fatal(err)
	}
	tampered.MaxNodes = 9999
	badPath := filepath.Join(dir, "tampered.json")
	badData, _ := json.Marshal(&tampered)
	if err := os.WriteFile(badPath, badData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyLicenseFile(pubB64, badPath); !errors.Is(err, license.ErrSignatureMismatch) {
		t.Fatalf("篡改后的授权自检应返回签名验证失败: %v", err)
	}
}
