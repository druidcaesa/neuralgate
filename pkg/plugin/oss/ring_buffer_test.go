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

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

func TestPushPop(t *testing.T) {
	rb := NewRingBuffer(4)
	ev := &plugin.AuditEvent{RequestID: "r1", EventType: plugin.AuditEventRequestStart}
	if err := rb.Push(ev); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	got, err := rb.Pop()
	if err != nil {
		t.Fatalf("Pop() error = %v", err)
	}
	if got != ev {
		t.Error("Pop() returned different event")
	}
}

func TestPushBlocksWhenFull(t *testing.T) {
	rb := NewRingBuffer(2)
	if err := rb.Push(&plugin.AuditEvent{RequestID: "r1"}); err != nil {
		t.Fatal(err)
	}
	if err := rb.Push(&plugin.AuditEvent{RequestID: "r2"}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		_ = rb.Push(&plugin.AuditEvent{RequestID: "r3"}) // 队列满，应阻塞
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("Push() should block when buffer is full")
	case <-time.After(100 * time.Millisecond):
	}
	// 消费一个，Push 应解除阻塞
	if _, err := rb.Pop(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("Push() did not unblock after Pop()")
	}
}

func TestPopBlocksWhenEmpty(t *testing.T) {
	rb := NewRingBuffer(4)
	done := make(chan struct{})
	go func() {
		_, _ = rb.Pop() // 队列空，应阻塞
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("Pop() should block when buffer is empty")
	case <-time.After(100 * time.Millisecond):
	}
	if err := rb.Push(&plugin.AuditEvent{RequestID: "r1"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("Pop() did not unblock after Push()")
	}
}

func TestCloseUnblocksAndRejects(t *testing.T) {
	rb := NewRingBuffer(4)
	done := make(chan struct{})
	go func() {
		_, _ = rb.Pop()
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	if err := rb.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("Close() did not unblock Pop()")
	}
	if err := rb.Push(&plugin.AuditEvent{RequestID: "r1"}); err != ErrBufferClosed {
		t.Errorf("Push() after Close() = %v, want ErrBufferClosed", err)
	}
}

func TestNewRingBufferInvalidSize(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewRingBuffer(0) should panic")
		}
	}()
	NewRingBuffer(0)
}
