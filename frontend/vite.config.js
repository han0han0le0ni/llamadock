import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// 构建产物直接输出到 Go 内嵌目录，桌面窗口与浏览器管理台共用同一份资源
export default defineConfig({
  plugins: [vue()],
  base: './',
  build: {
    outDir: '../internal/webui/dist',
    emptyOutDir: true,
    chunkSizeWarningLimit: 1500,
  },
  server: {
    port: 5173,
    proxy: { '/api': 'http://127.0.0.1:8081' },
  },
})
