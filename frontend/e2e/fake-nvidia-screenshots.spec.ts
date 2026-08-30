import { expect, test } from '@playwright/test'

const backendURL = 'http://127.0.0.1:8888'
const username = 'ux-review-admin'
const password = 'ux-review-password-1234'

async function loginToken(request: Parameters<Parameters<typeof test.beforeEach>[0]>[0]['request']) {
  const bootstrap = await request.get(`${backendURL}/api/v1/auth/bootstrap`)
  expect(bootstrap.ok()).toBeTruthy()
  const bootstrapState = await bootstrap.json() as { required: boolean }
  if (bootstrapState.required) {
    const created = await request.post(`${backendURL}/api/v1/auth/bootstrap`, {
      data: { username, password }
    })
    expect(created.ok()).toBeTruthy()
  }

  const login = await request.post(`${backendURL}/api/v1/auth/login`, {
    data: { username, password }
  })
  expect(login.ok()).toBeTruthy()
  const result = await login.json() as { access_token: string }
  return result.access_token
}

test.beforeEach(async ({ page, request }) => {
  test.skip(!process.env.FAKE_NVIDIA_E2E, 'requires the fake-nvidia runtime and real manager backend')
  const token = await loginToken(request)
  await page.addInitScript(({ token }) => {
    window.sessionStorage.setItem('lcm_management_token', token)
    window.localStorage.setItem('llamacpp-manager-theme', 'dark')
  }, { token })
  await page.emulateMedia({ reducedMotion: 'reduce' })
})

test('dashboard shows real fake-nvidia pressure state', async ({ page, request }, testInfo) => {
  const token = await loginToken(request)
  const hardware = await request.get(`${backendURL}/api/v1/hardware`, {
    headers: { Authorization: `Bearer ${token}` }
  })
  expect(hardware.ok()).toBeTruthy()
  const snapshot = await hardware.json() as {
    gpus: Array<{ backend: string, name: string, total_bytes: number, used_bytes: number }>
    processes: Array<{ process_name?: string, used_bytes: number }>
  }
  expect(snapshot.gpus).toHaveLength(2)
  expect(snapshot.gpus.every(gpu => gpu.backend === 'cuda')).toBeTruthy()
  expect(snapshot.gpus[0]?.name).toContain('4060 Ti')
  expect(snapshot.gpus[0]?.used_bytes || 0).toBeGreaterThan(14 * 1024 ** 3)
  expect(snapshot.processes.some(process => process.process_name?.includes('fake-llama-server'))).toBeTruthy()

  await page.goto('/', { waitUntil: 'domcontentloaded' })
  await expect(page.locator('#manager-main')).toBeVisible({ timeout: 15_000 })
  await expect(page.getByRole('heading', { name: 'Dashboard' })).toBeVisible()
  await expect(page.getByText(/RTX 4060 Ti/i).first()).toBeVisible({ timeout: 10_000 })
  await page.waitForTimeout(1500)
  await page.screenshot({
    path: `artifacts/ux-screenshots/fake-nvidia/${testInfo.project.name}/dashboard-pressure.png`,
    fullPage: true,
    animations: 'disabled'
  })
})

test('system diagnostics render against fake-nvidia manager', async ({ page }, testInfo) => {
  await page.goto('/admin/system', { waitUntil: 'domcontentloaded' })
  await expect(page.locator('#manager-main')).toBeVisible({ timeout: 15_000 })
  await expect(page.getByRole('heading', { name: 'System' })).toBeVisible()
  await expect(page.getByText('llama.cpp', { exact: true })).toBeVisible()
  await page.screenshot({
    path: `artifacts/ux-screenshots/fake-nvidia/${testInfo.project.name}/admin-system.png`,
    fullPage: true,
    animations: 'disabled'
  })
})
