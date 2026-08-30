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
    "const dashboardFailurePages = new WeakSet<Page>()\n",
    "const dashboardFailurePages = new WeakSet<Page>()\nconst modelDetailsFailurePages = new WeakSet<Page>()\nconst modelDetailsPageTwoPages = new WeakSet<Page>()\n",
    'Model details E2E state flags',
)

anchor = """    if (instancesStatePages.has(page) && url.pathname === '/api/v1/imports') {"""
fixture = """    if (url.pathname === '/api/v1/models/qwen3-8b-q4km/details') {
      if (modelDetailsFailurePages.has(page)) {
        await route.fulfill({ status: 503, headers: corsHeaders, body: JSON.stringify({ error: 'Representative GGUF details failure for visual QA.' }) })
        return
      }
      const base = responseFor(url.pathname, request.method()) as Record<string, any>
      const filter = url.searchParams.get('q') || ''
      if (filter) {
        await route.fulfill({ status: 200, headers: corsHeaders, body: JSON.stringify({ ...base, metadata: [], metadata_total: 0, offset: 0 }) })
        return
      }
      if (modelDetailsPageTwoPages.has(page)) {
        const pageOffset = Number(url.searchParams.get('offset') || 0)
        const metadata = pageOffset >= 100
          ? [
              { key: 'metadata.page2.first', type: 'string', value: 'First key on the second page' },
              { key: 'metadata.page2.second', type: 'uint32', value: '42' },
              { key: 'metadata.page2.third', type: 'string', value: 'Last representative key' }
            ]
          : Array.from({ length: 100 }, (_, index) => ({ key: `metadata.page1.key_${String(index + 1).padStart(3, '0')}`, type: 'string', value: `Representative metadata value ${index + 1}` }))
        await route.fulfill({ status: 200, headers: corsHeaders, body: JSON.stringify({ ...base, metadata_count: 103, metadata_total: 103, metadata, offset: pageOffset, limit: 100 }) })
        return
      }
    }
"""
replace_once(anchor, fixture + anchor, 'Model details route fixtures')

test_anchor = """test('model details expanded metadata screenshot', async ({ page }, testInfo) => {"""
new_tests = """test('model details filtered no-match screenshot', async ({ page }, testInfo) => {
  await page.goto('/models/qwen3-8b-q4km/details', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  await expect(page.getByRole('heading', { name: 'Qwen3 8B' })).toBeVisible()
  await page.locator('[data-testid="metadata-search"]').fill('tokenizer.missing')
  await page.locator('[data-testid="metadata-search-button"]').click()
  await expect(page.getByText('No matching GGUF metadata', { exact: true })).toBeVisible()
  await expect(page.getByText('No keys match “tokenizer.missing”. Clear the filter to see all metadata.', { exact: true })).toBeVisible()
  await expect(page.getByText('No matching keys', { exact: true })).toBeVisible()
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/model-details-no-match.png`, fullPage: true, animations: 'disabled' })
})


test('model details second page screenshot', async ({ page }, testInfo) => {
  modelDetailsPageTwoPages.add(page)
  await page.goto('/models/qwen3-8b-q4km/details', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  await expect(page.getByText('Showing 1–100 of 103 matching keys', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Next', exact: true }).click()
  await expect(page.getByText('Showing 101–103 of 103 matching keys', { exact: true })).toBeVisible()
  await expect(page.getByText('metadata.page2.first', { exact: true })).toBeVisible()
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/model-details-page-2.png`, fullPage: true, animations: 'disabled' })
})


test('model details API failure screenshot', async ({ page }, testInfo) => {
  modelDetailsFailurePages.add(page)
  await page.goto('/models/qwen3-8b-q4km/details', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  const failure = page.locator('[data-testid="model-details-error"]')
  await expect(failure).toContainText('Unable to load GGUF metadata')
  await expect(failure).toContainText('Representative GGUF details failure for visual QA.')
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/model-details-api-failure.png`, fullPage: true, animations: 'disabled' })
})


"""
replace_once(test_anchor, new_tests + test_anchor, 'Model details state screenshot tests')

path.write_text(text)
