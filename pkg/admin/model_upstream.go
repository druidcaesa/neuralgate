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
	"sort"
	"strings"
	"time"
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
