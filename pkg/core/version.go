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

// VersionInfo 版本信息（照设计文档 7.3）
type VersionInfo struct {
	Version   string   // 版本号
	BuildTime string   // 编译时间
	GitCommit string   // Git提交
	Edition   string   // 版本类型：oss/enterprise
	Features  []string // 可用功能列表
}

// 以下变量通过 Makefile -ldflags 注入
var (
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"
)
