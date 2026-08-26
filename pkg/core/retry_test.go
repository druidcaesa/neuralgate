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
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

func retryCfg() *plugin.ModelConfig {
	return &plugin.ModelConfig{
		Timeout: 2, MaxRetries: 2, RetryInterval: 0,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
}

// TestDoWithRetryPost5xxNotRetried 非幂等 POST 收到 5xx 不自动重试(防重复副作用)
func TestDoWithRetryPost5xxNotRetried(t *testing.T) {
	var hits int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer up.Close()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, up.URL, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&ProxyCore{}).doWithRetry(req, retryCfg())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Errorf("POST 5xx 应只打一次上游(不重试), hits=%d", n)
	}
}

// TestDoWithRetryGet5xxRetried 幂等 GET 保持 5xx 自动重试直至耗尽
func TestDoWithRetryGet5xxRetried(t *testing.T) {
	var hits int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer up.Close()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, up.URL, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&ProxyCore{}).doWithRetry(req, retryCfg())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if n := atomic.LoadInt32(&hits); n != 3 { // 1 原始 + 2 重试
		t.Errorf("GET 5xx 应重试至耗尽(hits=3), got %d", n)
	}
}
