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
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakePinger struct{ err error }

func (f fakePinger) Ping() error { return f.err }

// TestHandleReadyStates readyz 三态:依赖正常 200 / 依赖失败 503 / 排空 503
func TestHandleReadyStates(t *testing.T) {
	SetDraining(false)
	defer SetDraining(false)

	// 依赖正常 → 200 ok
	rec := httptest.NewRecorder()
	HandleReady(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil), fakePinger{})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("依赖正常应 200 ok: code=%d body=%s", rec.Code, rec.Body.String())
	}

	// 依赖失败 → 503 unavailable
	rec = httptest.NewRecorder()
	HandleReady(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil), fakePinger{err: errors.New("db down")})
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "db down") {
		t.Fatalf("依赖失败应 503 并带失败详情: code=%d body=%s", rec.Code, rec.Body.String())
	}

	// 排空 → 503 draining（即便依赖正常）
	SetDraining(true)
	rec = httptest.NewRecorder()
	HandleReady(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil), fakePinger{})
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "draining") {
		t.Fatalf("排空应 503 draining: code=%d body=%s", rec.Code, rec.Body.String())
	}
}
