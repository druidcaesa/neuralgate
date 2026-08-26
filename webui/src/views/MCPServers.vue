<template>
  <div>
    <el-form inline class="filter-bar">
      <el-button type="primary" @click="openCreate">新建上游</el-button>
    </el-form>

    <el-table :data="rows" v-loading="loading" border stripe>
      <el-table-column prop="name" label="名称" min-width="140" />
      <el-table-column prop="endpoint" label="端点" min-width="260" />
      <el-table-column label="状态" width="90">
        <template #default="{ row }">
          <el-tag :type="row.enabled ? 'success' : 'info'">{{ row.enabled ? '启用' : '停用' }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="updated_at" label="更新时间" width="180">
        <template #default="{ row }">{{ formatTime(row.updated_at) }}</template>
      </el-table-column>
      <el-table-column label="操作" width="160" fixed="right">
        <template #default="{ row }">
          <el-button link type="primary" @click="openEdit(row)">编辑</el-button>
          <el-popconfirm title="确认删除该上游？" @confirm="remove(row)">
            <template #reference><el-button link type="danger">删除</el-button></template>
          </el-popconfirm>
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

    <el-dialog v-model="dialogVisible" :title="editingId ? '编辑上游' : '新建上游'" width="560px">
      <el-form label-width="80px">
        <el-form-item label="名称" required>
          <el-input v-model="form.name" maxlength="128" placeholder="唯一名称" />
        </el-form-item>
        <el-form-item label="端点" required>
          <el-input v-model="form.endpoint" placeholder="http(s)://host/mcp (Streamable HTTP)" />
        </el-form-item>
        <el-form-item label="认证头">
          <el-input v-model="headersText" type="textarea" :rows="3" placeholder='JSON 对象，如 {"Authorization":"Bearer xxx"}' />
        </el-form-item>
        <el-form-item label="启用">
          <el-switch v-model="form.enabled" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import {
  createMCPServer,
  deleteMCPServer,
  listMCPServers,
  updateMCPServer,
  type MCPServerPayload
} from '../api/mcp'
import type { MCPServerItem } from '../types'

const rows = ref<MCPServerItem[]>([])
const total = ref(0)
const loading = ref(false)
const query = reactive({ page: 1, size: 20 })

// headers 以 JSON 文本编辑；解析失败在保存时报错
const dialogVisible = ref(false)
const saving = ref(false)
const editingId = ref('')
const form = reactive<MCPServerPayload>({ name: '', endpoint: '', enabled: true })
const headersText = ref('{}')

function formatTime(v: string): string {
  return v ? new Date(v).toLocaleString() : '-'
}

async function load(): Promise<void> {
  loading.value = true
  try {
    const data = await listMCPServers({ page: query.page, size: query.size })
    rows.value = data.items ?? []
    total.value = data.total
  } finally {
    loading.value = false
  }
}

function openCreate(): void {
  editingId.value = ''
  Object.assign(form, { name: '', endpoint: '', enabled: true })
  headersText.value = '{}'
  dialogVisible.value = true
}

function openEdit(row: MCPServerItem): void {
  editingId.value = row.id
  Object.assign(form, { name: row.name, endpoint: row.endpoint, enabled: row.enabled })
  headersText.value = JSON.stringify(row.headers ?? {})
  dialogVisible.value = true
}

function parseHeaders(): Record<string, string> {
  if (!headersText.value.trim()) return {}
  const parsed = JSON.parse(headersText.value) as Record<string, string>
  return parsed ?? {}
}

async function save(): Promise<void> {
  let headers: Record<string, string>
  try {
    headers = parseHeaders()
  } catch {
    ElMessage.error('认证头不是合法 JSON')
    return
  }
  saving.value = true
  try {
    if (editingId.value) {
      await updateMCPServer(editingId.value, { ...form, headers })
    } else {
      await createMCPServer({ ...form, headers })
    }
    ElMessage.success('已保存')
    dialogVisible.value = false
    await load()
  } finally {
    saving.value = false
  }
}

async function remove(row: MCPServerItem): Promise<void> {
  await deleteMCPServer(row.id)
  ElMessage.success('已删除')
  await load()
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
</style>
