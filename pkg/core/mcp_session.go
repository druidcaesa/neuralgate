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
	"sync"
	"time"

	"github.com/google/uuid"
)

// mcpSession 网关侧 MCP 会话：绑定调用方身份与上游会话。
// 上游 Streamable HTTP 服务在 initialize 时也会签发自己的 Mcp-Session-Id，
// 后续调用必须代传——故网关会话内保存该绑定
type mcpSession struct {
	CallerAgent       string // initialize 的 clientInfo.name（兜底 User-Agent）
	ServerID          string
	APIKeyID          string
	UpstreamSessionID string    // 上游签发的会话标识
	LastSeen          time.Time // 每次命中续期，超 TTL 视为终结
}

// sessionStore 会话登记表：TTL 惰性清理（无后台 goroutine，shutdown 免新增停止点）。
// 进程重启丢失由客户端重新 initialize 自愈（协议允许）
type sessionStore struct {
	mu       sync.Mutex
	sessions map[string]*mcpSession
	ttl      time.Duration
	now      func() time.Time
	visits   int
}

func newSessionStore(ttl time.Duration) *sessionStore {
	return &sessionStore{
		sessions: make(map[string]*mcpSession),
		ttl:      ttl,
		now:      time.Now,
	}
}

// create 登记新会话并返回网关生成的 Mcp-Session-Id
func (s *sessionStore) create(callerAgent, serverID, apiKeyID string) string {
	id := uuid.NewString()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[id] = &mcpSession{
		CallerAgent: callerAgent,
		ServerID:    serverID,
		APIKeyID:    apiKeyID,
		LastSeen:    s.now(),
	}
	return id
}

// get 校验存在、归属匹配与未过期，命中即续期。
// 每 100 次访问顺带做一次全量过期清扫，摊薄清理成本
func (s *sessionStore) get(id, serverID, apiKeyID string) (*mcpSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.visits++
	if s.visits >= 100 {
		s.visits = 0
		now := s.now()
		for k, sess := range s.sessions {
			if now.Sub(sess.LastSeen) > s.ttl {
				delete(s.sessions, k)
			}
		}
	}
	sess, ok := s.sessions[id]
	if !ok || sess.ServerID != serverID || sess.APIKeyID != apiKeyID {
		return nil, false
	}
	now := s.now()
	if now.Sub(sess.LastSeen) > s.ttl {
		delete(s.sessions, id)
		return nil, false
	}
	sess.LastSeen = now
	cp := *sess
	return &cp, true
}

// setUpstream 回填上游签发的会话标识（initialize 响应到达后调用）
func (s *sessionStore) setUpstream(id, upstreamSessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.sessions[id]; ok {
		sess.UpstreamSessionID = upstreamSessionID
	}
}

// delete 终止会话（客户端 DELETE）
func (s *sessionStore) delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
}
