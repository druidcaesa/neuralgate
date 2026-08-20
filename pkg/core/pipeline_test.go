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
	"net/http/httptest"
	"testing"

	"github.com/druidcaesa/neuralgate/pkg/plugin/oss"
)

func TestPipelineOrder(t *testing.T) {
	var calls []string
	record := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, name+"-in")
				next.ServeHTTP(w, r)
				calls = append(calls, name+"-out")
			})
		}
	}
	p := NewPipeline(oss.NewMemStorage(), oss.NewMemRateLimiter(), nil)
	p.Use(record("m1"))
	p.Use(record("m2"))
	handler := p.Apply(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "handler")
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	want := []string{"m1-in", "m2-in", "handler", "m2-out", "m1-out"}
	if len(calls) != len(want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("calls = %v, want %v", calls, want)
		}
	}
}

func TestCustomMiddlewareRunsAfterAuth(t *testing.T) {
	p := NewPipeline(oss.NewMemStorage(), oss.NewMemRateLimiter(), nil)
	var sawRC *RequestContext
	p.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rc, ok := RequestContextFrom(r.Context())
			if !ok {
				t.Error("custom middleware must run after AuthMiddleware: RequestContext missing")
				return
			}
			sawRC = rc
			next.ServeHTTP(w, r)
		})
	})
	handler := p.Apply(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer ng-test-key")
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if sawRC == nil {
		t.Fatal("RequestContext not visible in custom middleware")
	}
	if sawRC.APIKeyID != "ng-test-key" {
		t.Errorf("APIKeyID = %q, want ng-test-key", sawRC.APIKeyID)
	}
}

func TestAuthMiddlewareCreatesRequestContext(t *testing.T) {
	p := NewPipeline(oss.NewMemStorage(), oss.NewMemRateLimiter(), nil)
	var gotRC *RequestContext
	p.Use(AuthMiddleware(p.storage))
	handler := p.Apply(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc, ok := RequestContextFrom(r.Context())
		if !ok {
			t.Error("RequestContext not in context")
			return
		}
		gotRC = rc
	}))
	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer ng-test-key")
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if gotRC == nil {
		t.Fatal("RequestContext was not created")
	}
	if gotRC.RequestID == "" {
		t.Error("RequestID should not be empty")
	}
	if gotRC.APIKeyID != "ng-test-key" {
		t.Errorf("APIKeyID = %q, want ng-test-key", gotRC.APIKeyID)
	}
	if gotRC.RequestMethod != "GET" || gotRC.RequestPath != "/v1/models" {
		t.Errorf("RequestMethod/Path = %s %s", gotRC.RequestMethod, gotRC.RequestPath)
	}
	if _, ok := gotRC.RequestHeaders["Authorization"]; !ok {
		t.Error("RequestHeaders should contain Authorization")
	}
}

func TestRateLimitAndRouteMiddlewaresPassThrough(t *testing.T) {
	p := NewPipeline(oss.NewMemStorage(), oss.NewMemRateLimiter(), nil)
	hit := false
	handler := p.Build(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/chat/completions", nil))
	if !hit {
		t.Error("handler was not reached through Build() chain")
	}
}
