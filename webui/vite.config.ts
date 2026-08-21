import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  server: {
    port: 5173,
    proxy: { '/api': 'http://localhost:8081' }
  },
  build: {
    // 产物输出到 admin 包内(go:embed 路径不能跨目录)
    outDir: '../pkg/admin/webui/dist',
    emptyOutDir: true
  }
})
