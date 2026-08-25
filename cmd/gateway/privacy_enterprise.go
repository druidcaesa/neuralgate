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
	"time"

	"github.com/druidcaesa/neuralgate/pkg/config"
	"github.com/druidcaesa/neuralgate/pkg/core"
	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/enterprise"
	"go.uber.org/zap"
)

// privacyReloadInterval 规则缓存重载周期：管理后台 CRUD 生效延迟上界
const privacyReloadInterval = 30 * time.Second

// setupPrivacy 构造隐私引擎并挂载中间件；未满足门控条件时记录原因。
// 引擎无后台任务（TTL 惰性重载），无需停止函数
func setupPrivacy(gate core.LicenseGate, cfg config.Config, pipeline *core.Pipeline,
	auditor plugin.AuditPipeline, storage plugin.StoragePlugin, logger *zap.Logger) {
	start, reason := shouldStartPrivacy(gate, cfg.Privacy.Enabled)
	if !start {
		logger.Info("隐私防护未启用", zap.String("reason", reason))
		return
	}
	engine := enterprise.NewPrivacyEngine(storage, privacyReloadInterval, logger)
	pipeline.Use(enterprise.NewPrivacyMiddleware(engine, auditor, storage, logger))
	logger.Info("隐私防护已启用",
		zap.Duration("reload_interval", privacyReloadInterval),
		zap.Int("inspect_body_limit", 1<<20))
}
