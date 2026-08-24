import { createRouter, createWebHistory } from 'vue-router'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/models' },
    { path: '/models', name: 'models', component: () => import('./views/ModelList.vue'), meta: { title: '模型配置' } },
    { path: '/api-keys', name: 'api-keys', component: () => import('./views/ApiKeyList.vue'), meta: { title: 'API Key' } },
    { path: '/audit-logs', name: 'audit-logs', component: () => import('./views/AuditLogList.vue'), meta: { title: '审计日志' } },
    { path: '/rate-limits', name: 'rate-limits', component: () => import('./views/RateLimitList.vue'), meta: { title: '限流配置' } },
    { path: '/tamper-alerts', name: 'tamper-alerts', component: () => import('./views/TamperAlertList.vue'), meta: { title: '防篡改告警' } },
    { path: '/system', name: 'system', component: () => import('./views/SystemInfo.vue'), meta: { title: '系统信息' } }
  ]
})

export default router
