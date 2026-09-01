import { defineConfig, devices } from '@playwright/test'

const chrome = devices['Desktop Chrome']

export default defineConfig({
  testDir: './e2e',
  timeout: 30_000,
  expect: { timeout: 8_000 },
  fullyParallel: false,
  workers: 1,
  reporter: [['list'], ['html', { outputFolder: 'artifacts/playwright-report', open: 'never' }]],
  outputDir: 'artifacts/test-results',
  use: {
    baseURL: 'http://127.0.0.1:3000',
    colorScheme: 'dark',
    trace: 'retain-on-failure'
  },
  projects: [
    // {
    //   name: 'mobile-small-360x800',
    //   use: { ...chrome, viewport: { width: 360, height: 800 }, isMobile: true, hasTouch: true }
    // },
    {
      name: 'mobile-large-412x915',
      use: { ...chrome, viewport: { width: 412, height: 915 }, isMobile: true, hasTouch: true }
    },
    {
      name: 'tablet-768x1024',
      use: { ...chrome, viewport: { width: 768, height: 1024 }, hasTouch: true }
    },
    {
      name: 'laptop-1366x768',
      use: { ...chrome, viewport: { width: 1366, height: 768 } }
    },
    // {
    //   name: 'desktop-1440x1000',
    //   use: { ...chrome, viewport: { width: 1440, height: 1000 } }
    // },
    {
      name: 'desktop-wide-1920x1080',
      use: { ...chrome, viewport: { width: 1920, height: 1080 } }
    }
  ],
  webServer: {
    command: 'npm run dev -- --host 127.0.0.1 --port 3000',
    url: 'http://127.0.0.1:3000',
    reuseExistingServer: !process.env.CI,
    timeout: 120_000
  }
})
