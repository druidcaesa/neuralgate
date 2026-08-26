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

//go:build enterprise

package main

import (
	"database/sql"

	"github.com/lib/pq"

	// 达梦驱动: database/sql 注册名 "dm"，纯 Go 实现，信创环境无需 CGO
	_ "gitee.com/chunanyong/dm"
)

// init 将 PQ 驱动以 "kingbase" 别名注册——金仓 V8R6 PG 兼容模式
// 直接复用 postgres 协议栈；OSS 构建不含本文件，配置 dm/kingbase 时
// sql.Open 报 unknown driver(预期降级行为)
func init() {
	sql.Register("kingbase", &pq.Driver{})
}
