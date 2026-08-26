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

// NewDynamicStorage 创建按 driver 分发的存储(供各版本工厂统一使用)
func NewDynamicStorage() plugin.StoragePlugin { return &dynamicStorage{} }

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
func (d *dynamicStorage) CountAdminUsers() (int64, error) {
	return d.impl.CountAdminUsers()
}
func (d *dynamicStorage) GetAdminUserByUsername(username string) (*plugin.AdminUser, error) {
	return d.impl.GetAdminUserByUsername(username)
}
func (d *dynamicStorage) GetAdminUserByID(id string) (*plugin.AdminUser, error) {
	return d.impl.GetAdminUserByID(id)
}
func (d *dynamicStorage) SaveAdminUser(user *plugin.AdminUser) error {
	return d.impl.SaveAdminUser(user)
}
func (d *dynamicStorage) DeleteAdminUser(id string) error {
	return d.impl.DeleteAdminUser(id)
}
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
func (d *dynamicStorage) ListRateLimitConfigs(tenantID *string, page, size int) ([]*plugin.RateLimitConfig, int64, error) {
	return d.impl.ListRateLimitConfigs(tenantID, page, size)
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

// ===== 隐私合规(规则库/白名单/安全事件,委托底层实现) =====

func (d *dynamicStorage) SavePrivacyRule(rule *plugin.PrivacyRule) error {
	return d.impl.SavePrivacyRule(rule)
}
func (d *dynamicStorage) DeletePrivacyRule(id string) error {
	return d.impl.DeletePrivacyRule(id)
}
func (d *dynamicStorage) ListPrivacyRules(ruleType *string) ([]*plugin.PrivacyRule, error) {
	return d.impl.ListPrivacyRules(ruleType)
}
func (d *dynamicStorage) SavePrivacyWhitelistEntry(entry *plugin.PrivacyWhitelistEntry) error {
	return d.impl.SavePrivacyWhitelistEntry(entry)
}
func (d *dynamicStorage) DeletePrivacyWhitelistEntry(id string) error {
	return d.impl.DeletePrivacyWhitelistEntry(id)
}
func (d *dynamicStorage) ListPrivacyWhitelistEntries() ([]*plugin.PrivacyWhitelistEntry, error) {
	return d.impl.ListPrivacyWhitelistEntries()
}
func (d *dynamicStorage) SaveSecurityEvent(event *plugin.SecurityEvent) error {
	return d.impl.SaveSecurityEvent(event)
}
func (d *dynamicStorage) ListSecurityEvents(page, size int) ([]*plugin.SecurityEvent, int64, error) {
	return d.impl.ListSecurityEvents(page, size)
}

// ===== RBAC 权限体系(委托底层实现) =====

func (d *dynamicStorage) GetTenantByID(id string) (*plugin.Tenant, error) {
	return d.impl.GetTenantByID(id)
}
func (d *dynamicStorage) GetTenantByCode(code string) (*plugin.Tenant, error) {
	return d.impl.GetTenantByCode(code)
}
func (d *dynamicStorage) ListTenants(page, size int) ([]*plugin.Tenant, int64, error) {
	return d.impl.ListTenants(page, size)
}
func (d *dynamicStorage) SaveTenant(tenant *plugin.Tenant) error {
	return d.impl.SaveTenant(tenant)
}
func (d *dynamicStorage) DeleteTenant(id string) error {
	return d.impl.DeleteTenant(id)
}
func (d *dynamicStorage) CountTenants() (int64, error) {
	return d.impl.CountTenants()
}
func (d *dynamicStorage) CountAPIKeysByTenantID(tenantID string) (int64, error) {
	return d.impl.CountAPIKeysByTenantID(tenantID)
}
func (d *dynamicStorage) GetRoleByID(id string) (*plugin.Role, error) {
	return d.impl.GetRoleByID(id)
}
func (d *dynamicStorage) ListRoles() ([]*plugin.Role, error) {
	return d.impl.ListRoles()
}
func (d *dynamicStorage) SaveRole(role *plugin.Role) error {
	return d.impl.SaveRole(role)
}
func (d *dynamicStorage) DeleteRole(id string) error {
	return d.impl.DeleteRole(id)
}
func (d *dynamicStorage) CountAdminUsersByRoleID(roleID string) (int64, error) {
	return d.impl.CountAdminUsersByRoleID(roleID)
}
func (d *dynamicStorage) SaveAdminOperationLog(log *plugin.AdminOperationLog) error {
	return d.impl.SaveAdminOperationLog(log)
}
func (d *dynamicStorage) ListAdminOperationLogs(filter plugin.AdminOpLogFilter, page, size int) ([]*plugin.AdminOperationLog, int64, error) {
	return d.impl.ListAdminOperationLogs(filter, page, size)
}
func (d *dynamicStorage) ListAdminUsers() ([]*plugin.AdminUser, error) {
	return d.impl.ListAdminUsers()
}
func (d *dynamicStorage) CountActiveAdminUsersByRoleID(roleID string) (int64, error) {
	return d.impl.CountActiveAdminUsersByRoleID(roleID)
}

// ===== 合规报表(委托底层实现) =====

func (d *dynamicStorage) SaveComplianceReport(report *plugin.ComplianceReport) error {
	return d.impl.SaveComplianceReport(report)
}
func (d *dynamicStorage) ListComplianceReports(page, size int) ([]*plugin.ComplianceReport, int64, error) {
	return d.impl.ListComplianceReports(page, size)
}
func (d *dynamicStorage) GetComplianceReport(id string) (*plugin.ComplianceReport, error) {
	return d.impl.GetComplianceReport(id)
}
func (d *dynamicStorage) FindComplianceReportByPeriod(periodType string, periodStart time.Time) (*plugin.ComplianceReport, error) {
	return d.impl.FindComplianceReportByPeriod(periodType, periodStart)
}
func (d *dynamicStorage) CountComplianceReports() (int64, error) {
	return d.impl.CountComplianceReports()
}

// ===== MCP 上游配置(委托底层实现) =====

func (d *dynamicStorage) SaveMCPServer(server *plugin.MCPServer) error {
	return d.impl.SaveMCPServer(server)
}
func (d *dynamicStorage) GetMCPServer(id string) (*plugin.MCPServer, error) {
	return d.impl.GetMCPServer(id)
}
func (d *dynamicStorage) ListMCPServers(page, size int) ([]*plugin.MCPServer, int64, error) {
	return d.impl.ListMCPServers(page, size)
}
func (d *dynamicStorage) DeleteMCPServer(id string) error {
	return d.impl.DeleteMCPServer(id)
}
