<template>
  <el-card>
    <el-form inline class="filter-bar">
      <el-form-item label="操作人">
        <el-input v-model="userIdFilter" placeholder="user_id" clearable style="width: 220px" @change="search" />
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
      <el-table-column label="方法" width="90">
        <template #default="{ row }">
          <el-tag size="small" :type="methodTagType(row.method)">{{ row.method }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="path" label="路径" min-width="220" show-overflow-tooltip />
      <el-table-column label="状态码" width="90">
        <template #default="{ row }">
          <span :style="{ color: row.status_code < 400 ? '#67c23a' : '#f56c6c' }">{{ row.status_code }}</span>
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
import type { OperationLogItem } from '../types'
import { listOperationLogs } from '../api/rbac'

const logs = ref<OperationLogItem[]>([])
const total = ref(0)
const page = ref(1)
const size = ref(20)
const loading = ref(false)
const userIdFilter = ref('')

function formatTime(s: string): string {
  return s ? new Date(s).toLocaleString() : '-'
}
function methodTagType(m: string): 'success' | 'warning' | 'danger' | 'info' {
  if (m === 'POST') return 'success'
  if (m === 'PUT' || m === 'PATCH') return 'warning'
  if (m === 'DELETE') return 'danger'
  return 'info'
}

function search() {
  page.value = 1
  void load()
}

async function load() {
  loading.value = true
  try {
    const data = await listOperationLogs({ page: page.value, size: size.value, user_id: userIdFilter.value || undefined })
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
</style>
