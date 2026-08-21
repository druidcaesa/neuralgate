<template>
  <el-card>
    <div class="toolbar">
      <el-button type="primary" @click="openCreate">新增限流规则</el-button>
    </div>
    <el-table :data="items" v-loading="loading">
      <el-table-column prop="TenantID" label="租户ID" min-width="120">
        <template #default="{ row }">{{ row.TenantID || '(全局)' }}</template>
      </el-table-column>
      <el-table-column prop="ModelName" label="模型" min-width="120">
        <template #default="{ row }">{{ row.ModelName || '(全部)' }}</template>
      </el-table-column>
      <el-table-column prop="RequestsPerSec" label="RPS" width="90" />
      <el-table-column prop="TokensPerMin" label="TPM" width="110" />
      <el-table-column prop="Strategy" label="策略" width="130" />
      <el-table-column label="启用" width="80">
        <template #default="{ row }">{{ row.Enabled ? '是' : '否' }}</template>
      </el-table-column>
      <el-table-column label="操作" width="160">
        <template #default="{ row }">
          <el-button size="small" @click="openEdit(row)">编辑</el-button>
          <el-button size="small" type="danger" @click="remove(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>
    <el-pagination
      v-model:current-page="page" v-model:page-size="size" :total="total"
      layout="total, prev, pager, next" @current-change="load"
      style="margin-top:12px; justify-content:flex-end" />

    <el-dialog v-model="dialog" :title="editing ? '编辑限流规则' : '新增限流规则'" width="520px">
      <el-form :model="form" label-width="120px">
        <el-form-item label="租户ID"><el-input v-model="form.tenant_id" placeholder="留空=全局" /></el-form-item>
        <el-form-item label="模型名称"><el-input v-model="form.model_name" placeholder="留空=全部" /></el-form-item>
        <el-form-item label="RPS" required><el-input-number v-model="form.requests_per_sec" :min="1" :max="100000" /></el-form-item>
        <el-form-item label="TPM" required><el-input-number v-model="form.tokens_per_min" :min="1" :max="1000000000" /></el-form-item>
        <el-form-item label="策略" required>
          <el-select v-model="form.strategy">
            <el-option label="token_bucket" value="token_bucket" />
            <el-option label="sliding_window" value="sliding_window" />
          </el-select>
        </el-form-item>
        <el-form-item label="启用"><el-switch v-model="form.enabled" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialog=false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
      </template>
    </el-dialog>
  </el-card>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import type { RateLimitItem, RateLimitRequest } from '../types'
import { listRateLimits, createRateLimit, updateRateLimit, deleteRateLimit } from '../api/rateLimit'

const items = ref<RateLimitItem[]>([])
const page = ref(1)
const size = ref(10)
const total = ref(0)
const loading = ref(false)
const saving = ref(false)
const dialog = ref(false)
const editing = ref<RateLimitItem | null>(null)

const form = reactive<RateLimitRequest>({ tenant_id: '', model_name: '', requests_per_sec: 10, tokens_per_min: 100000, strategy: 'token_bucket', enabled: true })

async function load() {
  loading.value = true
  try {
    const data = await listRateLimits(page.value, size.value)
    items.value = data.items
    total.value = data.total
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editing.value = null
  Object.assign(form, { tenant_id: '', model_name: '', requests_per_sec: 10, tokens_per_min: 100000, strategy: 'token_bucket', enabled: true })
  dialog.value = true
}

function openEdit(row: RateLimitItem) {
  editing.value = row
  Object.assign(form, { tenant_id: row.TenantID, model_name: row.ModelName, requests_per_sec: row.RequestsPerSec, tokens_per_min: row.TokensPerMin, strategy: row.Strategy, enabled: row.Enabled })
  dialog.value = true
}

async function save() {
  saving.value = true
  try {
    if (editing.value) {
      await updateRateLimit(editing.value.ID, form)
    } else {
      await createRateLimit(form)
    }
    ElMessage.success('保存成功')
    dialog.value = false
    await load()
  } finally {
    saving.value = false
  }
}

async function remove(row: RateLimitItem) {
  await ElMessageBox.confirm('确定删除该限流规则?', '提示', { type: 'warning' })
  await deleteRateLimit(row.ID)
  ElMessage.success('已删除')
  await load()
}

onMounted(load)
</script>

<style scoped>
.toolbar { margin-bottom: 12px; }
</style>
