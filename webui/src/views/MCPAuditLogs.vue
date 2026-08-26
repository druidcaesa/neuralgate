<template>
  <div>
    <el-form inline class="filter-bar">
      <el-form-item label="工具">
        <el-input v-model="query.tool" placeholder="tool 名称" clearable style="width: 160px" />
      </el-form-item>
      <el-form-item label="状态">
        <el-select v-model="query.status" placeholder="全部" clearable style="width: 120px">
          <el-option label="成功" value="success" />
          <el-option label="失败" value="failed" />
        </el-select>
      </el-form-item>
      <el-form-item label="时间">
        <el-date-picker
          v-model="range"
          type="daterange"
          value-format="YYYY-MM-DD"
          start-placeholder="开始"
          end-placeholder="结束"
          style="width: 240px"
        />
      </el-form-item>
      <el-form-item>
        <el-button type="primary" @click="reload">查询</el-button>
      </el-form-item>
    </el-form>

    <el-table :data="rows" v-loading="loading" border stripe>
      <el-table-column prop="tool_name" label="工具" min-width="140" />
      <el-table-column label="状态" width="90">
        <template #default="{ row }">
          <el-tag :type="row.status === 'success' ? 'success' : 'danger'">
            {{ row.status === 'success' ? '成功' : '失败' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="caller_agent" label="调用方" min-width="130" show-overflow-tooltip />
      <el-table-column label="耗时" width="100">
        <template #default="{ row }">{{ row.duration_ms }} ms</template>
      </el-table-column>
      <el-table-column prop="client_ip" label="客户端 IP" width="140" />
      <el-table-column label="调用时间" width="180">
        <template #default="{ row }">{{ formatTime(row.created_at) }}</template>
      </el-table-column>
      <el-table-column label="操作" width="90" fixed="right">
        <template #default="{ row }">
          <el-button link type="primary" @click="openDetail(row)">详情</el-button>
        </template>
      </el-table-column>
    </el-table>
    <el-pagination
      class="pager"
      layout="total, prev, pager, next"
      :total="total"
      :page-size="query.size"
      :current-page="query.page"
      @current-change="(p: number) => { query.page = p; load() }"
    />

    <!-- 详情抽屉：参数/结果为 JSON 文本，格式化展示，超长滚动 -->
    <el-drawer v-model="detailVisible" title="工具调用详情" size="55%">
      <template v-if="detail">
        <el-descriptions :column="2" border size="small" class="detail-meta">
          <el-descriptions-item label="工具">{{ detail.tool_name }}</el-descriptions-item>
          <el-descriptions-item label="状态">
            <el-tag :type="detail.status === 'success' ? 'success' : 'danger'">
              {{ detail.status === 'success' ? '成功' : '失败' }}
            </el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="请求 ID">{{ detail.request_id }}</el-descriptions-item>
          <el-descriptions-item label="耗时">{{ detail.duration_ms }} ms</el-descriptions-item>
          <el-descriptions-item label="调用方">{{ detail.caller_agent }}</el-descriptions-item>
          <el-descriptions-item label="租户">{{ detail.tenant_id || '(global)' }}</el-descriptions-item>
        </el-descriptions>
        <p v-if="detail.error_message" class="error-line">错误：{{ detail.error_message }}</p>

        <h4>参数 (tool_arguments)</h4>
        <pre class="code-block">{{ pretty(detail.tool_arguments) }}</pre>

        <h4>结果 (tool_result)</h4>
        <pre class="code-block">{{ pretty(detail.tool_result) }}</pre>
      </template>
    </el-drawer>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { getMCPAuditLog, listMCPAuditLogs } from '../api/mcp'
import type { MCPAuditLogItem } from '../types'

const rows = ref<MCPAuditLogItem[]>([])
const total = ref(0)
const loading = ref(false)
const query = reactive({ page: 1, size: 20, tool: '', status: '' })
const range = ref<[string, string] | null>(null)

const detailVisible = ref(false)
const detail = ref<MCPAuditLogItem | null>(null)

function formatTime(v: string): string {
  return v ? new Date(v).toLocaleString() : '-'
}

// JSON 美化；非合法 JSON 原样展示
function pretty(text: string): string {
  if (!text) return '(空)'
  try {
    return JSON.stringify(JSON.parse(text), null, 2)
  } catch {
    return text
  }
}

async function load(): Promise<void> {
  loading.value = true
  try {
    const data = await listMCPAuditLogs({
      page: query.page,
      size: query.size,
      tool: query.tool || undefined,
      status: query.status || undefined,
      start: range.value?.[0] || undefined,
      end: range.value?.[1] || undefined
    })
    rows.value = data.items ?? []
    total.value = data.total
  } finally {
    loading.value = false
  }
}

function reload(): void {
  query.page = 1
  load()
}


async function openDetail(row: MCPAuditLogItem): Promise<void> {
  detail.value = await getMCPAuditLog(row.id)
  detailVisible.value = true
}

onMounted(load)
</script>

<style scoped>
.filter-bar {
  margin-bottom: 12px;
}
.pager {
  margin-top: 12px;
  justify-content: flex-end;
}
.code-block {
  background: var(--el-fill-color-light);
  border-radius: 6px;
  padding: 12px;
  max-height: 320px;
  overflow: auto;
  font-size: 12px;
  line-height: 1.5;
}
.error-line {
  color: var(--el-color-danger);
}
.detail-meta {
  margin-bottom: 16px;
}
</style>
