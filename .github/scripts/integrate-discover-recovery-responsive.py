from pathlib import Path

page = Path('frontend/app/components/ModelsDiscover.vue')
text = page.read_text()

old = '''      <div class="flex flex-wrap gap-2">
        <AppButton intent="secondary" to="/downloads">Downloads</AppButton>
        <AppButton intent="secondary" to="/models">Registered models</AppButton>
      </div>'''
new = '''      <div class="flex w-full flex-wrap justify-start gap-2 sm:w-auto sm:shrink-0 sm:justify-end" data-testid="discover-list-actions">
        <AppButton intent="secondary" to="/downloads">Downloads</AppButton>
        <AppButton intent="secondary" to="/models">Registered models</AppButton>
      </div>'''
if text.count(old) != 1:
    raise SystemExit(f'unexpected Discover list action marker count: {text.count(old)}')
text = text.replace(old, new, 1)

old = '''    <Frame v-if="error" class="border-[var(--accent-800)] p-4 text-sm text-[var(--accent-900)]" data-testid="discover-error">
      {{ error }}
    </Frame>'''
new = '''    <Frame v-if="error && !(isDetail && !selected)" class="border-[var(--accent-800)] p-4 text-sm text-[var(--accent-900)]" data-testid="discover-error">
      {{ error }}
    </Frame>'''
if text.count(old) != 1:
    raise SystemExit(f'unexpected Discover generic error marker count: {text.count(old)}')
text = text.replace(old, new, 1)

old = '''    <div v-if="isDetail && detailLoading" class="space-y-3" data-testid="discover-detail-loading">
      <USkeleton class="h-24 w-full" />
      <USkeleton class="h-52 w-full" />
    </div>

    <template v-else-if="isDetail && selected">'''
new = '''    <div v-if="isDetail && detailLoading" class="space-y-3" data-testid="discover-detail-loading">
      <USkeleton class="h-24 w-full" />
      <USkeleton class="h-52 w-full" />
    </div>

    <Frame v-else-if="isDetail && error && !selected" class="border-[var(--accent-800)] p-5" data-testid="discover-detail-error">
      <div class="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div class="min-w-0">
          <StatusTag variant="failed">Repository unavailable</StatusTag>
          <p class="mt-3 break-all font-mono text-[13px] font-semibold text-[var(--color-text)]">{{ repoID }}</p>
          <p class="mt-2 text-sm leading-6 text-[var(--neutral-800)]">{{ error }}</p>
        </div>
        <div class="flex w-full flex-wrap justify-start gap-2 sm:w-auto sm:shrink-0 sm:justify-end">
          <AppButton data-testid="discover-detail-back" intent="secondary" icon="i-lucide-arrow-left" @click="backToResults">Back to Discover</AppButton>
          <AppButton data-testid="discover-detail-retry" intent="primary" :loading="detailLoading" @click="openModel(repoID)">Retry</AppButton>
        </div>
      </div>
    </Frame>

    <template v-else-if="isDetail && selected">'''
if text.count(old) != 1:
    raise SystemExit(f'unexpected Discover detail state marker count: {text.count(old)}')
text = text.replace(old, new, 1)

old = '''        <div class="flex flex-wrap gap-2">
          <AppButton intent="secondary" icon="i-lucide-arrow-left" @click="backToResults">Back to Discover</AppButton>
          <AppButton intent="secondary" to="/downloads">Downloads</AppButton>
        </div>'''
new = '''        <div class="flex w-full flex-wrap justify-start gap-2 sm:w-auto sm:shrink-0 sm:justify-end" data-testid="discover-detail-actions">
          <AppButton intent="secondary" icon="i-lucide-arrow-left" @click="backToResults">Back to Discover</AppButton>
          <AppButton intent="secondary" to="/downloads">Downloads</AppButton>
        </div>'''
if text.count(old) != 1:
    raise SystemExit(f'unexpected Discover detail action marker count: {text.count(old)}')
page.write_text(text.replace(old, new, 1))

nav_test = Path('frontend/test/discover-navigation.nuxt.test.ts')
text = nav_test.read_text()
marker = '''  it('loads a model directly from /models/discover/:owner/:repo', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/huggingface/model?repo=Qwen%2FQwen3.8-Flash-Next') {
        return { id: 'Qwen/Qwen3.8-Flash-Next', downloads: 1, likes: 2, private: false, gated: false, revision: 'r1', artifacts: [] }
      }
      return []
    })

    const wrapper = await mountSuspended(DiscoverDetailPage, { route: '/models/discover/Qwen/Qwen3.8-Flash-Next' })
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/huggingface/model?repo=Qwen%2FQwen3.8-Flash-Next')
    expect(wrapper.text()).toContain('Qwen/Qwen3.8-Flash-Next')
    wrapper.unmount()
  })
'''
addition = marker + '''
  it('recovers a failed direct repository load with Retry and Back to Discover', async () => {
    let attempts = 0
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/huggingface/model?repo=Qwen%2FRecovery-GGUF') {
        attempts++
        if (attempts === 1) throw new Error('Repository temporarily unavailable')
        return { id: 'Qwen/Recovery-GGUF', downloads: 3, likes: 4, private: false, gated: false, revision: 'main', artifacts: [] }
      }
      if (path.startsWith('/api/v1/huggingface/recommendations?')) {
        return { context_length: 4096, context_capability: 4096, context_assumed: true, metadata: {}, hardware_available: false, hybrid_recommendations_enabled: false, artifacts: [] }
      }
      return []
    })

    const wrapper = await mountSuspended(DiscoverDetailPage, { route: '/models/discover/Qwen/Recovery-GGUF' })
    await flushPromises()
    const failure = wrapper.get('[data-testid="discover-detail-error"]')
    expect(failure.text()).toContain('Repository temporarily unavailable')
    expect(failure.text()).toContain('Qwen/Recovery-GGUF')

    await wrapper.get('[data-testid="discover-detail-retry"]').trigger('click')
    await flushPromises()
    expect(attempts).toBe(2)
    expect(wrapper.find('[data-testid="discover-detail-error"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('Qwen/Recovery-GGUF')

    await wrapper.get('[data-testid="discover-detail-actions"]').findAll('button')[0]!.trigger('click')
    await flushPromises()
    expect(mocks.navigateTo).toHaveBeenCalledWith('/models/discover')
    wrapper.unmount()
  })
'''
if text.count(marker) != 1:
    raise SystemExit(f'unexpected Discover navigation test marker count: {text.count(marker)}')
nav_test.write_text(text.replace(marker, addition, 1))

redesign_test = Path('frontend/test/discover-redesign.nuxt.test.ts')
text = redesign_test.read_text()
old = '''    expect(source).toContain('Registered models')
    expect(source).toContain('Downloads')'''
new = '''    expect(source).toContain('Registered models')
    expect(source).toContain('Downloads')
    expect(source).toContain('data-testid="discover-list-actions"')
    expect(source).toContain('data-testid="discover-detail-actions"')
    expect(source).toContain('w-full flex-wrap justify-start gap-2 sm:w-auto sm:shrink-0 sm:justify-end')'''
if text.count(old) != 1:
    raise SystemExit(f'unexpected Discover redesign test marker count: {text.count(old)}')
text = text.replace(old, new, 1)
old = '''    expect(source).toContain('clearRecommendationDebounce()')'''
new = '''    expect(source).toContain('clearRecommendationDebounce()')
    expect(source).toContain('data-testid="discover-detail-error"')
    expect(source).toContain('data-testid="discover-detail-retry"')
    expect(source).toContain('Repository unavailable')'''
if text.count(old) != 1:
    raise SystemExit(f'unexpected Discover recovery source marker count: {text.count(old)}')
redesign_test.write_text(text.replace(old, new, 1))

e2e = Path('frontend/e2e/redesign-screenshots.spec.ts')
text = e2e.read_text()
old = '''const dashboardFailurePages = new WeakSet<Page>()'''
new = '''const dashboardFailurePages = new WeakSet<Page>()
const discoverDetailFailurePages = new WeakSet<Page>()'''
if text.count(old) != 1:
    raise SystemExit(f'unexpected Discover E2E state marker count: {text.count(old)}')
text = text.replace(old, new, 1)

marker = '''    if (playgroundColdPages.has(page) && /^\\/api\\/v1\\/instances\\/[^/]+\\/runtime$/.test(url.pathname)) {'''
fixture = '''    if (discoverDetailFailurePages.has(page) && url.pathname === '/api/v1/huggingface/model') {
      await route.fulfill({ status: 503, headers: corsHeaders, body: JSON.stringify({ error: 'Repository temporarily unavailable for visual QA.' }) })
      return
    }
'''
if text.count(marker) != 1:
    raise SystemExit(f'unexpected Discover E2E fixture insertion marker count: {text.count(marker)}')
text = text.replace(marker, fixture + marker, 1)

marker = '''test('downloads lifecycle and files screenshot', async ({ page }, testInfo) => {'''
visual = '''test('discover repository failure and retry screenshot', async ({ page }, testInfo) => {
  discoverDetailFailurePages.add(page)
  await page.goto('/models/discover/Qwen/Qwen3-8B-GGUF', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  const failure = page.locator('[data-testid="discover-detail-error"]')
  await expect(failure).toContainText('Repository temporarily unavailable for visual QA.')
  await expect(failure).toContainText('Qwen/Qwen3-8B-GGUF')
  await expect(page.locator('[data-testid="discover-detail-back"]')).toBeVisible()
  await expect(page.locator('[data-testid="discover-detail-retry"]')).toBeVisible()
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/discover-detail-error.png`, fullPage: true, animations: 'disabled' })

  discoverDetailFailurePages.delete(page)
  await page.locator('[data-testid="discover-detail-retry"]').click()
  await expect(page.locator('[data-testid="discover-repository-header"]').getByRole('heading', { name: 'Qwen/Qwen3-8B-GGUF' })).toBeVisible()
  await expect(page.locator('[data-testid="discover-detail-error"]')).toBeHidden()
})


'''
if text.count(marker) != 1:
    raise SystemExit(f'unexpected Discover E2E visual insertion marker count: {text.count(marker)}')
if 'discover repository failure and retry screenshot' in text:
    raise SystemExit('Discover failure visual test already exists')
e2e.write_text(text.replace(marker, visual + marker, 1))
