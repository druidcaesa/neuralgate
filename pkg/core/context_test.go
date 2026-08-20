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
	"testing"
)

func TestRequestContextRoundTrip(t *testing.T) {
	ctx := context.Background()
	rc := &RequestContext{RequestID: "r1"}
	ctx = WithRequestContext(ctx, rc)
	got, ok := RequestContextFrom(ctx)
	if !ok {
		t.Fatal("RequestContextFrom() ok = false, want true")
	}
	if got != rc {
		t.Error("RequestContextFrom() returned different pointer")
	}
}

func TestRequestContextFromEmpty(t *testing.T) {
	_, ok := RequestContextFrom(context.Background())
	if ok {
		t.Error("RequestContextFrom() ok = true on empty context, want false")
	}
}
