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

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCompleteFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := `
server:
  proxy_addr: ":9090"
  admin_addr: ":9091"
  read_timeout: 10s
  write_timeout: 20s
  idle_timeout: 60s
  max_header_bytes: 2097152
storage:
  driver: mysql
  dsn: "user:pass@tcp(host:3306)/db"
  max_open_conns: 5
  max_idle_conns: 2
audit:
  queue_size: 1024
  worker_count: 2
  batch_size: 10
  flush_interval: 1s
  enable_sha256: false
  retention_days: 30
rate_limit:
  strategy: sliding_window
  default_rps: 100
  default_tpm: 1000
export:
  type: kafka
  endpoint: "http://kafka:9092"
  batch_size: 5
  flush_interval: 2s
license:
  file_path: "/tmp/license.lic"
  offline_mode: true
admin:
  bootstrap_password: "init-pass-123"
  allowed_origins: ["https://ops.example.com"]
log:
  level: debug
  format: console
  output: stderr
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := []struct{ got, want interface{} }{
		{cfg.Server.ProxyAddr, ":9090"},
		{cfg.Server.AdminAddr, ":9091"},
		{cfg.Server.MaxHeaderBytes, 2097152},
		{cfg.Storage.Driver, "mysql"},
		{cfg.Storage.MaxOpenConns, 5},
		{cfg.Audit.QueueSize, 1024},
		{cfg.Audit.FlushInterval.String(), "1s"},
		{cfg.RateLimit.Strategy, "sliding_window"},
		{cfg.RateLimit.DefaultRPS, 100},
		{cfg.Export.Type, "kafka"},
		{cfg.License.OfflineMode, true},
		{cfg.Admin.BootstrapPassword, "init-pass-123"},
		{len(cfg.Admin.AllowedOrigins), 1},
		{cfg.Admin.AllowedOrigins[0], "https://ops.example.com"},
		{cfg.Log.Level, "debug"},
	}
	for _, w := range want {
		if w.got != w.want {
			t.Errorf("got %v, want %v", w.got, w.want)
		}
	}
}

func TestLoadMissingFieldsUseDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("server:\n  proxy_addr: \":9090\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.ProxyAddr != ":9090" {
		t.Errorf("ProxyAddr = %q, want :9090", cfg.Server.ProxyAddr)
	}
	if cfg.Server.AdminAddr != ":8081" {
		t.Errorf("AdminAddr = %q, want default :8081", cfg.Server.AdminAddr)
	}
	if cfg.Storage.Driver != "sqlite" {
		t.Errorf("Driver = %q, want default sqlite", cfg.Storage.Driver)
	}
	if cfg.Audit.QueueSize != 65536 {
		t.Errorf("QueueSize = %d, want default 65536", cfg.Audit.QueueSize)
	}
	if cfg.RateLimit.DefaultRPS != 10 {
		t.Errorf("DefaultRPS = %d, want default 10", cfg.RateLimit.DefaultRPS)
	}
	if cfg.Log.Level != "info" {
		t.Errorf("Log.Level = %q, want default info", cfg.Log.Level)
	}
}

// TestDefaultsFullyApplied 只设置 server.proxy_addr，断言其余每个字段都被回填为默认值
func TestDefaultsFullyApplied(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("server:\n  proxy_addr: \":9090\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	d := Default()
	checks := []struct {
		field string
		got   interface{}
		want  interface{}
	}{
		// Server（proxy_addr 来自文件，其余默认值）
		{"Server.ProxyAddr", cfg.Server.ProxyAddr, ":9090"},
		{"Server.AdminAddr", cfg.Server.AdminAddr, d.Server.AdminAddr},
		{"Server.ReadTimeout", cfg.Server.ReadTimeout, d.Server.ReadTimeout},
		{"Server.WriteTimeout", cfg.Server.WriteTimeout, d.Server.WriteTimeout},
		{"Server.IdleTimeout", cfg.Server.IdleTimeout, d.Server.IdleTimeout},
		{"Server.MaxHeaderBytes", cfg.Server.MaxHeaderBytes, d.Server.MaxHeaderBytes},
		// Storage
		{"Storage.Driver", cfg.Storage.Driver, d.Storage.Driver},
		{"Storage.DSN", cfg.Storage.DSN, d.Storage.DSN},
		{"Storage.EncryptKey", cfg.Storage.EncryptKey, d.Storage.EncryptKey},
		{"Storage.MaxOpenConns", cfg.Storage.MaxOpenConns, d.Storage.MaxOpenConns},
		{"Storage.MaxIdleConns", cfg.Storage.MaxIdleConns, d.Storage.MaxIdleConns},
		// Audit
		{"Audit.QueueSize", cfg.Audit.QueueSize, d.Audit.QueueSize},
		{"Audit.WorkerCount", cfg.Audit.WorkerCount, d.Audit.WorkerCount},
		{"Audit.BatchSize", cfg.Audit.BatchSize, d.Audit.BatchSize},
		{"Audit.FlushInterval", cfg.Audit.FlushInterval, d.Audit.FlushInterval},
		{"Audit.EnableSHA256", cfg.Audit.EnableSHA256, d.Audit.EnableSHA256},
		{"Audit.RetentionDays", cfg.Audit.RetentionDays, d.Audit.RetentionDays},
		// RateLimit
		{"RateLimit.Strategy", cfg.RateLimit.Strategy, d.RateLimit.Strategy},
		{"RateLimit.DefaultRPS", cfg.RateLimit.DefaultRPS, d.RateLimit.DefaultRPS},
		{"RateLimit.DefaultTPM", cfg.RateLimit.DefaultTPM, d.RateLimit.DefaultTPM},
		// Export
		{"Export.Type", cfg.Export.Type, d.Export.Type},
		{"Export.Endpoint", cfg.Export.Endpoint, d.Export.Endpoint},
		{"Export.APIKey", cfg.Export.APIKey, d.Export.APIKey},
		{"Export.BatchSize", cfg.Export.BatchSize, d.Export.BatchSize},
		{"Export.FlushInterval", cfg.Export.FlushInterval, d.Export.FlushInterval},
		// License
		{"License.FilePath", cfg.License.FilePath, d.License.FilePath},
		{"License.OfflineMode", cfg.License.OfflineMode, d.License.OfflineMode},
		// Admin（bootstrap_password/allowed_origins 无默认值，缺省即空）
		{"Admin.BootstrapPassword", cfg.Admin.BootstrapPassword, ""},
		{"Admin.AllowedOrigins", len(cfg.Admin.AllowedOrigins), 0},
		// Log
		{"Log.Level", cfg.Log.Level, d.Log.Level},
		{"Log.Format", cfg.Log.Format, d.Log.Format},
		{"Log.Output", cfg.Log.Output, d.Log.Output},
		{"IPFilter.Mode", cfg.IPFilter.Mode, d.IPFilter.Mode},
		{"TLS.MinVersion", cfg.TLS.MinVersion, d.TLS.MinVersion},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.field, c.got, c.want)
		}
	}
}

func TestLoadNonexistentFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err == nil {
		t.Fatal("Load() expected error for missing file")
	}
}

// TestLoadPrivacyConfig privacy 段解析；bool 字段默认 false 不参与 applyDefaults
func TestLoadPrivacyConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := `
privacy:
  enabled: true
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Privacy.Enabled {
		t.Error("enabled: true 应被解析")
	}

	// 缺失段 → 默认关闭（升级零惊扰）
	path2 := filepath.Join(t.TempDir(), "empty.yaml")
	if err := os.WriteFile(path2, []byte("log:\n  level: info\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg2, err := Load(path2)
	if err != nil {
		t.Fatalf("Load empty: %v", err)
	}
	if cfg2.Privacy.Enabled {
		t.Error("未配置时 privacy.enabled 应为 false")
	}
}

// TestValidateRequiresEncryptKey 安全校验：encrypt_key 必须显式提供
func TestValidateRequiresEncryptKey(t *testing.T) {
	cfg := Default()
	cfg.Storage.EncryptKey = ""
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "encrypt_key") {
		t.Errorf("空密钥应报错并提示字段, got %v", err)
	}
	cfg.Storage.EncryptKey = "explicit-key"
	if err := cfg.Validate(); err != nil {
		t.Errorf("显式密钥应通过, got %v", err)
	}
}

// TestEnvOverrides NEURALGATE_ 前缀白名单覆盖 yaml(非空才生效)
func TestEnvOverrides(t *testing.T) {
	cfg := Default()
	t.Setenv("NEURALGATE_PROXY_ADDR", ":9999")
	t.Setenv("NEURALGATE_STORAGE_DSN", "/data/ng.db")
	t.Setenv("NEURALGATE_ADMIN_BOOTSTRAP_PASSWORD", "env-pass")
	t.Setenv("NEURALGATE_LOG_LEVEL", "") // 空值不覆盖
	cfg.applyEnvOverrides()
	if cfg.Server.ProxyAddr != ":9999" || cfg.Storage.DSN != "/data/ng.db" ||
		cfg.Admin.BootstrapPassword != "env-pass" {
		t.Errorf("环境覆盖未生效: %+v", cfg)
	}
	if cfg.Log.Level != Default().Log.Level {
		t.Error("空环境变量不应覆盖")
	}
}
