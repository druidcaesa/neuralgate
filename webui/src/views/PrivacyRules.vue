<template>
  <el-card>
    <el-tabs v-model="activeTab" @tab-change="load">
      <el-tab-pane label="脱敏规则" name="pii" />
      <el-tab-pane label="注入检测" name="injection" />
      <el-tab-pane label="输出风控" name="output" />
      <el-tab-pane label="白名单" name="whitelist" />
    </el-tabs>

    <div class="toolbar">
      <el-button type="primary" @click="openCreate">{{ activeTab === 'whitelist' ? '新增白名单' : '新增规则' }}</el-button>
      <el-button @click="load">刷新</el-button>
    </div>

    <!-- 规则表(pii / injection) -->
    <el-table v-if="activeTab !== 'whitelist'" :data="rules" v-loading="loading" border>
      <el-table-column prop="name" label="名称" min-width="140" />
      <el-table-column prop="pattern" label="正则" min-width="220" show-overflow-tooltip />
      <el-table-column prop="replacement" label="替换为" min-width="150" show-overflow-tooltip />
      <el-table-column prop="scope" label="作用域" width="100" />
      <el-table-column label="启用" width="90">
        <template #default="{ row }">
          <el-switch :model-value="row.enabled" @change="(v: string | number | boolean) => toggleRule(row, Boolean(v))" />
        </template>
      </el-table-column>
      <el-table-column label="操作" width="140">
        <template #default="{ row }">
          <el-button size="small" @click="openEdit(row)">编辑</el-button>
          <el-popconfirm title="确认删除该规则？" @confirm="removeRule(row.id)">
            <template #reference><el-button size="small" type="danger">删除</el-button></template>
          </el-popconfirm>
        </template>
      </el-table-column>
    </el-table>

    <!-- 白名单表 -->
    <el-table v-else :data="whitelist" v-loading="loading" border>
      <el-table-column prop="pattern" label="命中正则(整体跳过隐私检查)" min-width="280" show-overflow-tooltip />
      <el-table-column prop="note" label="备注" min-width="160" show-overflow-tooltip />
      <el-table-column prop="created_at" label="创建时间" width="170" />
      <el-table-column label="操作" width="90">
        <template #default="{ row }">
          <el-popconfirm title="确认删除该白名单条目？" @confirm="removeWhitelist(row.id)">
            <template #reference><el-button size="small" type="danger">删除</el-button></template>
          </el-popconfirm>
        </template>
      </el-table-column>
    </el-table>
    <p v-if="activeTab !== 'whitelist'" class="tip">规则变更 ≤30s 生效（引擎周期重载）</p>

    <!-- 编辑弹窗 -->
    <el-dialog v-model="dialogVisible" :title="editingId ? '编辑' : '新增'" width="520px">
      <el-form v-if="activeTab !== 'whitelist'" label-width="80px">
        <el-form-item label="名称">
          <el-input v-model="form.name" maxlength="64" placeholder="1-64 字符" />
        </el-form-item>
        <el-form-item label="类型">
          <el-select v-model="form.rule_type" :disabled="!!editingId">
            <el-option label="PII 脱敏" value="pii" />
            <el-option label="注入检测" value="injection" />
            <el-option label="输出风控" value="output" />
          </el-select>
        </el-form-item>
        <el-form-item label="正则">
          <el-input v-model="form.pattern" placeholder="合法正则表达式" />
        </el-form-item>
        <el-form-item label="替换为">
          <el-input v-model="form.replacement" :disabled="form.rule_type === 'injection'" placeholder="字面量,不解析 $1;注入检测忽略" />
        </el-form-item>
        <el-form-item label="作用域">
          <el-select v-model="form.scope" :disabled="form.rule_type === 'injection'">
            <el-option label="请求侧" value="request" />
            <el-option label="响应侧" value="response" />
            <el-option label="双向" value="both" />
          </el-select>
        </el-form-item>
      </el-form>
      <el-form v-else label-width="80px">
        <el-form-item label="正则">
          <el-input v-model="whitelistForm.pattern" placeholder="请求内容命中即跳过脱敏与注入检测" />
        </el-form-item>
        <el-form-item label="备注">
          <el-input v-model="whitelistForm.note" maxlength="255" />
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
import type { PrivacyRuleItem, PrivacyWhitelistItem } from '../types'
import {
  createPrivacyRule, createPrivacyWhitelistEntry, deletePrivacyRule, deletePrivacyWhitelistEntry,
  listPrivacyRules, listPrivacyWhitelist, updatePrivacyRule
} from '../api/privacy'

const activeTab = ref<'pii' | 'injection' | 'output' | 'whitelist'>('pii')
const rules = ref<PrivacyRuleItem[]>([])
const whitelist = ref<PrivacyWhitelistItem[]>([])
const loading = ref(false)
const saving = ref(false)
const dialogVisible = ref(false)
const editingId = ref('')

const form = reactive<PrivacyRuleItem>({ rule_type: 'pii', name: '', pattern: '', replacement: '', scope: 'both', enabled: true })
const whitelistForm = reactive<PrivacyWhitelistItem>({ pattern: '', note: '', enabled: true })

async function load() {
  if (activeTab.value === 'whitelist') {
    loading.value = true
    try {
      whitelist.value = await listPrivacyWhitelist()
    } finally {
      loading.value = false
    }
    return
  }
  loading.value = true
  try {
    const all = await listPrivacyRules()
    rules.value = all.filter((r) => r.rule_type === activeTab.value)
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editingId.value = ''
  if (activeTab.value === 'whitelist') {
    Object.assign(whitelistForm, { pattern: '', note: '' })
  } else {
    Object.assign(form, {
      rule_type: activeTab.value as 'pii' | 'injection',
      name: '', pattern: '',
      replacement: activeTab.value === 'injection' ? '' : '',
      scope: activeTab.value === 'injection' ? 'request' : 'both', enabled: true
    })
  }
  dialogVisible.value = true
}

function openEdit(row: PrivacyRuleItem) {
  editingId.value = row.id ?? ''
  Object.assign(form, row)
  if (row.rule_type === 'injection') form.scope = 'request'
  dialogVisible.value = true
}

async function submit() {
  saving.value = true
  try {
    if (activeTab.value === 'whitelist') {
      if (!whitelistForm.pattern) {
        ElMessage.warning('请填写正则')
        return
      }
      await createPrivacyWhitelistEntry({ ...whitelistForm })
    } else {
      if (!form.name || !form.pattern) {
        ElMessage.warning('请填写名称与正则')
        return
      }
      if (form.rule_type === 'injection') {
        form.replacement = ''
        form.scope = 'request'
      }
      if (editingId.value) {
        await updatePrivacyRule(editingId.value, { ...form })
      } else {
        await createPrivacyRule({ ...form })
      }
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

async function toggleRule(row: PrivacyRuleItem, enabled: boolean) {
  await updatePrivacyRule(row.id ?? '', { ...row, enabled })
  await load()
}

async function removeRule(id?: string) {
  if (!id) return
  await deletePrivacyRule(id)
  await load()
}

async function removeWhitelist(id?: string) {
  if (!id) return
  await deletePrivacyWhitelistEntry(id)
  await load()
}

onMounted(load)
</script>

<style scoped>
.toolbar { margin-bottom: 12px; display: flex; gap: 8px; }
.tip { color: #909399; font-size: 12px; margin-top: 10px; }
</style>
