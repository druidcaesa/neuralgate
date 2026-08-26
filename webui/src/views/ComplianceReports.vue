<template>
  <el-card>
    <el-form inline class="filter-bar">
      <el-form-item label="周期类型">
        <!-- 服务端暂无该过滤参数，为页内过滤 -->
        <el-select v-model="periodFilter" clearable placeholder="全部" style="width: 140px">
          <el-option label="日报" value="day" />
          <el-option label="周报" value="week" />
          <el-option label="月报" value="month" />
        </el-select>
      </el-form-item>
      <el-form-item>
        <el-button type="primary" @click="load">刷新</el-button>
        <el-button type="success" @click="openGenerate">立即生成</el-button>
      </el-form-item>
    </el-form>

    <el-table :data="filteredReports" v-loading="loading" border>
      <el-table-column label="周期类型" width="100">
        <template #default="{ row }">
          <el-tag size="small" :type="periodTagType(row.period_type)">{{ periodLabel(row.period_type) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="统计周期" min-width="200">
        <template #default="{ row }">{{ formatDate(row.period_start) }} ~ {{ formatDate(row.period_end) }}</template>
      </el-table-column>
      <el-table-column label="生成时间" width="180">
        <template #default="{ row }">{{ formatTime(row.generated_at) }}</template>
      </el-table-column>
      <el-table-column label="操作" width="220" fixed="right">
        <template #default="{ row }">
          <el-button size="small" :disabled="downloading === row.id + '-json'" @click="download(row.id, 'json')">下载 JSON</el-button>
          <el-button size="small" :disabled="downloading === row.id + '-csv'" @click="download(row.id, 'csv')">下载 CSV</el-button>
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
  </el-card>

  <el-dialog v-model="genVisible" title="立即生成报表" width="420px">
    <el-form label-width="90px">
      <el-form-item label="周期类型">
        <el-select v-model="genForm.periodType" style="width: 160px">
          <el-option label="日报" value="day" />
          <el-option label="周报" value="week" />
          <el-option label="月报" value="month" />
        </el-select>
      </el-form-item>
      <el-form-item label="周期起点">
        <el-date-picker
          v-model="genForm.start"
          type="date"
          value-format="YYYY-MM-DD"
          placeholder="留空取当前周期起点"
          clearable
          style="width: 160px"
        />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="genVisible = false">取消</el-button>
      <el-button type="primary" :loading="genLoading" @click="submitGenerate">生成</el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import type { ReportItem } from '../types'
import { downloadComplianceReport, generateComplianceReport, listComplianceReports } from '../api/compliance'

const reports = ref<ReportItem[]>([])
const total = ref(0)
const page = ref(1)
const size = ref(20)
const loading = ref(false)
const downloading = ref('')
const periodFilter = ref('')

// 服务端暂无该过滤参数，为页内过滤：仅对当前页已加载行做周期类型筛选
const filteredReports = computed(() =>
  periodFilter.value ? reports.value.filter((r) => r.period_type === periodFilter.value) : reports.value
)

const genVisible = ref(false)
const genLoading = ref(false)
const genForm = reactive<{ periodType: 'day' | 'week' | 'month'; start: string }>({
  periodType: 'day',
  start: ''
})

function periodLabel(t: string): string {
  if (t === 'day') return '日报'
  if (t === 'week') return '周报'
  if (t === 'month') return '月报'
  return t
}
function periodTagType(t: string): 'success' | 'warning' | '' {
  if (t === 'day') return 'success'
  if (t === 'week') return 'warning'
  return ''
}
function formatDate(s: string): string {
  return s ? new Date(s).toLocaleDateString() : '-'
}
function formatTime(s: string): string {
  return s ? new Date(s).toLocaleString() : '-'
}

async function load() {
  loading.value = true
  try {
    const data = await listComplianceReports({ page: page.value, size: size.value })
    reports.value = data.items ?? []
    total.value = data.total
  } finally {
    loading.value = false
  }
}

async function download(id: string, format: 'json' | 'csv') {
  downloading.value = id + '-' + format
  try {
    await downloadComplianceReport(id, format)
  } catch {
    // 错误提示由 client 拦截器统一弹出
  } finally {
    downloading.value = ''
  }
}

function openGenerate() {
  genForm.periodType = 'day'
  genForm.start = ''
  genVisible.value = true
}

async function submitGenerate() {
  genLoading.value = true
  try {
    await generateComplianceReport({
      period_type: genForm.periodType,
      start: genForm.start || undefined
    })
    ElMessage.success('报表已生成')
    genVisible.value = false
    page.value = 1
    await load()
  } catch {
    // 错误提示由 client 拦截器统一弹出（含 503 合规生成不可用）
  } finally {
    genLoading.value = false
  }
}

onMounted(load)
</script>

<style scoped>
.filter-bar { margin-bottom: 12px; }
</style>
