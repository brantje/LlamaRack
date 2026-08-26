import { defineVitestConfig } from '@nuxt/test-utils/config'

export default defineVitestConfig({
  test: {
    environment: 'nuxt',
    include: ['test/**/*.nuxt.test.ts'],
    coverage: {
      provider: 'v8',
      include: ['app/**/*.{ts,vue}'],
      reporter: ['text', 'text-summary'],
      thresholds: {
        statements: 90,
        branches: 90,
        functions: 90,
        lines: 90
      }
    }
  }
})
