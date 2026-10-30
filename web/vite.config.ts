import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    port: 5173,
    // Same-origin in dev: REST and WebSocket both go through Vite to the Go API.
    proxy: {
      '/api': { target: 'http://localhost:8080', ws: true },
    },
  },
})
