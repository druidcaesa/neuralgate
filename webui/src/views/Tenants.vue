<template>
  <el-card>
    <div class="toolbar">
      <el-button type="primary" @click="openCreate">新增租户</el-button>
      <el-button @click="load">刷新</el-button>
    </div>

    <el-table :data="tenants" v-loading="loading" border>
      <el-table-column prop="name" label="名称" min-width="140" />
      <el-table-column prop="code" label="编码" min-width="120" />
      <el-table-column label="状态" width="100">
        <template #default="{ row }">
          <el-tag :type="row.status === 'active' ? 'success' : 'danger'">
            {{ row.status === 'active' ? '启用' : '禁用' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="创建时间" width="170">
        <template #default="{ row }">{{ formatTime(row.created_at) }}</template>
      </el-table-column>
      <el-table-column label="操作" width="150">
        <template #default="{ row }">
          <el-button size="small" @click="openEdit(row)">编辑</el-button>
          <el-popconfirm title="确认删除该租户？" @confirm="remove(row.id)">
            <template #reference><el-button size="small" type="danger">删除</el-button></template>
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

    <el-dialog v-model="dialogVisible" :title="editingId ? '编辑租户' : '新增租户'" width="480px">
      <el-form label-width="80px">
        <el-form-item label="名称">
          <el-input v-model="form.name" maxlength="64" placeholder="1-64 字符" />
        </el-form-item>
        <el-form-item label="编码">
          <el-input v-model="form.code" maxlength="32" placeholder="1-32 位字母数字" />
        </el-form-item>
        <el-form-item label="状态">
          <el-select v-model="form.status">
            <el-option label="启用" value="active" />
            <el-option label="禁用" value="disabled" />
          </el-select>
        </el-form-item>
        <el-form-item label="配置 JSON">
          <el-input v-model="configText" type="textarea" :rows="3" placeholder='{"key":"value"}' />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="submit">保存</el-button>
      </template>
    </el-dialog>
  </el-card>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import type { TenantItem } from '../types'
import { createTenant, deleteTenant, listTenants, updateTenant } from '../api/rbac'

const tenants = ref<TenantItem[]>([])
const total = ref(0)
const page = ref(1)
const size = ref(20)
const loading = ref(false)
const saving = ref(false)
const dialogVisible = ref(false)
const editingId = ref('')
const configText = ref('{}')

const form = reactive<TenantItem>({ name: '', code: '', status: 'active' })

function formatTime(s?: string): string {
  return s ? new Date(s).toLocaleString() : '-'
}

async function load() {
  loading.value = true
  try {
    const data = await listTenants({ page: page.value, size: size.value })
    tenants.value = data.items
    total.value = data.total
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editingId.value = ''
  Object.assign(form, { name: '', code: '', status: 'active' })
  configText.value = '{}'
  dialogVisible.value = true
}

function openEdit(row: TenantItem) {
  editingId.value = row.id ?? ''
  Object.assign(form, row)
  configText.value = JSON.stringify(row.config ?? {})
  dialogVisible.value = true
}

async function submit() {
  let config: Record<string, string> = {}
  try {
    config = JSON.parse(configText.value || '{}')
  } catch {
    ElMessage.warning('配置不是合法 JSON')
    return
  }
  saving.value = true
  try {
    const payload = { ...form, config }
    if (editingId.value) {
      await updateTenant(editingId.value, payload)
    } else {
      await createTenant(payload)
    }
    ElMessage.success('已保存')
    dialogVisible.value = false
    await load()
  } catch {
    // 错误提示由 client 拦截器统一弹出
  } finally {
    saving.value = false
  }
}

async function remove(id?: string) {
  if (!id) return
  await deleteTenant(id)
  await load()
}

onMounted(load)
</script>

<style scoped>
.toolbar { margin-bottom: 12px; display: flex; gap: 8px; }
</style>
