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
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// AdminServer 管理后台（Gin）：低并发短连接，提供 CRUD 接口、配置管理、日志查询、授权展示
type AdminServer struct {
	storage        plugin.StoragePlugin
	rateLimiter    plugin.RateLimitPlugin
	logger         *zap.Logger
	engine         *gin.Engine
	edition        string // 运行版本（授权降级后为 oss）
	startedAt      time.Time
	license        *LicenseOverview // 授权概要快照（nil 按 OSS 未授权处理）
	sessions       *SessionManager  // 认证默认开启（fail-closed）；DisableAuth 仅限测试
	loginGuard     *loginGuard
	allowedOrigins []string // CORS 白名单（空=不发送跨域头）
}

// NewAdminServer 创建管理后台；license 为启动时校验得到的授权概要（OSS 版传 nil）。
// 默认启用管理面认证：会话密钥为进程级随机值，重启后需重新登录
func NewAdminServer(storage plugin.StoragePlugin, logger *zap.Logger, edition string, rateLimiter plugin.RateLimitPlugin, license *LicenseOverview) *AdminServer {
	gin.SetMode(gin.ReleaseMode)
	s := &AdminServer{
		storage: storage, rateLimiter: rateLimiter, logger: logger,
		edition: edition, startedAt: time.Now(), license: license,
		sessions:   NewSessionManager(randomSecret(32), 0),
		loginGuard: newLoginGuard(),
	}
	s.engine = gin.New()
	s.engine.Use(gin.Recovery(), s.CORS())
	s.registerRoutes(s.engine)
	return s
}

// Router 返回 Gin 路由
func (s *AdminServer) Router() *gin.Engine { return s.engine }

// Run 启动后台服务
func (s *AdminServer) Run(addr string) error {
	return s.engine.Run(addr)
}
