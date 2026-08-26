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

	"github.com/druidcaesa/neuralgate/pkg/admin"
	"github.com/druidcaesa/neuralgate/pkg/config"
	"github.com/druidcaesa/neuralgate/pkg/core"
	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/enterprise"
	"go.uber.org/zap"
)

// setupCompliance 启动合规报表调度器并向管理后台注入手动补生成器；未满足门控条件时记录原因。
// 调度器有分钟级后台循环，返回停止函数交由 shutdown 序列调用；
// pkg/admin 不依赖 enterprise 包，生成器经薄闭包适配注入
func setupCompliance(gate core.LicenseGate, cfg config.Config, storage plugin.StoragePlugin,
	logger *zap.Logger, adminServer *admin.AdminServer) func() {
	start, reason := shouldStartCompliance(gate, cfg.Compliance.Enabled)
	if !start {
		logger.Info("合规运维未启用", zap.String("reason", reason))
		return nil
	}
	sched := enterprise.NewReportScheduler(storage, logger)
	sched.Start()
	adminServer.SetReportGenerator(func(pt string, start time.Time) (*plugin.ComplianceReport, error) {
		return enterprise.GenerateComplianceReport(storage, logger, pt, start)
	})
	logger.Info("合规报表调度已启用")
	return sched.Stop
}
