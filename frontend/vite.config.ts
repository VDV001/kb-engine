import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// Relative base so built assets work when embedded and served by `kbengine
// serve` from any path. In dev, /api is proxied to the Go server.
export default defineConfig({
  base: './',
  plugins: [react(), tailwindcss()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },
  test: {
    // Зона прибита намеренно. Без неё тесты идут в зоне машины: у владельца
    // UTC+5, на CI — UTC, и проверки, где местная дата расходится с UTC,
    // молчали бы ровно там, где их и надо слушать. Зона выбрана та, в которой
    // живёт книга.
    env: { TZ: 'Asia/Yekaterinburg' },
  },
})
