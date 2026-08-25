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
	"sync"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// tenantCheckInterval 租户状态缓存重载周期：租户禁用生效延迟上界
const tenantCheckInterval = 30 * time.Second

// TenantGate 数据面租户联动门：缓存禁用租户集合 + TTL 自动重载。
// tenants 空表（OSS/未启用租户体系）恒放行；存储异常降级放行（可用性优先，
// 且存储不可用时鉴权本身也无法通过）
type TenantGate struct {
	storage plugin.StoragePlugin
	ttl     time.Duration

	mu       sync.RWMutex
	loadedAt time.Time
	empty    bool
	disabled map[string]bool
}

// NewTenantGate 创建门；ttl 为重载周期（生产 30s，测试注入短值）
func NewTenantGate(storage plugin.StoragePlugin, ttl time.Duration) *TenantGate {
	return &TenantGate{storage: storage, ttl: ttl}
}

// Allowed 判定租户是否放行；空租户 ID 恒放行
func (g *TenantGate) Allowed(tenantID string) bool {
	if tenantID == "" {
		return true
	}
	g.mu.RLock()
	expired := g.loadedAt.IsZero() || time.Since(g.loadedAt) > g.ttl
	empty, disabled := g.empty, g.disabled
	g.mu.RUnlock()
	if expired {
		g.reload()
		g.mu.RLock()
		empty, disabled = g.empty, g.disabled
		g.mu.RUnlock()
	}
	if empty {
		return true
	}
	return !disabled[tenantID]
}

// reload 拉取全量租户换入禁用集合；失败时视为空表放行（降级）
func (g *TenantGate) reload() {
	total, err := g.storage.CountTenants()
	if err != nil || total == 0 {
		g.swap(true, nil)
		return
	}
	disabled := make(map[string]bool)
	for page := 1; ; page++ {
		ts, _, lerr := g.storage.ListTenants(page, 100)
		if lerr != nil {
			g.swap(true, nil)
			return
		}
		for _, t := range ts {
			if t.Status == plugin.TenantStatusDisabled {
				disabled[t.ID] = true
			}
		}
		if len(ts) < 100 {
			break
		}
	}
	g.swap(false, disabled)
}

func (g *TenantGate) swap(empty bool, disabled map[string]bool) {
	g.mu.Lock()
	g.empty = empty
	g.disabled = disabled
	g.loadedAt = time.Now()
	g.mu.Unlock()
}
