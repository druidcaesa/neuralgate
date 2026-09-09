<template>
  <el-card>
    <el-form inline class="filter-bar">
      <el-form-item>
        <el-button type="primary" @click="load">刷新</el-button>
      </el-form-item>
    </el-form>

    <el-table :data="events" v-loading="loading" border>
      <el-table-column label="时间" width="180">
        <template #default="{ row }">{{ formatTime(row.created_at) }}</template>
      </el-table-column>
      <el-table-column prop="rule_name" label="命中规则" min-width="130" />
      <el-table-column label="租户" min-width="100">
        <template #default="{ row }">{{ tenantName(row.tenant_id) }}</template>
      </el-table-column>
      <el-table-column label="Key" min-width="130">
        <template #default="{ row }">{{ row.key_mask || '-' }}</template>
      </el-table-column>
      <el-table-column prop="model_name" label="模型" width="140" />
      <el-table-column prop="client_ip" label="客户端IP" width="140" />
      <el-table-column prop="snippet" label="请求片段" min-width="260" show-overflow-tooltip />
      <el-table-column prop="request_id" label="请求ID" min-width="200" show-overflow-tooltip />
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
import type { SecurityEventItem, TenantItem } from '../types'
import { listSecurityEvents } from '../api/privacy'
import { listTenants } from '../api/rbac'

const events = ref<SecurityEventItem[]>([])
const total = ref(0)
const page = ref(1)
const size = ref(20)
const loading = ref(false)

// 租户字典：安全事件行 tenant_id → 租户名展示(未关联为空)
const tenants = ref<TenantItem[]>([])
function tenantName(id: string): string {
  if (!id) return '(全局)'
  return tenants.value.find((t) => t.id === id)?.name ?? id
}
async function loadTenants() {
  try {
    const page = await listTenants({ page: 1, size: 100 })
    tenants.value = page.items
  } catch {
    tenants.value = []
  }
}

async function load() {
  loading.value = true
  try {
    const data = await listSecurityEvents({ page: page.value, size: size.value })
    events.value = data.items
    total.value = data.total
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  void load()
  void loadTenants()
})
</script>

<style scoped>
.filter-bar { margin-bottom: 12px; }
</style>
