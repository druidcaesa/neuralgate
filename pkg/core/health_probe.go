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
	"net/http"
	"net/url"
	"strings"
	"time"
)

// probeOK 连通性判定:任何 HTTP 响应视为上游可达(不因 4xx/5xx 误判宕机);
// 仅连接/读取错误视为失败
func probeOK(_ error, resp *http.Response) bool {
	return resp != nil
}

// probeIntervalFallback/probeTimeoutFallback 兜底周期与超时:配置 applyDefaults 后
// interval 恒为正,此处防直接调用方传非正导致 time.NewTicker panic
const (
	probeIntervalFallback = 15 * time.Second
	probeTimeoutFallback  = 5 * time.Second
)

// StartHealthProbe 对 enabled 上游集合周期性探活;结果喂入注册表(open 成功过冷却 → half-open
// 的加速机制在 recordProbe 语义下)。baseURLs 每次 tick 重新求值以支持 CRUD 热更;
// path/interval 来自 cfg.HealthProbe(只测连通性,不产生审计)。返回 stop:须在 storage.Close
// 前调用,返回即探针 goroutine 已退出
func StartHealthProbe(reg *BreakerRegistry, baseURLs func() map[string]string,
	interval time.Duration, path string, timeout time.Duration) (stop func()) {
	if reg == nil {
		return func() {}
	}
	if interval <= 0 {
		interval = probeIntervalFallback
	}
	if timeout <= 0 {
		timeout = probeTimeoutFallback
	}
	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		client := &http.Client{Timeout: timeout}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				for id, base := range baseURLs() {
					u, err := url.Parse(strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/"))
					if err != nil {
						continue
					}
					resp, err := client.Get(u.String())
					if resp != nil {
						// 关闭 body 复用连接;err 与 resp 并存时(如重定向超限)同样关闭防泄漏
						resp.Body.Close()
					}
					reg.recordProbe(id, probeOK(err, resp))
				}
			}
		}
	}()
	return func() {
		close(stopCh)
		<-doneCh
	}
}
