<template>
  <el-card>
    <div class="toolbar">
      <el-button type="primary" @click="openCreate">新增角色</el-button>
      <el-button @click="load">刷新</el-button>
      <span class="tip">内置超管角色不可修改或删除；权限变更须重新登录生效</span>
    </div>

    <el-table :data="roles" v-loading="loading" border>
      <el-table-column prop="name" label="名称" min-width="140" />
      <el-table-column label="租户" min-width="120">
        <template #default="{ row }">{{ row.tenant_id || '全局' }}</template>
      </el-table-column>
      <el-table-column label="权限数" width="90">
        <template #default="{ row }">{{ row.permissions.length }}</template>
      </el-table-column>
      <el-table-column label="权限明细" min-width="280">
        <template #default="{ row }">
          <el-tag v-for="p in row.permissions.slice(0, 6)" :key="p" size="small" class="perm-tag">{{ permLabel(p) }}</el-tag>
          <span v-if="row.permissions.length > 6">…</span>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="150">
        <template #default="{ row }">
          <el-button size="small" :disabled="row.name === '超级管理员' && !row.tenant_id" @click="openEdit(row)">编辑</el-button>
          <el-popconfirm title="确认删除该角色？" @confirm="remove(row.id)">
            <template #reference>
              <el-button size="small" type="danger" :disabled="row.name === '超级管理员' && !row.tenant_id">删除</el-button>
            </template>
          </el-popconfirm>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="dialogVisible" :title="editingId ? '编辑角色' : '新增角色'" width="560px">
      <el-form label-width="80px">
        <el-form-item label="名称">
          <el-input v-model="form.name" maxlength="64" />
        </el-form-item>
        <el-form-item label="租户">
          <el-select v-model="form.tenant_id" placeholder="全局（超管可设）" clearable filterable>
            <el-option label="全局" value="" />
            <el-option v-for="t in tenants" :key="t.id" :label="t.name" :value="t.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="权限">
          <el-checkbox-group v-model="form.permissions" class="perm-groups">
            <div v-for="g in permissionGroups" :key="g.module" class="perm-group">
              <div class="perm-module">
                <span class="perm-module-name">{{ g.module }}</span>
                <span class="perm-menus">{{ g.menus }}</span>
              </div>
              <div class="perm-items">
                <el-checkbox v-for="it in g.items" :key="it.code" :value="it.code">{{ it.label }}</el-checkbox>
              </div>
            </div>
          </el-checkbox-group>
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
import type { RoleItem, TenantItem } from '../types'
import { createRole, deleteRole, listRoles, updateRole } from '../api/rbac'
import { listTenants } from '../api/rbac'

// 权限按功能模块分组（对应菜单）：单一数据源，read/write/export 映射为中文；
// 勾选值仍是后端权限码（与 plugin.AllPermissions 对齐），仅显示层中文化。
interface PermItem { code: string; label: string }
interface PermGroup { module: string; menus: string; items: PermItem[] }
const permissionGroups: PermGroup[] = [
  { module: 'API Key', menus: 'API Key', items: [
    { code: 'api_key:read', label: '查看' }, { code: 'api_key:write', label: '管理' }
  ] },
  { module: '模型配置', menus: '模型配置', items: [
    { code: 'model:read', label: '查看' }, { code: 'model:write', label: '管理' }
  ] },
  { module: '限流配置', menus: '限流配置', items: [
    { code: 'rate_limit:read', label: '查看' }, { code: 'rate_limit:write', label: '管理' }
  ] },
  { module: '审计日志', menus: '审计日志、防篡改告警(查看)', items: [
    { code: 'audit:read', label: '查看' }, { code: 'audit:export', label: '导出' }
  ] },
  { module: '隐私合规', menus: '隐私合规、安全事件', items: [
    { code: 'privacy:read', label: '查看' }, { code: 'privacy:write', label: '管理' }
  ] },
  { module: '租户管理', menus: '租户管理', items: [
    { code: 'tenant:read', label: '查看' }, { code: 'tenant:write', label: '管理' }
  ] },
  { module: '角色与用户', menus: '角色管理、用户管理', items: [
    { code: 'rbac:read', label: '查看' }, { code: 'rbac:write', label: '管理' }
  ] },
  { module: '系统管理', menus: '系统信息、操作日志、合规报表、MCP', items: [
    { code: 'system:read', label: '查看' }, { code: 'system:write', label: '管理' }
  ] }
]

// permLabel 权限码 → “模块·动作” 中文（表格权限明细用；未知码回显原值）
function permLabel(code: string): string {
  for (const g of permissionGroups) {
    const it = g.items.find((i) => i.code === code)
    if (it) return `${g.module}·${it.label}`
  }
  return code
}

const roles = ref<RoleItem[]>([])
const tenants = ref<TenantItem[]>([])
const loading = ref(false)
const saving = ref(false)
const dialogVisible = ref(false)
const editingId = ref('')

const form = reactive<RoleItem>({ name: '', tenant_id: '', permissions: [] })

async function load() {
  loading.value = true
  try {
    roles.value = await listRoles()
    const page = await listTenants({ page: 1, size: 100 })
    tenants.value = page.items
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editingId.value = ''
  Object.assign(form, { name: '', tenant_id: '', permissions: [] })
  dialogVisible.value = true
}

function openEdit(row: RoleItem) {
  editingId.value = row.id ?? ''
  Object.assign(form, { name: row.name, tenant_id: row.tenant_id, permissions: [...row.permissions] })
  dialogVisible.value = true
}

async function submit() {
  if (!form.name || form.permissions.length === 0) {
    ElMessage.warning('请填写名称并勾选至少一项权限')
    return
  }
  saving.value = true
  try {
    if (editingId.value) {
      await updateRole(editingId.value, { ...form })
    } else {
      await createRole({ ...form })
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
  await deleteRole(id)
  await load()
}

onMounted(load)
</script>

<style scoped>
.toolbar { margin-bottom: 12px; display: flex; gap: 8px; align-items: center; }
.tip { color: #909399; font-size: 12px; margin-left: auto; }
.perm-tag { margin-right: 4px; margin-bottom: 4px; }
.perm-groups { display: block; width: 100%; }
.perm-group { display: flex; align-items: center; padding: 6px 0; border-bottom: 1px solid var(--ng-border, #ebeef5); }
.perm-group:last-child { border-bottom: none; }
.perm-module { width: 210px; flex: none; display: flex; flex-direction: column; }
.perm-module-name { font-weight: 500; }
.perm-menus { color: #909399; font-size: 12px; line-height: 1.4; }
.perm-items { display: flex; gap: 16px; flex-wrap: wrap; }
</style>
