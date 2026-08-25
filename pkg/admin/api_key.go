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

package admin

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// apiKeyCreateRequest 创建 API Key 请求体(字段校验按 PRD 3.2)
type apiKeyCreateRequest struct {
	Name          string     `json:"name" binding:"required,min=1,max=64"`
	TenantID      string     `json:"tenant_id"`
	Quota         *int64     `json:"quota"`      // 未传=无限(-1),显式 0=立即用尽
	RateLimit     int        `json:"rate_limit"` // 1-10000,默认 10
	AllowedModels []string   `json:"allowed_models"`
	ExpiresAt     *time.Time `json:"expires_at"`
}

// apiKeyUpdateRequest 更新 Key 请求体(禁用/启用)
type apiKeyUpdateRequest struct {
	Status string `json:"status" binding:"required,oneof=active disabled"`
}

// createAPIKey POST /api/api-keys:创建并返回明文(仅一次)
func (s *AdminServer) createAPIKey(c *gin.Context) {
	var req apiKeyCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	if req.RateLimit < 1 || req.RateLimit > 10000 {
		req.RateLimit = 10
	}
	if forced := s.scopeTenant(c); forced != nil {
		req.TenantID = *forced // 租户内用户创建的 Key 强制归属自身租户
	}
	if req.ExpiresAt != nil && req.ExpiresAt.Before(time.Now()) {
		Error(c, http.StatusBadRequest, 400, "expires_at must be in the future")
		return
	}

	// 生成随机 Key:ng- + 32 hex
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to generate key")
		return
	}
	rawKey := "ng-" + hex.EncodeToString(buf)
	sum := sha256.Sum256([]byte(rawKey))

	now := time.Now()
	// PRD 3.2:quota 未传默认 -1(无限);显式传 0 表示立即用尽
	quota := int64(-1)
	if req.Quota != nil {
		quota = *req.Quota
	}
	key := &plugin.APIKey{
		ID:            uuid.NewString(),
		KeyHash:       hex.EncodeToString(sum[:]),
		KeyPrefix:     rawKey[:11], // ng- + 8 hex
		TenantID:      req.TenantID,
		Name:          req.Name,
		Status:        plugin.APIKeyStatusActive,
		Quota:         quota,
		RateLimit:     req.RateLimit,
		AllowedModels: req.AllowedModels,
		ExpiresAt:     req.ExpiresAt,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.storage.SaveAPIKey(key); err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to save api key")
		return
	}
	OK(c, gin.H{
		"id": key.ID, "key": rawKey, "key_hash": key.KeyHash, "key_prefix": key.KeyPrefix,
		"name": key.Name, "quota": key.Quota, "rate_limit": key.RateLimit,
		"allowed_models": key.AllowedModels, "expires_at": key.ExpiresAt,
	})
}

// listAPIKeys GET /api/api-keys:分页列表(脱敏,不返回哈希;RBAC 启用时强制本租户)
func (s *AdminServer) listAPIKeys(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "10"))
	tenantID := c.Query("tenant_id")
	if forced := s.scopeTenant(c); forced != nil {
		tenantID = *forced
	}
	keys, total, err := s.storage.ListAPIKeys(tenantID, page, size)
	if err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to list api keys")
		return
	}
	type item struct {
		ID            string     `json:"id"`
		KeyPrefix     string     `json:"key_prefix"`
		Name          string     `json:"name"`
		Status        string     `json:"status"`
		Quota         int64      `json:"quota"`
		UsedQuota     int64      `json:"used_quota"`
		RateLimit     int        `json:"rate_limit"`
		AllowedModels []string   `json:"allowed_models"`
		ExpiresAt     *time.Time `json:"expires_at"`
		CreatedAt     time.Time  `json:"created_at"`
	}
	items := make([]item, 0, len(keys))
	for _, k := range keys {
		items = append(items, item{
			ID: k.ID, KeyPrefix: k.KeyPrefix + "****", Name: k.Name,
			Status: string(k.Status), Quota: k.Quota, UsedQuota: k.UsedQuota,
			RateLimit: k.RateLimit, AllowedModels: k.AllowedModels,
			ExpiresAt: k.ExpiresAt, CreatedAt: k.CreatedAt,
		})
	}
	OK(c, gin.H{"items": items, "total": total, "page": page, "size": size})
}

// updateAPIKey PATCH /api/api-keys/:id:禁用/启用（跨租户返回 404 不暴露存在性）
func (s *AdminServer) updateAPIKey(c *gin.Context) {
	id := c.Param("id")
	var req apiKeyUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	key, err := s.storage.GetAPIKeyByID(id)
	if err != nil || s.tenantMismatch(c, key.TenantID) {
		Error(c, http.StatusNotFound, 404, "api key not found")
		return
	}
	key.Status = plugin.APIKeyStatus(req.Status)
	key.UpdatedAt = time.Now()
	if err := s.storage.SaveAPIKey(key); err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to update api key")
		return
	}
	OK(c, gin.H{"id": id, "status": key.Status})
}

// deleteAPIKey DELETE /api/api-keys/:id:软删除（跨租户返回 404）
func (s *AdminServer) deleteAPIKey(c *gin.Context) {
	id := c.Param("id")
	key, err := s.storage.GetAPIKeyByID(id)
	if err != nil || s.tenantMismatch(c, key.TenantID) {
		Error(c, http.StatusNotFound, 404, "api key not found")
		return
	}
	if err := s.storage.DeleteAPIKey(id); err != nil {
		Error(c, http.StatusNotFound, 404, "api key not found")
		return
	}
	OK(c, gin.H{"id": id, "deleted": true})
}

// tenantMismatch 判断目标记录租户与当前会话租户不符（RBAC 关闭或超管恒为 false）
func (s *AdminServer) tenantMismatch(c *gin.Context, recordTenantID string) bool {
	forced := s.scopeTenant(c)
	return forced != nil && recordTenantID != *forced
}
