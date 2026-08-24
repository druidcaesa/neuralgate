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

//go:build enterprise

package enterprise

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// 指纹算法注册表：算法选定后不可更换——换算法会使历史指纹全部失配，
// 校验任务会误报篡改。sm3 随信创迭代注册，本期仅 sha256（未知值回退）。
var fingerprintAlgos = map[string]func(*plugin.AuditLog) string{
	"sha256": fingerprintSHA256,
}

// Fingerprint 用指定算法计算审计日志全量内容指纹；algo 未注册时回退 sha256
// （启动接线处已对未知配置值输出告警日志）
func Fingerprint(algo string, log *plugin.AuditLog) string {
	fn, ok := fingerprintAlgos[algo]
	if !ok {
		fn = fingerprintAlgos["sha256"]
	}
	return fn(log)
}

// fingerprintSHA256 对除指纹字段外的全部内容做长度前缀确定性序列化后取 sha256 hex。
// 字段顺序即签名口径：任何内容字段变化都会导致指纹变化；
// Headers 按 key 字典序拼接保证 map 确定性；时间统一 UTC RFC3339。
func fingerprintSHA256(log *plugin.AuditLog) string {
	fields := []string{
		log.ID,
		log.RequestID,
		log.TenantID,
		log.APIKeyID,
		log.ModelName,
		log.Provider,
		log.RequestMethod,
		log.RequestPath,
		headersSegment(log.RequestHeaders),
		log.RequestBody,
		strconv.Itoa(log.ResponseStatus),
		log.ResponseBody,
		sseSegments(log.SSEChunks),
		strconv.Itoa(log.PromptTokens),
		strconv.Itoa(log.CompletionTokens),
		strconv.Itoa(log.TotalTokens),
		strconv.FormatInt(log.Duration, 10),
		log.ClientIP,
		strconv.FormatBool(log.IsStream),
		strconv.FormatBool(log.Disconnected),
		log.DisconnectReason,
		log.CreatedAt.UTC().Format(time.RFC3339),
	}
	var buf strings.Builder
	for _, f := range fields {
		fmt.Fprintf(&buf, "%d:%s;", len(f), f)
	}
	sum := sha256.Sum256([]byte(buf.String()))
	return hex.EncodeToString(sum[:])
}

// headersSegment Headers 子段：key 字典序排列，逐项 "len(k):k=len(v):v;"，
// 与外层字段以长度前缀隔离，键值含分隔符不产生歧义
func headersSegment(headers map[string]string) string {
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var buf strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&buf, "%d:%s=%d:%s;", len(k), k, len(headers[k]), headers[k])
	}
	return buf.String()
}

// sseSegments 流式分片子段：按原序逐条做长度前缀拼接
func sseSegments(chunks []plugin.SSEChunk) string {
	var buf strings.Builder
	for _, c := range chunks {
		item := fmt.Sprintf("%d:%s:%s:%s",
			c.Index, c.EventType, c.Data, c.Timestamp.UTC().Format(time.RFC3339))
		fmt.Fprintf(&buf, "%d:%s;", len(item), item)
	}
	return buf.String()
}
