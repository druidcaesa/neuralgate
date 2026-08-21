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
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

//go:embed all:webui/dist
var webuiFS embed.FS

// registerWebUI 注册静态资源与 SPA fallback
// 非 /api 且命中静态资源 → 直接服务;否则回退 index.html(SPA 前端路由)
func (s *AdminServer) registerWebUI(r *gin.Engine) {
	dist, err := fs.Sub(webuiFS, "webui/dist")
	if err != nil {
		return // embed 目录异常时不注册(不影响 API)
	}
	// vite 产物结构:dist/assets/* → 挂载到 /assets/*
	assets, err := fs.Sub(dist, "assets")
	if err == nil {
		r.StaticFS("/assets", http.FS(assets))
	}
	index := func(c *gin.Context) {
		data, _ := fs.ReadFile(dist, "index.html")
		c.Header("Content-Type", "text/html; charset=utf-8")
		// NoRoute 场景 gin 已置 404,SPA fallback 需显式复位为 200
		c.Status(http.StatusOK)
		_, _ = c.Writer.Write(data)
	}
	r.GET("/", index)
	r.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.JSON(http.StatusNotFound, Response{Code: 404, Message: "not found"})
			return
		}
		index(c)
	})
}
