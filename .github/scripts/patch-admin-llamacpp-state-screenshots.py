from pathlib import Path

path = Path('frontend/e2e/redesign-screenshots.spec.ts')
text = path.read_text()


def replace_once(old: str, new: str, label: str) -> None:
    global text
    if new in text:
        return
    count = text.count(old)
    if count != 1:
        raise SystemExit(f'{label}: expected one match, found {count}')
    text = text.replace(old, new, 1)


replace_once(
    "const modelDetailsPageTwoPages = new WeakSet<Page>()\n",
    "const modelDetailsPageTwoPages = new WeakSet<Page>()\nconst adminLlamaPopulatedPages = new WeakSet<Page>()\nconst adminLlamaUnavailablePages = new WeakSet<Page>()\nconst adminLlamaSaveFailurePages = new WeakSet<Page>()\n",
    'llama.cpp E2E state flags',
)

route_anchor = """    if (modelInspectFailurePages.has(page) && url.pathname === '/api/v1/models/inspect' && request.method() === 'POST') {"""
route_fixture = """    if (adminLlamaUnavailablePages.has(page) && url.pathname === '/api/v1/llamacpp/profile') {
      await route.fulfill({ status: 200, headers: corsHeaders, body: JSON.stringify({ available: false, profile: null }) })
      return
    }
    if (adminLlamaUnavailablePages.has(page) && url.pathname === '/api/v1/llamacpp/config' && request.method() === 'GET') {
      await route.fulfill({ status: 200, headers: corsHeaders, body: JSON.stringify({ profile: null, effective: { global: {}, values: {}, sources: {} } }) })
      return
    }
    if (adminLlamaPopulatedPages.has(page) && url.pathname === '/api/v1/llamacpp/config' && request.method() === 'GET') {
      await route.fulfill({
        status: 200,
        headers: corsHeaders,
        body: JSON.stringify({
          profile: llamaProfile,
          effective: {
            global: { 'ctx-size': '32768', 'flash-attn': 'on', parallel: '4' },
            values: {},
            sources: {}
          }
        })
      })
      return
    }
    if (adminLlamaSaveFailurePages.has(page) && url.pathname === '/api/v1/llamacpp/config' && request.method() === 'PUT') {
      await route.fulfill({ status: 422, headers: corsHeaders, body: JSON.stringify({ error: 'Representative invalid llama.cpp default for visual QA.' }) })
      return
    }
"""
replace_once(route_anchor, route_fixture + route_anchor, 'llama.cpp visual fixture routes')

test_anchor = """test('downloads lifecycle and files screenshot', async ({ page }, testInfo) => {"""
new_tests = """test('administration llama.cpp populated defaults and save failure screenshots', async ({ page }, testInfo) => {
  adminLlamaPopulatedPages.add(page)
  await page.goto('/admin/llamacpp', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  const rows = page.locator('[data-testid="admin-global-default-row"]')
  await expect(rows).toHaveCount(3)
  await expect(rows.nth(0)).toContainText('Size of the prompt context')
  await expect(rows.nth(1)).toContainText('Enable Flash Attention')
  await expect(rows.nth(2)).toContainText('Number of parallel sequences')
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/admin-llamacpp-populated.png`, fullPage: true, animations: 'disabled' })

  adminLlamaSaveFailurePages.add(page)
  await page.getByRole('button', { name: 'Save defaults', exact: true }).click()
  await expect(page.getByText('Representative invalid llama.cpp default for visual QA.', { exact: true })).toBeVisible()
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/admin-llamacpp-save-error.png`, fullPage: true, animations: 'disabled' })
})


test('administration llama.cpp unavailable screenshot', async ({ page }, testInfo) => {
  adminLlamaUnavailablePages.add(page)
  await page.goto('/admin/llamacpp', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  const warning = page.locator('[data-testid="llamacpp-unavailable-warning"]')
  await expect(warning).toContainText('Unavailable')
  await expect(warning).toContainText('llama-server could not be discovered.')
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/admin-llamacpp-unavailable.png`, fullPage: true, animations: 'disabled' })
})


"""
replace_once(test_anchor, new_tests + test_anchor, 'llama.cpp state screenshot tests')

path.write_text(text)
