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

package mcp

import (
	"bufio"
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestNextSSEMessageMultipleFrames 连续多帧逐条解析,只取 data 内容
func TestNextSSEMessageMultipleFrames(t *testing.T) {
	stream := "event: message\ndata: {\"a\":1}\n\n" +
		"id: 42\nevent: message\ndata: {\"b\":2}\n\n" +
		"data: {\"c\":3}\n\n"
	r := bufio.NewReader(strings.NewReader(stream))
	want := []string{`{"a":1}`, `{"b":2}`, `{"c":3}`}
	for i, w := range want {
		got, ok, err := NextSSEMessage(r)
		if err != nil || !ok {
			t.Fatalf("帧 %d 解析失败: ok=%v err=%v", i, ok, err)
		}
		if string(got) != w {
			t.Errorf("帧 %d = %s, want %s", i, got, w)
		}
	}
	if _, ok, _ := NextSSEMessage(r); ok {
		t.Error("流应已结束")
	}
}

// TestNextSSEMessageMultiLineData 跨多行 data 按换行拼接(SSE 规范)
func TestNextSSEMessageMultiLineData(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("data: line1\ndata: line2\n\n"))
	got, ok, err := NextSSEMessage(r)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if string(got) != "line1\nline2" {
		t.Errorf("多行 data 应 \\n 拼接, got %q", got)
	}
}

// TestNextSSEMessageSkipsComments 注释行(心跳)与空行跳过不产出帧
func TestNextSSEMessageSkipsComments(t *testing.T) {
	stream := ": heartbeat\n\n: another\n\n" + "data: real\n\n"
	r := bufio.NewReader(strings.NewReader(stream))
	got, ok, err := NextSSEMessage(r)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if string(got) != "real" {
		t.Errorf("注释后应取到真实帧, got %q", got)
	}
}

// TestWriteSSEMessage 写出格式含 event 行与双换行结尾并 Flush
func TestWriteSSEMessage(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := WriteSSEMessage(rec, []byte(`{"x":true}`)); err != nil {
		t.Fatal(err)
	}
	got := rec.Body.String()
	want := "event: message\ndata: {\"x\":true}\n\n"
	if got != want {
		t.Errorf("写出 = %q, want %q", got, want)
	}
}

// TestWriteSSEMessageBytesBuffer 无 Flusher 的 writer 也安全
func TestWriteSSEMessageBytesBuffer(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteSSEMessage(&buf, []byte(`ok`)); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "event: message\ndata: ok\n\n" {
		t.Errorf("buffer 输出不符: %q", buf.String())
	}
}
