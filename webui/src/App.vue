<template>
  <el-container class="app-layout">
    <el-aside width="200px" class="app-aside">
      <div class="app-logo">NeuralGate</div>
      <el-menu router :default-active="$route.path">
        <el-menu-item index="/models"><el-icon><Cpu /></el-icon>模型配置</el-menu-item>
        <el-menu-item index="/api-keys"><el-icon><Key /></el-icon>API Key</el-menu-item>
        <el-menu-item index="/audit-logs"><el-icon><Document /></el-icon>审计日志</el-menu-item>
        <el-menu-item index="/rate-limits"><el-icon><Timer /></el-icon>限流配置</el-menu-item>
        <el-menu-item index="/tamper-alerts"><el-icon><Warning /></el-icon>防篡改告警</el-menu-item>
        <el-menu-item index="/system"><el-icon><Setting /></el-icon>系统信息</el-menu-item>
      </el-menu>
    </el-aside>
    <el-container>
      <el-header class="app-header">
        <span class="app-title">{{ $route.meta.title }}</span>
      </el-header>
      <el-main class="app-main">
        <router-view />
      </el-main>
    </el-container>
  </el-container>
</template>

<script setup lang="ts">
import { Cpu, Key, Document, Timer, Setting, Warning } from '@element-plus/icons-vue'
</script>

<style>
body { margin: 0; font-family: 'Helvetica Neue', Arial, sans-serif; }
.app-layout { height: 100vh; }
.app-aside { background: #1f2937; }
.app-logo { color: #fff; font-size: 18px; font-weight: bold; padding: 16px; text-align: center; }
.app-aside .el-menu { border-right: none; background: transparent; }
.app-aside .el-menu-item { color: #cbd5e1; }
.app-aside .el-menu-item.is-active { color: #fff; background: #374151; }
.app-header { background: #fff; border-bottom: 1px solid #e5e7eb; display: flex; align-items: center; }
.app-title { font-size: 16px; font-weight: 500; }
.app-main { background: #f3f4f6; }
</style>
