from pathlib import Path

page = Path('frontend/app/pages/instances/[id]/detail.vue')
source = page.read_text()
old = """const loading = ref(true)
const historyLoading = ref(false)
const historyError = ref('')
const pending = ref('')
const error = ref('')
"""
new = """const loading = ref(true)
const loadError = ref('')
const historyLoading = ref(false)
const historyError = ref('')
const pending = ref('')
const error = ref('')
"""
if source.count(old) != 1:
    raise SystemExit(f'Instance detail state marker: expected one match, found {source.count(old)}')
source = source.replace(old, new, 1)

old = """async function loadPage() {
  try {
    if (!instance.value) await manager.refresh()
    if (!instance.value) {
      error.value = `Instance “${instanceID.value}” was not found.`
      return
    }
    try { settings.value = await manager.request<GeneralSettings>('/api/v1/settings/general') } catch { settings.value = null }
    if (selectedWindow.value > retentionSeconds.value) selectedWindow.value = [...rangeOptions].reverse().find(option => option.value <= retentionSeconds.value)?.value ?? 900
    await Promise.all([loadHistory(), loadCompanions()])
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to load Instance details'
  } finally {
    loading.value = false
  }
}
"""
new = """async function loadPage() {
  loading.value = true
  loadError.value = ''
  try {
    if (!instance.value) await manager.refresh()
    if (!instance.value) {
      loadError.value = `Instance “${instanceID.value}” was not found.`
      return
    }
    try { settings.value = await manager.request<GeneralSettings>('/api/v1/settings/general') } catch { settings.value = null }
    if (selectedWindow.value > retentionSeconds.value) selectedWindow.value = [...rangeOptions].reverse().find(option => option.value <= retentionSeconds.value)?.value ?? 900
    await Promise.all([loadHistory(), loadCompanions()])
  } catch (value: any) {
    loadError.value = value?.data?.error || value?.message || 'Unable to load Instance details'
  } finally {
    loading.value = false
  }
}
"""
if source.count(old) != 1:
    raise SystemExit(f'Instance detail loadPage marker: expected one match, found {source.count(old)}')
source = source.replace(old, new, 1)

old = """          <h1 class="text-2xl font-semibold text-[var(--color-text)]">{{ instance?.name || instanceID }}</h1>
          <StatusTag v-if="instance" :variant="statusVariant(runtime?.state)">{{ runtime?.state || 'UNLOADED' }}</StatusTag>
"""
new = """          <h1 class="text-2xl font-semibold text-[var(--color-text)]">{{ instance && !loadError ? instance.name : instanceID }}</h1>
          <StatusTag v-if="instance && !loadError" :variant="statusVariant(runtime?.state)">{{ runtime?.state || 'UNLOADED' }}</StatusTag>
"""
if source.count(old) != 1:
    raise SystemExit(f'Instance detail header state marker: expected one match, found {source.count(old)}')
source = source.replace(old, new, 1)
source = source.replace('      <div v-if="instance" class="flex flex-wrap items-center justify-end gap-2">', '      <div v-if="instance && !loadError" class="flex flex-wrap items-center justify-end gap-2">', 1)

old = """    <Frame v-if="error" class="p-3" data-testid="instance-detail-error">
      <div class="flex flex-wrap items-start gap-2">
        <StatusTag variant="failed">Instance detail unavailable</StatusTag>
        <p class="min-w-0 flex-1 text-xs text-muted">{{ error }}</p>
      </div>
    </Frame>
    <div v-if="loading" class="grid gap-4 md:grid-cols-2 xl:grid-cols-4"><USkeleton v-for="n in 4" :key="n" class="h-36 w-full" /></div>

    <template v-else-if="instance">
"""
new = """    <div v-if="loading" class="grid gap-4 md:grid-cols-2 xl:grid-cols-4"><USkeleton v-for="n in 4" :key="n" class="h-36 w-full" /></div>

    <Frame v-else-if="loadError" class="p-4" data-testid="instance-detail-error">
      <div class="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div class="min-w-0">
          <StatusTag variant="failed">Instance detail unavailable</StatusTag>
          <p class="mt-2 text-xs leading-5 text-muted">{{ loadError }}</p>
        </div>
        <div class="flex w-full flex-wrap gap-2 sm:w-auto sm:shrink-0 sm:justify-end">
          <AppButton to="/instances" intent="secondary">Back to Instances</AppButton>
          <AppButton intent="primary" :loading="loading" @click="loadPage">Retry</AppButton>
        </div>
      </div>
    </Frame>

    <template v-else-if="instance">
      <Frame v-if="error" class="mb-4 p-3" data-testid="instance-detail-action-error">
        <div class="flex flex-wrap items-start gap-2">
          <StatusTag variant="failed">Instance action failed</StatusTag>
          <p class="min-w-0 flex-1 text-xs text-muted">{{ error }}</p>
        </div>
      </Frame>
"""
if source.count(old) != 1:
    raise SystemExit(f'Instance detail fatal error template marker: expected one match, found {source.count(old)}')
source = source.replace(old, new, 1)
page.write_text(source)

unit = Path('frontend/test/instance-detail-redesign-cleanup.nuxt.test.ts')
text = unit.read_text()
old = """  it('uses the semantic error note instead of UAlert', () => {
    expect(detailSource).toContain('data-testid="instance-detail-error"')
    expect(detailSource).toContain('<StatusTag variant="failed">Instance detail unavailable</StatusTag>')
    expect(detailSource).not.toContain('<UAlert')
  })
"""
new = """  it('keeps fatal load failures separate from recoverable Instance action errors', () => {
    expect(detailSource).toContain("const loadError = ref('')")
    expect(detailSource).toContain('data-testid="instance-detail-error"')
    expect(detailSource).toContain('<StatusTag variant="failed">Instance detail unavailable</StatusTag>')
    expect(detailSource).toContain('data-testid="instance-detail-action-error"')
    expect(detailSource).toContain('<StatusTag variant="failed">Instance action failed</StatusTag>')
    expect(detailSource).toContain('v-else-if="loadError"')
    expect(detailSource).toContain('@click="loadPage">Retry</AppButton>')
    expect(detailSource).not.toContain('<UAlert')
  })
"""
if text.count(old) != 1:
    raise SystemExit(f'Instance detail test marker: expected one match, found {text.count(old)}')
unit.write_text(text.replace(old, new, 1))

e2e = Path('frontend/e2e/redesign-screenshots.spec.ts')
text = e2e.read_text()
old = """const modelDetailsPageTwoPages = new WeakSet<Page>()
"""
new = """const modelDetailsPageTwoPages = new WeakSet<Page>()
const instanceDetailMissingPages = new WeakSet<Page>()
"""
if text.count(old) != 1:
    raise SystemExit(f'Instance detail visual state marker: expected one match, found {text.count(old)}')
text = text.replace(old, new, 1)

marker = """    if (instancesStatePages.has(page) && url.pathname === '/api/v1/instances' && request.method() === 'GET') {
"""
fixture = """    if (instanceDetailMissingPages.has(page) && url.pathname === '/api/v1/instances' && request.method() === 'GET') {
      await route.fulfill({ status: 200, headers: corsHeaders, body: JSON.stringify(instances.filter(item => item.id !== 'qwen3-primary').slice(0, 1)) })
      return
    }
"""
if text.count(marker) != 1:
    raise SystemExit(f'Instance detail visual route marker: expected one match, found {text.count(marker)}')
text = text.replace(marker, fixture + marker, 1)

anchor = """test('model details filtered no-match screenshot', async ({ page }, testInfo) => {
"""
visual = """test('instance detail fatal load and retry screenshot', async ({ page }, testInfo) => {
  instanceDetailMissingPages.add(page)
  await page.goto('/instances/qwen3-primary/detail', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  const failure = page.locator('[data-testid="instance-detail-error"]')
  await expect(failure).toContainText('Instance detail unavailable')
  await expect(failure).toContainText('qwen3-primary')
  await expect(page.getByText('READY', { exact: true })).toHaveCount(0)
  await expect(failure.getByRole('button', { name: 'Retry' })).toBeVisible()
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/instance-detail-fatal.png`, fullPage: true, animations: 'disabled' })

  instanceDetailMissingPages.delete(page)
  await failure.getByRole('button', { name: 'Retry' }).click()
  await expect(page.getByRole('heading', { name: 'Qwen3 primary' })).toBeVisible()
  await expect(page.getByText('READY', { exact: true })).toBeVisible()
  await expect(page.locator('[data-testid="instance-detail-error"]')).toBeHidden()
})

"""
if text.count(anchor) != 1:
    raise SystemExit(f'Instance detail visual test anchor: expected one match, found {text.count(anchor)}')
if 'instance detail fatal load and retry screenshot' in text:
    raise SystemExit('Instance detail fatal visual test already exists')
e2e.write_text(text.replace(anchor, visual + anchor, 1))
