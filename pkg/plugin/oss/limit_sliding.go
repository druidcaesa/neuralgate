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

// slidingWindow 固定窗口计数器(窗口滚动即清零,近似滑动窗口)
// 窗口内累计值超过 limit 则拒绝
type slidingWindow struct {
	limit       int64
	window      time.Duration
	count       int64
	windowStart time.Time
}

// newSlidingWindow 创建滑动窗口
func newSlidingWindow(limit int64, window time.Duration) *slidingWindow {
	return &slidingWindow{limit: limit, window: window}
}

// roll 若已越过当前窗口,重置计数与窗口起点
func (w *slidingWindow) roll(now time.Time) {
	if w.windowStart.IsZero() || now.Sub(w.windowStart) >= w.window {
		w.windowStart = now
		w.count = 0
	}
}

// allow 尝试累加 n,累加后不超过 limit 则成功
func (w *slidingWindow) allow(n int64, now time.Time) bool {
	w.roll(now)
	if w.count+n > w.limit {
		return false
	}
	w.count += n
	return true
}

// add 无条件累加 n(用于事后记录已消耗量,可越过 limit)
func (w *slidingWindow) add(n int64, now time.Time) {
	w.roll(now)
	w.count += n
}

// current 当前窗口累计值
func (w *slidingWindow) current(now time.Time) int64 {
	w.roll(now)
	return w.count
}
