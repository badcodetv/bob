import path from 'node:path'
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// Dev: the UI on :8080 (an origin the Google sign-in client already allows), the API on :8090.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: { alias: { '@': path.resolve(__dirname, './src') } },
  server: { port: 8080, strictPort: true, proxy: { '/api': { target: 'http://127.0.0.1:8090', changeOrigin: false } } },
})
