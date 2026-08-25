import { createRouter, createWebHistory } from 'vue-router'
import { getAdminToken } from './api/auth'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/models' },
    { path: '/login', name: 'login', component: () => import('./views/Login.vue'), meta: { title: '登录' } },
    { path: '/models', name: 'models', component: () => import('./views/ModelList.vue'), meta: { title: '模型配置' } },
    { path: '/api-keys', name: 'api-keys', component: () => import('./views/ApiKeyList.vue'), meta: { title: 'API Key' } },
    { path: '/audit-logs', name: 'audit-logs', component: () => import('./views/AuditLogList.vue'), meta: { title: '审计日志' } },
    { path: '/rate-limits', name: 'rate-limits', component: () => import('./views/RateLimitList.vue'), meta: { title: '限流配置' } },
    { path: '/tamper-alerts', name: 'tamper-alerts', component: () => import('./views/TamperAlertList.vue'), meta: { title: '防篡改告警' } },
    { path: '/privacy-rules', name: 'privacy-rules', component: () => import('./views/PrivacyRules.vue'), meta: { title: '隐私合规' } },
    { path: '/security-events', name: 'security-events', component: () => import('./views/SecurityEvents.vue'), meta: { title: '安全事件' } },
    { path: '/system', name: 'system', component: () => import('./views/SystemInfo.vue'), meta: { title: '系统信息' } }
  ]
})

// 全局守卫：无会话 token 一律回登录页（token 失效由 401 拦截兜底）
router.beforeEach((to) => {
  if (to.path !== '/login' && !getAdminToken()) {
    return { path: '/login' }
  }
  return true
})

export default router
