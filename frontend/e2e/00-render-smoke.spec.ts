import { expect, test, type Page, type Route } from '@playwright/test'

const headers = {
  'access-control-allow-origin': 'http://127.0.0.1:3000',
  'access-control-allow-headers': 'authorization,content-type',
  'access-control-allow-methods': 'GET,POST,PATCH,PUT,DELETE,OPTIONS',
  'content-type': 'application/json'
}

function payload(pathname: string) {
  if (pathname === '/api/v1/auth/bootstrap') return { required: false }
  if (pathname === '/api/v1/auth/providers') return { local_login_enabled: true, providers: [] }
  if (pathname === '/api/v1/me') return { id: 1, username: 'admin', enabled: true }
  if (pathname === '/api/v1/models') return []
  if (pathname === '/api/v1/instances') return []
  if (pathname === '/api/v1/llamacpp/profile') return { available: false, profile: null }
  if (pathname === '/api/v1/settings/general') return {
    idle_unload_seconds: { value: 300, source: 'default', editable: true },
    observability_retention_days: { value: 30, source: 'default', editable: true }
  }
  if (pathname === '/api/v1/observability/summary') return {
    since: Date.now() - 900_000,
    requests: 0,
    successes: 0,
    errors: 0,
    active: 0,
    queued: 0,
    active_api_keys: 0,
    prompt_tokens: 0,
    generated_tokens: 0,
    total_tokens: 0,
    lifecycle: { autoloads: 0, failed_starts: 0, load_duration_ms_total: 0 },
    hardware: {
      hardware: {
        ram_total_bytes: 16 * 1024 ** 3,
        ram_available_bytes: 8 * 1024 ** 3,
        gpus: [],
        processes: [],
        collected_at: new Date().toISOString()
      },
      telemetry: []
    }
  }
  if (pathname === '/api/v1/observability/requests') return { items: [] }
  if (pathname === '/api/v1/auth/ws-ticket') return { error: 'disabled' }
  return {}
}

async function installFixture(page: Page) {
  await page.addInitScript(() => {
    window.sessionStorage.setItem('lcm_management_token', 'ux-review-token')
    window.localStorage.setItem('llamacpp-manager-theme', 'dark')
  })
  await page.route('**/api/v1/**', async (route: Route) => {
    const request = route.request()
    if (request.method() === 'OPTIONS') {
      await route.fulfill({ status: 204, headers, body: '' })
      return
    }
    const url = new URL(request.url())
    const status = url.pathname === '/api/v1/auth/ws-ticket' ? 503 : 200
    await route.fulfill({ status, headers, body: JSON.stringify(payload(url.pathname)) })
  })
}

test('authenticated application renders before screenshots', async ({ page }) => {
  const pageErrors: string[] = []
  const consoleErrors: string[] = []
  page.on('pageerror', error => pageErrors.push(error.stack || error.message))
  page.on('console', message => {
    if (message.type() === 'error') consoleErrors.push(message.text())
  })

  await installFixture(page)
  await page.goto('/', { waitUntil: 'domcontentloaded' })

  try {
    await expect(page.locator('#manager-main')).toBeVisible({ timeout: 10_000 })
    await expect(page.getByRole('heading', { name: 'Dashboard' })).toBeVisible()
  } catch (error) {
    console.log('PAGE_ERRORS', JSON.stringify(pageErrors, null, 2))
    console.log('CONSOLE_ERRORS', JSON.stringify(consoleErrors, null, 2))
    console.log('BODY_HTML', await page.locator('body').innerHTML())
    throw error
  }
})
