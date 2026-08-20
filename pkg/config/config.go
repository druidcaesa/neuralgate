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
	Server    ServerConfig    `yaml:"server"`
	Storage   StorageConfig   `yaml:"storage"`
	Audit     AuditConfig     `yaml:"audit"`
	RateLimit RateLimitConfig `yaml:"rate_limit"`
	Export    ExportConfig    `yaml:"export"`
	License   LicenseConfig   `yaml:"license"`
	Log       LogConfig       `yaml:"log"`
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
	MaxOpenConns int    `yaml:"max_open_conns"`
	MaxIdleConns int    `yaml:"max_idle_conns"`
}

type AuditConfig struct {
	QueueSize     int           `yaml:"queue_size"`
	WorkerCount   int           `yaml:"worker_count"`
	BatchSize     int           `yaml:"batch_size"`
	FlushInterval time.Duration `yaml:"flush_interval"`
	EnableSHA256  bool          `yaml:"enable_sha256"`
	RetentionDays int           `yaml:"retention_days"`
}

type RateLimitConfig struct {
	Strategy   string `yaml:"strategy"`
	DefaultRPS int    `yaml:"default_rps"`
	DefaultTPM int64  `yaml:"default_tpm"`
}

type ExportConfig struct {
	Type          string        `yaml:"type"`
	Endpoint      string        `yaml:"endpoint"`
	APIKey        string        `yaml:"api_key"`
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
			Driver:       "mem",
			MaxOpenConns: 20,
			MaxIdleConns: 10,
		},
		Audit: AuditConfig{
			QueueSize:     65536,
			WorkerCount:   4,
			BatchSize:     100,
			FlushInterval: 5 * time.Second,
			RetentionDays: 90,
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
