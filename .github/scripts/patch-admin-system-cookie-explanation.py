from pathlib import Path

page = Path('frontend/app/pages/admin/system.vue')
source = page.read_text()
old = """          <div class="grid gap-1 border-t border-[var(--color-divider)] py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Secure session cookie</dt><dd class="min-w-0"><StatusTag :variant="info.network.secure_cookie ? 'ready' : 'neutral'">{{ info.network.secure_cookie ? 'Enabled' : 'Disabled' }}</StatusTag></dd></div>
"""
new = """          <div class="grid gap-1 border-t border-[var(--color-divider)] py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Secure session cookie</dt><dd class="min-w-0"><StatusTag :variant="info.network.secure_cookie ? 'ready' : 'neutral'">{{ info.network.secure_cookie ? 'Enabled' : 'Disabled' }}</StatusTag><p v-if="!info.network.secure_cookie && info.network.effective_scheme !== 'https'" class="mt-1 text-xs text-[var(--neutral-700)]" data-testid="secure-cookie-explanation">Disabled because the effective scheme is {{ info.network.effective_scheme }}.</p></dd></div>
"""
count = source.count(old)
if count != 1:
    raise SystemExit(f'secure-cookie row marker: expected one match, found {count}')
page.write_text(source.replace(old, new, 1))

unit = Path('frontend/test/admin-system-feedback.nuxt.test.ts')
text = unit.read_text()
anchor = """  it('shows a stable pending refresh state and a clear empty proxy state', async () => {
"""
test = """  it('explains why secure cookies are disabled for an HTTP effective scheme', async () => {
    const response: any = systemResponse()
    response.network.effective_scheme = 'http'
    response.network.secure_cookie = false
    mocks.request.mockResolvedValue(response)
    const wrapper = await mountSuspended(AdminSystemPage, { route: '/admin/system' })
    await flushPromises()
    expect(wrapper.get('[data-testid="secure-cookie-explanation"]').text()).toBe('Disabled because the effective scheme is http.')
    wrapper.unmount()
  })

"""
if text.count(anchor) != 1:
    raise SystemExit(f'admin system unit anchor: expected one match, found {text.count(anchor)}')
unit.write_text(text.replace(anchor, test + anchor, 1))

e2e = Path('frontend/e2e/redesign-screenshots.spec.ts')
text = e2e.read_text()
old = "const dashboardFailurePages = new WeakSet<Page>()\n"
new = "const dashboardFailurePages = new WeakSet<Page>()\nconst systemUnavailableLlamaPages = new WeakSet<Page>()\n"
if text.count(old) != 1:
    raise SystemExit(f'system unavailable state marker: expected one match, found {text.count(old)}')
text = text.replace(old, new, 1)

marker = """    const url = new URL(request.url())
    if (instancesStatePages.has(page) && url.pathname === '/api/v1/instances' && request.method() === 'GET') {
"""
route_patch = """    const url = new URL(request.url())
    if (systemUnavailableLlamaPages.has(page) && url.pathname === '/api/v1/system' && request.method() === 'GET') {
      const base = responseFor(url.pathname, request.method()) as Record<string, any>
      await route.fulfill({ status: 200, headers: corsHeaders, body: JSON.stringify({ ...base, llamacpp: { available: false } }) })
      return
    }
    if (instancesStatePages.has(page) && url.pathname === '/api/v1/instances' && request.method() === 'GET') {
"""
if text.count(marker) != 1:
    raise SystemExit(f'system unavailable route marker: expected one match, found {text.count(marker)}')
text = text.replace(marker, route_patch, 1)

anchor = "\ntest('downloads lifecycle and files screenshot', async ({ page }, testInfo) => {\n"
visual = """

test('Administration System unavailable llama.cpp screenshot', async ({ page }, testInfo) => {
  systemUnavailableLlamaPages.add(page)
  await page.goto('/admin/system', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  await expect(page.locator('[data-testid="secure-cookie-explanation"]')).toContainText('effective scheme is http')
  await expect(page.locator('[data-testid="system-llamacpp-warning"]')).toContainText('llama-server is unavailable')
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/admin-system-llamacpp-unavailable.png`, fullPage: true, animations: 'disabled' })
})
"""
if text.count(anchor) != 1:
    raise SystemExit(f'admin system visual anchor: expected one match, found {text.count(anchor)}')
e2e.write_text(text.replace(anchor, visual + anchor, 1))
