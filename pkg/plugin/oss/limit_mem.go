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
)

// windowEntry 固定窗口计数（每秒重置）
type windowEntry struct {
	count       int
	windowStart time.Time
}

// MemRateLimiter 内存限流（固定窗口，骨架期最简实现；令牌桶 Phase 4 精细化）
type MemRateLimiter struct {
	mu         sync.Mutex
	windows    map[string]*windowEntry
	defaultRPS int
}

// NewMemRateLimiter 创建内存限流器
func NewMemRateLimiter() *MemRateLimiter {
	return &MemRateLimiter{
		windows:    make(map[string]*windowEntry),
		defaultRPS: 10,
	}
}

// Init 初始化限流配置，支持 "default_rps" (int) 配置项
func (l *MemRateLimiter) Init(config map[string]interface{}) error {
	if v, ok := config["default_rps"]; ok {
		if rps, ok := v.(int); ok && rps > 0 {
			l.defaultRPS = rps
		}
	}
	return nil
}

// Allow 尝试获取令牌：当前秒窗口内计数未超过上限则允许
func (l *MemRateLimiter) Allow(tenantID string, model string, tokens int) (bool, int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := tenantID + "|" + model
	now := time.Now()
	e, ok := l.windows[key]
	if !ok || now.Sub(e.windowStart) >= time.Second {
		e = &windowEntry{windowStart: now}
		l.windows[key] = e
	}
	e.count++
	if e.count > l.defaultRPS {
		return false, 0, nil
	}
	return true, int64(l.defaultRPS - e.count), nil
}

// Status 获取当前限流状态
func (l *MemRateLimiter) Status(tenantID string, model string) (int64, int64, time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := tenantID + "|" + model
	e, ok := l.windows[key]
	if !ok {
		return 0, int64(l.defaultRPS), time.Now().Add(time.Second)
	}
	return int64(e.count), int64(l.defaultRPS), e.windowStart.Add(time.Second)
}

// Reset 重置限流计数器
func (l *MemRateLimiter) Reset(tenantID string, model string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.windows, tenantID+"|"+model)
	return nil
}
