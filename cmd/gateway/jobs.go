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

package main

// jobHandle 可统一启停的后台任务句柄。OSS/enterprise 两版工厂与 cluster 装配文件
// 以同签名引用它,避免 OSS build 因返回具体 enterprise 类型被迫反向 import 企业包。
// 动态类型在 enterprise 下均为 enterprise.ClusterJob(断言后交 Coordinator 选主),
// OSS 下为 nil;单机路径由 main 直接 Start/Stop
type jobHandle interface {
	Name() string
	Start()
	Stop()
}
