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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/google/uuid"
)

// AuthMiddleware 鉴权中间件：提取 Bearer API Key → 查存储校验 → 写入 RequestContext。
// 内置 TenantGate 联动租户禁用（每条链一份，空表/无租户恒放行）
func AuthMiddleware(storage plugin.StoragePlugin) Middleware {
	tenantGate := NewTenantGate(storage, tenantCheckInterval)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rc := &RequestContext{
				RequestID:      uuid.NewString(),
				StartTime:      time.Now(),
				ClientIP:       clientIP(r),
				RequestMethod:  r.Method,
				RequestPath:    r.URL.Path,
				RequestHeaders: headerMap(r.Header),
			}
			// 脱敏:移除 Authorization 头,避免 API Key 明文进入审计日志(PRD 5.4)
			delete(rc.RequestHeaders, "Authorization")

			// /healthz 免鉴权:构建 rc 后直接放行,不校验 Key(运维探活需求)
			if r.URL.Path == "/healthz" {
				next.ServeHTTP(w, r.WithContext(WithRequestContext(r.Context(), rc)))
				return
			}

			// 提取 Bearer Key
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") || len(strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))) == 0 {
				writeOpenAIError(w, http.StatusUnauthorized, "invalid_request_error", "invalid_api_key", "Incorrect API key provided")
				return
			}
			rawKey := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))

			// SHA256 查存储
			sum := sha256.Sum256([]byte(rawKey))
			key, err := storage.GetAPIKey(hex.EncodeToString(sum[:]))
			if err != nil || key == nil {
				writeOpenAIError(w, http.StatusUnauthorized, "invalid_request_error", "invalid_api_key", "Incorrect API key provided")
				return
			}
			// 租户禁用联动：所属租户被停用的 Key 一并拒绝(≤TTL 生效)
			if key.TenantID != "" && !tenantGate.Allowed(key.TenantID) {
				writeOpenAIError(w, http.StatusUnauthorized, "invalid_request_error", "api_key_disabled", "tenant is disabled")
				return
			}
			rc.APIKeyID = key.ID
			rc.TenantID = key.TenantID

			// 状态校验(与下方 ExpiresAt 时间检查互为补充,两者都拒)
			switch key.Status {
			case plugin.APIKeyStatusDisabled:
				writeOpenAIError(w, http.StatusUnauthorized, "invalid_request_error", "api_key_disabled", "API key is disabled")
				return
			case plugin.APIKeyStatusExpired:
				writeOpenAIError(w, http.StatusUnauthorized, "invalid_request_error", "api_key_expired", "API key has expired")
				return
			}
			if key.ExpiresAt != nil && time.Now().After(*key.ExpiresAt) {
				writeOpenAIError(w, http.StatusUnauthorized, "invalid_request_error", "api_key_expired", "API key has expired")
				return
			}
			// 额度校验(quota != -1 表示有限额)
			if key.Quota >= 0 && key.UsedQuota >= key.Quota {
				writeOpenAIError(w, http.StatusTooManyRequests, "rate_limit_exceeded", "quota_exceeded", "API key quota exceeded")
				return
			}

			ctx := WithRequestContext(r.Context(), rc)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// 可信代理清单：仅当直连地址命中清单时才采信 X-Forwarded-For，
// 否则一律取 RemoteAddr——防止伪造 XFF 污染审计 IP。空清单=全部不信任
var (
	trustedProxiesMu sync.RWMutex
	trustedProxies   []*net.IPNet
)

// SetTrustedProxies 配置可信代理 CIDR/单 IP 清单（进程级，启动装配一次）
func SetTrustedProxies(cidrs []string) error {
	parsed := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		if !strings.Contains(c, "/") {
			c += "/32"
		}
		_, ipnet, err := net.ParseCIDR(c)
		if err != nil {
			return fmt.Errorf("trusted proxy %q: %w", c, err)
		}
		parsed = append(parsed, ipnet)
	}
	trustedProxiesMu.Lock()
	trustedProxies = parsed
	trustedProxiesMu.Unlock()
	return nil
}

func remoteAddrTrusted(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	trustedProxiesMu.RLock()
	defer trustedProxiesMu.RUnlock()
	for _, n := range trustedProxies {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// clientIP 提取客户端 IP：仅当直连来源为可信代理时才采信 XFF 首段，
// 其余情况取 RemoteAddr（安全默认收紧）
func clientIP(r *http.Request) string {
	if remoteAddrTrusted(r) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			return strings.TrimSpace(strings.Split(xff, ",")[0])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// headerMap http.Header 转换为 map[string]string（取每个头的第一个值）
func headerMap(h http.Header) map[string]string {
	m := make(map[string]string, len(h))
	for k, v := range h {
		if len(v) > 0 {
			m[k] = v[0]
		}
	}
	return m
}
