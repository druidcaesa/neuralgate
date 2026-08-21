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

package core

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"testing"
	"time"
)

func TestIPFilterDisabled(t *testing.T) {
	f := NewIPFilter("disabled", nil, nil)
	if !f.Allow("1.2.3.4") {
		t.Fatal("disabled mode should allow all")
	}
}

func TestIPFilterWhitelist(t *testing.T) {
	f := NewIPFilter("whitelist", []string{"10.0.0.0/8", "192.168.1.100"}, nil)
	if !f.Allow("10.5.6.7") {
		t.Fatal("10.5.6.7 in 10.0.0.0/8 should be allowed")
	}
	if !f.Allow("192.168.1.100") {
		t.Fatal("exact IP should be allowed")
	}
	if f.Allow("8.8.8.8") {
		t.Fatal("8.8.8.8 not in whitelist should be denied")
	}
}

func TestIPFilterBlacklist(t *testing.T) {
	f := NewIPFilter("blacklist", nil, []string{"1.2.3.0/24"})
	if f.Allow("1.2.3.4") {
		t.Fatal("1.2.3.4 in blacklist should be denied")
	}
	if !f.Allow("5.6.7.8") {
		t.Fatal("5.6.7.8 not in blacklist should be allowed")
	}
}

func TestIPFilterInvalidIPDenied(t *testing.T) {
	f := NewIPFilter("whitelist", []string{"10.0.0.0/8"}, nil)
	if f.Allow("not-an-ip") {
		t.Fatal("unparseable IP in whitelist mode should be denied")
	}
}

func TestTLSHandlerDisabled(t *testing.T) {
	h := NewTLSHandler(false, "", "", "1.2")
	cfg, err := h.TLSConfig()
	if err != nil {
		t.Fatalf("disabled TLS should not error: %v", err)
	}
	if cfg != nil {
		t.Fatal("disabled TLS should return nil config")
	}
}

func TestTLSHandlerMissingCertErrors(t *testing.T) {
	h := NewTLSHandler(true, "/nonexistent/cert.pem", "/nonexistent/key.pem", "1.2")
	if _, err := h.TLSConfig(); err == nil {
		t.Fatal("enabled TLS with missing cert should error")
	}
}

func TestTLSHandlerMinVersion(t *testing.T) {
	certPEM, keyPEM := genSelfSignedCert(t)
	certFile := writeTemp(t, "cert.pem", certPEM)
	keyFile := writeTemp(t, "key.pem", keyPEM)
	h := NewTLSHandler(true, certFile, keyFile, "1.3")
	cfg, err := h.TLSConfig()
	if err != nil {
		t.Fatalf("valid cert should load: %v", err)
	}
	if cfg == nil || cfg.MinVersion != tls.VersionTLS13 {
		t.Fatalf("MinVersion = %v; want TLS1.3", cfg.MinVersion)
	}
	if len(cfg.Certificates) != 1 {
		t.Fatalf("should load 1 certificate")
	}
}

// genSelfSignedCert 生成临时自签 RSA 证书 PEM
func genSelfSignedCert(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Unix(1000, 0),
		NotAfter:     time.Unix(1<<31, 0),
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certPEM, keyPEM
}

func writeTemp(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := t.TempDir() + "/" + name
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
