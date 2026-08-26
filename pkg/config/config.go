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
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 系统级配置（不含模型配置，模型配置存储在数据库中）
type Config struct {
	Server     ServerConfig     `yaml:"server"`
	Storage    StorageConfig    `yaml:"storage"`
	Audit      AuditConfig      `yaml:"audit"`
	RateLimit  RateLimitConfig  `yaml:"rate_limit"`
	Export     ExportConfig     `yaml:"export"`
	Privacy    PrivacyConfig    `yaml:"privacy"`
	RBAC       RBACConfig       `yaml:"rbac"`
	Compliance ComplianceConfig `yaml:"compliance"`
	MCPAudit   MCPAuditConfig   `yaml:"mcp_audit"`
	License    LicenseConfig    `yaml:"license"`
	Admin      AdminConfig      `yaml:"admin"`
	Log        LogConfig        `yaml:"log"`
	IPFilter   IPFilterConfig   `yaml:"ip_filter"`
	TLS        TLSConfig        `yaml:"tls"`
}

// PrivacyConfig 隐私合规配置（Enterprise：需 privacy 授权）
type PrivacyConfig struct {
	Enabled bool `yaml:"enabled"` // 是否启用隐私防护(PII 脱敏/注入拦截)；bool 不参与 applyDefaults
}

// RBACConfig 权限体系配置（Enterprise：需 rbac 授权）
type RBACConfig struct {
	Enabled bool `yaml:"enabled"` // 是否启用权限体系(RBAC/租户/操作审计)；bool 不参与 applyDefaults
}

// ComplianceConfig 合规运维配置（Enterprise：需 compliance 授权）
type ComplianceConfig struct {
	Enabled bool `yaml:"enabled"` // 是否启用合规报表调度(日/周/月周期聚合)；bool 不参与 applyDefaults
}

// MCPAuditConfig MCP 智能体审计配置（Enterprise：需 mcp_audit 授权）；
// 中继通道本身 OSS+ 恒可用，此项仅控制审计留存与失败告警
type MCPAuditConfig struct {
	Enabled bool `yaml:"enabled"` // 是否启用工具调用审计与告警；bool 不参与 applyDefaults
}

// AdminConfig 管理后台配置：bootstrap 密码仅用于首个管理员账号的种子（登录凭证存数据库），
// allowed_origins 为 CORS 白名单（空=不发送跨域头，同源部署）
type AdminConfig struct {
	BootstrapPassword string   `yaml:"bootstrap_password"` // 为空则首次启动随机生成并打印日志
	AllowedOrigins    []string `yaml:"allowed_origins"`
}

type ServerConfig struct {
	ProxyAddr      string        `yaml:"proxy_addr"`
	AdminAddr      string        `yaml:"admin_addr"`
	ReadTimeout    time.Duration `yaml:"read_timeout"`
	WriteTimeout   time.Duration `yaml:"write_timeout"`
	IdleTimeout    time.Duration `yaml:"idle_timeout"`
	MaxHeaderBytes int           `yaml:"max_header_bytes"`
}

type StorageConfig struct {
	Driver       string `yaml:"driver"`
	DSN          string `yaml:"dsn"`
	EncryptKey   string `yaml:"encrypt_key"` // AES-GCM 加密密钥(上游 API Key 加密)
	MaxOpenConns int    `yaml:"max_open_conns"`
	MaxIdleConns int    `yaml:"max_idle_conns"`
}

type AuditConfig struct {
	QueueSize       int           `yaml:"queue_size"`
	WorkerCount     int           `yaml:"worker_count"`
	BatchSize       int           `yaml:"batch_size"`
	FlushInterval   time.Duration `yaml:"flush_interval"`
	EnableSHA256    bool          `yaml:"enable_sha256"`
	RetentionDays   int           `yaml:"retention_days"`
	VerifyInterval  time.Duration `yaml:"verify_interval"`   // 哈希校验间隔(Enterprise)
	VerifyBatchSize int           `yaml:"verify_batch_size"` // 每次校验批次大小(Enterprise)
	FingerprintAlgo string        `yaml:"fingerprint_algo"`  // 指纹算法(本期仅 sha256)
}

type RateLimitConfig struct {
	Strategy   string `yaml:"strategy"`
	DefaultRPS int    `yaml:"default_rps"`
	DefaultTPM int64  `yaml:"default_tpm"`
}

type ExportConfig struct {
	Enabled       bool          `yaml:"enabled"`  // 是否启用外推(bool 不参与 applyDefaults)
	Type          string        `yaml:"type"`     // siem/syslog/kafka
	Endpoint      string        `yaml:"endpoint"` // kafka 为逗号分隔 broker 列表
	APIKey        string        `yaml:"api_key"`  // SIEM 认证密钥(kafka 本期忽略)
	Topic         string        `yaml:"topic"`    // Kafka 目标 topic(空=neuralgate-audit)
	BatchSize     int           `yaml:"batch_size"`
	FlushInterval time.Duration `yaml:"flush_interval"`
}

type LicenseConfig struct {
	FilePath    string `yaml:"file_path"`
	OfflineMode bool   `yaml:"offline_mode"`
}

type LogConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
	Output string `yaml:"output"`
}

// IPFilterConfig IP 黑白名单配置
type IPFilterConfig struct {
	Mode      string   `yaml:"mode"` // disabled/whitelist/blacklist
	Whitelist []string `yaml:"whitelist"`
	Blacklist []string `yaml:"blacklist"`
}

// TLSConfig 代理服务 TLS 配置
type TLSConfig struct {
	Enabled    bool   `yaml:"enabled"`
	CertFile   string `yaml:"cert_file"`
	KeyFile    string `yaml:"key_file"`
	MinVersion string `yaml:"min_version"` // "1.2"/"1.3"
}

// Default 返回默认配置
func Default() *Config {
	return &Config{
		Server: ServerConfig{
			ProxyAddr:      ":8080",
			AdminAddr:      ":8081",
			ReadTimeout:    30 * time.Second,
			WriteTimeout:   30 * time.Second,
			IdleTimeout:    120 * time.Second,
			MaxHeaderBytes: 1 << 20,
		},
		Storage: StorageConfig{
			Driver:       "sqlite",
			DSN:          "neuralgate.db",
			EncryptKey:   "neuralgate-default-encrypt-key",
			MaxOpenConns: 20,
			MaxIdleConns: 10,
		},
		Audit: AuditConfig{
			QueueSize:       65536,
			WorkerCount:     4,
			BatchSize:       100,
			FlushInterval:   5 * time.Second,
			RetentionDays:   90,
			VerifyInterval:  24 * time.Hour,
			VerifyBatchSize: 1000,
			FingerprintAlgo: "sha256",
		},
		RateLimit: RateLimitConfig{
			Strategy:   "token_bucket",
			DefaultRPS: 10,
			DefaultTPM: 100000,
		},
		Export: ExportConfig{
			Type:          "siem",
			BatchSize:     50,
			FlushInterval: 10 * time.Second,
		},
		License: LicenseConfig{
			FilePath: "/etc/neuralgate/license.lic",
		},
		Log: LogConfig{
			Level:  "info",
			Format: "json",
			Output: "stdout",
		},
		IPFilter: IPFilterConfig{Mode: "disabled"},
		TLS:      TLSConfig{MinVersion: "1.2"},
	}
}

// Load 加载配置文件，缺失字段使用默认值
func Load(path string) (*Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}
	cfg.applyDefaults()
	return cfg, nil
}

// applyDefaults 将零值字段替换为默认值
// 注意：bool 字段（EnableSHA256/OfflineMode）不在此处理（无法区分"未设置"与"显式 false"）
func (c *Config) applyDefaults() {
	d := Default()
	c.Server.apply(d.Server)
	c.Storage.apply(d.Storage)
	c.Audit.apply(d.Audit)
	c.RateLimit.apply(d.RateLimit)
	c.Export.apply(d.Export)
	c.License.apply(d.License)
	c.Log.apply(d.Log)
	if c.IPFilter.Mode == "" {
		c.IPFilter.Mode = d.IPFilter.Mode
	}
	if c.TLS.MinVersion == "" {
		c.TLS.MinVersion = d.TLS.MinVersion
	}
}

// apply 零值字段回填默认值（bool 字段不处理）
func (s *ServerConfig) apply(d ServerConfig) {
	if s.ProxyAddr == "" {
		s.ProxyAddr = d.ProxyAddr
	}
	if s.AdminAddr == "" {
		s.AdminAddr = d.AdminAddr
	}
	if s.ReadTimeout == 0 {
		s.ReadTimeout = d.ReadTimeout
	}
	if s.WriteTimeout == 0 {
		s.WriteTimeout = d.WriteTimeout
	}
	if s.IdleTimeout == 0 {
		s.IdleTimeout = d.IdleTimeout
	}
	if s.MaxHeaderBytes == 0 {
		s.MaxHeaderBytes = d.MaxHeaderBytes
	}
}

// apply 零值字段回填默认值
func (s *StorageConfig) apply(d StorageConfig) {
	if s.Driver == "" {
		s.Driver = d.Driver
	}
	if s.DSN == "" {
		s.DSN = d.DSN
	}
	if s.EncryptKey == "" {
		s.EncryptKey = d.EncryptKey
	}
	if s.MaxOpenConns == 0 {
		s.MaxOpenConns = d.MaxOpenConns
	}
	if s.MaxIdleConns == 0 {
		s.MaxIdleConns = d.MaxIdleConns
	}
}

// apply 零值字段回填默认值（EnableSHA256 bool 字段不处理）
func (s *AuditConfig) apply(d AuditConfig) {
	if s.QueueSize == 0 {
		s.QueueSize = d.QueueSize
	}
	if s.WorkerCount == 0 {
		s.WorkerCount = d.WorkerCount
	}
	if s.BatchSize == 0 {
		s.BatchSize = d.BatchSize
	}
	if s.FlushInterval == 0 {
		s.FlushInterval = d.FlushInterval
	}
	if s.RetentionDays == 0 {
		s.RetentionDays = d.RetentionDays
	}
	if s.VerifyInterval == 0 {
		s.VerifyInterval = d.VerifyInterval
	}
	if s.VerifyBatchSize == 0 {
		s.VerifyBatchSize = d.VerifyBatchSize
	}
	if s.FingerprintAlgo == "" {
		s.FingerprintAlgo = d.FingerprintAlgo
	}
	// 指纹算法选定后不可更换；未知值在启动接线处回退并告警
}

// apply 零值字段回填默认值
func (s *RateLimitConfig) apply(d RateLimitConfig) {
	if s.Strategy == "" {
		s.Strategy = d.Strategy
	}
	if s.DefaultRPS == 0 {
		s.DefaultRPS = d.DefaultRPS
	}
	if s.DefaultTPM == 0 {
		s.DefaultTPM = d.DefaultTPM
	}
}

// apply 零值字段回填默认值
func (s *ExportConfig) apply(d ExportConfig) {
	if s.Type == "" {
		s.Type = d.Type
	}
	if s.BatchSize == 0 {
		s.BatchSize = d.BatchSize
	}
	if s.FlushInterval == 0 {
		s.FlushInterval = d.FlushInterval
	}
}

// apply 零值字段回填默认值（OfflineMode bool 字段不处理）
func (s *LicenseConfig) apply(d LicenseConfig) {
	if s.FilePath == "" {
		s.FilePath = d.FilePath
	}
}

// apply 零值字段回填默认值
func (s *LogConfig) apply(d LogConfig) {
	if s.Level == "" {
		s.Level = d.Level
	}
	if s.Format == "" {
		s.Format = d.Format
	}
	if s.Output == "" {
		s.Output = d.Output
	}
}
