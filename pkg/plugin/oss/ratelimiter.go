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

package oss

import (
	"sync"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// resolvedConfig 三层解析后生效的限流规则
type resolvedConfig struct {
	rps      int
	tpm      int64
	strategy string
}

// limitBucket 单个 (tenant|model) 的双维度限流器
type limitBucket struct {
	strategy string
	// token_bucket 用
	rpsBucket *tokenBucket
	tpmBucket *tokenBucket
	// sliding_window 用
	rpsWindow *slidingWindow
	tpmWindow *slidingWindow
}

// RateLimiter 三层配置 + 双维度双策略限流器
type RateLimiter struct {
	storage         plugin.StoragePlugin
	defaultRPS      int
	defaultTPM      int64
	defaultStrategy string

	mu      sync.Mutex
	configs []*plugin.RateLimitConfig // 缓存(ReloadConfig 刷新)
	buckets map[string]*limitBucket   // key = tenant|model
	now     func() time.Time          // 可注入(测试用),默认 time.Now
}

// NewRateLimiter 创建限流器
func NewRateLimiter(storage plugin.StoragePlugin, defaultRPS int, defaultTPM int64, defaultStrategy string) *RateLimiter {
	return &RateLimiter{
		storage:         storage,
		defaultRPS:      defaultRPS,
		defaultTPM:      defaultTPM,
		defaultStrategy: defaultStrategy,
		buckets:         make(map[string]*limitBucket),
		now:             time.Now,
	}
}

// Init 解析 default_rps/default_tpm/strategy 覆盖构造默认值,并触发首次加载
func (l *RateLimiter) Init(config map[string]interface{}) error {
	if v, ok := config["default_rps"].(int); ok && v > 0 {
		l.defaultRPS = v
	}
	switch v := config["default_tpm"].(type) {
	case int64:
		if v > 0 {
			l.defaultTPM = v
		}
	case int:
		if v > 0 {
			l.defaultTPM = int64(v)
		}
	}
	if v, ok := config["strategy"].(string); ok && v != "" {
		l.defaultStrategy = v
	}
	return l.ReloadConfig()
}

// ReloadConfig 从存储全量加载限流配置到缓存,并清空桶(下次按新配置重建)
func (l *RateLimiter) ReloadConfig() error {
	var all []*plugin.RateLimitConfig
	page := 1
	for {
		cfgs, total, err := l.storage.ListRateLimitConfigs(nil, page, 100)
		if err != nil {
			return err
		}
		all = append(all, cfgs...)
		if page*100 >= int(total) {
			break
		}
		page++
	}
	l.mu.Lock()
	l.configs = all
	l.buckets = make(map[string]*limitBucket)
	l.mu.Unlock()
	return nil
}

// resolve 三层匹配:模型级 > 租户级 > 全局 > 默认(调用方持锁)
func (l *RateLimiter) resolve(tenantID, model string) resolvedConfig {
	match := func(tid, m string) *plugin.RateLimitConfig {
		for _, c := range l.configs {
			if c.Enabled && c.TenantID == tid && c.ModelName == m {
				return c
			}
		}
		return nil
	}
	if c := match(tenantID, model); c != nil {
		return resolvedConfig{c.RequestsPerSec, c.TokensPerMin, c.Strategy}
	}
	if c := match(tenantID, ""); c != nil {
		return resolvedConfig{c.RequestsPerSec, c.TokensPerMin, c.Strategy}
	}
	if c := match("", ""); c != nil {
		return resolvedConfig{c.RequestsPerSec, c.TokensPerMin, c.Strategy}
	}
	return resolvedConfig{l.defaultRPS, l.defaultTPM, l.defaultStrategy}
}

// bucketFor 获取或创建 (tenant|model) 桶(调用方持锁)
func (l *RateLimiter) bucketFor(tenantID, model string) *limitBucket {
	key := tenantID + "|" + model
	if b, ok := l.buckets[key]; ok {
		return b
	}
	rc := l.resolve(tenantID, model)
	now := l.now()
	b := &limitBucket{strategy: rc.strategy}
	if rc.strategy == "sliding_window" {
		b.rpsWindow = newSlidingWindow(int64(rc.rps), time.Second)
		b.tpmWindow = newSlidingWindow(rc.tpm, time.Minute)
	} else {
		b.rpsBucket = newTokenBucket(float64(rc.rps), float64(rc.rps), now)
		b.tpmBucket = newTokenBucket(float64(rc.tpm), float64(rc.tpm)/60.0, now)
	}
	l.buckets[key] = b
	return b
}

// Allow 预检:取 1 个 RPS 令牌 + 校验 TPM 是否已耗尽(tokens 参数保留,预检传 0 不扣 TPM)
func (l *RateLimiter) Allow(tenantID string, model string, tokens int) (bool, int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.bucketFor(tenantID, model)
	now := l.now()
	if b.strategy == "sliding_window" {
		// TPM 已达上限则拒(current >= limit)
		if b.tpmWindow.current(now) >= b.tpmWindow.limit {
			return false, 0, nil
		}
		if !b.rpsWindow.allow(1, now) {
			return false, 0, nil
		}
		return true, b.rpsWindow.limit - b.rpsWindow.current(now), nil
	}
	// token_bucket:TPM 桶无可用令牌则拒
	if b.tpmBucket.remaining(now) <= 0 {
		return false, 0, nil
	}
	if !b.rpsBucket.take(1, now) {
		return false, 0, nil
	}
	return true, b.rpsBucket.remaining(now), nil
}

// RecordTokens 请求完成后回补 TPM 计数
func (l *RateLimiter) RecordTokens(tenantID string, model string, tokens int) error {
	if tokens <= 0 {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.bucketFor(tenantID, model)
	now := l.now()
	if b.strategy == "sliding_window" {
		b.tpmWindow.add(int64(tokens), now) // 无条件累加 TPM 计数
	} else {
		b.tpmBucket.consume(float64(tokens), now) // 无条件扣减 TPM 计数
	}
	return nil
}

// Status 返回当前 RPS 用量/上限/重置时间
func (l *RateLimiter) Status(tenantID string, model string) (int64, int64, time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.bucketFor(tenantID, model)
	now := l.now()
	rc := l.resolve(tenantID, model)
	if b.strategy == "sliding_window" {
		return b.rpsWindow.current(now), int64(rc.rps), now.Add(time.Second)
	}
	used := int64(rc.rps) - b.rpsBucket.remaining(now)
	if used < 0 {
		used = 0
	}
	return used, int64(rc.rps), now.Add(time.Second)
}

// Reset 清除某 (tenant|model) 桶
func (l *RateLimiter) Reset(tenantID string, model string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.buckets, tenantID+"|"+model)
	return nil
}
