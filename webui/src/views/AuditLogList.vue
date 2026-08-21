<template>
  <el-card>
    <el-form inline class="filter-bar">
      <el-form-item label="模型">
        <el-input v-model="filters.model_name" placeholder="模型名称" clearable style="width:140px" />
      </el-form-item>
      <el-form-item label="状态">
        <el-input-number v-model="filters.response_status" :min="100" :max="599" placeholder="200" style="width:110px" />
      </el-form-item>
      <el-form-item label="流式">
        <el-select v-model="filters.is_stream" clearable style="width:110px">
          <el-option label="是" value="true" /><el-option label="否" value="false" />
        </el-select>
      </el-form-item>
      <el-form-item label="关键词"><el-input v-model="filters.keyword" clearable style="width:160px" /></el-form-item>
      <el-form-item label="时间">
        <el-date-picker v-model="timeRange" type="datetimerange" start-placeholder="开始" end-placeholder="结束" />
      </el-form-item>
      <el-form-item>
        <el-button type="primary" @click="search">查询</el-button>
        <el-button @click="reset">重置</el-button>
        <el-button @click="exportLogs('json')">导出 JSON</el-button>
        <el-button @click="exportLogs('csv')">导出 CSV</el-button>
      </el-form-item>
    </el-form>
    <el-table :data="logs" v-loading="loading" @row-click="openDetail">
      <el-table-column prop="CreatedAt" label="时间" width="180" />
      <el-table-column prop="ModelName" label="模型" min-width="110" />
      <el-table-column label="状态" width="90">
        <template #default="{ row }">
          <el-tag :type="row.ResponseStatus < 400 ? 'success' : 'danger'">{{ row.ResponseStatus }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="TotalTokens" label="Tokens" width="90" />
      <el-table-column prop="Duration" label="耗时(ms)" width="100" />
      <el-table-column label="流式" width="70">
        <template #default="{ row }">{{ row.IsStream ? '是' : '否' }}</template>
      </el-table-column>
      <el-table-column prop="RequestID" label="请求ID" min-width="200" show-overflow-tooltip />
    </el-table>
    <el-pagination
      v-model:current-page="page" v-model:page-size="size" :total="total"
      layout="total, prev, pager, next" @current-change="load"
      style="margin-top:12px; justify-content:flex-end" />

    <!-- 详情抽屉 -->
    <el-drawer v-model="detailVisible" title="审计日志详情" size="55%">
      <template v-if="detail">
        <el-descriptions :column="2" border size="small">
          <el-descriptions-item label="请求ID">{{ detail.request_id }}</el-descriptions-item>
          <el-descriptions-item label="模型">{{ detail.model_name }}</el-descriptions-item>
          <el-descriptions-item label="状态">{{ detail.response_status }}</el-descriptions-item>
          <el-descriptions-item label="Tokens">{{ detail.total_tokens }}</el-descriptions-item>
          <el-descriptions-item label="耗时(ms)">{{ detail.duration_ms }}</el-descriptions-item>
          <el-descriptions-item label="流式">{{ detail.is_stream ? '是' : '否' }}</el-descriptions-item>
          <el-descriptions-item label="断连">{{ detail.disconnected ? detail.disconnect_reason : '否' }}</el-descriptions-item>
          <el-descriptions-item label="时间">{{ detail.created_at }}</el-descriptions-item>
        </el-descriptions>
        <h4>请求体</h4>
        <pre class="json-block">{{ pretty(detail.request_body) }}</pre>
        <h4>响应体</h4>
        <pre class="json-block">{{ pretty(detail.response_body) }}</pre>
        <template v-if="detail.is_stream && detail.sse_chunks?.length">
          <h4>SSE 分片({{ detail.sse_chunks.length }})</h4>
          <el-table :data="detail.sse_chunks" size="small" max-height="300">
            <el-table-column prop="Index" label="#" width="60" />
            <el-table-column prop="Data" label="数据" show-overflow-tooltip />
          </el-table>
          <h4>重组文本</h4>
          <pre class="json-block">{{ detail.reassembled || '(无法重组)' }}</pre>
        </template>
      </template>
    </el-drawer>
  </el-card>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import type { AuditDetail, AuditLogItem, AuditQueryParams } from '../types'
import { listAuditLogs, getAuditDetail, auditExportURL } from '../api/audit'

const logs = ref<AuditLogItem[]>([])
const page = ref(1)
const size = ref(10)
const total = ref(0)
const loading = ref(false)
const detailVisible = ref(false)
const detail = ref<AuditDetail | null>(null)
const timeRange = ref<[Date, Date] | null>(null)

const filters = reactive<AuditQueryParams>({ page: 1, size: 10, model_name: '', response_status: undefined, is_stream: undefined, keyword: '' })

async function load() {
  loading.value = true
  try {
    const data = await listAuditLogs({ ...filters, page: page.value, size: size.value })
    logs.value = data.items
    total.value = data.total
  } finally {
    loading.value = false
  }
}

function search() {
  page.value = 1
  if (timeRange.value) {
    filters.start_time = timeRange.value[0].toISOString()
    filters.end_time = timeRange.value[1].toISOString()
  } else {
    filters.start_time = undefined
    filters.end_time = undefined
  }
  void load()
}

function reset() {
  Object.assign(filters, { model_name: '', response_status: undefined, is_stream: undefined, keyword: '', start_time: undefined, end_time: undefined })
  timeRange.value = null
  page.value = 1
  void load()
}

async function openDetail(row: AuditLogItem) {
  detail.value = await getAuditDetail(row.RequestID)
  detailVisible.value = true
}

function exportLogs(format: string) {
  window.open(auditExportURL(format, { keyword: filters.keyword }), '_blank')
}

function pretty(s: string): string {
  if (!s) return ''
  try {
    return JSON.stringify(JSON.parse(s), null, 2)
  } catch {
    return s
  }
}

onMounted(load)
</script>

<style scoped>
.filter-bar { margin-bottom: 12px; }
.json-block { background: #f9fafb; padding: 10px; border-radius: 4px; max-height: 300px; overflow: auto; font-size: 12px; white-space: pre-wrap; word-break: break-all; }
</style>
