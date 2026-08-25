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
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/druidcaesa/neuralgate/pkg/plugin"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

const (
	sessionTTLDefault      = 24 * time.Hour // 未指定时的会话有效期
	loginMaxFailures       = 5              // 窗口内允许的连续失败次数
	loginFailureWindow     = time.Minute    // 失败计数窗口
	minAdminPasswordLength = 8              // 新口令最小长度
	tokenHeader            = "X-Admin-Token"
	claimsContextKey       = "admin_claims"
)

// ===== 会话签发与校验 =====

// sessionClaims 会话载荷；PwdDigest 绑定当前口令哈希摘要，
// 使改密/禁用在下一次请求即生效（无需等待过期）。
// 权限快照登录时固化：角色权限变更后须重新登录才生效（与改密失效机制对等）
type sessionClaims struct {
	Sub         string   `json:"sub"`
	Name        string   `json:"name"`
	PwdDigest   string   `json:"pwd"`
	Exp         int64    `json:"exp"`
	TenantID    string   `json:"tid,omitempty"`
	Permissions []string `json:"perm,omitempty"`
	IsSuper     bool     `json:"su,omitempty"`
}

// SessionManager HMAC-SHA256 签名会话：secret 仅存内存，重启后全部会话失效（需重新登录）
type SessionManager struct {
	secret []byte
	ttl    time.Duration
}

// NewSessionManager 创建会话管理器；ttl<=0 取默认 24h
func NewSessionManager(secret []byte, ttl time.Duration) *SessionManager {
	if ttl <= 0 {
		ttl = sessionTTLDefault
	}
	return &SessionManager{secret: secret, ttl: ttl}
}

var b64 = base64.RawURLEncoding

// Mint 为账号签发会话 token；perms/isSuper 为登录时的权限快照（now 由调用方注入便于测试）
func (m *SessionManager) Mint(user *plugin.AdminUser, perms []string, isSuper bool, now time.Time) (string, time.Time, error) {
	exp := now.Add(m.ttl)
	payload, err := json.Marshal(sessionClaims{
		Sub: user.ID, Name: user.Username,
		PwdDigest: pwdDigest(user.PasswordHash), Exp: exp.Unix(),
		TenantID: user.TenantID, Permissions: perms, IsSuper: isSuper,
	})
	if err != nil {
		return "", time.Time{}, err
	}
	payloadB64 := b64.EncodeToString(payload)
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(payloadB64))
	return payloadB64 + "." + b64.EncodeToString(mac.Sum(nil)), exp, nil
}

// Verify 校验签名、有效期、账号状态与口令摘要；lookup 提供按 ID 取最新账号的能力
func (m *SessionManager) Verify(token string, lookup func(id string) (*plugin.AdminUser, error), now time.Time) (*sessionClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return nil, errors.New("malformed session token")
	}
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(parts[0]))
	if !hmac.Equal([]byte(mac.Sum(nil)), b64Bytes(parts[1])) {
		return nil, errors.New("invalid session signature")
	}
	var claims sessionClaims
	raw, err := b64.DecodeString(parts[0])
	if err != nil {
		return nil, errors.New("malformed session payload")
	}
	if err := json.Unmarshal(raw, &claims); err != nil {
		return nil, errors.New("malformed session payload")
	}
	if now.After(time.Unix(claims.Exp, 0)) {
		return nil, errors.New("session expired")
	}
	user, err := lookup(claims.Sub)
	if err != nil || user == nil {
		return nil, errors.New("account no longer exists")
	}
	if user.Status != plugin.AdminUserStatusActive {
		return nil, errors.New("account disabled")
	}
	if claims.PwdDigest != pwdDigest(user.PasswordHash) {
		return nil, errors.New("credential rotated")
	}
	return &claims, nil
}

// pwdDigest 口令哈希的 SHA256 摘要（token 内不携带哈希原文）
func pwdDigest(passwordHash string) string {
	sum := sha256.Sum256([]byte(passwordHash))
	return hex.EncodeToString(sum[:])
}

func b64Bytes(s string) []byte {
	b, _ := b64.DecodeString(s)
	return b
}

// randomSecret 生成进程级会话密钥
func randomSecret(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("生成会话密钥失败: " + err.Error())
	}
	return b
}

// ===== 登录防爆破 =====

// loginGuard 按 ip|用户名 维度的失败计数：窗口内连败达上限则锁定一个窗口周期
type loginGuard struct {
	mu       sync.Mutex
	fails    map[string]*failureRecord
	window   time.Duration
	maxFails int
	now      func() time.Time
}

type failureRecord struct {
	count  int
	start  time.Time
	locked bool
}

func newLoginGuard() *loginGuard {
	return &loginGuard{
		fails:    make(map[string]*failureRecord),
		window:   loginFailureWindow,
		maxFails: loginMaxFailures,
		now:      time.Now,
	}
}

// Allow 返回是否放行本次尝试
func (g *loginGuard) Allow(key string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	r, ok := g.fails[key]
	if !ok {
		return true
	}
	if g.now().Sub(r.start) > g.window {
		delete(g.fails, key)
		return true
	}
	return !r.locked
}

func (g *loginGuard) Fail(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	r, ok := g.fails[key]
	now := g.now()
	if !ok || now.Sub(r.start) > g.window {
		g.fails[key] = &failureRecord{count: 1, start: now}
		return
	}
	r.count++
	if r.count >= g.maxFails {
		r.locked = true
		r.start = now // 锁定自此刻起再持续一个窗口
		r.count = 0
	}
}

func (g *loginGuard) Reset(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.fails, key)
}

// ===== 中间件与路由处理 =====

// EnableAuth 注入会话管理器与 CORS 白名单（生产装配入口；sm 为 nil 时仅更新白名单）
func (s *AdminServer) EnableAuth(sm *SessionManager, allowedOrigins []string) {
	if sm != nil {
		s.sessions = sm
	}
	s.allowedOrigins = allowedOrigins
}

// DisableAuth 关闭认证。仅供既有处理器级测试使用——生产装配不得调用；
// NewAdminServer 默认已启用认证（fail-closed），关闭是显式行为
func (s *AdminServer) DisableAuth() { s.sessions = nil }

// EnableRBAC 启用权限体系（rbac.enabled + FeatureRBAC 双条件在 main 计算后注入）。
// 中间件闭包动态读取开关，须在服务开始监听前调用
func (s *AdminServer) EnableRBAC(enabled bool) { s.rbacEnabled = enabled }

// currentClaims 取当前会话 claims（RequireAuth 已注入）
func (s *AdminServer) currentClaims(c *gin.Context) *sessionClaims {
	claims, _ := c.Get(claimsContextKey)
	session, _ := claims.(*sessionClaims)
	return session
}

// RequirePermission 功能权限守卫：RBAC 未启用恒放行（现状零变化）；
// 超管或会话权限含 perm 放行，否则 403 无权限
func (s *AdminServer) RequirePermission(perm string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !s.rbacEnabled {
			c.Next()
			return
		}
		claims := s.currentClaims(c)
		if claims != nil && (claims.IsSuper || containsPerm(claims.Permissions, perm)) {
			c.Next()
			return
		}
		Error(c, http.StatusForbidden, http.StatusForbidden, "无权限")
		c.Abort()
	}
}

func containsPerm(perms []string, perm string) bool {
	for _, p := range perms {
		if p == perm {
			return true
		}
	}
	return false
}

// scopeTenant 租户过滤注入：非超管且绑定租户时返回自身租户指针，其余返回 nil（不过滤）
func (s *AdminServer) scopeTenant(c *gin.Context) *string {
	if !s.rbacEnabled {
		return nil
	}
	claims := s.currentClaims(c)
	if claims == nil || claims.IsSuper || claims.TenantID == "" {
		return nil
	}
	v := claims.TenantID
	return &v
}

// RequireAuth 校验 X-Admin-Token（兼容 Authorization: Bearer），通过后注入 claims
func (s *AdminServer) RequireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.sessions == nil {
			c.Next()
			return
		}
		token := c.GetHeader(tokenHeader)
		if token == "" {
			auth := c.GetHeader("Authorization")
			if strings.HasPrefix(auth, "Bearer ") {
				token = strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
			}
		}
		claims, err := s.sessions.Verify(token, s.storage.GetAdminUserByID, time.Now())
		if err != nil {
			Error(c, http.StatusUnauthorized, http.StatusUnauthorized, "unauthorized")
			c.Abort()
			return
		}
		c.Set(claimsContextKey, claims)
		c.Next()
	}
}

// CORS 跨域白名单：仅回显命中的 Origin；列表为空不发送跨域头（同源部署）
func (s *AdminServer) CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" && s.originAllowed(origin) {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, "+tokenHeader)
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func (s *AdminServer) originAllowed(origin string) bool {
	for _, o := range s.allowedOrigins {
		if o == origin {
			return true
		}
	}
	return false
}

// loginRequest POST /api/auth/login 请求体
type loginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// handleLogin 登录：验密成功签发会话 token；失败统一 401 不区分账号是否存在
func (s *AdminServer) handleLogin(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, http.StatusBadRequest, http.StatusBadRequest, err.Error())
		return
	}
	guardKey := c.ClientIP() + "|" + req.Username
	if !s.loginGuard.Allow(guardKey) {
		Error(c, http.StatusTooManyRequests, http.StatusTooManyRequests, "too many failed attempts, try later")
		return
	}
	user, err := s.storage.GetAdminUserByUsername(req.Username)
	if err != nil || user == nil {
		s.loginGuard.Fail(guardKey)
		Error(c, http.StatusUnauthorized, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if user.Status != plugin.AdminUserStatusActive {
		Error(c, http.StatusForbidden, http.StatusForbidden, "account disabled")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)) != nil {
		s.loginGuard.Fail(guardKey)
		Error(c, http.StatusUnauthorized, http.StatusUnauthorized, "invalid credentials")
		return
	}
	s.loginGuard.Reset(guardKey)
	// 权限快照：登录时解析角色并固化进会话
	perms := []string{}
	isSuper := false
	if user.RoleID != "" {
		if role, rerr := s.storage.GetRoleByID(user.RoleID); rerr == nil && role != nil {
			perms = role.Permissions
			isSuper = plugin.IsSuperRole(role)
		}
	}
	token, exp, err := s.sessions.Mint(user, perms, isSuper, time.Now())
	if err != nil {
		Error(c, http.StatusInternalServerError, http.StatusInternalServerError, "failed to issue token")
		return
	}
	// 尽力记录最近登录时间（失败不影响登录结果）
	user.LastLoginAt = ptrTime(time.Now())
	user.UpdatedAt = time.Now()
	_ = s.storage.SaveAdminUser(user)
	OK(c, gin.H{
		"token": token, "expires_at": exp.UTC().Format(time.RFC3339),
		"username": user.Username, "tenant_id": user.TenantID,
		"permissions": perms, "is_super": isSuper,
	})
}

// changePasswordRequest PUT /api/auth/password 请求体
type changePasswordRequest struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=8"`
}

// handleChangePassword 改密：验证旧口令后轮换；旧 token 因摘要绑定立即失效
func (s *AdminServer) handleChangePassword(c *gin.Context) {
	claims, _ := c.Get(claimsContextKey)
	session, _ := claims.(*sessionClaims)
	if session == nil {
		Error(c, http.StatusUnauthorized, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req changePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, http.StatusBadRequest, http.StatusBadRequest, err.Error())
		return
	}
	user, err := s.storage.GetAdminUserByID(session.Sub)
	if err != nil || user == nil {
		Error(c, http.StatusUnauthorized, http.StatusUnauthorized, "account no longer exists")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.OldPassword)) != nil {
		Error(c, http.StatusBadRequest, http.StatusBadRequest, "old password incorrect")
		return
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		Error(c, http.StatusInternalServerError, http.StatusInternalServerError, "failed to hash password")
		return
	}
	user.PasswordHash = string(newHash)
	user.UpdatedAt = time.Now()
	if err := s.storage.SaveAdminUser(user); err != nil {
		Error(c, http.StatusInternalServerError, http.StatusInternalServerError, "failed to save password")
		return
	}
	OK(c, gin.H{"changed": true})
}

func ptrTime(t time.Time) *time.Time { return &t }

// EnsureBootstrapAdmin 首次启动（无任何账号）时创建初始管理员 admin：
// 密码取 bootstrapPassword（自动化场景）；为空则随机生成并以日志打印一次
func EnsureBootstrapAdmin(storage plugin.StoragePlugin, logger *zap.Logger, bootstrapPassword string) error {
	n, err := storage.CountAdminUsers()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	password := bootstrapPassword
	generated := false
	if password == "" {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return err
		}
		password = hex.EncodeToString(b)
		generated = true
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	now := time.Now()
	user := &plugin.AdminUser{
		ID:           uuid.NewString(),
		Username:     "admin",
		PasswordHash: string(hash),
		Status:       plugin.AdminUserStatusActive,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	// 挂载超管角色（不存在则创建），保证首启账号拥有全部权限
	superID := ""
	roles, rerr := storage.ListRoles()
	if rerr == nil {
		for _, r := range roles {
			if plugin.IsSuperRole(r) {
				superID = r.ID
				break
			}
		}
	}
	if superID == "" {
		role := &plugin.Role{Name: plugin.SuperRoleName, Permissions: plugin.AllPermissions}
		if serr := storage.SaveRole(role); serr == nil {
			superID = role.ID
		}
	}
	user.RoleID = superID
	if err := storage.SaveAdminUser(user); err != nil {
		return err
	}
	if generated {
		logger.Warn("已创建初始管理员账号 admin（随机密码，仅此一次显示，请立即登录并修改密码）",
			zap.String("password", password))
	} else {
		logger.Info("已使用配置的 bootstrap_password 创建初始管理员账号 admin")
	}
	return nil
}
