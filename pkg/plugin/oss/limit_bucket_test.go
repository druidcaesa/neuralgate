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

func TestTokenBucketBurstThenReject(t *testing.T) {
	// 容量 3、每秒填充 3;起始满桶
	b := newTokenBucket(3, 3, time.Unix(1000, 0))
	// 突发:前 3 次取 1 令牌成功
	for i := 0; i < 3; i++ {
		if !b.take(1, time.Unix(1000, 0)) {
			t.Fatalf("take %d should succeed within capacity", i)
		}
	}
	// 第 4 次同一时刻:桶空,拒绝
	if b.take(1, time.Unix(1000, 0)) {
		t.Fatal("take beyond capacity should fail")
	}
}

func TestTokenBucketRefill(t *testing.T) {
	b := newTokenBucket(3, 3, time.Unix(1000, 0))
	for i := 0; i < 3; i++ {
		b.take(1, time.Unix(1000, 0))
	}
	// 1 秒后填满 3 个,可再取
	if !b.take(1, time.Unix(1001, 0)) {
		t.Fatal("take after 1s refill should succeed")
	}
}

func TestTokenBucketTakeN(t *testing.T) {
	// 容量 100、每秒 100;一次取 60 成功,再取 60 失败(剩 40)
	b := newTokenBucket(100, 100, time.Unix(1000, 0))
	if !b.take(60, time.Unix(1000, 0)) {
		t.Fatal("take 60 within capacity should succeed")
	}
	if b.take(60, time.Unix(1000, 0)) {
		t.Fatal("take 60 with only 40 left should fail")
	}
}

func TestTokenBucketRemaining(t *testing.T) {
	b := newTokenBucket(10, 10, time.Unix(1000, 0))
	b.take(3, time.Unix(1000, 0))
	if got := b.remaining(time.Unix(1000, 0)); got != 7 {
		t.Fatalf("remaining = %d; want 7", got)
	}
}
