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
	"time"

	"go.uber.org/zap"
)

// accessLogger 数据面访问日志：nil 安全（未配置即静默）
type accessLogger struct {
	l *zap.Logger
}

func newAccessLogger(l *zap.Logger) *accessLogger {
	if l == nil {
		return nil
	}
	return &accessLogger{l: l}
}

// info 输出一条访问日志：method/path/status/duration_ms/request_id/client_ip
func (a *accessLogger) info(method, path string, status int, rc *RequestContext) {
	if a == nil || a.l == nil || rc == nil {
		return
	}
	a.l.Info("访问日志",
		zap.String("method", method),
		zap.String("path", path),
		zap.Int("status", status),
		zap.Int64("duration_ms", time.Since(rc.StartTime).Milliseconds()),
		zap.String("request_id", rc.RequestID),
		zap.String("client_ip", rc.ClientIP),
	)
}
