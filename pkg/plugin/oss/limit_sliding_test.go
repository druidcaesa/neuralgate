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

package oss

import (
	"testing"
	"time"
)

func TestSlidingWindowWithinLimit(t *testing.T) {
	// 窗口 1s、上限 3
	w := newSlidingWindow(3, time.Second)
	base := time.Unix(1000, 0)
	for i := 0; i < 3; i++ {
		if !w.allow(1, base) {
			t.Fatalf("allow %d within limit should succeed", i)
		}
	}
	// 第 4 次同窗口:拒绝
	if w.allow(1, base) {
		t.Fatal("allow beyond limit should fail")
	}
}

func TestSlidingWindowRoll(t *testing.T) {
	w := newSlidingWindow(3, time.Second)
	base := time.Unix(1000, 0)
	for i := 0; i < 3; i++ {
		w.allow(1, base)
	}
	// 超过窗口后计数清零,可再允许
	if !w.allow(1, base.Add(time.Second+time.Millisecond)) {
		t.Fatal("allow after window roll should succeed")
	}
}

func TestSlidingWindowTakeN(t *testing.T) {
	// 60s 窗口、上限 100 tokens;取 60 成功,再取 50 失败(110>100)
	w := newSlidingWindow(100, 60*time.Second)
	base := time.Unix(1000, 0)
	if !w.allow(60, base) {
		t.Fatal("allow 60 within limit should succeed")
	}
	if w.allow(50, base) {
		t.Fatal("allow 50 exceeding 100 should fail")
	}
}

func TestSlidingWindowCurrent(t *testing.T) {
	w := newSlidingWindow(10, time.Second)
	base := time.Unix(1000, 0)
	w.allow(3, base)
	if got := w.current(base); got != 3 {
		t.Fatalf("current = %d; want 3", got)
	}
}
