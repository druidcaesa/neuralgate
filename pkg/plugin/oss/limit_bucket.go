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

import "time"

// tokenBucket 令牌桶:容量 capacity,每秒填充 refillPerSec 个令牌,允许突发
type tokenBucket struct {
	capacity     float64
	refillPerSec float64
	tokens       float64
	last         time.Time
}

// newTokenBucket 创建满桶令牌桶
func newTokenBucket(capacity, refillPerSec float64, now time.Time) *tokenBucket {
	return &tokenBucket{
		capacity:     capacity,
		refillPerSec: refillPerSec,
		tokens:       capacity,
		last:         now,
	}
}

// refill 按经过时间补充令牌(上限 capacity)
func (b *tokenBucket) refill(now time.Time) {
	elapsed := now.Sub(b.last).Seconds()
	if elapsed <= 0 {
		return
	}
	b.tokens += elapsed * b.refillPerSec
	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}
	b.last = now
}

// take 尝试取 n 个令牌,成功返回 true
func (b *tokenBucket) take(n float64, now time.Time) bool {
	b.refill(now)
	if b.tokens >= n {
		b.tokens -= n
		return true
	}
	return false
}

// remaining 当前可用令牌数(整数向下取整)
func (b *tokenBucket) remaining(now time.Time) int64 {
	b.refill(now)
	return int64(b.tokens)
}
