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
	"errors"
	"sync"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// ErrBufferClosed 环形队列已关闭
var ErrBufferClosed = errors.New("ring buffer closed")

// RingBuffer 环形队列：固定大小内存预分配；
// 队列满时阻塞写入方，队列空时阻塞消费方；
// 支持优雅关闭：Close 后不再接收新数据，剩余数据仍可取出
type RingBuffer struct {
	buf           []*plugin.AuditEvent
	size          int
	head          int // 写入位置
	tail          int // 读取位置
	count         int // 当前元素数量
	mu            sync.Mutex
	notFull       *sync.Cond
	notEmpty      *sync.Cond
	closed        bool
	overflowCount int64 // 溢出计数（当前恒为 0，预留）
}

// NewRingBuffer 创建环形队列；size 必须为正数，否则 panic
func NewRingBuffer(size int) *RingBuffer {
	if size <= 0 {
		panic("ring buffer size must be positive")
	}
	rb := &RingBuffer{
		buf:  make([]*plugin.AuditEvent, size),
		size: size,
	}
	rb.notFull = sync.NewCond(&rb.mu)
	rb.notEmpty = sync.NewCond(&rb.mu)
	return rb
}

// Push 写入事件；队列满时阻塞等待；关闭后返回 ErrBufferClosed
func (rb *RingBuffer) Push(event *plugin.AuditEvent) error {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	for rb.isFull() {
		if rb.closed {
			return ErrBufferClosed
		}
		rb.notFull.Wait()
	}
	if rb.closed {
		return ErrBufferClosed
	}
	rb.buf[rb.head] = event
	rb.head = (rb.head + 1) % rb.size
	rb.count++
	rb.notEmpty.Signal()
	return nil
}

// Pop 取出事件；队列空时阻塞等待；关闭且队列空时返回 ErrBufferClosed
// （关闭后队列中剩余数据仍可 Pop 取出）
func (rb *RingBuffer) Pop() (*plugin.AuditEvent, error) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	for rb.isEmpty() {
		if rb.closed {
			return nil, ErrBufferClosed
		}
		rb.notEmpty.Wait()
	}
	ev := rb.buf[rb.tail]
	rb.buf[rb.tail] = nil
	rb.tail = (rb.tail + 1) % rb.size
	rb.count--
	rb.notFull.Signal()
	return ev, nil
}

// Close 关闭队列，唤醒所有阻塞的 Push/Pop
func (rb *RingBuffer) Close() error {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.closed = true
	rb.notFull.Broadcast()
	rb.notEmpty.Broadcast()
	return nil
}

// OverflowCount 返回溢出计数（当前恒为 0）
func (rb *RingBuffer) OverflowCount() int64 {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return rb.overflowCount
}

func (rb *RingBuffer) isFull() bool {
	return rb.count == rb.size
}

func (rb *RingBuffer) isEmpty() bool {
	return rb.count == 0
}
