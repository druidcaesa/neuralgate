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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// 上游模型清单拉取的业务码。与 HTTP status 值错开,理由同 CodeFeatureLocked:
// 前端与测试以码唯一识别失败原因。本功能整体价值在精确归因,
// 若各类失败共用同一个码,与代理面折叠上游错误的老问题殊途同归
const (
	CodeUpstreamModelsMissingBaseURL   = 4601
	CodeUpstreamModelsMissingAPIKey    = 4602
	CodeUpstreamModelsModelNotFound    = 4603
	CodeUpstreamModelsKeyUnreadable    = 4604
	CodeUpstreamModelsBadScheme        = 4605
	CodeUpstreamModelsAuthFailed       = 4611
	CodeUpstreamModelsForbidden        = 4612
	CodeUpstreamModelsUpstreamNotFound = 4613
	CodeUpstreamModelsUnreachable      = 4614
	CodeUpstreamModelsBadResponse      = 4615
	CodeUpstreamModelsUpstreamError    = 4616
)

// upstreamModelsTimeout 出网超时上限;短于前端 axios 的 15s,保证先由网关给出归类错误
const upstreamModelsTimeout = 10 * time.Second

// maxUpstreamModelsBody 响应体读取上限,防止失控上游撑爆内存
const maxUpstreamModelsBody = 4 << 20

// upstreamModelItem 清单条目。只保留 id,上游的 owned_by/created/status 等
// 一律不透传——本功能只用于回填一个模型 ID
type upstreamModelItem struct {
	ID string `json:"id"`
}

// fetchUpstreamModels 代拉上游 /v1/models 并解析。
// 成功返回 (models, 0, 0, "");失败返回 (nil, httpStatus, bizCode, message)。
// data 为 null 与 data 缺失、非数组同样归为 bad_response;空数组则是合法空清单
func fetchUpstreamModels(baseURL, apiKey string) ([]upstreamModelItem, int, int, string) {
	client := &http.Client{Timeout: upstreamModelsTimeout}
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(baseURL, "/")+"/v1/models", nil)
	if err != nil {
		return nil, http.StatusBadRequest, CodeUpstreamModelsBadScheme, "上游地址无效"
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return nil, http.StatusBadGateway, CodeUpstreamModelsUnreachable, "无法连接上游"
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return nil, http.StatusBadRequest, CodeUpstreamModelsAuthFailed, "密钥无效或已过期"
	case http.StatusForbidden:
		return nil, http.StatusBadRequest, CodeUpstreamModelsForbidden, "密钥有效,但该账号无此权限"
	case http.StatusNotFound:
		return nil, http.StatusBadRequest, CodeUpstreamModelsUpstreamNotFound, "上游无此地址,请检查上游地址"
	}
	if resp.StatusCode >= 400 {
		return nil, http.StatusBadGateway, CodeUpstreamModelsUpstreamError,
			fmt.Sprintf("上游返回 %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxUpstreamModelsBody))
	if err != nil {
		return nil, http.StatusBadGateway, CodeUpstreamModelsBadResponse, "读取上游响应失败"
	}
	// Data 用 RawMessage 以便区分「字段缺失」「null」「非数组」三种形态
	var parsed struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || len(parsed.Data) == 0 {
		return nil, http.StatusBadGateway, CodeUpstreamModelsBadResponse, "上游返回了无法解析的内容"
	}
	var items []upstreamModelItem
	if err := json.Unmarshal(parsed.Data, &items); err != nil || items == nil {
		return nil, http.StatusBadGateway, CodeUpstreamModelsBadResponse, "上游返回了无法解析的内容"
	}

	out := make([]upstreamModelItem, 0, len(items))
	for _, m := range items {
		if m.ID == "" {
			continue
		}
		out = append(out, upstreamModelItem{ID: m.ID})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, 0, 0, ""
}

// upstreamModelsRequest 拉取清单请求体。
// 密钥二选一:api_key 非空直接用(新建态表单里是明文);
// 否则用 model_id 取库中已存密钥(编辑态密钥不回显)
type upstreamModelsRequest struct {
	ModelID string `json:"model_id"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
}

// listUpstreamModels POST /api/models/upstream-models:代拉上游模型清单。
// 错误按原因归类返回,刻意不折叠成 502——本接口是管理面诊断入口,
// 用户意图正是「为什么不行」,与代理面只关心成败的取舍不同
func (s *AdminServer) listUpstreamModels(c *gin.Context) {
	var req upstreamModelsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	baseURL := strings.TrimSpace(req.BaseURL)
	if baseURL == "" {
		Error(c, http.StatusBadRequest, CodeUpstreamModelsMissingBaseURL, "缺少上游地址")
		return
	}
	if u, err := url.Parse(baseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		Error(c, http.StatusBadRequest, CodeUpstreamModelsBadScheme, "上游地址必须是 http 或 https")
		return
	}

	apiKey := req.APIKey
	if apiKey == "" {
		if req.ModelID == "" {
			Error(c, http.StatusBadRequest, CodeUpstreamModelsMissingAPIKey, "缺少 API Key")
			return
		}
		cfg, err := s.storage.GetModelConfigByID(req.ModelID)
		if err != nil {
			Error(c, http.StatusNotFound, CodeUpstreamModelsModelNotFound, "模型配置不存在")
			return
		}
		// 密钥不可解密时不发请求,避免用空 key 换回误导性的上游 401
		if cfg.APIKeyUnreadable {
			Error(c, http.StatusBadRequest, CodeUpstreamModelsKeyUnreadable,
				"该模型密钥无法解密,请重新填写 API Key")
			return
		}
		apiKey = cfg.APIKey
	}

	models, status, code, msg := fetchUpstreamModels(baseURL, apiKey)
	if code != 0 {
		Error(c, status, code, msg)
		return
	}
	OK(c, gin.H{"models": models})
}
