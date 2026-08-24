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

// licensegen 供应商侧授权签发工具：生成 Ed25519 密钥对、签发与自检 license。
// 私钥由供应商离线保管；公钥打印后硬编码进网关企业版二进制。
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/license"
	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

const (
	productName    = "NeuralGate Enterprise"
	privateKeyName = "license_private.pem"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = cmdKeygen(os.Args[2:])
	case "sign":
		err = cmdSign(os.Args[2:])
	case "verify":
		err = cmdVerify(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `用法:
  licensegen keygen -out <目录>                        生成密钥对(私钥写入 <目录>/license_private.pem,公钥打印到 stdout)
  licensegen sign -key <私钥pem> -customer <名称> -expires <YYYY-MM-DD> [-max-nodes N] [-max-tenants N] [-features f1,f2] [-out license.json]
  licensegen verify -pub <公钥base64> -license <文件>    用公钥自检授权签名
`)
}

// ===== keygen =====

func cmdKeygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	out := fs.String("out", ".", "密钥输出目录")
	if err := fs.Parse(args); err != nil {
		return err
	}
	pub, err := keygen(*out)
	if err != nil {
		return err
	}
	fmt.Println("私钥已写入:", filepath.Join(*out, privateKeyName), "(请离线妥善保管)")
	fmt.Println("公钥(base64,硬编码进 pkg/plugin/enterprise/license.go):")
	fmt.Println(base64.StdEncoding.EncodeToString(pub))
	return nil
}

// keygen 生成 Ed25519 密钥对：私钥以 PKCS8 PEM 写入 outDir（权限 0600），返回公钥
func keygen(outDir string) (ed25519.PublicKey, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建输出目录失败: %w", err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("生成密钥对失败: %w", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("编码私钥失败: %w", err)
	}
	block := &pem.Block{Type: "PRIVATE KEY", Bytes: der}
	if err := os.WriteFile(filepath.Join(outDir, privateKeyName), pem.EncodeToMemory(block), 0o600); err != nil {
		return nil, fmt.Errorf("写入私钥失败: %w", err)
	}
	return pub, nil
}

// readPrivateKey 从 PKCS8 PEM 文件读取 Ed25519 私钥
func readPrivateKey(path string) (ed25519.PrivateKey, error) {
	pemBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取私钥失败: %w", err)
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("私钥文件不是有效的 PEM 格式")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("解析私钥失败: %w", err)
	}
	priv, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("私钥类型不是 Ed25519")
	}
	return priv, nil
}

// ===== sign =====

type signOptions struct {
	KeyPath    string
	Customer   string
	MaxNodes   int
	MaxTenants int
	Features   []string
	ExpiresAt  time.Time
	OutPath    string
}

func cmdSign(args []string) error {
	fs := flag.NewFlagSet("sign", flag.ExitOnError)
	keyPath := fs.String("key", privateKeyName, "私钥 PEM 路径")
	customer := fs.String("customer", "", "客户名称(必填)")
	maxNodes := fs.Int("max-nodes", 1, "最大节点数")
	maxTenants := fs.Int("max-tenants", 10, "最大租户数")
	features := fs.String("features", "", "授权功能,逗号分隔")
	expires := fs.String("expires", "", "到期日期 YYYY-MM-DD(必填)")
	out := fs.String("out", "license.json", "授权文件输出路径")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *customer == "" || *expires == "" {
		return errors.New("-customer 与 -expires 为必填")
	}
	expiresAt, err := time.ParseInLocation("2006-01-02", *expires, time.UTC)
	if err != nil {
		return fmt.Errorf("解析 -expires 失败(应为 YYYY-MM-DD): %w", err)
	}
	var feats []string
	for _, f := range strings.Split(*features, ",") {
		if f = strings.TrimSpace(f); f != "" {
			feats = append(feats, f)
		}
	}
	info, err := signLicense(signOptions{
		KeyPath:    *keyPath,
		Customer:   *customer,
		MaxNodes:   *maxNodes,
		MaxTenants: *maxTenants,
		Features:   feats,
		ExpiresAt:  expiresAt,
		OutPath:    *out,
	})
	if err != nil {
		return err
	}
	fmt.Printf("已签发 %s: customer=%s expires=%s features=%v\n",
		*out, info.CustomerName, info.ExpiresAt.Format("2006-01-02"), info.Features)
	return nil
}

// signLicense 组装授权信息并用私钥签名，写入缩进 JSON 后返回
func signLicense(opts signOptions) (*plugin.LicenseInfo, error) {
	priv, err := readPrivateKey(opts.KeyPath)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	key, err := newLicenseKey(now)
	if err != nil {
		return nil, err
	}
	info := &plugin.LicenseInfo{
		LicenseKey:   key,
		ProductName:  productName,
		CustomerName: opts.Customer,
		MaxNodes:     opts.MaxNodes,
		MaxTenants:   opts.MaxTenants,
		IssuedAt:     now,
		ExpiresAt:    opts.ExpiresAt,
		Features:     opts.Features,
		IsOffline:    true,
	}
	sig, err := license.Sign(priv, info)
	if err != nil {
		return nil, err
	}
	info.Signature = sig
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("序列化授权失败: %w", err)
	}
	if err := os.WriteFile(opts.OutPath, data, 0o644); err != nil {
		return nil, fmt.Errorf("写入授权文件失败: %w", err)
	}
	return info, nil
}

// newLicenseKey 生成形如 NG-ENT-20260824-ab12 的授权码（日期 + 随机 4 位 hex）
func newLicenseKey(now time.Time) (string, error) {
	suffix := make([]byte, 2)
	if _, err := rand.Read(suffix); err != nil {
		return "", fmt.Errorf("生成随机后缀失败: %w", err)
	}
	return fmt.Sprintf("NG-ENT-%s-%s", now.Format("20060102"), hex.EncodeToString(suffix)), nil
}

// ===== verify =====

func cmdVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	pubB64 := fs.String("pub", "", "签发公钥 base64(必填)")
	licPath := fs.String("license", "license.json", "授权文件路径")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := verifyLicenseFile(*pubB64, *licPath); err != nil {
		return err
	}
	fmt.Println("签名校验通过:", *licPath)
	return nil
}

// verifyLicenseFile 用 base64 公钥校验授权文件签名与有效期
func verifyLicenseFile(pubB64, licPath string) error {
	raw, err := base64.StdEncoding.DecodeString(pubB64)
	if err != nil {
		return fmt.Errorf("解码公钥失败: %w", err)
	}
	data, err := os.ReadFile(licPath)
	if err != nil {
		return fmt.Errorf("读取授权文件失败: %w", err)
	}
	var info plugin.LicenseInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return fmt.Errorf("解析授权文件失败: %w", err)
	}
	return license.Verify(ed25519.PublicKey(raw), &info)
}
