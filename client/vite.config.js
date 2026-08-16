import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],

  // Vitest runs the same config. jsdom gives component tests a DOM, and
  // the setup file registers the jest-dom matchers plus a per-test cleanup.
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: './src/__tests__/setup.js',
    // The setup file is support code, not a suite of its own.
    exclude: ['**/node_modules/**', '**/dist/**', '**/setup.js'],
  },
})
