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
	"fmt"
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
	Provider      string            `json:"provider" binding:"required,min=1,max=32"` // 内置 openai/qwen/zhipu/deepseek;自定义任意(走 OpenAI 兼容透传)
	ProviderModel string            `json:"provider_model" binding:"required,min=1,max=128"`
	BaseURL       string            `json:"base_url" binding:"required"`
	APIKey        string            `json:"api_key" binding:"omitempty,min=1,max=256"` // 创建必填;更新留空=保留原值
	Timeout       int               `json:"timeout"`                                   // 1-300,默认 60
	MaxRetries    int               `json:"max_retries"`                               // 0-5,默认 2
	RetryInterval int               `json:"retry_interval"`                            // 1-30,默认 3
	Weight        int               `json:"weight"`                                    // 1-100,默认 1
	MaxTokens     int               `json:"max_tokens"`                                // 默认 max_tokens(0=不注入;Anthropic 语义),夹取 [0,1_000_000]
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
	if req.MaxTokens < 0 || req.MaxTokens > 1_000_000 {
		req.MaxTokens = 0
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
		Weight: req.Weight, MaxTokens: req.MaxTokens, Enabled: *req.Enabled, Tags: req.Tags,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.storage.SaveModelConfig(config); err != nil {
		ErrorCause(c, http.StatusInternalServerError, 500, "failed to save model config", err)
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
		ErrorCause(c, http.StatusInternalServerError, 500, "failed to list model configs", err)
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
		MaxTokens     int               `json:"max_tokens"`
		Enabled       bool              `json:"enabled"`
		KeyUnreadable bool              `json:"key_unreadable"`
		Tags          map[string]string `json:"tags"`
		CreatedAt     time.Time         `json:"created_at"`
	}
	items := make([]item, 0, len(configs))
	for _, cfg := range configs {
		items = append(items, item{
			ID: cfg.ID, Name: cfg.ModelName, Provider: cfg.Provider,
			ProviderModel: cfg.ProviderModel, BaseURL: cfg.BaseURL,
			Timeout: int(cfg.Timeout), MaxRetries: cfg.MaxRetries, RetryInterval: int(cfg.RetryInterval),
			Weight: cfg.Weight, MaxTokens: cfg.MaxTokens, Enabled: cfg.Enabled,
			KeyUnreadable: cfg.APIKeyUnreadable, Tags: cfg.Tags, CreatedAt: cfg.CreatedAt,
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
	// 原密钥不可解密时必须重填,否则下方的「留空保留原值」会把密文覆盖成空密钥
	if existing.APIKeyUnreadable && req.APIKey == "" {
		Error(c, http.StatusBadRequest, 400, "该模型密钥无法解密,请重新填写 API Key")
		return
	}
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
	existing.MaxTokens = req.MaxTokens
	existing.Enabled = *req.Enabled
	existing.Tags = req.Tags
	existing.UpdatedAt = time.Now()
	if err := s.storage.SaveModelConfig(existing); err != nil {
		ErrorCause(c, http.StatusInternalServerError, 500, "failed to update model config", err)
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

// testModelConfig POST /api/models/:id/test:测试连接(轻量请求,返回延迟)。
// 按上游协议分路:anthropic(内置 provider 或 tags[adapter]=anthropic)走 POST /v1/messages
// + x-api-key/anthropic-version(最小请求带 max_tokens);其余走现 GET /v1/models + Bearer
func (s *AdminServer) testModelConfig(c *gin.Context) {
	id := c.Param("id")
	config, err := s.storage.GetModelConfigByID(id)
	if err != nil {
		Error(c, http.StatusNotFound, 404, "model config not found")
		return
	}
	// 密钥不可解密时不发请求,避免用一个空 key 换回误导性的上游 401
	if config.APIKeyUnreadable {
		OK(c, gin.H{"ok": false, "latency_ms": 0, "error": "该模型密钥无法解密,请重新填写 API Key"})
		return
	}
	url := strings.TrimRight(config.BaseURL, "/") + "/v1/models"
	client := &http.Client{Timeout: 5 * time.Second}
	var req *http.Request
	if config.Provider == "anthropic" || config.Tags["adapter"] == "anthropic" {
		url = strings.TrimRight(config.BaseURL, "/") + "/v1/messages"
		body := fmt.Sprintf(`{"model":%q,"max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`, config.ProviderModel)
		req, _ = http.NewRequest(http.MethodPost, url, strings.NewReader(body))
		req.Header.Set("x-api-key", config.APIKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("Content-Type", "application/json")
	} else {
		req, _ = http.NewRequest(http.MethodGet, url, nil)
		req.Header.Set("Authorization", "Bearer "+config.APIKey)
	}
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
