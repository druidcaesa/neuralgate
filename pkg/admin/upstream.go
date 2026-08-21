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
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// upstreamRequest 上游创建/更新请求体
type upstreamRequest struct {
	BaseURL string `json:"base_url" binding:"required"`
	APIKey  string `json:"api_key" binding:"required"`
	Weight  int    `json:"weight"`
	Enabled *bool  `json:"enabled"`
}

// createUpstream POST /api/models/:id/upstreams
func (s *AdminServer) createUpstream(c *gin.Context) {
	modelID := c.Param("id")
	if _, err := s.storage.GetModelConfigByID(modelID); err != nil {
		Error(c, http.StatusNotFound, 404, "model config not found")
		return
	}
	var req upstreamRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	if req.Weight < 1 {
		req.Weight = 1
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	now := time.Now()
	up := &plugin.Upstream{
		ID: uuid.NewString(), ModelConfigID: modelID, BaseURL: req.BaseURL,
		APIKey: req.APIKey, Weight: req.Weight, Enabled: enabled,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.storage.SaveUpstream(up); err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to save upstream")
		return
	}
	OK(c, gin.H{"id": up.ID})
}

// listUpstreams GET /api/models/:id/upstreams(api_key 脱敏不回显)
func (s *AdminServer) listUpstreams(c *gin.Context) {
	modelID := c.Param("id")
	ups, err := s.storage.ListUpstreams(modelID)
	if err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to list upstreams")
		return
	}
	type item struct {
		ID        string    `json:"id"`
		BaseURL   string    `json:"base_url"`
		Weight    int       `json:"weight"`
		Enabled   bool      `json:"enabled"`
		CreatedAt time.Time `json:"created_at"`
	}
	items := make([]item, 0, len(ups))
	for _, u := range ups {
		items = append(items, item{ID: u.ID, BaseURL: u.BaseURL, Weight: u.Weight, Enabled: u.Enabled, CreatedAt: u.CreatedAt})
	}
	OK(c, gin.H{"items": items, "total": len(items)})
}

// updateUpstream PUT /api/upstreams/:uid
func (s *AdminServer) updateUpstream(c *gin.Context) {
	uid := c.Param("uid")
	existing, err := s.storage.GetUpstreamByID(uid)
	if err != nil {
		Error(c, http.StatusNotFound, 404, "upstream not found")
		return
	}
	var req upstreamRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	if req.Weight < 1 {
		req.Weight = 1
	}
	existing.BaseURL = req.BaseURL
	existing.APIKey = req.APIKey
	existing.Weight = req.Weight
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}
	existing.UpdatedAt = time.Now()
	if err := s.storage.SaveUpstream(existing); err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to update upstream")
		return
	}
	OK(c, gin.H{"id": uid})
}

// deleteUpstream DELETE /api/upstreams/:uid
func (s *AdminServer) deleteUpstream(c *gin.Context) {
	uid := c.Param("uid")
	if err := s.storage.DeleteUpstream(uid); err != nil {
		Error(c, http.StatusNotFound, 404, "upstream not found")
		return
	}
	OK(c, gin.H{"id": uid, "deleted": true})
}
