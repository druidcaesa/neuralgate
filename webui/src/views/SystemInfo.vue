<template>
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
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import type { SystemInfo } from '../types'
import { getSystemInfo } from '../api/system'

const info = ref<SystemInfo | null>(null)

async function load() {
  info.value = await getSystemInfo()
}

onMounted(load)
</script>
