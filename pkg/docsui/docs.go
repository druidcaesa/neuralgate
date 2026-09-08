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

// Package docsui 承载网关 AI 端口公开的 /docs 接口说明页(纯静态,免鉴权)。
// 页面与 admin SPA 解耦:不参与 webui 构建,单一 index.html 经 go:embed 内嵌。
package docsui

import (
	_ "embed"
	"net/http"
)

//go:embed index.html
var indexHTML []byte

// allowed 命中即写单页的路径集合
func allowed(path string) bool {
	switch path {
	case "/docs", "/docs/", "/docs/index.html":
		return true
	}
	return false
}

// Serve 命中 /docs 相关路径时写内嵌单页并返回 true;未命中返回 false 由调用方继续。
// 定位在 acceptor 管道外调用,天然免鉴权;页面纯静态,不回显任何真实配置。
func Serve(w http.ResponseWriter, r *http.Request) bool {
	if !allowed(r.URL.Path) {
		return false
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-cache") // 单页升级即时生效,不做长缓存
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(indexHTML)
	return true
}
