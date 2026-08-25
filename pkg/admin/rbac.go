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

package admin

import (
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// registerRBACRoutes 注册租户/角色/用户/操作日志路由（权限码守卫）
func (s *AdminServer) registerRBACRoutes(authz *gin.RouterGroup) {
	authz.POST("/tenants", s.RequirePermission(plugin.PermTenantWrite), s.createTenant)
	authz.GET("/tenants", s.RequirePermission(plugin.PermTenantRead), s.listTenants)
	authz.PUT("/tenants/:id", s.RequirePermission(plugin.PermTenantWrite), s.updateTenant)
	authz.DELETE("/tenants/:id", s.RequirePermission(plugin.PermTenantWrite), s.deleteTenant)

	authz.POST("/roles", s.RequirePermission(plugin.PermRBACWrite), s.createRole)
	authz.GET("/roles", s.RequirePermission(plugin.PermRBACRead), s.listRoles)
	authz.PUT("/roles/:id", s.RequirePermission(plugin.PermRBACWrite), s.updateRole)
	authz.DELETE("/roles/:id", s.RequirePermission(plugin.PermRBACWrite), s.deleteRole)

	authz.POST("/admin-users", s.RequirePermission(plugin.PermRBACWrite), s.createAdminUser)
	authz.GET("/admin-users", s.RequirePermission(plugin.PermRBACRead), s.listAdminUsers)
	authz.PUT("/admin-users/:id", s.RequirePermission(plugin.PermRBACWrite), s.updateAdminUser)
	authz.DELETE("/admin-users/:id", s.RequirePermission(plugin.PermRBACWrite), s.deleteAdminUser)

	authz.GET("/operation-logs", s.RequirePermission(plugin.PermSystemRead), s.listOperationLogs)
}

var tenantCodePattern = regexp.MustCompile(`^[A-Za-z0-9]{1,32}$`)
var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9]{3,32}$`)

// ===== 租户管理 =====

type tenantRequest struct {
	Name   string            `json:"name" binding:"required,min=1,max=64"`
	Code   string            `json:"code" binding:"required"`
	Status string            `json:"status" binding:"omitempty,oneof=active disabled"`
	Config map[string]string `json:"config"`
}

func (s *AdminServer) createTenant(c *gin.Context) {
	var req tenantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	if !tenantCodePattern.MatchString(req.Code) {
		Error(c, http.StatusBadRequest, 400, "code 须为 1-32 位字母数字")
		return
	}
	if _, err := s.storage.GetTenantByCode(req.Code); err == nil {
		Error(c, http.StatusConflict, 409, "租户编码已存在")
		return
	}
	status := req.Status
	if status == "" {
		status = string(plugin.TenantStatusActive)
	}
	now := time.Now()
	tenant := &plugin.Tenant{
		ID: uuid.NewString(), Name: req.Name, Code: req.Code,
		Status: plugin.TenantStatus(status), Config: req.Config,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.storage.SaveTenant(tenant); err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to save tenant")
		return
	}
	OK(c, gin.H{"id": tenant.ID})
}

func (s *AdminServer) listTenants(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	tenants, total, err := s.storage.ListTenants(page, size)
	if err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to list tenants")
		return
	}
	OK(c, gin.H{"items": tenants, "total": total, "page": page, "size": size})
}

func (s *AdminServer) updateTenant(c *gin.Context) {
	id := c.Param("id")
	var req tenantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	existing, err := s.storage.GetTenantByID(id)
	if err != nil {
		Error(c, http.StatusNotFound, 404, "tenant not found")
		return
	}
	// code 冲突校验（改成他人 code → 409）
	if other, gerr := s.storage.GetTenantByCode(req.Code); gerr == nil && other.ID != id {
		Error(c, http.StatusConflict, 409, "租户编码已存在")
		return
	}
	existing.Name = req.Name
	existing.Code = req.Code
	if req.Status != "" {
		existing.Status = plugin.TenantStatus(req.Status)
	}
	existing.Config = req.Config
	if err := s.storage.SaveTenant(existing); err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to update tenant")
		return
	}
	OK(c, gin.H{"id": id})
}

func (s *AdminServer) deleteTenant(c *gin.Context) {
	id := c.Param("id")
	if n, err := s.storage.CountAPIKeysByTenantID(id); err == nil && n > 0 {
		Error(c, http.StatusConflict, 409, "租户下存在 API Key，禁止删除")
		return
	}
	if err := s.storage.DeleteTenant(id); err != nil {
		Error(c, http.StatusNotFound, 404, "tenant not found")
		return
	}
	OK(c, gin.H{"id": id, "deleted": true})
}

// ===== 角色管理 =====

type roleRequest struct {
	Name        string   `json:"name" binding:"required,min=1,max=64"`
	TenantID    string   `json:"tenant_id"`
	Permissions []string `json:"permissions" binding:"required,min=1,dive,required"`
}

// validPermissions 校验权限码全部在清单内
func validPermissions(perms []string) bool {
	allowed := make(map[string]bool, len(plugin.AllPermissions))
	for _, p := range plugin.AllPermissions {
		allowed[p] = true
	}
	for _, p := range perms {
		if !allowed[p] {
			return false
		}
	}
	return true
}

func (s *AdminServer) createRole(c *gin.Context) {
	var req roleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	if !validPermissions(req.Permissions) {
		Error(c, http.StatusBadRequest, 400, "permissions 含未定义的权限码")
		return
	}
	if forced := s.scopeTenant(c); forced != nil {
		req.TenantID = *forced // 租户内用户只能建本租户角色
	}
	now := time.Now()
	role := &plugin.Role{
		ID: uuid.NewString(), Name: req.Name, TenantID: req.TenantID,
		Permissions: req.Permissions, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.storage.SaveRole(role); err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to save role")
		return
	}
	OK(c, gin.H{"id": role.ID})
}

func (s *AdminServer) listRoles(c *gin.Context) {
	roles, err := s.storage.ListRoles()
	if err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to list roles")
		return
	}
	OK(c, gin.H{"items": roles})
}

// superRoleGuard 超管角色禁改禁删；命中时已写响应并返回 true
func (s *AdminServer) superRoleGuard(c *gin.Context, role *plugin.Role) bool {
	if plugin.IsSuperRole(role) || role.Name == plugin.SuperRoleName {
		Error(c, http.StatusConflict, 409, "内置超管角色不可修改或删除")
		return true
	}
	return false
}

func (s *AdminServer) updateRole(c *gin.Context) {
	id := c.Param("id")
	var req roleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	existing, err := s.storage.GetRoleByID(id)
	if err != nil {
		Error(c, http.StatusNotFound, 404, "role not found")
		return
	}
	if s.superRoleGuard(c, existing) {
		return
	}
	if !validPermissions(req.Permissions) {
		Error(c, http.StatusBadRequest, 400, "permissions 含未定义的权限码")
		return
	}
	existing.Name = req.Name
	existing.Permissions = req.Permissions
	if err := s.storage.SaveRole(existing); err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to update role")
		return
	}
	OK(c, gin.H{"id": id})
}

func (s *AdminServer) deleteRole(c *gin.Context) {
	id := c.Param("id")
	role, err := s.storage.GetRoleByID(id)
	if err != nil {
		Error(c, http.StatusNotFound, 404, "role not found")
		return
	}
	if s.superRoleGuard(c, role) {
		return
	}
	if n, gerr := s.storage.CountAdminUsersByRoleID(id); gerr == nil && n > 0 {
		Error(c, http.StatusConflict, 409, "仍有账号挂载该角色，禁止删除")
		return
	}
	if err := s.storage.DeleteRole(id); err != nil {
		Error(c, http.StatusNotFound, 404, "role not found")
		return
	}
	OK(c, gin.H{"id": id, "deleted": true})
}

// ===== 用户管理（admin_users）=====

type adminUserCreateRequest struct {
	Username string `json:"username" binding:"required"`
	TenantID string `json:"tenant_id"`
	RoleID   string `json:"role_id" binding:"required"`
	Status   string `json:"status" binding:"omitempty,oneof=active disabled"`
}

type adminUserCreateWithPassword struct {
	adminUserCreateRequest
	Password string `json:"password" binding:"required,min=8,max=64"`
}

type adminUserUpdateRequest struct {
	TenantID string `json:"tenant_id"`
	RoleID   string `json:"role_id" binding:"required"`
	Status   string `json:"status" binding:"required,oneof=active disabled"`
	Password string `json:"password" binding:"omitempty,min=8,max=64"` // 不传不改动
}

func (s *AdminServer) createAdminUser(c *gin.Context) {
	var req adminUserCreateWithPassword
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	if !usernamePattern.MatchString(req.Username) {
		Error(c, http.StatusBadRequest, 400, "username 须为 3-32 位字母数字")
		return
	}
	if _, err := s.storage.GetAdminUserByUsername(req.Username); err == nil {
		Error(c, http.StatusConflict, 409, "用户名已存在")
		return
	}
	if _, err := s.storage.GetRoleByID(req.RoleID); err != nil {
		Error(c, http.StatusBadRequest, 400, "role_id 不存在")
		return
	}
	if req.TenantID != "" {
		if _, err := s.storage.GetTenantByID(req.TenantID); err != nil {
			Error(c, http.StatusBadRequest, 400, "tenant_id 不存在")
			return
		}
	} else if forced := s.scopeTenant(c); forced != nil {
		req.TenantID = *forced // 租户内用户建号强制归属自身租户
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to hash password")
		return
	}
	status := req.Status
	if status == "" {
		status = string(plugin.AdminUserStatusActive)
	}
	now := time.Now()
	user := &plugin.AdminUser{
		ID: uuid.NewString(), Username: req.Username, PasswordHash: string(hash),
		TenantID: req.TenantID, RoleID: req.RoleID, Status: plugin.AdminUserStatus(status),
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.storage.SaveAdminUser(user); err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to save user")
		return
	}
	OK(c, gin.H{"id": user.ID, "username": user.Username})
}

func (s *AdminServer) listAdminUsers(c *gin.Context) {
	users, err := s.storage.ListAdminUsers()
	if err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to list users")
		return
	}
	type item struct {
		ID        string    `json:"id"`
		Username  string    `json:"username"`
		TenantID  string    `json:"tenant_id"`
		RoleID    string    `json:"role_id"`
		Status    string    `json:"status"`
		CreatedAt time.Time `json:"created_at"`
	}
	items := make([]item, 0, len(users))
	for _, u := range users {
		items = append(items, item{
			ID: u.ID, Username: u.Username, TenantID: u.TenantID,
			RoleID: u.RoleID, Status: string(u.Status), CreatedAt: u.CreatedAt,
		}) // 不回显 password_hash
	}
	OK(c, gin.H{"items": items})
}

// lastSuperGuard 处置目标后是否再无活跃超管账号；命中时已写响应并返回 true。
// 以存储中的实时角色判定（会话快照不参与），避免降权自己后误锁系统
func (s *AdminServer) lastSuperGuard(c *gin.Context, target *plugin.AdminUser) bool {
	role, err := s.storage.GetRoleByID(target.RoleID)
	if err != nil || !plugin.IsSuperRole(role) {
		return false // 目标非超管角色，无守卫必要
	}
	actives, _ := s.storage.CountActiveAdminUsersByRoleID(target.RoleID)
	if actives > 1 {
		return false // 除目标外仍有活跃超管
	}
	if target.Status == plugin.AdminUserStatusDisabled && actives >= 1 {
		return false // 禁用非活跃账号不影响超管在位数量
	}
	Error(c, http.StatusConflict, 409, "不能移除最后一个超管账号")
	return true
}

func (s *AdminServer) updateAdminUser(c *gin.Context) {
	id := c.Param("id")
	var req adminUserUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	user, err := s.storage.GetAdminUserByID(id)
	if err != nil {
		Error(c, http.StatusNotFound, 404, "user not found")
		return
	}
	if _, gerr := s.storage.GetRoleByID(req.RoleID); gerr != nil {
		Error(c, http.StatusBadRequest, 400, "role_id 不存在")
		return
	}
	user.TenantID = req.TenantID
	user.RoleID = req.RoleID
	user.Status = plugin.AdminUserStatus(req.Status)
	if req.Password != "" {
		hash, herr := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if herr != nil {
			Error(c, http.StatusInternalServerError, 500, "failed to hash password")
			return
		}
		user.PasswordHash = string(hash)
	}
	user.UpdatedAt = time.Now()
	if s.lastSuperGuard(c, user) {
		return
	}
	if err := s.storage.SaveAdminUser(user); err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to update user")
		return
	}
	OK(c, gin.H{"id": id})
}

func (s *AdminServer) deleteAdminUser(c *gin.Context) {
	id := c.Param("id")
	claims := s.currentClaims(c)
	if claims != nil && claims.Sub == id {
		Error(c, http.StatusConflict, 409, "不能删除当前登录账号")
		return
	}
	user, err := s.storage.GetAdminUserByID(id)
	if err != nil {
		Error(c, http.StatusNotFound, 404, "user not found")
		return
	}
	if s.lastSuperGuard(c, user) {
		return
	}
	if err := s.storage.DeleteAdminUser(id); err != nil {
		Error(c, http.StatusNotFound, 404, "user not found")
		return
	}
	OK(c, gin.H{"id": id, "deleted": true})
}

// ===== 操作日志 =====

func (s *AdminServer) listOperationLogs(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	filter := plugin.AdminOpLogFilter{UserID: c.Query("user_id")}
	logs, total, err := s.storage.ListAdminOperationLogs(filter, page, size)
	if err != nil {
		Error(c, http.StatusInternalServerError, 500, "failed to list operation logs")
		return
	}
	OK(c, gin.H{"items": logs, "total": total, "page": page, "size": size})
}
