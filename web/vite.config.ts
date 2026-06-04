import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    proxy: {
      '/api': 'http://localhost:7088',
    },
  },
  build: {
    // Build straight into the Go embed location: internal/daemon/webui.go has
    // `//go:embed all:webdist`. emptyOutDir is required because the target is
    // outside the Vite root (web/). This removes the separate web/dist + copy.
    outDir: '../internal/daemon/webdist',
    emptyOutDir: true,
  },
  test: {
    globals: true,
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    exclude: ['tests/**', 'node_modules/**'],
  },
})
