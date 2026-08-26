<template>
  <router-view v-if="isLoginRoute" />
  <el-container v-else class="app-layout">
    <el-aside width="200px" class="app-aside">
      <div class="app-logo">NeuralGate</div>
      <el-menu router :default-active="$route.path">
        <el-menu-item index="/models"><el-icon><Cpu /></el-icon>模型配置</el-menu-item>
        <el-menu-item index="/api-keys"><el-icon><Key /></el-icon>API Key</el-menu-item>
        <el-menu-item index="/audit-logs"><el-icon><Document /></el-icon>审计日志</el-menu-item>
        <el-menu-item index="/rate-limits"><el-icon><Timer /></el-icon>限流配置</el-menu-item>
        <el-menu-item index="/tamper-alerts"><el-icon><Warning /></el-icon>防篡改告警</el-menu-item>
        <el-menu-item index="/privacy-rules"><el-icon><Lock /></el-icon>隐私合规</el-menu-item>
        <el-menu-item index="/security-events"><el-icon><Bell /></el-icon>安全事件</el-menu-item>
        <el-menu-item v-if="hasPerm('tenant:read')" index="/tenants"><el-icon><OfficeBuilding /></el-icon>租户管理</el-menu-item>
        <el-menu-item v-if="hasPerm('rbac:read')" index="/roles"><el-icon><Avatar /></el-icon>角色管理</el-menu-item>
        <el-menu-item v-if="hasPerm('rbac:read')" index="/users"><el-icon><UserFilled /></el-icon>用户管理</el-menu-item>
        <el-menu-item v-if="hasPerm('system:read')" index="/operation-logs"><el-icon><List /></el-icon>操作日志</el-menu-item>
        <el-menu-item v-if="hasPerm('system:read')" index="/compliance-reports"><el-icon><DataAnalysis /></el-icon>合规报表</el-menu-item>
        <el-menu-item index="/system"><el-icon><Setting /></el-icon>系统信息</el-menu-item>
      </el-menu>
    </el-aside>
    <el-container>
      <el-header class="app-header">
        <span class="app-title">{{ $route.meta.title }}</span>
        <div class="app-user-area">
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
import { computed, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import {
  Cpu, Key, Document, Timer, Setting, Warning, User, ArrowDown, Lock, Bell,
  OfficeBuilding, Avatar, UserFilled, List, DataAnalysis
} from '@element-plus/icons-vue'
import { changePassword, clearAdminSession, getAdminUsername, hasPerm } from './api/auth'

const route = useRoute()
const router = useRouter()
const isLoginRoute = computed(() => route.path === '/login')
const username = computed(() => getAdminUsername() || 'admin')

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

<style>
body { margin: 0; font-family: 'Helvetica Neue', Arial, sans-serif; }
.app-layout { height: 100vh; }
.app-aside { background: #1f2937; }
.app-logo { color: #fff; font-size: 18px; font-weight: bold; padding: 16px; text-align: center; }
.app-aside .el-menu { border-right: none; background: transparent; }
.app-aside .el-menu-item { color: #cbd5e1; }
.app-aside .el-menu-item.is-active { color: #fff; background: #374151; }
.app-header { background: #fff; border-bottom: 1px solid #e5e7eb; display: flex; align-items: center; }
.app-title { font-size: 16px; font-weight: 500; }
.app-main { background: #f3f4f6; }
.app-user-area { margin-left: auto; }
.app-user { display: inline-flex; align-items: center; gap: 4px; cursor: pointer; font-size: 14px; color: #374151; outline: none; }
</style>
