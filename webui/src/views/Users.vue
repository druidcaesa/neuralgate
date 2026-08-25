<template>
  <el-card>
    <div class="toolbar">
      <el-button type="primary" @click="openCreate">新增用户</el-button>
      <el-button @click="load">刷新</el-button>
    </div>

    <el-table :data="users" v-loading="loading" border>
      <el-table-column prop="username" label="用户名" min-width="120" />
      <el-table-column label="租户" min-width="120">
        <template #default="{ row }">{{ tenantName(row.tenant_id) }}</template>
      </el-table-column>
      <el-table-column label="角色" min-width="140">
        <template #default="{ row }">{{ roleName(row.role_id) }}</template>
      </el-table-column>
      <el-table-column label="状态" width="100">
        <template #default="{ row }">
          <el-tag :type="row.status === 'active' ? 'success' : 'danger'">
            {{ row.status === 'active' ? '启用' : '禁用' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="150">
        <template #default="{ row }">
          <el-button size="small" @click="openEdit(row)">编辑</el-button>
          <el-popconfirm title="确认删除该用户？" @confirm="remove(row.id)">
            <template #reference><el-button size="small" type="danger">删除</el-button></template>
          </el-popconfirm>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="dialogVisible" :title="editingId ? '编辑用户' : '新增用户'" width="480px">
      <el-form label-width="90px">
        <el-form-item label="用户名">
          <el-input v-model="form.username" :disabled="!!editingId" maxlength="32" placeholder="3-32 位字母数字" />
        </el-form-item>
        <el-form-item :label="editingId ? '新密码' : '密码'">
          <el-input v-model="password" type="password" show-password
            :placeholder="editingId ? '不填则不修改' : '8-64 位'" />
        </el-form-item>
        <el-form-item label="租户">
          <el-select v-model="form.tenant_id" placeholder="全局（超管可设）" clearable filterable>
            <el-option label="全局" value="" />
            <el-option v-for="t in tenants" :key="t.id" :label="t.name" :value="t.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="角色">
          <el-select v-model="form.role_id" filterable>
            <el-option v-for="r in roles" :key="r.id" :label="`${r.name}(${r.tenant_id || '全局'})`" :value="r.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="状态">
          <el-select v-model="form.status">
            <el-option label="启用" value="active" />
            <el-option label="禁用" value="disabled" />
          </el-select>
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
import type { AdminUserItem, RoleItem, TenantItem } from '../types'
import { createAdminUser, deleteAdminUser, listAdminUsers, updateAdminUser } from '../api/rbac'
import { listRoles, listTenants } from '../api/rbac'

const users = ref<AdminUserItem[]>([])
const roles = ref<RoleItem[]>([])
const tenants = ref<TenantItem[]>([])
const loading = ref(false)
const saving = ref(false)
const dialogVisible = ref(false)
const editingId = ref('')
const password = ref('')

const form = reactive<AdminUserItem>({ username: '', tenant_id: '', role_id: '', status: 'active' })

function roleName(id: string): string {
  return roles.value.find((r) => r.id === id)?.name ?? (id || '-')
}
function tenantName(id: string): string {
  if (!id) return '全局'
  return tenants.value.find((t) => t.id === id)?.name ?? id
}

async function load() {
  loading.value = true
  try {
    users.value = await listAdminUsers()
    roles.value = await listRoles()
    const page = await listTenants({ page: 1, size: 100 })
    tenants.value = page.items
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editingId.value = ''
  Object.assign(form, { username: '', tenant_id: '', role_id: '', status: 'active' })
  password.value = ''
  dialogVisible.value = true
}

function openEdit(row: AdminUserItem) {
  editingId.value = row.id ?? ''
  Object.assign(form, row)
  password.value = ''
  dialogVisible.value = true
}

async function submit() {
  if (!editingId.value && (!form.username || !password.value)) {
    ElMessage.warning('请填写用户名与密码')
    return
  }
  if (!form.role_id) {
    ElMessage.warning('请选择角色')
    return
  }
  saving.value = true
  try {
    if (editingId.value) {
      await updateAdminUser(editingId.value,
        password.value ? { ...form, password: password.value } : { ...form })
    } else {
      await createAdminUser({ ...form, password: password.value })
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
  await deleteAdminUser(id)
  await load()
}

onMounted(load)
</script>

<style scoped>
.toolbar { margin-bottom: 12px; display: flex; gap: 8px; }
</style>
