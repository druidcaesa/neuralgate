<template>
  <el-card>
    <el-form inline class="filter-bar">
      <el-form-item label="处置状态">
        <el-select v-model="resolvedFilter" style="width: 140px" @change="search">
          <el-option label="全部" value="" />
          <el-option label="未处置" value="false" />
          <el-option label="已处置" value="true" />
        </el-select>
      </el-form-item>
      <el-form-item>
        <el-button type="primary" @click="load">刷新</el-button>
      </el-form-item>
    </el-form>

    <el-table :data="alerts" v-loading="loading" border>
      <el-table-column prop="audit_log_id" label="日志ID" min-width="220" />
      <el-table-column prop="reason" label="原因" min-width="180" />
      <el-table-column label="首次发现" min-width="170">
        <template #default="{ row }">{{ formatTime(row.first_seen_at) }}</template>
      </el-table-column>
      <el-table-column label="最近检查" min-width="170">
        <template #default="{ row }">{{ formatTime(row.last_checked_at) }}</template>
      </el-table-column>
      <el-table-column label="状态" width="100">
        <template #default="{ row }">
          <el-tag :type="row.resolved ? 'success' : 'danger'">{{ row.resolved ? '已处置' : '未处置' }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="140">
        <template #default="{ row }">
          <el-popconfirm
            v-if="!row.resolved"
            title="确认该告警已核实处置？"
            @confirm="resolve(row.id)"
          >
            <template #reference>
              <el-button size="small" type="warning">标记处置</el-button>
            </template>
          </el-popconfirm>
        </template>
      </el-table-column>
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
import type { TamperAlertItem } from '../types'
import { listTamperAlerts, resolveTamperAlert } from '../api/tamper'

const alerts = ref<TamperAlertItem[]>([])
const total = ref(0)
const page = ref(1)
const size = ref(20)
const loading = ref(false)
const resolvedFilter = ref('false')

function formatTime(s: string): string {
  return s ? new Date(s).toLocaleString() : '-'
}

async function load() {
  loading.value = true
  try {
    const data = await listTamperAlerts({ page: page.value, size: size.value, resolved: resolvedFilter.value || undefined })
    alerts.value = data.items
    total.value = data.total
  } finally {
    loading.value = false
  }
}

function search() {
  page.value = 1
  void load()
}

async function resolve(id: string) {
  await resolveTamperAlert(id, true)
  await load()
}

onMounted(load)
</script>

<style scoped>
.filter-bar { margin-bottom: 12px; }
</style>
