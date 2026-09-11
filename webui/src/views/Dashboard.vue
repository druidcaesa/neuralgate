<template>
  <div>
    <el-alert
      v-for="(a, i) in data?.alerts ?? []"
      :key="i"
      :title="a.title"
      :type="a.level === 'error' ? 'error' : 'warning'"
      show-icon
      :closable="false"
      style="margin-bottom:12px"
    >
      {{ a.detail }}
      <router-link v-if="a.link && hasFeature('tamper_proof')" :to="a.link" style="margin-left:8px">前往处理 →</router-link>
    </el-alert>

    <el-row :gutter="16">
      <el-col v-for="card in cards" :key="card.label" :span="6">
        <el-card shadow="never">
          <div class="metric-label">{{ card.label }}</div>
          <div class="metric-value">{{ card.value }}</div>
        </el-card>
      </el-col>
    </el-row>

    <el-card style="margin-top:16px">
      <template #header>
        <div style="display:flex;justify-content:space-between;align-items:center">
          <span>近期趋势</span>
          <el-radio-group v-model="activeWindow" size="small" @change="load">
            <el-radio-button v-for="w in windows" :key="w" :value="w">{{ w }}</el-radio-button>
          </el-radio-group>
        </div>
      </template>

      <el-alert
        v-if="data?.truncated"
        type="warning"
        show-icon
        :closable="false"
        title="统计已截断"
        description="数据量超过单次聚合上限，仅含最近的部分记录，趋势最左侧可能偏低"
        style="margin-bottom:12px"
      />

      <div ref="chartEl" style="height:320px"></div>

      <p class="latency-note">
        平均延迟为全部请求耗时的算术平均，长连接流式请求会显著拉高该值，<strong>不是 SLA 指标</strong>。
      </p>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import * as echarts from 'echarts/core'
import { LineChart } from 'echarts/charts'
import { GridComponent, TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import type { DashboardData, DashboardWindow } from '../types'
import { getDashboard } from '../api/dashboard'
import { hasFeature } from '../api/auth'

// 按需注册：只引折线图 + 网格 + 提示框 + Canvas 渲染器，避免全量引入撑大产物
echarts.use([LineChart, GridComponent, TooltipComponent, CanvasRenderer])

const windows: DashboardWindow[] = ['24h', '7d', '30d']
// 变量名不得用 window：会遮蔽全局 window，导致下方 resize 监听挂到 ref 上
const activeWindow = ref<DashboardWindow>('7d')
const data = ref<DashboardData | null>(null)
const chartEl = ref<HTMLElement | null>(null)
let chart: ReturnType<typeof echarts.init> | null = null
// 观察图表容器自身尺寸，覆盖侧栏折叠等不触发 window resize 的布局变动
let ro: ResizeObserver | null = null
// 请求序号，只采纳最新一次 load 的响应，避免快速切换窗口时旧数据覆盖新数据
let seq = 0

const cards = computed(() => {
  const s = data.value?.summary
  return [
    { label: '请求量', value: s ? s.requests.toLocaleString() : '-' },
    { label: '成功率', value: s ? `${s.success_rate}%` : '-' },
    { label: 'Token 总量', value: s ? s.tokens.toLocaleString() : '-' },
    { label: '平均延迟', value: s ? `${s.avg_latency_ms} ms` : '-' }
  ]
})

function render() {
  if (!chartEl.value) return
  if (!chart) chart = echarts.init(chartEl.value)
  const trend = data.value?.trend ?? []
  chart.setOption({
    tooltip: { trigger: 'axis' },
    grid: { left: 48, right: 48, top: 32, bottom: 32 },
    xAxis: { type: 'category', data: trend.map(p => p.date), boundaryGap: false },
    yAxis: [
      { type: 'value', name: '请求量' },
      { type: 'value', name: 'Token' }
    ],
    series: [
      { name: '请求量', type: 'line', data: trend.map(p => p.requests), smooth: true },
      { name: 'Token', type: 'line', yAxisIndex: 1, data: trend.map(p => p.tokens), smooth: true }
    ]
  })
}

async function load() {
  const cur = ++seq
  const d = await getDashboard(activeWindow.value)
  // 迟到的旧响应直接丢弃，不能先写 data.value 再判断，否则仍会覆盖已渲染的新数据
  if (cur !== seq) return
  data.value = d
  render()
}

onMounted(async () => {
  if (chartEl.value) {
    ro = new ResizeObserver(() => chart?.resize())
    ro.observe(chartEl.value)
  }
  await load()
})

onBeforeUnmount(() => {
  // 先断开观察器再销毁图表，避免卸载后回调再触发 resize
  ro?.disconnect()
  ro = null
  chart?.dispose()
  chart = null
})
</script>

<style scoped>
.metric-label {
  color: var(--el-text-color-secondary);
  font-size: 13px;
}
.metric-value {
  margin-top: 8px;
  font-size: 24px;
  font-weight: 600;
}
.latency-note {
  margin: 12px 0 0;
  color: var(--el-text-color-secondary);
  font-size: 12px;
}
</style>
