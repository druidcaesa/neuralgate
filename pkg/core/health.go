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
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"time"
)

// StoragePinger 依赖存活探测：plugin.StoragePlugin 均满足
type StoragePinger interface {
	Ping() error
}

// pingTimeout 单次依赖探测超时
const pingTimeout = 2 * time.Second

// 排空标记：进程级。SIGTERM 后置 true，readiness 转 503——滚动发布期间
// 让 LB 先把本副本摘除，再进入 Shutdown 断开存量连接。
// 数据面与管理面共用同一标记，语义一致。
var draining atomic.Bool

// SetDraining 设置排空标记（main 收到退出信号后、Shutdown 前置 true）
func SetDraining(d bool) { draining.Store(d) }

// IsDraining 返回排空标记（供端点/测试读取）
func IsDraining() bool { return draining.Load() }

// readyResult 就绪探测结果（HTTP 层 JSON 应答体）
type readyResult struct {
	Status  string `json:"status"`            // ok / unavailable
	Reason  string `json:"reason,omitempty"`  // 失败/排空原因
	Storage string `json:"storage,omitempty"` // storage 探测结果 ok / 失败详情
}

// pingWithin 对依赖做带超时的存活探测；Ping 不支持 context，用 goroutine+select 收口
func pingWithin(p StoragePinger, timeout time.Duration) error {
	done := make(chan error, 1)
	go func() { done <- p.Ping() }()
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		return errors.New("storage ping timeout")
	}
}

// HandleReady readiness 探针：storage 存活 + 非排空 → 200；任一不满足 → 503。
// 与 /healthz(纯存活恒 200)语义区分：readiness 面向 LB/编排器，依赖不可用即摘流。
// 数据面(main rootHandler)与管理面(admin /readyz)共用。
func HandleReady(w http.ResponseWriter, _ *http.Request, storage StoragePinger) {
	if draining.Load() {
		writeReadyResult(w, http.StatusServiceUnavailable, readyResult{
			Status: "unavailable", Reason: "draining",
		})
		return
	}
	if err := pingWithin(storage, pingTimeout); err != nil {
		writeReadyResult(w, http.StatusServiceUnavailable, readyResult{
			Status: "unavailable", Reason: "dependency check failed",
			Storage: err.Error(),
		})
		return
	}
	writeReadyResult(w, http.StatusOK, readyResult{
		Status: "ok", Storage: "ok",
	})
}

func writeReadyResult(w http.ResponseWriter, code int, body readyResult) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
