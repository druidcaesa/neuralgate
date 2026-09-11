import { createRouter, createWebHistory } from 'vue-router'
import { ElMessageBox } from 'element-plus'
import { getAdminToken, hasFeature, hasPerm } from './api/auth'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', name: 'dashboard', component: () => import('./views/Dashboard.vue'), meta: { title: '概览', perm: 'system:read' } },
    { path: '/login', name: 'login', component: () => import('./views/Login.vue'), meta: { title: '登录' } },
    { path: '/models', name: 'models', component: () => import('./views/ModelList.vue'), meta: { title: '模型配置' } },
    { path: '/api-keys', name: 'api-keys', component: () => import('./views/ApiKeyList.vue'), meta: { title: 'API Key' } },
    { path: '/audit-logs', name: 'audit-logs', component: () => import('./views/AuditLogList.vue'), meta: { title: '审计日志' } },
    { path: '/rate-limits', name: 'rate-limits', component: () => import('./views/RateLimitList.vue'), meta: { title: '限流配置' } },
    { path: '/tamper-alerts', name: 'tamper-alerts', component: () => import('./views/TamperAlertList.vue'), meta: { title: '防篡改告警', feature: 'tamper_proof' } },
    { path: '/privacy-rules', name: 'privacy-rules', component: () => import('./views/PrivacyRules.vue'), meta: { title: '隐私合规', feature: 'privacy' } },
    { path: '/security-events', name: 'security-events', component: () => import('./views/SecurityEvents.vue'), meta: { title: '安全事件', feature: 'privacy' } },
    { path: '/tenants', name: 'tenants', component: () => import('./views/Tenants.vue'), meta: { title: '租户管理', feature: 'rbac' } },
    { path: '/roles', name: 'roles', component: () => import('./views/Roles.vue'), meta: { title: '角色管理', feature: 'rbac' } },
    { path: '/users', name: 'users', component: () => import('./views/Users.vue'), meta: { title: '用户管理', feature: 'rbac' } },
    { path: '/operation-logs', name: 'operation-logs', component: () => import('./views/OperationLogs.vue'), meta: { title: '操作日志' } },
    { path: '/compliance-reports', name: 'compliance-reports', component: () => import('./views/ComplianceReports.vue'), meta: { title: '合规报表', feature: 'compliance' } },
    { path: '/mcp-servers', name: 'mcp-servers', component: () => import('./views/MCPServers.vue'), meta: { title: 'MCP 上游' } },
    { path: '/mcp-audit-logs', name: 'mcp-audit-logs', component: () => import('./views/MCPAuditLogs.vue'), meta: { title: 'MCP 审计', feature: 'mcp_audit' } },
    { path: '/system', name: 'system', component: () => import('./views/SystemInfo.vue'), meta: { title: '系统信息' } }
  ]
})

// 全局守卫：无会话回登录页；企业功能未授权则拦截并弹升级提示（点击锁定菜单/直接改 URL 均覆盖）；
// 缺所需权限码则回落到 /models 而非拦截
router.beforeEach((to) => {
  if (to.path !== '/login' && !getAdminToken()) {
    return { path: '/login' }
  }
  const feature = to.meta.feature as string | undefined
  if (feature && !hasFeature(feature)) {
    ElMessageBox.alert('该功能需要企业版授权，请升级后使用', '企业版功能', {
      confirmButtonText: '知道了'
    }).catch(() => {})
    return false
  }
  // 权限门控：缺权限码时回落而非中止导航——登录后跳 / 再中止会让人卡在登录页，形似「登录成功却进不去」；
  // 回落目标 /models 未声明 perm（全库仅 / 声明），故不会再触发本分支，无重定向环
  const perm = to.meta.perm as string | undefined
  if (perm && !hasPerm(perm)) {
    return { path: '/models' }
  }
  return true
})

export default router
