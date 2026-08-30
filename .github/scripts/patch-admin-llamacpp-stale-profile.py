from pathlib import Path

page = Path('frontend/app/pages/admin/llamacpp.vue')
source = page.read_text()
old = """    globalOptions.value = { ...(result.effective.global || {}) }
    if (result.profile?.path && Array.isArray(result.profile.options)) profile.value = result.profile
"""
new = """    globalOptions.value = { ...(result.effective.global || {}) }
    profile.value = result.profile?.path && Array.isArray(result.profile.options) ? result.profile : null
"""
count = source.count(old)
if count != 1:
    raise SystemExit(f'profile refresh marker: expected one match, found {count}')
page.write_text(source.replace(old, new, 1))

unit = Path('frontend/test/admin-llamacpp-stale-profile.nuxt.test.ts')
unit.write_text("""import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import AdminLlamaCppPage from '~/pages/admin/llamacpp.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

beforeEach(() => {
  mocks.request.mockReset()
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.profile.value = {
    path: '/usr/local/bin/old-llama-server',
    version: 'old',
    fingerprint: 'stale-profile',
    options: [{ key: 'ctx-size', description: 'Context size' }]
  }
})

describe('Administration llama.cpp profile freshness', () => {
  it('clears a previously discovered profile when the current config reports no binary', async () => {
    mocks.request.mockResolvedValue({ effective: { global: {} } })
    const wrapper = await mountSuspended(AdminLlamaCppPage, { route: false })
    await flushPromises()
    expect(useManager().profile.value).toBeNull()
    expect(wrapper.get('[data-testid="llamacpp-unavailable-warning"]').text()).toContain('llama-server could not be discovered')
    expect(wrapper.text()).not.toContain('/usr/local/bin/old-llama-server')
    wrapper.unmount()
  })
})
""")

e2e = Path('frontend/e2e/redesign-screenshots.spec.ts')
text = e2e.read_text()
old = "const dashboardFailurePages = new WeakSet<Page>()\n"
new = """const dashboardFailurePages = new WeakSet<Page>()
const llamaCppMissingProfilePages = new WeakSet<Page>()
const llamaCppPopulatedDefaultsPages = new WeakSet<Page>()
const llamaCppSaveFailurePages = new WeakSet<Page>()
"""
if text.count(old) != 1:
    raise SystemExit(f'llama.cpp visual state marker: expected one match, found {text.count(old)}')
text = text.replace(old, new, 1)

marker = """    const url = new URL(request.url())
    if (instancesStatePages.has(page) && url.pathname === '/api/v1/instances' && request.method() === 'GET') {
"""
route_patch = """    const url = new URL(request.url())
    if (llamaCppMissingProfilePages.has(page) && url.pathname === '/api/v1/llamacpp/config' && request.method() === 'GET') {
      await route.fulfill({ status: 200, headers: corsHeaders, body: JSON.stringify({ effective: { global: {}, values: {}, sources: {} } }) })
      return
    }
    if (llamaCppPopulatedDefaultsPages.has(page) && url.pathname === '/api/v1/llamacpp/config' && request.method() === 'GET') {
      await route.fulfill({ status: 200, headers: corsHeaders, body: JSON.stringify({ profile: llamaProfile, effective: { global: { 'ctx-size': '32768', parallel: '4', 'flash-attn': 'true' }, values: {}, sources: {} } }) })
      return
    }
    if (llamaCppSaveFailurePages.has(page) && url.pathname === '/api/v1/llamacpp/config' && request.method() === 'PUT') {
      await route.fulfill({ status: 422, headers: corsHeaders, body: JSON.stringify({ error: 'Representative llama.cpp defaults validation failure for visual QA.' }) })
      return
    }
    if (instancesStatePages.has(page) && url.pathname === '/api/v1/instances' && request.method() === 'GET') {
"""
if text.count(marker) != 1:
    raise SystemExit(f'llama.cpp visual route marker: expected one match, found {text.count(marker)}')
text = text.replace(marker, route_patch, 1)

anchor = "\ntest('downloads lifecycle and files screenshot', async ({ page }, testInfo) => {\n"
visual = """

test('Administration llama.cpp populated defaults screenshot', async ({ page }, testInfo) => {
  llamaCppPopulatedDefaultsPages.add(page)
  await page.goto('/admin/llamacpp', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  await expect(page.locator('[data-testid="admin-global-default-row"]')).toHaveCount(3)
  await expect(page.locator('[data-testid="admin-llamacpp-capabilities"]')).toContainText('b6124')
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/admin-llamacpp-populated-defaults.png`, fullPage: true, animations: 'disabled' })
})


test('Administration llama.cpp unavailable binary screenshot', async ({ page }, testInfo) => {
  llamaCppMissingProfilePages.add(page)
  await page.goto('/admin/llamacpp', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  await expect(page.locator('[data-testid="llamacpp-unavailable-warning"]')).toContainText('llama-server could not be discovered')
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/admin-llamacpp-unavailable.png`, fullPage: true, animations: 'disabled' })
})


test('Administration llama.cpp save failure screenshot', async ({ page }, testInfo) => {
  llamaCppPopulatedDefaultsPages.add(page)
  llamaCppSaveFailurePages.add(page)
  await page.goto('/admin/llamacpp', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  await expect(page.locator('[data-testid="admin-global-default-row"]')).toHaveCount(3)
  await page.getByRole('button', { name: 'Save defaults' }).click()
  await expect(page.getByText('Representative llama.cpp defaults validation failure for visual QA.', { exact: true })).toBeVisible()
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/admin-llamacpp-save-failure.png`, fullPage: true, animations: 'disabled' })
})
"""
if text.count(anchor) != 1:
    raise SystemExit(f'llama.cpp visual insertion marker: expected one match, found {text.count(anchor)}')
text = text.replace(anchor, visual + anchor, 1)
e2e.write_text(text)
