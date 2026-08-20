package admin

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// registerRoutes 注册路由
// 骨架期：健康检查 + API 占位组；CRUD 路由 Phase 6 注册
func (s *AdminServer) registerRoutes(r *gin.Engine) {
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	api := r.Group("/api")
	{
		api.GET("/ping", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"message": "pong"})
		})
	}
}
