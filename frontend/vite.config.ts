import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const __dirname = path.dirname(fileURLToPath(import.meta.url))

const BACKEND_TARGET = process.env.VITE_BACKEND_URL ?? 'http://localhost:8418'

function escapeRegExp(value: string) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

function matchPackages(packages: readonly string[]) {
  const patterns = packages.map((packageName) => {
    const escapedName = packageName.split('/').map(escapeRegExp).join('[\\\\/]')
    return new RegExp(`[\\\\/]node_modules[\\\\/](?:\\.pnpm[\\\\/][^\\\\/]+[\\\\/]node_modules[\\\\/])?${escapedName}(?:[\\\\/]|$)`)
  })
  return (moduleId: string) => patterns.some((pattern) => pattern.test(moduleId))
}

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, '.'),
    },
  },
  server: {
    port: 3010,
    strictPort: true,
    proxy: {
      '/api':     { target: BACKEND_TARGET, changeOrigin: true },
      '/healthz': { target: BACKEND_TARGET, changeOrigin: true },
    },
  },
  build: {
    rolldownOptions: {
      output: {
        entryFileNames: 'assets/[name]-[hash:8].js',
        chunkFileNames: 'assets/[name]-[hash:8].js',
        assetFileNames: 'assets/[name]-[hash:8][extname]',
        codeSplitting: {
          minSize: 80 * 1024,
          groups: [
            {
              name: 'vendor-react',
              test: matchPackages(['react', 'react-dom', 'react-router', 'react-router-dom', 'scheduler']),
              priority: 50,
            },
            {
              name: 'vendor-charts',
              test: matchPackages(['recharts']),
              priority: 40,
              includeDependenciesRecursively: false,
            },
          ],
        },
      },
    },
  },
})
