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
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/google/uuid"
)

// ErrNotFound 记录不存在
var ErrNotFound = errors.New("record not found")

// MemStorage 内存存储实现
type MemStorage struct {
	mu           sync.RWMutex
	apiKeys      map[string]*plugin.APIKey          // keyHash -> key
	modelConfigs map[string]*plugin.ModelConfig     // modelName -> config
	auditLogs    []*plugin.AuditLog                 // 按写入顺序
	tamperAlerts map[string]*plugin.TamperAlert     // 告警ID -> 篡改告警
	rateLimits   map[string]*plugin.RateLimitConfig // id -> config
	upstreams    map[string]*plugin.Upstream        // id -> upstream
	adminUsers   map[string]*plugin.AdminUser       // id -> 管理后台账号
}

// NewMemStorage 创建内存存储
func NewMemStorage() *MemStorage {
	return &MemStorage{
		apiKeys:      make(map[string]*plugin.APIKey),
		modelConfigs: make(map[string]*plugin.ModelConfig),
		rateLimits:   make(map[string]*plugin.RateLimitConfig),
		upstreams:    make(map[string]*plugin.Upstream),
		tamperAlerts: make(map[string]*plugin.TamperAlert),
		adminUsers:   make(map[string]*plugin.AdminUser),
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

// ===== 管理后台账号 =====

func (s *MemStorage) CountAdminUsers() (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return int64(len(s.adminUsers)), nil
}

func (s *MemStorage) GetAdminUserByUsername(username string) (*plugin.AdminUser, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, u := range s.adminUsers {
		if u.Username == username {
			return u, nil
		}
	}
	return nil, ErrNotFound
}

func (s *MemStorage) GetAdminUserByID(id string) (*plugin.AdminUser, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if u, ok := s.adminUsers[id]; ok {
		return u, nil
	}
	return nil, ErrNotFound
}

// SaveAdminUser UPSERT：按主键插入或全量更新
func (s *MemStorage) SaveAdminUser(user *plugin.AdminUser) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.adminUsers[user.ID] = user
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

// ===== 限流配置 =====

func (s *MemStorage) GetRateLimitConfig(tenantID, modelName string) (*plugin.RateLimitConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, c := range s.rateLimits {
		if c.TenantID == tenantID && c.ModelName == modelName {
			return c, nil
		}
	}
	return nil, ErrNotFound
}

func (s *MemStorage) SaveRateLimitConfig(cfg *plugin.RateLimitConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rateLimits[cfg.ID] = cfg
	return nil
}

func (s *MemStorage) ListRateLimitConfigs(page, size int) ([]*plugin.RateLimitConfig, int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var all []*plugin.RateLimitConfig
	for _, c := range s.rateLimits {
		all = append(all, c)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	page, size = normalizePage(page, size)
	start := min((page-1)*size, len(all))
	end := min(start+size, len(all))
	return all[start:end], int64(len(all)), nil
}

func (s *MemStorage) DeleteRateLimitConfig(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rateLimits[id]; !ok {
		return ErrNotFound
	}
	delete(s.rateLimits, id)
	return nil
}

// ===== 上游管理 =====

func (s *MemStorage) ListUpstreams(modelConfigID string) ([]*plugin.Upstream, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var ups []*plugin.Upstream
	for _, u := range s.upstreams {
		if u.ModelConfigID == modelConfigID {
			ups = append(ups, u)
		}
	}
	sort.Slice(ups, func(i, j int) bool { return ups[i].ID < ups[j].ID })
	return ups, nil
}

func (s *MemStorage) GetUpstreamByID(id string) (*plugin.Upstream, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if u, ok := s.upstreams[id]; ok {
		return u, nil
	}
	return nil, ErrNotFound
}

func (s *MemStorage) SaveUpstream(up *plugin.Upstream) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upstreams[up.ID] = up
	return nil
}

func (s *MemStorage) DeleteUpstream(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.upstreams[id]; !ok {
		return ErrNotFound
	}
	delete(s.upstreams, id)
	return nil
}

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

// DeleteAuditLogsBefore 删除 cutoff 之前的审计日志，返回删除条数
func (s *MemStorage) DeleteAuditLogsBefore(cutoff time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.auditLogs[:0]
	var n int64
	for _, l := range s.auditLogs {
		if l.CreatedAt.Before(cutoff) {
			n++
			continue
		}
		kept = append(kept, l)
	}
	s.auditLogs = kept
	return n, nil
}

// SaveTamperAlerts upsert 篡改告警：同一 AuditLogID 存在未处置告警则更新检查时间，否则插入
func (s *MemStorage) SaveTamperAlerts(alerts []*plugin.TamperAlert) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for _, a := range alerts {
		if existing := s.findUnresolvedAlertLocked(a.AuditLogID); existing != nil {
			existing.Reason = a.Reason
			existing.LastCheckedAt = now
			continue
		}
		cp := *a
		if cp.ID == "" {
			cp.ID = uuid.NewString()
		}
		if cp.FirstSeenAt.IsZero() {
			cp.FirstSeenAt = now
		}
		cp.LastCheckedAt = now
		s.tamperAlerts[cp.ID] = &cp
	}
	return nil
}

// findUnresolvedAlertLocked 查找指定日志的未处置告警（调用方须持锁）
func (s *MemStorage) findUnresolvedAlertLocked(auditLogID string) *plugin.TamperAlert {
	for _, a := range s.tamperAlerts {
		if a.AuditLogID == auditLogID && !a.Resolved {
			return a
		}
	}
	return nil
}

// ListTamperAlerts 查询篡改告警：resolved nil=全部；按最近检查时间倒序分页
func (s *MemStorage) ListTamperAlerts(resolved *bool, page, size int) ([]*plugin.TamperAlert, int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	matched := make([]*plugin.TamperAlert, 0, len(s.tamperAlerts))
	for _, a := range s.tamperAlerts {
		if resolved != nil && a.Resolved != *resolved {
			continue
		}
		matched = append(matched, a)
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].LastCheckedAt.After(matched[j].LastCheckedAt) })
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

// SetTamperAlertResolved 标记告警处置状态
func (s *MemStorage) SetTamperAlertResolved(id string, resolved bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.tamperAlerts[id]
	if !ok {
		return fmt.Errorf("告警不存在: %s", id)
	}
	a.Resolved = resolved
	return nil
}
