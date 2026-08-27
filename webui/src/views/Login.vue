<template>
  <div class="login-wrap">
    <el-card class="login-card">
      <div class="login-brand">
        <div class="login-logo">NeuralGate</div>
        <div class="login-sub">AI 网关 · 管理后台</div>
      </div>
      <el-form :model="form" label-position="top" @submit.prevent="submit">
        <el-form-item label="用户名">
          <el-input v-model="form.username" placeholder="用户名" size="large" autofocus>
            <template #prefix><el-icon><User /></el-icon></template>
          </el-input>
        </el-form-item>
        <el-form-item label="密码">
          <el-input
            v-model="form.password"
            type="password"
            placeholder="密码"
            size="large"
            show-password
            @keyup.enter="submit"
          >
            <template #prefix><el-icon><Lock /></el-icon></template>
          </el-input>
        </el-form-item>
        <el-button
          type="primary"
          class="login-btn"
          size="large"
          :loading="loading"
          native-type="submit"
        >登 录</el-button>
      </el-form>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { User, Lock } from '@element-plus/icons-vue'
import { login, setAdminSession } from '../api/auth'

const router = useRouter()
const form = reactive({ username: '', password: '' })
const loading = ref(false)

async function submit() {
  if (!form.username || !form.password) {
    ElMessage.warning('请输入用户名和密码')
    return
  }
  loading.value = true
  try {
    const result = await login(form.username, form.password)
    setAdminSession(
      result.token, result.username, result.permissions ?? [], result.is_super ?? false,
      result.tenant_id ?? '', result.edition ?? '', result.features ?? []
    )
    router.replace('/models')
  } catch {
    // 错误提示由 client 拦截器统一弹出(登录页 401 不跳转)
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
.login-wrap {
  height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: linear-gradient(135deg, #1e2333 0%, #3730a3 100%);
}
.login-card {
  width: 380px;
  border-radius: var(--ng-radius-lg);
  box-shadow: var(--ng-shadow-dialog);
}
.login-brand { text-align: center; margin-bottom: var(--ng-space-5); }
.login-logo { font-size: 24px; font-weight: bold; color: var(--ng-primary); }
.login-sub { font-size: 13px; color: var(--ng-text-secondary); margin-top: var(--ng-space-1); }
.login-btn { width: 100%; }
</style>
