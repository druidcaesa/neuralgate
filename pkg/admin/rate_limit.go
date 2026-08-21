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
	"net/http"
	"strconv"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// rateLimitRequest 限流配置创建/更新请求体
type rateLimitRequest struct {
	TenantID       string `json:"tenant_id"`
	ModelName      string `json:"model_name"`
	RequestsPerSec int    `json:"requests_per_sec" binding:"required,min=1,max=100000"`
	TokensPerMin   int64  `json:"tokens_per_min" binding:"required,min=1,max=1000000000"`
	Strategy       string `json:"strategy" binding:"required,oneof=token_bucket sliding_window"`
	Enabled        *bool  `json:"enabled"`
}

// createRateLimit POST /api/rate-limits
func (s *AdminServer) createRateLimit(c *gin.Context) {
	var req rateLimitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	// 同维度唯一:已存在则 409
	if _, err := s.storage.GetRateLimitConfig(req.TenantID, req.ModelName); err == nil {
		Error(c, http.StatusConflict, 409, "该维度限流配置已存在")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	now := time.Now()
	cfg := &plugin.RateLimitConfig{
		ID: uuid.NewString(), TenantID: req.TenantID, ModelName: req.ModelName,
		RequestsPerSec: req.RequestsPerSec, TokensPerMin: req.TokensPerMin,
		Strategy: req.Strategy, Enabled: enabled, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.storage.SaveRateLimitConfig(cfg); err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to save rate limit config")
		return
	}
	_ = s.rateLimiter.ReloadConfig()
	OK(c, gin.H{"id": cfg.ID})
}

// listRateLimits GET /api/rate-limits
func (s *AdminServer) listRateLimits(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "10"))
	cfgs, total, err := s.storage.ListRateLimitConfigs(page, size)
	if err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to list rate limit configs")
		return
	}
	OK(c, gin.H{"items": cfgs, "total": total, "page": page, "size": size})
}

// updateRateLimit PUT /api/rate-limits/:id
func (s *AdminServer) updateRateLimit(c *gin.Context) {
	id := c.Param("id")
	var req rateLimitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	// 定位现有(存储无按 id 查限流的方法,用 List 找)
	cfgs, _, err := s.storage.ListRateLimitConfigs(1, 100000)
	if err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to load rate limit configs")
		return
	}
	var existing *plugin.RateLimitConfig
	for _, cfg := range cfgs {
		if cfg.ID == id {
			existing = cfg
			break
		}
	}
	if existing == nil {
		Error(c, http.StatusNotFound, 404, "rate limit config not found")
		return
	}
	existing.TenantID = req.TenantID
	existing.ModelName = req.ModelName
	existing.RequestsPerSec = req.RequestsPerSec
	existing.TokensPerMin = req.TokensPerMin
	existing.Strategy = req.Strategy
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}
	existing.UpdatedAt = time.Now()
	if err := s.storage.SaveRateLimitConfig(existing); err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to update rate limit config")
		return
	}
	_ = s.rateLimiter.ReloadConfig()
	OK(c, gin.H{"id": id})
}

// deleteRateLimit DELETE /api/rate-limits/:id
func (s *AdminServer) deleteRateLimit(c *gin.Context) {
	id := c.Param("id")
	if err := s.storage.DeleteRateLimitConfig(id); err != nil {
		Error(c, http.StatusNotFound, 404, "rate limit config not found")
		return
	}
	_ = s.rateLimiter.ReloadConfig()
	OK(c, gin.H{"id": id, "deleted": true})
}
