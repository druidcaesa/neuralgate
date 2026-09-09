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
	"github.com/druidcaesa/neuralgate/pkg/admin"
	"github.com/druidcaesa/neuralgate/pkg/config"
	"github.com/druidcaesa/neuralgate/pkg/core"
	"github.com/druidcaesa/neuralgate/pkg/license"
	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/druidcaesa/neuralgate/pkg/plugin/enterprise"
	"go.uber.org/zap"
)

// shouldStartCluster 集群协同门控:cluster 授权 + 配置 enabled(与其它 shouldStart* 同款)
func shouldStartCluster(gate core.LicenseGate, enabled bool) (bool, string) {
	if !enabled {
		return false, "配置未启用(cluster.enabled=false)"
	}
	if !gate.HasFeature(license.FeatureCluster) {
		return false, "授权未包含 cluster 功能"
	}
	return true, ""
}

// exportLeaderJob 把 TailExporter 适配成可选主 job:Start=激活(换主时从水位续拉,见任务 8),
// Stop=停拉取。租约键取 Name
type exportLeaderJob struct{ exp *enterprise.TailExporter }

func (j *exportLeaderJob) Name() string { return "audit-export" }
func (j *exportLeaderJob) Start()       { j.exp.SetActive(true) }
func (j *exportLeaderJob) Stop()        { j.exp.SetActive(false) }

// setupCluster 集群装配(Enterprise):建 Redis → RedisLoginGuard 注入 admin →
// 注册 tamper/compliance/exporter job → Coordinator.Start。
// 返回协调器停止函数;门控不满足或 Redis 不可达时返回 nil,调用方按单机全量启动(降级)
func setupCluster(gate core.LicenseGate, cfg config.Config, adminServer *admin.AdminServer,
	exporter plugin.LogExporter, exportStarted bool, jobs []jobHandle, logger *zap.Logger) func() {
	start, reason := shouldStartCluster(gate, cfg.Cluster.Enabled)
	if !start {
		logger.Info("集群协同未启用", zap.String("reason", reason))
		return nil
	}
	rdb, err := enterprise.NewClusterClient(cfg.Cluster)
	if err != nil {
		logger.Warn("集群 Redis 不可用,回退单机", zap.Error(err))
		return nil // 软降级
	}
	// A: 注入共享登录守卫(默认阈值取自 admin,避免两处写死互不校验)
	maxFails, window := admin.DefaultLoginGuardParams()
	adminServer.SetLoginGuard(enterprise.NewRedisLoginGuard(rdb, maxFails, window))

	coord := enterprise.NewCoordinator(rdb, cfg.Cluster.LeaseTTL, logger)
	// B: 手工任务注册(jobHandle 动态类型均为 enterprise.ClusterJob,故断言安全)
	for _, j := range jobs {
		if cj, ok := j.(enterprise.ClusterJob); ok && j != nil {
			coord.Register(cj)
		}
	}
	// exporter:仅真正 Init 过才纳管;先置从属(不拉取),成为 leader 时 SetActive(true) 从水位续拉
	if exportStarted {
		if ex, ok := exporter.(*enterprise.TailExporter); ok {
			ex.SetActive(false)
			ex.SetWatermarkSink(enterprise.NewRedisWatermarkSink(rdb))
			coord.Register(&exportLeaderJob{exp: ex})
		}
	}
	coord.Start()
	logger.Info("集群协同已启用",
		zap.String("redis", cfg.Cluster.RedisAddr),
		zap.Duration("lease_ttl", cfg.Cluster.LeaseTTL))
	return coord.Stop
}
