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
	storage         plugin.StoragePlugin
	rateLimiter     plugin.RateLimitPlugin
	logger          *zap.Logger
	engine          *gin.Engine
	edition         string // 运行版本（授权降级后为 oss）
	startedAt       time.Time
	license         *LicenseOverview // 授权概要快照（nil 按 OSS 未授权处理）
	sessions        *SessionManager  // 认证默认开启（fail-closed）；DisableAuth 仅限测试
	loginGuard      LoginGuard
	allowedOrigins  []string                                                                   // CORS 白名单（空=不发送跨域头）
	rbacEnabled     bool                                                                       // 权限体系开关（EnableRBAC 注入，未启用恒放行）
	reportGenerator func(periodType string, start time.Time) (*plugin.ComplianceReport, error) // 合规补生成器（enterprise 装配注入，nil 时手动生成返回 503）
	licenseMgr      LicenseManager                                                             // 授权上传管理（enterprise 装配注入；OSS 为 nil → 上传 501）
	proxyScheme     string                                                                     // 代理服务公开 scheme(http/https),gateway-meta 下发用;空=未注入
	proxyPort       int                                                                        // 代理服务公开端口;0=未注入或解析失败
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
	// AccessLog 置于最外层:内层 Recovery 把 panic 转为 500 后,c.Next() 才带着终态状态码返回
	s.engine.Use(s.AccessLog(), gin.Recovery(), s.CORS())
	s.registerRoutes(s.engine)
	return s
}

// Router 返回 Gin 路由
func (s *AdminServer) Router() *gin.Engine { return s.engine }

// Run 启动后台服务
func (s *AdminServer) Run(addr string) error {
	return s.engine.Run(addr)
}

// SetGatewayMeta 注入代理服务公开元信息(scheme+端口),供前端拼「接口说明」(/docs)地址;
// 未调用时 gateway-meta 返回空值,前端隐藏入口(低版本/未接线不报错)
func (s *AdminServer) SetGatewayMeta(scheme string, port int) {
	s.proxyScheme = scheme
	s.proxyPort = port
}
