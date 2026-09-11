<template>
  <router-view v-if="isLoginRoute" />
  <el-container v-else class="app-layout">
    <el-aside :width="collapsed ? '64px' : '210px'" class="app-aside">
      <div class="app-logo">
        <span v-if="!collapsed">NeuralGate</span>
        <span v-else>NG</span>
      </div>
      <el-menu
        router
        :default-active="route.path"
        :collapse="collapsed"
        :collapse-transition="false"
        background-color="transparent"
      >
        <template v-for="group in menuGroups" :key="group.title">
          <el-menu-item-group v-if="visibleItems(group).length" :title="group.title">
            <el-menu-item
              v-for="item in visibleItems(group)"
              :key="item.index"
              :index="item.index"
              :class="{ 'menu-locked': isLocked(item) }"
            >
              <el-icon><component :is="item.icon" /></el-icon>
              <template #title>
                <span class="menu-title">{{ item.title }}</span>
                <el-tag v-if="isLocked(item)" size="small" type="warning" class="ent-tag">企业版</el-tag>
              </template>
            </el-menu-item>
          </el-menu-item-group>
        </template>
      </el-menu>
    </el-aside>

    <el-container>
      <el-header class="app-header">
        <el-icon class="app-collapse-btn" @click="toggleCollapse">
          <Expand v-if="collapsed" />
          <Fold v-else />
        </el-icon>
        <el-breadcrumb class="app-breadcrumb" separator="/">
          <el-breadcrumb-item>首页</el-breadcrumb-item>
          <el-breadcrumb-item>{{ route.meta.title }}</el-breadcrumb-item>
        </el-breadcrumb>
        <div class="app-header-right">
          <el-tooltip v-if="docsHref" content="接口调用说明">
            <a :href="docsHref" target="_blank" rel="noopener" class="app-docs-link">
              <el-icon><Document /></el-icon>
            </a>
          </el-tooltip>
          <el-tooltip :content="theme === 'dark' ? '切换到亮色' : '切换到暗色'">
            <el-icon class="app-theme-btn" @click="toggle">
              <Moon v-if="theme === 'dark'" />
              <Sunny v-else />
            </el-icon>
          </el-tooltip>
          <el-dropdown @command="onUserCommand">
            <span class="app-user">
              <el-icon><User /></el-icon>{{ username }}
              <el-icon><ArrowDown /></el-icon>
            </span>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="password">修改密码</el-dropdown-item>
                <el-dropdown-item command="logout" divided>退出登录</el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </el-header>
      <el-main class="app-main">
        <router-view />
      </el-main>
    </el-container>
  </el-container>

  <el-dialog v-model="pwdVisible" title="修改密码" width="400px">
    <el-form label-width="90px">
      <el-form-item label="当前密码">
        <el-input v-model="pwdForm.oldPassword" type="password" show-password />
      </el-form-item>
      <el-form-item label="新密码">
        <el-input v-model="pwdForm.newPassword" type="password" show-password placeholder="至少 8 位" />
      </el-form-item>
      <el-form-item label="确认新密码">
        <el-input v-model="pwdForm.confirm" type="password" show-password />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="pwdVisible = false">取消</el-button>
      <el-button type="primary" :loading="pwdLoading" @click="submitChangePassword">确认修改</el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch, type Component } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import {
  Cpu, Key, Document, Timer, Setting, Warning, User, ArrowDown, Lock, Bell,
  OfficeBuilding, Avatar, UserFilled, List, DataAnalysis, Connection, Tickets,
  Sunny, Moon, Fold, Expand, Odometer
} from '@element-plus/icons-vue'
import { changePassword, clearAdminSession, getAdminUsername, hasPerm, hasFeature } from './api/auth'
import { getGatewayMeta } from './api/system'
import { useTheme } from './composables/useTheme'

interface MenuItem { index: string; title: string; icon: Component; perm?: string; feature?: string }
interface MenuGroup { title: string; items: MenuItem[] }

const route = useRoute()
const router = useRouter()
const { theme, toggle } = useTheme()

const isLoginRoute = computed(() => route.path === '/login')
const username = computed(() => getAdminUsername() || 'admin')

// 接口说明入口地址(代理端口 /docs):后端下发 scheme+端口,host 取当前 location.hostname 拼;
// 未配置/拉取失败保持空 → 模板 v-if 隐藏入口(fail-closed)
const docsHref = ref('')
async function loadGatewayMeta() {
  try {
    const meta = await getGatewayMeta()
    if (meta.port > 0 && meta.scheme) {
      docsHref.value = `${meta.scheme}://${location.hostname}:${meta.port}${meta.docs_path || '/docs'}`
    }
  } catch {
    // 接口异常/未配置:保持隐藏(错误提示已由 client 拦截器统一处理)
  }
}
// 进入非登录路由时拉取(immediate 覆盖刷新直达已登录页;login → 布局切换时再拉一次)
watch(isLoginRoute, (login) => { if (!login) loadGatewayMeta() }, { immediate: true })

// 侧栏折叠态（记忆到 localStorage）
const collapsed = ref(localStorage.getItem('ng-sidebar-collapsed') === '1')
function toggleCollapse() {
  collapsed.value = !collapsed.value
  localStorage.setItem('ng-sidebar-collapsed', collapsed.value ? '1' : '0')
}

// 侧栏分组（perm 字段与原 v-if 权限守卫一一对应，逻辑不变）
const menuGroups: MenuGroup[] = [
  { title: '概览', items: [
    { index: '/', title: '概览', icon: Odometer }
  ] },
  { title: '配置', items: [
    { index: '/models', title: '模型配置', icon: Cpu },
    { index: '/api-keys', title: 'API Key', icon: Key },
    { index: '/rate-limits', title: '限流配置', icon: Timer }
  ] },
  { title: '安全合规', items: [
    { index: '/audit-logs', title: '审计日志', icon: Document },
    { index: '/tamper-alerts', title: '防篡改告警', icon: Warning, feature: 'tamper_proof' },
    { index: '/privacy-rules', title: '隐私合规', icon: Lock, feature: 'privacy' },
    { index: '/security-events', title: '安全事件', icon: Bell, feature: 'privacy' },
    { index: '/compliance-reports', title: '合规报表', icon: DataAnalysis, perm: 'system:read', feature: 'compliance' },
    { index: '/mcp-servers', title: 'MCP 上游', icon: Connection, perm: 'system:read' },
    { index: '/mcp-audit-logs', title: 'MCP 审计', icon: Tickets, perm: 'system:read', feature: 'mcp_audit' }
  ] },
  { title: '系统', items: [
    { index: '/system', title: '系统信息', icon: Setting },
    { index: '/operation-logs', title: '操作日志', icon: List, perm: 'system:read' }
  ] },
  { title: '权限', items: [
    { index: '/tenants', title: '租户管理', icon: OfficeBuilding, perm: 'tenant:read', feature: 'rbac' },
    { index: '/roles', title: '角色管理', icon: Avatar, perm: 'rbac:read', feature: 'rbac' },
    { index: '/users', title: '用户管理', icon: UserFilled, perm: 'rbac:read', feature: 'rbac' }
  ] }
]

// 仅展示有权限的项（无 perm 恒展示，与原逻辑一致）
function visibleItems(group: MenuGroup): MenuItem[] {
  return group.items.filter(i => !i.perm || hasPerm(i.perm))
}

// 锁定：声明了 feature 但当前授权不含（可见性仍由 perm 决定，与 visibleItems 无关）
function isLocked(item: MenuItem): boolean {
  return !!item.feature && !hasFeature(item.feature)
}

const pwdVisible = ref(false)
const pwdLoading = ref(false)
const pwdForm = reactive({ oldPassword: '', newPassword: '', confirm: '' })

function onUserCommand(command: string) {
  if (command === 'logout') {
    clearAdminSession()
    router.replace('/login')
    return
  }
  if (command === 'password') {
    pwdForm.oldPassword = ''
    pwdForm.newPassword = ''
    pwdForm.confirm = ''
    pwdVisible.value = true
  }
}

async function submitChangePassword() {
  if (!pwdForm.oldPassword || pwdForm.newPassword.length < 8) {
    ElMessage.warning('请填写完整，新密码至少 8 位')
    return
  }
  if (pwdForm.newPassword !== pwdForm.confirm) {
    ElMessage.warning('两次输入的新密码不一致')
    return
  }
  pwdLoading.value = true
  try {
    await changePassword(pwdForm.oldPassword, pwdForm.newPassword)
    ElMessage.success('密码已修改，请重新登录')
    pwdVisible.value = false
    clearAdminSession()
    router.replace('/login')
  } catch {
    // 错误提示由 client 拦截器统一弹出
  } finally {
    pwdLoading.value = false
  }
}
</script>

<style scoped>
.app-layout { height: 100vh; }
.app-aside {
  background: var(--ng-sidebar-bg);
  transition: width 0.2s ease;
  overflow-x: hidden;
}
.app-logo {
  color: #fff;
  font-size: 18px;
  font-weight: bold;
  padding: var(--ng-space-4);
  text-align: center;
  white-space: nowrap;
}
.app-aside :deep(.el-menu) { border-right: none; background: transparent; }
.app-aside :deep(.el-menu-item-group__title) {
  color: var(--ng-gray-500);
  font-size: 12px;
  padding-left: var(--ng-space-4);
}
.app-aside :deep(.el-menu-item) { color: var(--ng-sidebar-text); }
.app-aside :deep(.el-menu-item:hover) { background: var(--ng-sidebar-hover); }
.app-aside :deep(.el-menu-item.is-active) {
  color: var(--ng-sidebar-active-text);
  background: var(--ng-sidebar-hover);
  border-left: 3px solid var(--ng-primary);
}
.app-header {
  background: var(--ng-bg-card);
  border-bottom: 1px solid var(--ng-border);
  display: flex;
  align-items: center;
  gap: var(--ng-space-4);
}
.app-collapse-btn, .app-theme-btn, .app-docs-link { font-size: 18px; cursor: pointer; color: var(--ng-text-secondary); }
.app-docs-link { text-decoration: none; display: inline-flex; align-items: center; }
.app-breadcrumb { flex: none; }
.app-header-right { margin-left: auto; display: inline-flex; align-items: center; gap: var(--ng-space-4); }
.app-user {
  display: inline-flex; align-items: center; gap: 4px;
  cursor: pointer; font-size: 14px; color: var(--ng-text-primary); outline: none;
}
.app-main { background: var(--ng-bg-body); padding: var(--ng-space-5); }
.menu-title { vertical-align: middle; }
.ent-tag { margin-left: 6px; transform: scale(0.85); }
.app-aside :deep(.menu-locked) { opacity: 0.7; }
</style>
