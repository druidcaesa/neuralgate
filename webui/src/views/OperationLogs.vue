<template>
  <el-card>
    <el-form inline class="filter-bar">
      <el-form-item label="功能模块">
        <el-select v-model="moduleFilter" clearable placeholder="全部" style="width: 150px" @change="search">
          <el-option v-for="m in moduleOptions" :key="m" :label="m" :value="m" />
        </el-select>
      </el-form-item>
      <el-form-item label="操作类型">
        <el-select v-model="actionFilter" clearable placeholder="全部" style="width: 150px" @change="search">
          <el-option v-for="a in actionOptions" :key="a" :label="a" :value="a" />
        </el-select>
      </el-form-item>
      <el-form-item label="操作人">
        <el-input v-model="userIdFilter" placeholder="user_id" clearable style="width: 180px" @change="search" />
      </el-form-item>
      <el-form-item>
        <el-button type="primary" @click="load">刷新</el-button>
      </el-form-item>
    </el-form>

    <el-table :data="logs" v-loading="loading" border>
      <el-table-column label="时间" width="180">
        <template #default="{ row }">{{ formatTime(row.created_at) }}</template>
      </el-table-column>
      <el-table-column prop="username" label="操作人" width="120" />
      <el-table-column label="功能模块" width="140">
        <template #default="{ row }">
          <el-tag size="small" type="info" effect="plain">{{ row.module || '-' }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="操作类型" min-width="200">
        <template #default="{ row }">
          <div class="op-line">
            <el-tag size="small" :type="actionTagType(row.action)">{{ row.action || '-' }}</el-tag>
          </div>
          <div class="op-meta">{{ row.method }} · {{ row.path }} · <span :style="statusStyle(row.status_code)">{{ row.status_code }}</span></div>
        </template>
      </el-table-column>
      <el-table-column prop="client_ip" label="客户端IP" width="140" />
    </el-table>

    <el-pagination
      style="margin-top: 12px"
      layout="prev, pager, next, total"
      :total="total"
      :page-size="size"
      :current-page="page"
      @current-change="(p: number) => { page = p; void load() }"
    />
  </el-card>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { formatTime } from '../utils/time'
import type { OperationLogItem } from '../types'
import { listOperationLogs } from '../api/rbac'

const logs = ref<OperationLogItem[]>([])
const total = ref(0)
const page = ref(1)
const size = ref(20)
const loading = ref(false)
const moduleFilter = ref('')
const actionFilter = ref('')
const userIdFilter = ref('')

// 筛选项与后端 ClassifyOperation 入库文案一致
const moduleOptions = [
  'API Key', '模型配置', '限流配置', '隐私合规', 'MCP 服务', '合规报表',
  '篡改告警', '授权管理', '租户管理', '角色管理', '账号管理', '系统管理'
]
const actionOptions = [
  '创建', '批量创建', '编辑', '删除', '批量删除', '启用', '禁用',
  '连通测试', '添加上游', '编辑上游', '删除上游', '上传授权',
  '标记处置', '生成报表', '添加白名单', '删除白名单', '修改密码', '启停'
]

function actionTagType(a: string): 'success' | 'info' | 'warning' | 'danger' {
  if (['创建', '批量创建', '启用', '添加上游', '添加白名单', '上传授权', '生成报表'].includes(a)) return 'success'
  if (['删除', '批量删除', '删除上游', '删除白名单', '禁用'].includes(a)) return 'danger'
  if (['编辑', '编辑上游', '修改密码', '标记处置', '启停'].includes(a)) return 'warning'
  return 'info'
}

function statusStyle(code: number): { color: string } {
  return { color: code < 400 ? '#67c23a' : '#f56c6c' }
}

function search() {
  page.value = 1
  void load()
}

async function load() {
  loading.value = true
  try {
    const data = await listOperationLogs({
      page: page.value,
      size: size.value,
      user_id: userIdFilter.value || undefined,
      module: moduleFilter.value || undefined,
      action: actionFilter.value || undefined
    })
    logs.value = data.items
    total.value = data.total
  } finally {
    loading.value = false
  }
}

onMounted(load)
</script>

<style scoped>
.filter-bar { margin-bottom: 12px; }
.op-line { margin-bottom: 2px; }
.op-meta {
  font-size: 12px;
  color: var(--el-text-color-secondary);
  line-height: 1.4;
}
</style>
