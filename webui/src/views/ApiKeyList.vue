<template>
  <el-card>
    <div class="toolbar">
      <el-button type="primary" @click="openCreate">创建 Key</el-button>
    </div>
    <el-table :data="keys" v-loading="loading">
      <el-table-column prop="key_prefix" label="Key" min-width="140" />
      <el-table-column prop="name" label="名称" min-width="100" />
      <el-table-column label="状态" width="90">
        <template #default="{ row }">
          <el-tag :type="row.status === 'active' ? 'success' : 'danger'">{{ row.status }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="quota" label="额度" width="90" />
      <el-table-column prop="used_quota" label="已用" width="90" />
      <el-table-column prop="rate_limit" label="限流(rps)" width="100" />
      <el-table-column label="允许模型" min-width="140">
        <template #default="{ row }">{{ row.allowed_models?.length ? row.allowed_models.join(', ') : '全部' }}</template>
      </el-table-column>
      <el-table-column prop="expires_at" label="过期时间" width="170" />
      <el-table-column prop="created_at" label="创建时间" width="170" />
      <el-table-column label="操作" width="160">
        <template #default="{ row }">
          <el-button size="small" @click="toggleStatus(row)">{{ row.status === 'active' ? '禁用' : '启用' }}</el-button>
          <el-button size="small" type="danger" @click="remove(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>
    <el-pagination
      v-model:current-page="page" v-model:page-size="size"
      :total="total" layout="total, prev, pager, next" @current-change="load"
      style="margin-top:12px; justify-content:flex-end" />

    <!-- 创建表单 -->
    <el-dialog v-model="createDialog" title="创建 API Key" width="520px">
      <el-form :model="form" label-width="110px">
        <el-form-item label="名称" required><el-input v-model="form.name" /></el-form-item>
        <el-form-item label="额度(负=无限)"><el-input-number v-model="form.quota" :min="-1" /></el-form-item>
        <el-form-item label="限流(rps)"><el-input-number v-model="form.rate_limit" :min="1" :max="10000" /></el-form-item>
        <el-form-item label="允许模型">
          <el-select v-model="form.allowed_models" multiple filterable allow-create default-first-option placeholder="留空=全部">
          </el-select>
        </el-form-item>
        <el-form-item label="过期时间"><el-date-picker v-model="form.expires_at" type="datetime" placeholder="留空=永不过期" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createDialog=false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">创建</el-button>
      </template>
    </el-dialog>

    <!-- 明文 Key 仅一次展示 -->
    <el-dialog v-model="plainDialog" title="API Key 已创建" width="480px" :close-on-click-modal="false" @close="confirmClosePlain">
      <el-alert type="warning" title="密钥仅显示一次，关闭后无法再次查看，请立即复制保存" :closable="false" style="margin-bottom:12px" />
      <el-input :model-value="plainKey" readonly>
        <template #append>
          <el-button @click="copyKey">复制</el-button>
        </template>
      </el-input>
      <template #footer>
        <el-button type="primary" @click="closePlain">我已保存</el-button>
      </template>
    </el-dialog>
  </el-card>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import type { ApiKeyItem, ApiKeyCreateRequest } from '../types'
import { listApiKeys, createApiKey, updateApiKeyStatus, deleteApiKey } from '../api/apiKey'

const keys = ref<ApiKeyItem[]>([])
const page = ref(1)
const size = ref(10)
const total = ref(0)
const loading = ref(false)
const saving = ref(false)
const createDialog = ref(false)
const plainDialog = ref(false)
const plainKey = ref('')
let plainConfirmed = false

const form = reactive<ApiKeyCreateRequest>({ name: '', quota: -1, rate_limit: 10, allowed_models: [], expires_at: null })

async function load() {
  loading.value = true
  try {
    const data = await listApiKeys(page.value, size.value)
    keys.value = data.items
    total.value = data.total
  } finally {
    loading.value = false
  }
}

function openCreate() {
  Object.assign(form, { name: '', quota: -1, rate_limit: 10, allowed_models: [], expires_at: null })
  createDialog.value = true
}

async function save() {
  saving.value = true
  try {
    const res = await createApiKey({ ...form, expires_at: form.expires_at ? (form.expires_at as unknown as Date).toISOString() : null })
    plainKey.value = res.key
    plainConfirmed = false
    createDialog.value = false
    plainDialog.value = true
    await load()
  } finally {
    saving.value = false
  }
}

async function toggleStatus(row: ApiKeyItem) {
  const next = row.status === 'active' ? 'disabled' : 'active'
  await updateApiKeyStatus(row.id, next)
  ElMessage.success(next === 'active' ? '已启用' : '已禁用')
  await load()
}

async function remove(row: ApiKeyItem) {
  await ElMessageBox.confirm(`确定删除 Key「${row.name}」?删除后该 Key 立即失效。`, '提示', { type: 'warning' })
  await deleteApiKey(row.id)
  ElMessage.success('已删除')
  await load()
}

async function copyKey() {
  await navigator.clipboard.writeText(plainKey.value)
  ElMessage.success('已复制')
}

async function confirmClosePlain() {
  if (!plainConfirmed && plainKey.value) {
    await ElMessageBox.confirm('密钥未保存，关闭后将无法再次查看。确定关闭?', '警告', { type: 'warning' })
  }
  plainConfirmed = true
}

function closePlain() {
  plainConfirmed = true
  plainDialog.value = false
}

onMounted(load)
</script>

<style scoped>
.toolbar { margin-bottom: 12px; }
</style>
