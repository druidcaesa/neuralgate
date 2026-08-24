<template>
  <div>
  <el-alert
    v-if="tamperUnresolved > 0"
    :title="`检测到 ${tamperUnresolved} 条审计篡改告警，请立即核实处置`"
    type="error"
    show-icon
    :closable="false"
    style="margin-bottom:16px"
  >
    <router-link to="/tamper-alerts">前往告警列表 →</router-link>
  </el-alert>

  <el-card>
    <template #header><span>系统信息</span></template>
    <el-descriptions v-if="info" :column="2" border>
      <el-descriptions-item label="版本">{{ info.version }}</el-descriptions-item>
      <el-descriptions-item label="版本类型">{{ info.edition }}</el-descriptions-item>
      <el-descriptions-item label="编译时间">{{ info.build_time }}</el-descriptions-item>
      <el-descriptions-item label="Git Commit">{{ info.git_commit }}</el-descriptions-item>
      <el-descriptions-item label="运行时长">{{ info.uptime }}</el-descriptions-item>
      <el-descriptions-item label="数据库状态">
        <el-tag :type="info.db_status === 'ok' ? 'success' : 'danger'">{{ info.db_status }}</el-tag>
      </el-descriptions-item>
      <el-descriptions-item label="审计队列">{{ info.audit_queue_status?.status ?? 'ok' }}</el-descriptions-item>
      <el-descriptions-item label="限流器">{{ info.rate_limiter_status?.status ?? 'ok' }}</el-descriptions-item>
    </el-descriptions>
    <el-button style="margin-top:16px" @click="load">刷新</el-button>
  </el-card>

  <el-card style="margin-top:16px">
    <template #header><span>授权信息</span></template>
    <el-alert
      v-if="license && license.status !== 'valid'"
      :title="licenseMessage"
      type="warning"
      show-icon
      :closable="false"
      style="margin-bottom:12px"
    />
    <el-descriptions v-if="license" :column="2" border>
      <el-descriptions-item label="授权状态">
        <el-tag :type="license.status === 'valid' ? 'success' : 'warning'">{{ licenseStatusText }}</el-tag>
      </el-descriptions-item>
      <el-descriptions-item label="运行版本">{{ license.edition }}</el-descriptions-item>
      <el-descriptions-item label="客户名称">{{ license.customer_name ?? '-' }}</el-descriptions-item>
      <el-descriptions-item label="产品名称">{{ license.product_name ?? '-' }}</el-descriptions-item>
      <el-descriptions-item label="授权码">{{ license.license_key ?? '-' }}</el-descriptions-item>
      <el-descriptions-item label="到期时间">{{ license.expires_at ?? '-' }}</el-descriptions-item>
      <el-descriptions-item label="剩余天数">{{ license.days_remaining ?? '-' }}</el-descriptions-item>
      <el-descriptions-item label="节点/租户上限">{{ license.max_nodes ?? '-' }} / {{ license.max_tenants ?? '-' }}</el-descriptions-item>
      <el-descriptions-item label="授权功能" :span="2">
        <template v-if="license.features?.length">
          <el-tag v-for="f in license.features" :key="f" size="small" style="margin-right:4px">{{ f }}</el-tag>
        </template>
        <span v-else>-</span>
      </el-descriptions-item>
    </el-descriptions>
  </el-card>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import type { LicenseDetail, SystemInfo } from '../types'
import { getLicense, getSystemInfo } from '../api/system'

const info = ref<SystemInfo | null>(null)
const license = ref<LicenseDetail | null>(null)

const tamperUnresolved = computed(() => info.value?.tamper?.unresolved_count ?? 0)

const statusLabels: Record<string, string> = {
  valid: '有效',
  expired: '已过期',
  invalid: '无效',
  missing: '未授权',
  oss: '开源版'
}

const licenseStatusText = computed(
  () => statusLabels[license.value?.status ?? ''] ?? license.value?.status ?? '-'
)

const licenseMessage = computed(
  () => `当前${licenseStatusText.value}，以开源模式运行`
)

async function load() {
  info.value = await getSystemInfo()
  // 授权接口失败不阻塞系统信息展示
  try {
    license.value = await getLicense()
  } catch {
    license.value = null
  }
}

onMounted(load)
</script>
