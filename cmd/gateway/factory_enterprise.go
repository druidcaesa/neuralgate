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

const edition = "enterprise"

// newPluginFactory 由 BuildTag 决定返回哪个版本的插件工厂
func newPluginFactory() plugin.PluginFactory {
	return enterprise.NewPluginFactory()
}

// setupTamper 注入指纹钩子并启动校验与留存任务；未满足门控条件时记录原因并返回 nil。
// 返回的停止函数须在 auditor.Shutdown 之前调用
func setupTamper(gate core.LicenseGate, auditor plugin.AuditPipeline,
	storage plugin.StoragePlugin, cfg config.AuditConfig, logger *zap.Logger) func() {
	setter, can := auditor.(plugin.FingerprintHook)
	if !can {
		logger.Warn("审计器不支持指纹注入")
		return nil
	}
	start, reason := shouldStartTamper(gate, cfg.EnableSHA256)
	if !start {
		logger.Info("审计防篡改未启用", zap.String("reason", reason))
		return nil
	}
	if cfg.FingerprintAlgo != "sha256" {
		logger.Warn("指纹算法未注册，回退 sha256", zap.String("algo", cfg.FingerprintAlgo))
	}
	setter.SetFingerprintFunc(func(log *plugin.AuditLog) string {
		return enterprise.Fingerprint(cfg.FingerprintAlgo, log)
	})
	retention := time.Duration(cfg.RetentionDays) * 24 * time.Hour
	tasks := enterprise.NewTasks(storage, cfg.FingerprintAlgo,
		cfg.VerifyInterval, cfg.VerifyBatchSize, retention, logger)
	tasks.Start()
	logger.Info("审计防篡改已启用",
		zap.String("algo", cfg.FingerprintAlgo),
		zap.Duration("verify_interval", cfg.VerifyInterval))
	return tasks.Stop
}
