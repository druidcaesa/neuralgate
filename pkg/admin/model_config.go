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
	"strings"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// modelConfigRequest 模型配置请求体(字段校验按 PRD 3.1)
type modelConfigRequest struct {
	Name          string            `json:"name" binding:"required,min=1,max=64"`
	Provider      string            `json:"provider" binding:"required,oneof=openai tongyi zhipu deepseek"`
	ProviderModel string            `json:"provider_model" binding:"required,min=1,max=128"`
	BaseURL       string            `json:"base_url" binding:"required"`
	APIKey        string            `json:"api_key" binding:"omitempty,min=1,max=256"` // 创建必填;更新留空=保留原值
	Timeout       int               `json:"timeout"`                                   // 1-300,默认 60
	MaxRetries    int               `json:"max_retries"`                               // 0-5,默认 2
	RetryInterval int               `json:"retry_interval"`                            // 1-30,默认 3
	Weight        int               `json:"weight"`                                    // 1-100,默认 1
	Enabled       *bool             `json:"enabled"`                                   // 默认 true
	Tags          map[string]string `json:"tags"`
}

func (req *modelConfigRequest) normalize() {
	if req.Timeout < 1 || req.Timeout > 300 {
		req.Timeout = 60
	}
	if req.MaxRetries < 0 || req.MaxRetries > 5 {
		req.MaxRetries = 2
	}
	if req.RetryInterval < 1 || req.RetryInterval > 30 {
		req.RetryInterval = 3
	}
	if req.Weight < 1 || req.Weight > 100 {
		req.Weight = 1
	}
	if req.Enabled == nil {
		t := true
		req.Enabled = &t
	}
	if req.Tags == nil {
		req.Tags = map[string]string{}
	}
}

// createModelConfig POST /api/models
func (s *AdminServer) createModelConfig(c *gin.Context) {
	var req modelConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	req.normalize()
	// 名称唯一校验
	if _, err := s.storage.GetModelConfig(req.Name); err == nil {
		Error(c, http.StatusConflict, 409, "模型名称已存在")
		return
	}
	now := time.Now()
	config := &plugin.ModelConfig{
		ID: uuid.NewString(), ModelName: req.Name, Provider: req.Provider,
		ProviderModel: req.ProviderModel, BaseURL: req.BaseURL, APIKey: req.APIKey,
		Timeout: time.Duration(req.Timeout), MaxRetries: req.MaxRetries, RetryInterval: time.Duration(req.RetryInterval),
		Weight: req.Weight, Enabled: *req.Enabled, Tags: req.Tags,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.storage.SaveModelConfig(config); err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to save model config")
		return
	}
	OK(c, gin.H{"id": config.ID, "name": config.ModelName})
}

// listModelConfigs GET /api/models(不回显上游 api_key)
func (s *AdminServer) listModelConfigs(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "10"))
	configs, total, err := s.storage.ListModelConfigs(page, size)
	if err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to list model configs")
		return
	}
	type item struct {
		ID            string            `json:"id"`
		Name          string            `json:"name"`
		Provider      string            `json:"provider"`
		ProviderModel string            `json:"provider_model"`
		BaseURL       string            `json:"base_url"`
		Timeout       int               `json:"timeout"`
		MaxRetries    int               `json:"max_retries"`
		RetryInterval int               `json:"retry_interval"`
		Weight        int               `json:"weight"`
		Enabled       bool              `json:"enabled"`
		Tags          map[string]string `json:"tags"`
		CreatedAt     time.Time         `json:"created_at"`
	}
	items := make([]item, 0, len(configs))
	for _, cfg := range configs {
		items = append(items, item{
			ID: cfg.ID, Name: cfg.ModelName, Provider: cfg.Provider,
			ProviderModel: cfg.ProviderModel, BaseURL: cfg.BaseURL,
			Timeout: int(cfg.Timeout), MaxRetries: cfg.MaxRetries, RetryInterval: int(cfg.RetryInterval),
			Weight: cfg.Weight, Enabled: cfg.Enabled, Tags: cfg.Tags, CreatedAt: cfg.CreatedAt,
		})
	}
	OK(c, gin.H{"items": items, "total": total, "page": page, "size": size})
}

// updateModelConfig PUT /api/models/:id
func (s *AdminServer) updateModelConfig(c *gin.Context) {
	id := c.Param("id")
	existing, err := s.storage.GetModelConfigByID(id)
	if err != nil {
		Error(c, http.StatusNotFound, 404, "model config not found")
		return
	}
	var req modelConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	req.normalize()
	// 名称唯一校验(排除自身)
	if existingConfig, err := s.storage.GetModelConfig(req.Name); err == nil && existingConfig.ID != id {
		Error(c, http.StatusConflict, 409, "模型名称已存在")
		return
	}
	existing.ModelName = req.Name
	existing.Provider = req.Provider
	existing.ProviderModel = req.ProviderModel
	existing.BaseURL = req.BaseURL
	// api_key 留空 = 保留原值(编辑/启停场景前端不回传明文 key)
	if req.APIKey != "" {
		existing.APIKey = req.APIKey
	}
	existing.Timeout = time.Duration(req.Timeout)
	existing.MaxRetries = req.MaxRetries
	existing.RetryInterval = time.Duration(req.RetryInterval)
	existing.Weight = req.Weight
	existing.Enabled = *req.Enabled
	existing.Tags = req.Tags
	existing.UpdatedAt = time.Now()
	if err := s.storage.SaveModelConfig(existing); err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to update model config")
		return
	}
	OK(c, gin.H{"id": id})
}

// deleteModelConfig DELETE /api/models/:id
func (s *AdminServer) deleteModelConfig(c *gin.Context) {
	id := c.Param("id")
	if err := s.storage.DeleteModelConfig(id); err != nil {
		Error(c, http.StatusNotFound, 404, "model config not found")
		return
	}
	OK(c, gin.H{"id": id, "deleted": true})
}

// testModelConfig POST /api/models/:id/test:测试连接(轻量请求,返回延迟)
func (s *AdminServer) testModelConfig(c *gin.Context) {
	id := c.Param("id")
	config, err := s.storage.GetModelConfigByID(id)
	if err != nil {
		Error(c, http.StatusNotFound, 404, "model config not found")
		return
	}
	url := strings.TrimRight(config.BaseURL, "/") + "/v1/models"
	client := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+config.APIKey)
	start := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		OK(c, gin.H{"ok": false, "latency_ms": latency, "error": err.Error()})
		return
	}
	defer resp.Body.Close()
	OK(c, gin.H{"ok": resp.StatusCode < 500, "latency_ms": latency, "status": resp.StatusCode})
}
