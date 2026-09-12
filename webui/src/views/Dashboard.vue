<template>
  <div v-loading="loading" element-loading-text="加载中">
    <div class="toolbar">
      <el-radio-group v-model="activeWindow" size="small" @change="load">
        <el-radio-button v-for="w in windows" :key="w" :value="w">{{ w }}</el-radio-button>
      </el-radio-group>
    </div>

    <el-alert
      v-if="error"
      type="error"
      show-icon
      :closable="false"
      title="加载失败"
      class="stack"
    >
      {{ error }}
      <el-button link type="primary" class="retry" @click="load">重试</el-button>
    </el-alert>

    <el-alert
      v-if="data?.truncated"
      type="warning"
      show-icon
      :closable="false"
      title="统计已截断"
      description="数据量超过单次聚合上限，统计仅覆盖最近的部分记录；趋势最左侧与各分布、排行面板均可能偏低"
      class="stack"
    />

    <el-alert
      v-for="(a, i) in data?.alerts ?? []"
      :key="i"
      :title="a.title"
      :type="a.level === 'error' ? 'error' : 'warning'"
      show-icon
      :closable="false"
      class="stack"
    >
      {{ a.detail }}
      <router-link v-if="a.link && hasFeature('tamper_proof')" :to="a.link" class="alert-link">
        前往处理 →
      </router-link>
    </el-alert>

    <el-row :gutter="16">
      <el-col v-for="card in cards" :key="card.label" :xs="12" :sm="8" :lg="4">
        <el-card shadow="never" class="metric-card">
          <div class="metric-label">{{ card.label }}</div>
          <div class="metric-value">{{ card.value }}</div>
        </el-card>
      </el-col>
    </el-row>

    <el-card shadow="never" class="panel">
      <template #header><span>近期趋势</span></template>
      <div class="chart-wrap">
        <div ref="trendEl" class="chart chart-lg"></div>
        <div v-if="showEmpty" class="chart-empty">窗口内暂无请求</div>
      </div>
      <p class="note">
        平均延迟为全部请求耗时的算术平均，长连接流式请求会显著拉高该值，<strong>不是 SLA 指标</strong>；
        P50/P95/P99 同理。
      </p>
    </el-card>

    <el-row :gutter="16">
      <el-col :xs="24" :lg="12">
        <el-card shadow="never" class="panel">
          <template #header><span>延迟分位与分布</span></template>
          <div class="stat-row">
            <div v-for="q in percentiles" :key="q.label">
              <div class="metric-label">{{ q.label }}</div>
              <div class="stat-value">{{ q.value }}</div>
            </div>
          </div>
          <div class="chart-wrap">
            <div ref="latencyEl" class="chart"></div>
            <div v-if="showEmpty" class="chart-empty">窗口内暂无请求</div>
          </div>
        </el-card>
      </el-col>
      <el-col :xs="24" :lg="12">
        <el-card shadow="never" class="panel">
          <template #header><span>状态码分布与错误数</span></template>
          <div class="chart-wrap">
            <div ref="statusEl" class="chart"></div>
            <div v-if="showEmpty" class="chart-empty">窗口内暂无请求</div>
          </div>
          <p class="note">失败 {{ failedText }} 次 · 失败率 {{ failRateText }}</p>
        </el-card>
      </el-col>
    </el-row>

    <el-row :gutter="16">
      <el-col :xs="24" :lg="12">
        <el-card shadow="never" class="panel">
          <template #header><span>模型 TOP 8</span></template>
          <div class="chart-wrap">
            <div ref="modelsEl" class="chart"></div>
            <div v-if="showEmpty" class="chart-empty">窗口内暂无请求</div>
          </div>
        </el-card>
      </el-col>
      <el-col :xs="24" :lg="12">
        <el-card shadow="never" class="panel">
          <template #header><span>Token 构成与流式拆分</span></template>
          <div class="chart-wrap">
            <div ref="tokensEl" class="chart"></div>
            <div v-if="showEmpty" class="chart-empty">窗口内暂无请求</div>
          </div>
          <p class="note">流式 {{ streamText }}<br />非流式 {{ nonStreamText }}</p>
        </el-card>
      </el-col>
    </el-row>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import * as echarts from 'echarts/core'
import { BarChart, LineChart, PieChart } from 'echarts/charts'
import { GridComponent, LegendComponent, TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import type { ComposeOption } from 'echarts/core'
import type { BarSeriesOption, LineSeriesOption, PieSeriesOption } from 'echarts/charts'
import type { GridComponentOption, LegendComponentOption, TooltipComponentOption } from 'echarts/components'
import type { DashboardData, DashboardTokens, DashboardWindow } from '../types'
import { getDashboard } from '../api/dashboard'
import { hasFeature } from '../api/auth'
import { useChartTheme } from '../composables/useChartTheme'
import type { ChartPalette } from '../composables/useChartTheme'

// 按需注册：折线 + 条形 + 环形 + 网格/图例/提示框 + Canvas 渲染器，避免全量引入撑大产物
echarts.use([
  LineChart, BarChart, PieChart,
  GridComponent, LegendComponent, TooltipComponent, CanvasRenderer
])

// ComposeOption 是唯一能让 option 获得上下文类型的写法：
// EChartsCoreOption 过宽，formatter 参数会退化成隐式 any
type ChartOption = ComposeOption<
  | LineSeriesOption
  | BarSeriesOption
  | PieSeriesOption
  | GridComponentOption
  | LegendComponentOption
  | TooltipComponentOption
>

// tooltipTheme 提示框跟随主题：echarts 默认底色在暗色卡底上是一块亮斑，
// 与卡片脱节，故各图统一以卡片色作底、边框色描边
function tooltipTheme(p: ChartPalette) {
  return { backgroundColor: p.card, borderColor: p.border, textStyle: { color: p.textSecondary } }
}

// 状态码按语义取色，不参与 primary → success → … 的序列取色
const STATUS_COLORS: Record<string, keyof ChartPalette> = {
  '2xx': 'success',
  '3xx': 'info',
  '4xx': 'warning',
  '5xx': 'danger',
  other: 'textSecondary'
}

// STATUS_LABELS 环形图图例文案；other 如出现异常取值也如实标注
const STATUS_LABELS: Record<string, string> = {
  '2xx': '2xx 成功',
  '3xx': '3xx 跳转',
  '4xx': '4xx 客户端错误',
  '5xx': '5xx 服务端错误',
  other: '无响应/其他'
}

const windows: DashboardWindow[] = ['24h', '7d', '30d']
// 变量名不得用 window：会遮蔽全局 window
const activeWindow = ref<DashboardWindow>('7d')
const data = ref<DashboardData | null>(null)
const loading = ref(false)
// 非空即整页错误态
const error = ref('')
const trendEl = ref<HTMLElement | null>(null)
const latencyEl = ref<HTMLElement | null>(null)
const statusEl = ref<HTMLElement | null>(null)
const modelsEl = ref<HTMLElement | null>(null)
const tokensEl = ref<HTMLElement | null>(null)

// chartInstances 按面板键复用 echarts 实例，避免每次重绘重复 init
const chartInstances = new Map<string, ReturnType<typeof echarts.init>>()
// 观察图表容器自身尺寸，覆盖侧栏折叠等不触发 window resize 的布局变动
let ro: ResizeObserver | null = null
// 请求序号，只采纳最新一次 load 的响应，避免快速切换窗口时旧数据覆盖新数据
let seq = 0

// 主题切换后重绘，使坐标轴、分割线与图例取到新令牌
const { palette } = useChartTheme(() => renderAll())

// 空态：仅当确实加载成功且窗口内无请求时提示；
// 加载失败由页面级错误提示负责，不得显示「暂无请求」掩盖故障
const showEmpty = computed(() => !!data.value && data.value.summary.requests === 0)

// 无采样时延迟无定义，「0 ms」会被读成瞬时，故显示占位符
const avgLatencyText = computed(() => {
  const s = data.value?.summary
  if (!s || s.requests === 0) return '-'
  return `${s.avg_latency_ms} ms`
})

const p95LatencyText = computed(() => {
  const d = data.value
  if (!d || d.summary.requests === 0) return '-'
  return `${d.latency.p95_ms} ms`
})

const cards = computed(() => {
  const s = data.value?.summary
  return [
    { label: '请求量', value: s ? s.requests.toLocaleString() : '-' },
    { label: '成功率', value: s ? `${s.success_rate}%` : '-' },
    { label: '失败数', value: s ? s.failed.toLocaleString() : '-' },
    { label: 'Token 总量', value: s ? s.tokens.toLocaleString() : '-' },
    { label: '平均延迟', value: avgLatencyText.value },
    { label: 'P95 延迟', value: p95LatencyText.value }
  ]
})

const percentiles = computed(() => {
  const d = data.value
  // 无采样时延迟无定义，「0 ms」会被读成瞬时，故显示占位符
  if (!d || d.summary.requests === 0) {
    return ['P50', 'P95', 'P99'].map((label) => ({ label, value: '-' }))
  }
  return [
    { label: 'P50', value: `${d.latency.p50_ms} ms` },
    { label: 'P95', value: `${d.latency.p95_ms} ms` },
    { label: 'P99', value: `${d.latency.p99_ms} ms` }
  ]
})

// 失败数与请求数均取自服务端 summary；此处只做除法展示，
// 不得由 status 分桶求和重算失败数
const failedText = computed(() => (data.value ? data.value.summary.failed.toLocaleString() : '-'))

// 空窗按规格取 0：requests 为 0 时 0/0 无定义，须显式短路，
// 不能指望除法；只有「未取到数据」才用占位符
const failRateText = computed(() => {
  const s = data.value?.summary
  if (!s) return '-'
  if (s.requests === 0) return '0.0%'
  return `${((s.failed / s.requests) * 100).toFixed(1)}%`
})

// splitText 流式与非流式口径完全一致，仅取的字段不同
function splitText(t: DashboardTokens, stream: boolean): string {
  const requests = stream ? t.stream_requests : t.non_stream_requests
  const tokens = stream ? t.stream_tokens : t.non_stream_tokens
  const total = t.stream_requests + t.non_stream_requests
  const pct = total === 0 ? 0 : Math.round((requests / total) * 1000) / 10
  return `${requests.toLocaleString()} 次（${pct}%）· ${tokens.toLocaleString()} Token`
}

const streamText = computed(() => {
  const t = data.value?.tokens
  return t ? splitText(t, true) : '-'
})

const nonStreamText = computed(() => {
  const t = data.value?.tokens
  return t ? splitText(t, false) : '-'
})

// draw 在指定容器上绘制：首次 init，之后复用实例；
// notMerge 置真，避免上一份 option 的残留系列影响新图形
function draw(key: string, el: HTMLElement | null, option: ChartOption) {
  if (!el) return
  let chart = chartInstances.get(key)
  if (!chart) {
    chart = echarts.init(el)
    chartInstances.set(key, chart)
  }
  chart.setOption(option, true)
}

function renderAll() {
  // 失败或空窗口时清空图表：旧窗口的图形不得继续显示
  if (!data.value || showEmpty.value) {
    for (const c of chartInstances.values()) c.clear()
    return
  }
  // 每次绘制都重新读取色板，否则明暗切换后仍用旧色
  const p = palette()
  renderTrend(p)
  renderLatency(p)
  renderStatus(p)
  renderModels(p)
  renderTokens(p)
}

function renderTrend(p: ChartPalette) {
  const trend = data.value?.trend ?? []
  draw('trend', trendEl.value, {
    color: [p.primary, p.success],
    tooltip: { trigger: 'axis', ...tooltipTheme(p) },
    legend: { data: ['请求量', 'Token'], textStyle: { color: p.textSecondary } },
    grid: { left: 56, right: 56, top: 40, bottom: 32 },
    xAxis: {
      type: 'category',
      data: trend.map(t => t.date),
      boundaryGap: false,
      axisLine: { lineStyle: { color: p.border } },
      axisLabel: { color: p.textSecondary }
    },
    yAxis: [
      {
        type: 'value',
        name: '请求量',
        nameTextStyle: { color: p.textSecondary },
        axisLabel: { color: p.textSecondary },
        splitLine: { lineStyle: { color: p.border } }
      },
      {
        type: 'value',
        name: 'Token',
        nameTextStyle: { color: p.textSecondary },
        axisLabel: { color: p.textSecondary },
        splitLine: { show: false }
      }
    ],
    series: [
      {
        name: '请求量',
        type: 'line',
        smooth: true,
        data: trend.map(t => t.requests),
        areaStyle: { opacity: 0.12 }
      },
      {
        name: 'Token',
        type: 'line',
        smooth: true,
        yAxisIndex: 1,
        data: trend.map(t => t.tokens),
        areaStyle: { opacity: 0.12 }
      }
    ]
  })
}

function renderLatency(p: ChartPalette) {
  const buckets = data.value?.latency.buckets ?? []
  draw('latency', latencyEl.value, {
    color: [p.primary],
    tooltip: { trigger: 'axis', axisPointer: { type: 'shadow' }, ...tooltipTheme(p) },
    grid: { left: 48, right: 16, top: 16, bottom: 32 },
    xAxis: {
      type: 'category',
      data: buckets.map(b => b.label),
      axisLine: { lineStyle: { color: p.border } },
      axisLabel: { color: p.textSecondary, interval: 0, fontSize: 11 }
    },
    yAxis: {
      type: 'value',
      // 计数为整数，避免出现小数刻度
      minInterval: 1,
      axisLabel: { color: p.textSecondary },
      splitLine: { lineStyle: { color: p.border } }
    },
    series: [{ type: 'bar', data: buckets.map(b => b.count), barMaxWidth: 36 }]
  })
}

function renderStatus(p: ChartPalette) {
  const buckets = data.value?.status ?? []
  draw('status', statusEl.value, {
    color: buckets.map(b => p[STATUS_COLORS[b.class] ?? 'textSecondary']),
    tooltip: { trigger: 'item', formatter: '{b}: {c} ({d}%)', ...tooltipTheme(p) },
    legend: { bottom: 0, textStyle: { color: p.textSecondary } },
    series: [
      {
        type: 'pie',
        radius: ['52%', '74%'],
        center: ['50%', '44%'],
        label: { color: p.textSecondary, formatter: '{b}\n{c}' },
        data: buckets.map(b => ({
          name: STATUS_LABELS[b.class] ?? b.class,
          value: b.count
        }))
      }
    ]
  })
}

const HTML_ESCAPES: Record<string, string> = {
  '&': '&amp;',
  '<': '&lt;',
  '>': '&gt;',
  '"': '&quot;',
  "'": '&#39;'
}

// escapeHTML 模型名由管理端写入且未经字符集校验，而 echarts 的 html tooltip
// 按 innerHTML 渲染，故插值前必须转义
function escapeHTML(s: string): string {
  return s.replace(/[&<>"']/g, c => HTML_ESCAPES[c] ?? c)
}

function renderModels(p: ChartPalette) {
  const models = data.value?.top_models ?? []
  draw('models', modelsEl.value, {
    color: [p.primary],
    tooltip: {
      trigger: 'axis',
      axisPointer: { type: 'shadow' },
      ...tooltipTheme(p),
      // 轴标签为截断显示，tooltip 补全名与 Token/失败数
      formatter: params => {
        const first = Array.isArray(params) ? params[0] : params
        const m = models[first?.dataIndex ?? -1]
        if (!m) return ''
        return `${escapeHTML(m.model_name)}<br/>请求 ${m.requests} · Token ${m.tokens} · 失败 ${m.failed}`
      }
    },
    grid: { left: 100, right: 24, top: 16, bottom: 24 },
    xAxis: {
      type: 'value',
      minInterval: 1,
      axisLabel: { color: p.textSecondary },
      splitLine: { lineStyle: { color: p.border } }
    },
    yAxis: {
      type: 'category',
      // 横向条形自下而上排布，inverse 让请求量最高者落在顶部
      inverse: true,
      data: models.map(m => m.model_name),
      axisLine: { lineStyle: { color: p.border } },
      axisLabel: { color: p.textSecondary, width: 88, overflow: 'truncate' }
    },
    series: [{ type: 'bar', data: models.map(m => m.requests), barMaxWidth: 18 }]
  })
}

function renderTokens(p: ChartPalette) {
  const t = data.value?.tokens
  draw('tokens', tokensEl.value, {
    color: [p.primary, p.success],
    tooltip: { trigger: 'item', formatter: '{b}: {c} ({d}%)', ...tooltipTheme(p) },
    legend: { bottom: 0, textStyle: { color: p.textSecondary } },
    series: [
      {
        type: 'pie',
        radius: ['52%', '74%'],
        center: ['50%', '44%'],
        label: { color: p.textSecondary, formatter: '{b}\n{c}' },
        data: [
          { name: 'Prompt', value: t?.prompt_tokens ?? 0 },
          { name: 'Completion', value: t?.completion_tokens ?? 0 }
        ]
      }
    ]
  })
}

async function load() {
  const cur = ++seq
  loading.value = true
  error.value = ''
  try {
    const d = await getDashboard(activeWindow.value)
    // 迟到的旧响应直接丢弃，不能先写 data.value 再判断，否则仍会覆盖已渲染的新数据
    if (cur !== seq) return
    data.value = d
    renderAll()
  } catch {
    if (cur !== seq) return
    // client.ts 拦截器已弹 toast；此处负责页面内的持久错误态。
    // 清空数据与图表：toast 消失后，旧窗口的数字不得继续冒充当前值
    error.value = '无法加载概览数据，请稍后重试。'
    data.value = null
    renderAll()
  } finally {
    // 仅最新一次请求负责解除加载态，避免旧请求提前摘掉新请求的遮罩
    if (cur === seq) loading.value = false
  }
}

onMounted(async () => {
  const containers = [trendEl.value, latencyEl.value, statusEl.value, modelsEl.value, tokensEl.value]
  if (containers.some(Boolean)) {
    ro = new ResizeObserver(() => {
      for (const c of chartInstances.values()) c.resize()
    })
    for (const el of containers) {
      if (el) ro.observe(el)
    }
  }
  await load()
})

onBeforeUnmount(() => {
  // 先断开观察器再销毁图表，避免卸载后回调再触发 resize
  ro?.disconnect()
  ro = null
  for (const c of chartInstances.values()) c.dispose()
  chartInstances.clear()
})
</script>

<style scoped>
.toolbar {
  display: flex;
  justify-content: flex-end;
  margin-bottom: var(--ng-space-4);
}
.stack { margin-bottom: var(--ng-space-3); }
.alert-link { margin-left: var(--ng-space-2); }
.retry { margin-left: var(--ng-space-2); }
.metric-card {
  border-radius: var(--ng-radius-lg);
  box-shadow: var(--ng-shadow-card);
  margin-bottom: var(--ng-space-4);
  transition: box-shadow 0.2s ease, transform 0.2s ease;
}
.metric-card:hover {
  box-shadow: var(--ng-shadow-dropdown);
  transform: translateY(-2px);
}
.metric-label {
  color: var(--ng-text-secondary);
  font-size: 13px;
}
.metric-value {
  margin-top: var(--ng-space-2);
  font-family: var(--ng-font-mono);
  font-size: 28px;
  font-weight: 600;
  color: var(--ng-text-primary);
}
.panel {
  border-radius: var(--ng-radius-lg);
  box-shadow: var(--ng-shadow-card);
  margin-bottom: var(--ng-space-4);
}
.chart-wrap { position: relative; }
.chart { height: 300px; }
.chart-lg { height: 340px; }
.chart-empty {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--ng-text-secondary);
  font-size: 13px;
}
.stat-row {
  display: flex;
  gap: var(--ng-space-5);
  margin-bottom: var(--ng-space-3);
}
.stat-value {
  margin-top: var(--ng-space-1);
  font-family: var(--ng-font-mono);
  font-size: 20px;
  font-weight: 600;
  color: var(--ng-text-primary);
}
.note {
  margin: var(--ng-space-3) 0 0;
  color: var(--ng-text-secondary);
  font-size: 12px;
}
</style>
