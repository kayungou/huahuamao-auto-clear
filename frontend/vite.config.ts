import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// 构建产物输出到 ../assets/dist，由 Go 的 embed 打包进二进制；
// 开发时 vite dev（5173）把 /api 代理到本地 Go 服务（8902）。
export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) }
  },
  build: {
    outDir: '../assets/dist',
    emptyOutDir: true,
    target: 'es2022',
    cssTarget: 'safari16'
  },
  server: {
    port: 5173,
    strictPort: false,
    proxy: { '/api': 'http://127.0.0.1:8902' }
  }
})
