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
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/config"
	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// tokenBucketScript 令牌桶原子判定+扣减：
// KEYS[1]=桶键 ARGV: 容量/每秒补充/当前毫秒/本次消耗
// 返回 {allowed(0|1), 剩余令牌}
var tokenBucketScript = redis.NewScript(`
local key = KEYS[1]
local capacity = tonumber(ARGV[1])
local refill = tonumber(ARGV[2])
local now_ms = tonumber(ARGV[3])
local cost = tonumber(ARGV[4])
local data = redis.call('HMGET', key, 'tokens', 'ts')
local tokens = tonumber(data[1])
local ts = tonumber(data[2])
if tokens == nil then
    tokens = capacity
    ts = now_ms
end
local elapsed = math.max(0, now_ms - ts) / 1000.0
tokens = math.min(capacity, tokens + elapsed * refill)
local allowed = 0
if tokens >= cost then
    tokens = tokens - cost
    allowed = 1
end
redis.call('HMSET', key, 'tokens', tokens, 'ts', now_ms)
redis.call('PEXPIRE', key, math.ceil(capacity / refill * 2000) + 60000)
return {allowed, tostring(math.floor(tokens))}
`)

// tpmWindowScript TPM 固定窗口计数：超限不再累加（拒绝请求不计消耗）
// KEYS[1]=窗口键 ARGV: 上限/本次token/当前毫秒/窗口毫秒 → {allowed, 当前计数}
var tpmWindowScript = redis.NewScript(`
local key = KEYS[1]
local limit = tonumber(ARGV[1])
local cost = tonumber(ARGV[2])
local now_ms = tonumber(ARGV[3])
local window_ms = tonumber(ARGV[4])
local used = tonumber(redis.call('GET', key) or '0')
if used + cost > limit then
    return {0, used}
end
used = redis.call('INCRBY', key, cost)
redis.call('PEXPIRE', key, window_ms)
return {1, used}
`)

// DistributedRateLimiter Redis 集中计数的分布式限流器：
// RPS 走令牌桶 Lua，TPM 走固定窗口；阈值三层配置经本地存储热加载缓存。
// 可用性优先：Redis 故障回退内嵌本地限流器判定并告警（单实例保护仍在）
type DistributedRateLimiter struct {
	local plugin.RateLimitPlugin // 降级路径；ReloadConfig 时同步刷新其阈值缓存
	rdb   *redis.Client
	cfg   config.DistributedRateLimitConfig

	mu        sync.RWMutex
	overrides map[string][2]int64 // "tenant|model" → {rps, tpm}（空串表示全局维度）
	logger    *zap.Logger
	now       func() time.Time
}

// NewDistributedRateLimiter 创建分布式限流器。local 必传——既是降级路径，
// 也是三层配置的存储加载载体（ReloadConfig 会同时刷新两者）
func NewDistributedRateLimiter(local plugin.RateLimitPlugin, cfg config.DistributedRateLimitConfig, logger *zap.Logger) (*DistributedRateLimiter, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr: cfg.RedisAddr, Password: cfg.RedisPassword, DB: cfg.RedisDB,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis 连接失败: %w", err)
	}
	return &DistributedRateLimiter{
		local: local, rdb: rdb, cfg: cfg,
		overrides: make(map[string][2]int64),
		logger:    logger,
		now:       time.Now,
	}, nil
}

// Init 透传本地实现（保持接口完整）
func (d *DistributedRateLimiter) Init(config map[string]interface{}) error {
	return d.local.Init(config)
}

// Allow 双维度判定：RPS 令牌桶 + TPM 固定窗，任一拒绝即拒绝。
// Redis 任一脚本失败 → 回退本地限流器结果（可用性优先）
func (d *DistributedRateLimiter) Allow(tenantID string, model string, tokens int) (bool, int64, error) {
	rps, tpm := d.thresholdsFor(tenantID, model)
	nowMS := d.now().UnixMilli()
	ctx := context.Background()

	tbKey := d.bucketKey("rl:tb", tenantID, model)
	res, err := tokenBucketScript.Run(ctx, d.rdb, []string{tbKey},
		rps, rps, nowMS, 1).Result()
	if err != nil {
		d.logger.Warn("分布式限流 Redis 异常，回退本地判定", zap.Error(err))
		return d.local.Allow(tenantID, model, tokens)
	}
	allowed := res.([]interface{})[0].(int64) == 1
	remaining, _ := strconv.ParseInt(res.([]interface{})[1].(string), 10, 64)
	if !allowed {
		return false, remaining, nil
	}
	if tokens > 0 && tpm > 0 {
		winKey := d.bucketKey("rl:tpm", tenantID, model) + ":" +
			strconv.FormatInt(nowMS/(60*1000), 10)
		tres, err := tpmWindowScript.Run(ctx, d.rdb, []string{winKey},
			tpm, tokens, nowMS, 2*60*1000).Result()
		if err != nil {
			d.logger.Warn("分布式限流 TPM 检查异常，按 RPS 结果放行", zap.Error(err))
			return true, remaining, nil
		}
		if tres.([]interface{})[0].(int64) == 0 {
			return false, remaining, nil
		}
	}
	return true, remaining, nil
}

// Status 本地近似值：跨实例真实余量在 Redis 内，管理面展示以单实例口径为准
func (d *DistributedRateLimiter) Status(tenantID string, model string) (int64, int64, time.Time) {
	return d.local.Status(tenantID, model)
}

// Reset 同时清本地与 Redis 对应键
func (d *DistributedRateLimiter) Reset(tenantID string, model string) error {
	if err := d.local.Reset(tenantID, model); err != nil {
		return err
	}
	return d.rdb.Del(context.Background(),
		d.bucketKey("rl:tb", tenantID, model)).Err()
}

// RecordTokens 记录已消耗 Token（TPM 窗口累加；Redis 异常仅落本地）
func (d *DistributedRateLimiter) RecordTokens(tenantID string, model string, tokens int) error {
	if err := d.local.RecordTokens(tenantID, model, tokens); err != nil {
		d.logger.Warn("本地 Token 回补失败", zap.Error(err))
	}
	_, tpm := d.thresholdsFor(tenantID, model)
	if tokens <= 0 || tpm <= 0 {
		return nil
	}
	nowMS := d.now().UnixMilli()
	winKey := d.bucketKey("rl:tpm", tenantID, model) + ":" +
		strconv.FormatInt(nowMS/(60*1000), 10)
	return d.rdb.IncrBy(context.Background(), winKey, int64(tokens)).Err()
}

// ReloadConfig 刷新三层配置缓存并同时刷新本地限流器
func (d *DistributedRateLimiter) ReloadConfig() error {
	if err := d.local.ReloadConfig(); err != nil {
		return err
	}
	d.mu.Lock()
	d.overrides = make(map[string][2]int64)
	d.mu.Unlock()
	return nil
}

// thresholdsFor 三层命中：精确 tenant|model → 全局租户 ""|model → 全局 ""|""。
// 缓存未命中的键走构造时的默认值（默认 RPS/TPM 由 local 的存储加载体现于回退路径）
func (d *DistributedRateLimiter) thresholdsFor(tenantID, model string) (int, int) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, k := range []string{tenantID + "|" + model, "|" + model, "|"} {
		if v, ok := d.overrides[k]; ok {
			return int(v[0]), int(v[1])
		}
	}
	return 0, 0
}

// SetOverride 注入维度阈值（main 装配时以默认值填充）
func (d *DistributedRateLimiter) SetOverride(tenantID, model string, rps int, tpm int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.overrides[tenantID+"|"+model] = [2]int64{int64(rps), tpm}
}

func (d *DistributedRateLimiter) bucketKey(prefix, tenantID, model string) string {
	return prefix + ":" + tenantID + ":" + model
}
