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
	"io"
	"net/http"
	"strings"
)

// NextSSEMessage 从流中读取下一个 SSE 事件帧的 data 载荷。
// 多行 data 按 \n 拼接(SSE 规范)；event:/id:/retry: 行与注释行(: 开头)忽略；
// 返回 false 表示流正常结束(io.EOF)；非 EOF 读错误经 error 返回。
// 仅支持 UTF-8 单字节换行分隔——MCP Streamable HTTP 场景足够
func NextSSEMessage(r *bufio.Reader) ([]byte, bool, error) {
	var data bytes.Buffer
	for {
		line, err := r.ReadString('\n')
		if len(line) == 0 && err != nil {
			if err == io.EOF {
				return nil, false, nil
			}
			return nil, false, err
		}
		line = trimCRLF(line)
		switch {
		case line == "":
			// 空行=事件边界：已聚合到 data 则产出，否则继续(跳过纯注释块)
			if data.Len() > 0 {
				return data.Bytes(), true, nil
			}
		case bytes.HasPrefix([]byte(line), []byte(":")):
			// 注释行(心跳)，忽略
		case bytes.HasPrefix([]byte(line), []byte("data:")):
			value := trimField(line, "data:")
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(value)
		default:
			// event:/id:/retry: 及未知字段，本层不消费
		}
	}
}

// WriteSSEMessage 写出一个 message 事件帧并以空行结束；
// writer 支持 Flusher 时立即冲刷(SSE 长流必需)
func WriteSSEMessage(w io.Writer, payload []byte) error {
	if _, err := w.Write([]byte("event: message\ndata: ")); err != nil {
		return err
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	if _, err := w.Write([]byte("\n\n")); err != nil {
		return err
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}

// trimCRLF 去除行尾 \n 或 \r\n
func trimCRLF(line string) string {
	line = strings.TrimSuffix(line, "\n")
	return strings.TrimSuffix(line, "\r")
}

// trimField 去掉字段名与其后的一个空格(SSE 规范允许 "data:x" 与 "data: x")
func trimField(line, field string) string {
	value := line[len(field):]
	return strings.TrimPrefix(value, " ")
}
