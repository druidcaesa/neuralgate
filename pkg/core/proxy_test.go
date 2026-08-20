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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/druidcaesa/neuralgate/pkg/adapter"
	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
)

func TestProxyCoreSkeletonResponse(t *testing.T) {
	pipeline := NewPipeline(oss.NewMemStorage(), oss.NewMemRateLimiter(), nil)
	registry := adapter.NewAdapterRegistry()
	proxy := NewProxyCore(pipeline, registry)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	proxy.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
	var body struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body.Error.Type != "api_error" || body.Error.Code != "service_unavailable" {
		t.Errorf("error body = %+v, want api_error/service_unavailable", body.Error)
	}
}

func TestProxyCoreHealthz(t *testing.T) {
	pipeline := NewPipeline(oss.NewMemStorage(), oss.NewMemRateLimiter(), nil)
	registry := adapter.NewAdapterRegistry()
	proxy := NewProxyCore(pipeline, registry)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/healthz", nil)
	proxy.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != `{"status":"ok"}` {
		t.Errorf("body = %q, want {\"status\":\"ok\"}", body)
	}
}

func TestIPFilterDefaultsAllow(t *testing.T) {
	f := NewIPFilter()
	if !f.Allow("192.168.1.1") {
		t.Error("Allow() should default to true")
	}
}

func TestProtocolParserIsSSE(t *testing.T) {
	p := NewProtocolParser()
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("Accept", "text/event-stream")
	if !p.IsSSE(req) {
		t.Error("IsSSE() = false, want true for text/event-stream Accept")
	}
	req2 := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	if p.IsSSE(req2) {
		t.Error("IsSSE() = true, want false without Accept header")
	}
}
