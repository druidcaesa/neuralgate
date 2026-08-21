<template>
  <el-card>
    <div class="toolbar">
      <el-button type="primary" @click="openCreate">新增模型</el-button>
    </div>
    <el-table :data="models" v-loading="loading" @expand-change="onExpand">
      <el-table-column type="expand">
        <template #default="{ row }">
          <el-table :data="row._upstreams || []" size="small" class="upstream-table">
            <el-table-column prop="base_url" label="上游地址" />
            <el-table-column prop="weight" label="权重" width="80" />
            <el-table-column label="启用" width="80">
              <template #default="{ row: u }">{{ u.enabled ? '是' : '否' }}</template>
            </el-table-column>
            <el-table-column label="操作" width="160">
              <template #default="{ row: u }">
                <el-button size="small" @click="openUpstreamEdit(row, u)">编辑</el-button>
                <el-button size="small" type="danger" @click="removeUpstream(row, u)">删除</el-button>
              </template>
            </el-table-column>
          </el-table>
          <el-button size="small" type="primary" plain style="margin-top:8px" @click="openUpstreamCreate(row)">+ 添加上游</el-button>
        </template>
      </el-table-column>
      <el-table-column prop="name" label="模型名称" min-width="120" />
      <el-table-column prop="provider" label="供应商" width="100" />
      <el-table-column prop="provider_model" label="上游模型" min-width="120" />
      <el-table-column prop="base_url" label="上游地址" min-width="180" show-overflow-tooltip />
      <el-table-column prop="timeout" label="超时(s)" width="90" />
      <el-table-column prop="weight" label="权重" width="70" />
      <el-table-column label="启用" width="80">
        <template #default="{ row }">
          <el-switch :model-value="row.enabled" @change="(v:boolean)=>toggleModel(row, v)" />
        </template>
      </el-table-column>
      <el-table-column label="操作" width="240">
        <template #default="{ row }">
          <el-button size="small" @click="openEdit(row)">编辑</el-button>
          <el-button size="small" @click="testModelConn(row)">测试</el-button>
          <el-button size="small" type="danger" @click="removeModel(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>
    <el-pagination
      v-model:current-page="page" v-model:page-size="size"
      :total="total" layout="total, prev, pager, next" @current-change="load"
      style="margin-top:12px; justify-content:flex-end" />

    <!-- 模型表单 -->
    <el-dialog v-model="modelDialog" :title="editing ? '编辑模型' : '新增模型'" width="560px">
      <el-form :model="modelForm" label-width="120px">
        <el-form-item label="名称" required><el-input v-model="modelForm.name" /></el-form-item>
        <el-form-item label="供应商" required>
          <el-select v-model="modelForm.provider" allow-create filterable placeholder="选择或输入自定义供应商" @change="onProviderChange">
            <el-option label="openai" value="openai" />
            <el-option label="deepseek" value="deepseek" />
            <el-option label="qwen（通义千问）" value="qwen" />
            <el-option label="zhipu" value="zhipu" />
          </el-select>
        </el-form-item>
        <el-form-item label="上游模型" required><el-input v-model="modelForm.provider_model" /></el-form-item>
        <el-form-item label="上游地址" required>
          <el-input v-model="modelForm.base_url" placeholder="https://api.openai.com" :disabled="isBuiltinProvider(modelForm.provider)" />
          <el-text v-if="isBuiltinProvider(modelForm.provider)" type="info" size="small">云服务商地址已锁定</el-text>
        </el-form-item>
        <el-form-item label="API Key" required><el-input v-model="modelForm.api_key" show-password /></el-form-item>
        <el-form-item label="超时(秒)"><el-input-number v-model="modelForm.timeout" :min="1" :max="300" /></el-form-item>
        <el-form-item label="重试次数"><el-input-number v-model="modelForm.max_retries" :min="0" :max="5" /></el-form-item>
        <el-form-item label="权重"><el-input-number v-model="modelForm.weight" :min="1" :max="100" /></el-form-item>
        <el-form-item label="启用"><el-switch v-model="modelForm.enabled" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="modelDialog=false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveModel">保存</el-button>
      </template>
    </el-dialog>

    <!-- 上游表单 -->
    <el-dialog v-model="upstreamDialog" :title="editingUpstream ? '编辑上游' : '添加上游'" width="480px">
      <el-form :model="upstreamForm" label-width="100px">
        <el-form-item label="地址" required><el-input v-model="upstreamForm.base_url" /></el-form-item>
        <el-form-item label="API Key" required><el-input v-model="upstreamForm.api_key" show-password /></el-form-item>
        <el-form-item label="权重"><el-input-number v-model="upstreamForm.weight" :min="1" :max="100" /></el-form-item>
        <el-form-item label="启用"><el-switch v-model="upstreamForm.enabled" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="upstreamDialog=false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveUpstream">保存</el-button>
      </template>
    </el-dialog>
  </el-card>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import type { ModelItem, ModelCreateRequest, UpstreamItem, UpstreamRequest } from '../types'
import { listModels, createModel, updateModel, deleteModel, testModel, listUpstreams, createUpstream, updateUpstream, deleteUpstream } from '../api/model'

const models = ref<ModelItem[]>([])
const page = ref(1)
const size = ref(10)
const total = ref(0)
const loading = ref(false)
const saving = ref(false)
const modelDialog = ref(false)
const upstreamDialog = ref(false)
const editing = ref<ModelItem | null>(null)
const editingUpstream = ref<UpstreamItem | null>(null)
const currentModelForUpstream = ref<ModelItem | null>(null)

const modelForm = reactive<ModelCreateRequest>({
  name: '', provider: 'openai', provider_model: '', base_url: '', api_key: '',
  timeout: 60, max_retries: 2, weight: 1, enabled: true
})

// 内置云服务商预设上游地址(适配 base_url + /v1/chat/completions 拼接)
const BUILTIN_BASE_URLS: Record<string, string> = {
  openai: 'https://api.openai.com',
  deepseek: 'https://api.deepseek.com',
  qwen: 'https://dashscope.aliyuncs.com/compatible-mode',
  zhipu: 'https://open.bigmodel.cn/api/paas/v4'
}

// 是否内置云服务商(内置则锁定 base_url)
function isBuiltinProvider(p: string): boolean {
  return p in BUILTIN_BASE_URLS
}

// 供应商变更:内置 → 自动填预设地址;自定义 → 清空地址让用户输入
function onProviderChange(p: string) {
  if (p in BUILTIN_BASE_URLS) {
    modelForm.base_url = BUILTIN_BASE_URLS[p]
  } else {
    modelForm.base_url = ''
  }
}
const upstreamForm = reactive<UpstreamRequest>({ base_url: '', api_key: '', weight: 1, enabled: true })

async function load() {
  loading.value = true
  try {
    const data = await listModels(page.value, size.value)
    models.value = data.items
    total.value = data.total
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editing.value = null
  Object.assign(modelForm, { name: '', provider: 'openai', provider_model: '', base_url: BUILTIN_BASE_URLS.openai, api_key: '', timeout: 60, max_retries: 2, weight: 1, enabled: true })
  modelDialog.value = true
}

function openEdit(row: ModelItem) {
  editing.value = row
  Object.assign(modelForm, { name: row.name, provider: row.provider, provider_model: row.provider_model, base_url: row.base_url, api_key: '', timeout: row.timeout, max_retries: row.max_retries, weight: row.weight, enabled: row.enabled })
  modelDialog.value = true
}

async function saveModel() {
  saving.value = true
  try {
    if (editing.value) {
      await updateModel(editing.value.id, modelForm)
    } else {
      await createModel(modelForm)
    }
    ElMessage.success('保存成功')
    modelDialog.value = false
    await load()
  } finally {
    saving.value = false
  }
}

async function removeModel(row: ModelItem) {
  await ElMessageBox.confirm(`确定删除模型 ${row.name}?`, '提示', { type: 'warning' })
  await deleteModel(row.id)
  ElMessage.success('已删除')
  await load()
}

async function toggleModel(row: ModelItem, enabled: boolean) {
  await updateModel(row.id, { ...row, api_key: '', enabled })
  ElMessage.success(enabled ? '已启用' : '已禁用')
  await load()
}

async function testModelConn(row: ModelItem) {
  const res = await testModel(row.id)
  if (res.ok) {
    ElMessage.success(`连接正常，延迟 ${res.latency_ms}ms`)
  } else {
    ElMessage.error(`连接失败: ${res.error || '未知错误'}`)
  }
}

async function onExpand(row: ModelItem, expandedRows: ModelItem[]) {
  if (expandedRows.includes(row)) {
    row._upstreams = await listUpstreams(row.id)
  }
}

function openUpstreamCreate(row: ModelItem) {
  currentModelForUpstream.value = row
  editingUpstream.value = null
  Object.assign(upstreamForm, { base_url: '', api_key: '', weight: 1, enabled: true })
  upstreamDialog.value = true
}

function openUpstreamEdit(row: ModelItem, up: UpstreamItem) {
  currentModelForUpstream.value = row
  editingUpstream.value = up
  Object.assign(upstreamForm, { base_url: up.base_url, api_key: '', weight: up.weight, enabled: up.enabled })
  upstreamDialog.value = true
}

async function saveUpstream() {
  saving.value = true
  try {
    const model = currentModelForUpstream.value!
    if (editingUpstream.value) {
      await updateUpstream(editingUpstream.value.id, upstreamForm)
    } else {
      await createUpstream(model.id, upstreamForm)
    }
    ElMessage.success('保存成功')
    upstreamDialog.value = false
    model._upstreams = await listUpstreams(model.id)
  } finally {
    saving.value = false
  }
}

async function removeUpstream(row: ModelItem, up: UpstreamItem) {
  await ElMessageBox.confirm('确定删除该上游?', '提示', { type: 'warning' })
  await deleteUpstream(up.id)
  ElMessage.success('已删除')
  row._upstreams = await listUpstreams(row.id)
}

onMounted(load)
</script>

<style scoped>
.toolbar { margin-bottom: 12px; }
.upstream-table { margin: 8px 0; }
</style>
