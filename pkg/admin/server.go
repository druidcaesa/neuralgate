package admin

import (
	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// AdminServer 管理后台（Gin，照设计文档 1.1）
// 低并发短连接：CRUD 接口、配置管理、日志查询、授权校验
type AdminServer struct {
	storage plugin.StoragePlugin
	logger  *zap.Logger
	engine  *gin.Engine
}

// NewAdminServer 创建管理后台
func NewAdminServer(storage plugin.StoragePlugin, logger *zap.Logger) *AdminServer {
	gin.SetMode(gin.ReleaseMode)
	s := &AdminServer{storage: storage, logger: logger}
	s.engine = gin.New()
	s.engine.Use(gin.Recovery(), CORS())
	s.registerRoutes(s.engine)
	return s
}

// Router 返回 Gin 路由
func (s *AdminServer) Router() *gin.Engine { return s.engine }

// Run 启动后台服务
func (s *AdminServer) Run(addr string) error {
	return s.engine.Run(addr)
}
