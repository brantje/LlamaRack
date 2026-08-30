from pathlib import Path

page = Path('frontend/app/pages/models/[id]/details.vue')
text = page.read_text()
old = '<div class="flex items-start justify-between gap-6">'
new = '<div class="flex flex-wrap items-start justify-between gap-4" data-testid="model-details-header">'
if text.count(old) != 1:
    raise SystemExit(f'unexpected Model details header marker count: {text.count(old)}')
text = text.replace(old, new, 1)
old = '      <div class="flex flex-wrap justify-end gap-2">\n        <AppButton to="/models" intent="secondary">Back to models</AppButton>'
new = '      <div class="flex w-full flex-wrap justify-start gap-2 sm:w-auto sm:justify-end" data-testid="model-details-actions">\n        <AppButton to="/models" intent="secondary">Back to models</AppButton>'
if old not in text:
    raise SystemExit('missing Model details action marker')
page.write_text(text.replace(old, new, 1))

test = Path('frontend/test/model-details-redesign.nuxt.test.ts')
text = test.read_text()
old = "    expect(wrapper.text()).toContain('General metadata read directly from the registered GGUF. Runtime controls remain on Instances.')"
new = old + "\n\n    const header = wrapper.get('[data-testid=\"model-details-header\"]')\n    expect(header.classes()).toContain('flex-wrap')\n    const headerActions = wrapper.get('[data-testid=\"model-details-actions\"]')\n    expect(headerActions.classes()).toContain('w-full')\n    expect(headerActions.classes()).toContain('sm:w-auto')\n    expect(headerActions.classes()).toContain('sm:justify-end')"
if old not in text:
    raise SystemExit('missing Model details responsive test marker')
test.write_text(text.replace(old, new, 1))

e2e = Path('frontend/e2e/redesign-screenshots.spec.ts')
text = e2e.read_text()
marker = "  if (pathname === '/api/v1/instances' && method === 'GET') return instances.slice(0, 2)"
fixture = "  if (pathname === '/api/v1/models/qwen3-8b-q4km/details/value') return { key: 'tokenizer.ggml.tokens', type: 'array[string]', items: ['<|endoftext|>', '<|im_start|>', '<|im_end|>', 'hello'], offset: 0, limit: 100, total: 4, has_more: false }\n"
if marker not in text:
    raise SystemExit('missing Model details value fixture insertion marker')
if "/api/v1/models/qwen3-8b-q4km/details/value" in text:
    raise SystemExit('Model details value fixture already exists')
text = text.replace(marker, fixture + marker, 1)
marker = "test('downloads lifecycle and files screenshot', async ({ page }, testInfo) => {"
visual_test = """test('model details expanded metadata screenshot', async ({ page }, testInfo) => {
  await page.goto('/models/qwen3-8b-q4km/details', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  await expect(page.getByRole('heading', { name: 'Qwen3 8B' })).toBeVisible()
  await page.locator('[data-testid=\"metadata-expand\"]').first().click()
  await expect(page.locator('[data-testid=\"metadata-expanded-items\"]')).toContainText('<|endoftext|>')
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/model-details-expanded.png`, fullPage: true, animations: 'disabled' })
})


"""
if marker not in text:
    raise SystemExit('missing Model details visual-test insertion marker')
if "model details expanded metadata screenshot" in text:
    raise SystemExit('Model details expanded visual test already exists')
e2e.write_text(text.replace(marker, visual_test + marker, 1))
