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

package main

import (
	"net/http"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/config"
	"github.com/druidcaesa/neuralgate/pkg/core"
	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/enterprise"
	"go.uber.org/zap"
)

// buildMCPRelay 构造 MCP 中继：通道对全部版本开放（OSS+）；
// enterprise 版在门控满足时挂接审计落地器（留存+失败告警），未满足仅记录原因
func buildMCPRelay(gate core.LicenseGate, cfg config.Config, storage plugin.StoragePlugin,
	auditor plugin.AuditPipeline, logger *zap.Logger) *core.MCPRelay {
	var hook plugin.MCPAuditHook
	if start, reason := shouldStartMCPAudit(gate, cfg.MCPAudit.Enabled); !start {
		logger.Info("MCP 调用审计未启用", zap.String("reason", reason))
	} else {
		hook = enterprise.NewMCPAuditRecorder(storage, logger)
		logger.Info("MCP 调用审计已启用")
	}
	return core.NewMCPRelay(storage, auditor, hook, &http.Client{Timeout: 60 * time.Second})
}
