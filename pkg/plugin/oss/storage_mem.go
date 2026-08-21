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
	"errors"
	"sort"
	"strings"
	"sync"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// ErrNotFound 记录不存在
var ErrNotFound = errors.New("record not found")

// MemStorage 内存存储实现
type MemStorage struct {
	mu           sync.RWMutex
	apiKeys      map[string]*plugin.APIKey      // keyHash -> key
	modelConfigs map[string]*plugin.ModelConfig // modelName -> config
	auditLogs    []*plugin.AuditLog             // 按写入顺序
}

// NewMemStorage 创建内存存储
func NewMemStorage() *MemStorage {
	return &MemStorage{
		apiKeys:      make(map[string]*plugin.APIKey),
		modelConfigs: make(map[string]*plugin.ModelConfig),
	}
}

// Init 初始化存储连接（内存实现无需连接）
func (s *MemStorage) Init(config map[string]interface{}) error { return nil }

// ===== API Key 管理 =====

func (s *MemStorage) GetAPIKey(keyHash string) (*plugin.APIKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if k, ok := s.apiKeys[keyHash]; ok {
		return k, nil
	}
	return nil, ErrNotFound
}

func (s *MemStorage) GetAPIKeyByID(id string) (*plugin.APIKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, k := range s.apiKeys {
		if k.ID == id {
			return k, nil
		}
	}
	return nil, ErrNotFound
}

func (s *MemStorage) SaveAPIKey(key *plugin.APIKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.apiKeys[key.KeyHash] = key
	return nil
}

// IncrementAPIKeyUsage 原子累加已用额度(写锁内原地修改,并发安全)
func (s *MemStorage) IncrementAPIKeyUsage(keyID string, delta int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range s.apiKeys {
		if k.ID == keyID {
			k.UsedQuota += delta
			return nil
		}
	}
	return ErrNotFound
}

func (s *MemStorage) ListAPIKeys(tenantID string, page, size int) ([]*plugin.APIKey, int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var all []*plugin.APIKey
	for _, k := range s.apiKeys {
		if tenantID == "" || k.TenantID == tenantID {
			all = append(all, k)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	page, size = normalizePage(page, size)
	start := min((page-1)*size, len(all))
	end := min(start+size, len(all))
	return all[start:end], int64(len(all)), nil
}

func (s *MemStorage) DeleteAPIKey(keyID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for hash, k := range s.apiKeys {
		if k.ID == keyID {
			delete(s.apiKeys, hash)
			return nil
		}
	}
	return ErrNotFound
}

// ===== 模型配置管理 =====

func (s *MemStorage) GetModelConfig(modelName string) (*plugin.ModelConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if c, ok := s.modelConfigs[modelName]; ok {
		return c, nil
	}
	return nil, ErrNotFound
}

func (s *MemStorage) GetModelConfigByID(id string) (*plugin.ModelConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, c := range s.modelConfigs {
		if c.ID == id {
			return c, nil
		}
	}
	return nil, ErrNotFound
}

func (s *MemStorage) ListModelConfigs(page, size int) ([]*plugin.ModelConfig, int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var all []*plugin.ModelConfig
	for _, c := range s.modelConfigs {
		all = append(all, c)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ModelName < all[j].ModelName })
	page, size = normalizePage(page, size)
	start := (page - 1) * size
	if start > len(all) {
		start = len(all)
	}
	end := start + size
	if end > len(all) {
		end = len(all)
	}
	return all[start:end], int64(len(all)), nil
}

func (s *MemStorage) SaveModelConfig(config *plugin.ModelConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.modelConfigs[config.ModelName] = config
	return nil
}

func (s *MemStorage) DeleteModelConfig(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, c := range s.modelConfigs {
		if c.ID == id {
			delete(s.modelConfigs, name)
			return nil
		}
	}
	return ErrNotFound
}

// ===== 审计日志 =====

func (s *MemStorage) SaveAuditLog(log *plugin.AuditLog) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.auditLogs = append(s.auditLogs, log)
	return nil
}

func (s *MemStorage) BatchSaveAuditLogs(logs []*plugin.AuditLog) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.auditLogs = append(s.auditLogs, logs...)
	return nil
}

func (s *MemStorage) QueryAuditLogs(filter plugin.AuditLogFilter, page, size int) ([]*plugin.AuditLog, int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var matched []*plugin.AuditLog
	for _, l := range s.auditLogs {
		if !matchAuditLog(l, filter) {
			continue
		}
		matched = append(matched, l)
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].CreatedAt.After(matched[j].CreatedAt) })
	page, size = normalizePage(page, size)
	start := (page - 1) * size
	if start > len(matched) {
		start = len(matched)
	}
	end := start + size
	if end > len(matched) {
		end = len(matched)
	}
	return matched[start:end], int64(len(matched)), nil
}

// ===== 健康检查 =====

func (s *MemStorage) Ping() error { return nil }

func (s *MemStorage) Close() error { return nil }

// normalizePage 分页参数规范化：page>=1，size 取 [1,100]
func normalizePage(page, size int) (int, int) {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 10
	}
	if size > 100 {
		size = 100
	}
	return page, size
}

// matchAuditLog 按过滤器匹配审计日志
func matchAuditLog(l *plugin.AuditLog, f plugin.AuditLogFilter) bool {
	if f.RequestID != "" && l.RequestID != f.RequestID {
		return false
	}
	if f.TenantID != "" && l.TenantID != f.TenantID {
		return false
	}
	if f.APIKeyID != "" && l.APIKeyID != f.APIKeyID {
		return false
	}
	if f.ModelName != "" && l.ModelName != f.ModelName {
		return false
	}
	if f.Status != 0 && l.ResponseStatus != f.Status {
		return false
	}
	if f.IsStream != nil && l.IsStream != *f.IsStream {
		return false
	}
	if f.StartTime != nil && l.CreatedAt.Before(*f.StartTime) {
		return false
	}
	if f.EndTime != nil && l.CreatedAt.After(*f.EndTime) {
		return false
	}
	if f.Keyword != "" {
		haystack := strings.Join([]string{
			l.RequestID, l.TenantID, l.APIKeyID, l.ModelName,
			l.RequestBody, l.ResponseBody, l.DisconnectReason,
		}, " ")
		if !strings.Contains(haystack, f.Keyword) {
			return false
		}
	}
	return true
}
