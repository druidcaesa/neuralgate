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
	"fmt"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
)

// dynamicStorage 按 Init 传入的 driver 分发到 mem/sqlite/mysql 实现
type dynamicStorage struct {
	impl plugin.StoragePlugin
}

func (d *dynamicStorage) Init(config map[string]interface{}) error {
	driver, _ := config["driver"].(string)
	switch driver {
	case "", "mem":
		d.impl = NewMemStorage()
	case "sqlite", "mysql":
		d.impl = NewSQLStorage()
	default:
		return fmt.Errorf("unsupported storage driver: %s", driver)
	}
	return d.impl.Init(config)
}

func (d *dynamicStorage) GetAPIKey(keyHash string) (*plugin.APIKey, error) {
	return d.impl.GetAPIKey(keyHash)
}
func (d *dynamicStorage) GetAPIKeyByID(id string) (*plugin.APIKey, error) {
	return d.impl.GetAPIKeyByID(id)
}
func (d *dynamicStorage) SaveAPIKey(key *plugin.APIKey) error { return d.impl.SaveAPIKey(key) }
func (d *dynamicStorage) IncrementAPIKeyUsage(keyID string, delta int64) error {
	return d.impl.IncrementAPIKeyUsage(keyID, delta)
}
func (d *dynamicStorage) ListAPIKeys(tenantID string, page, size int) ([]*plugin.APIKey, int64, error) {
	return d.impl.ListAPIKeys(tenantID, page, size)
}
func (d *dynamicStorage) DeleteAPIKey(keyID string) error { return d.impl.DeleteAPIKey(keyID) }
func (d *dynamicStorage) GetModelConfig(modelName string) (*plugin.ModelConfig, error) {
	return d.impl.GetModelConfig(modelName)
}
func (d *dynamicStorage) GetModelConfigByID(id string) (*plugin.ModelConfig, error) {
	return d.impl.GetModelConfigByID(id)
}
func (d *dynamicStorage) ListModelConfigs(page, size int) ([]*plugin.ModelConfig, int64, error) {
	return d.impl.ListModelConfigs(page, size)
}
func (d *dynamicStorage) SaveModelConfig(config *plugin.ModelConfig) error {
	return d.impl.SaveModelConfig(config)
}
func (d *dynamicStorage) DeleteModelConfig(id string) error { return d.impl.DeleteModelConfig(id) }
func (d *dynamicStorage) SaveAuditLog(log *plugin.AuditLog) error {
	return d.impl.SaveAuditLog(log)
}
func (d *dynamicStorage) BatchSaveAuditLogs(logs []*plugin.AuditLog) error {
	return d.impl.BatchSaveAuditLogs(logs)
}
func (d *dynamicStorage) QueryAuditLogs(filter plugin.AuditLogFilter, page, size int) ([]*plugin.AuditLog, int64, error) {
	return d.impl.QueryAuditLogs(filter, page, size)
}
func (d *dynamicStorage) GetRateLimitConfig(tenantID, modelName string) (*plugin.RateLimitConfig, error) {
	return d.impl.GetRateLimitConfig(tenantID, modelName)
}
func (d *dynamicStorage) SaveRateLimitConfig(cfg *plugin.RateLimitConfig) error {
	return d.impl.SaveRateLimitConfig(cfg)
}
func (d *dynamicStorage) ListRateLimitConfigs(page, size int) ([]*plugin.RateLimitConfig, int64, error) {
	return d.impl.ListRateLimitConfigs(page, size)
}
func (d *dynamicStorage) DeleteRateLimitConfig(id string) error {
	return d.impl.DeleteRateLimitConfig(id)
}
func (d *dynamicStorage) ListUpstreams(modelConfigID string) ([]*plugin.Upstream, error) {
	return d.impl.ListUpstreams(modelConfigID)
}
func (d *dynamicStorage) GetUpstreamByID(id string) (*plugin.Upstream, error) {
	return d.impl.GetUpstreamByID(id)
}
func (d *dynamicStorage) SaveUpstream(up *plugin.Upstream) error {
	return d.impl.SaveUpstream(up)
}
func (d *dynamicStorage) DeleteUpstream(id string) error {
	return d.impl.DeleteUpstream(id)
}
func (d *dynamicStorage) Ping() error  { return d.impl.Ping() }
func (d *dynamicStorage) Close() error { return d.impl.Close() }

// ===== 留存清理与篡改告警(委托底层实现) =====

func (d *dynamicStorage) DeleteAuditLogsBefore(cutoff time.Time) (int64, error) {
	return d.impl.DeleteAuditLogsBefore(cutoff)
}
func (d *dynamicStorage) SaveTamperAlerts(alerts []*plugin.TamperAlert) error {
	return d.impl.SaveTamperAlerts(alerts)
}
func (d *dynamicStorage) ListTamperAlerts(resolved *bool, page, size int) ([]*plugin.TamperAlert, int64, error) {
	return d.impl.ListTamperAlerts(resolved, page, size)
}
func (d *dynamicStorage) SetTamperAlertResolved(id string, resolved bool) error {
	return d.impl.SetTamperAlertResolved(id, resolved)
}
