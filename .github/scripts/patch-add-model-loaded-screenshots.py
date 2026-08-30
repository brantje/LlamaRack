from pathlib import Path

path = Path("frontend/e2e/redesign-screenshots.spec.ts")
source = path.read_text()


def replace_once(old: str, new: str, label: str) -> None:
    global source
    if new in source:
        return
    count = source.count(old)
    if count != 1:
        raise SystemExit(f"{label}: expected one match, found {count}")
    source = source.replace(old, new, 1)


replace_once(
    "const dashboardFailurePages = new WeakSet<Page>()\n",
    "const dashboardFailurePages = new WeakSet<Page>()\nconst modelInspectFailurePages = new WeakSet<Page>()\n",
    "model inspect failure page state",
)

replace_once(
    "  if (pathname === '/api/v1/models/available') return []\n  if (pathname === '/api/v1/models/inspect') return { dependencies: [], suggested_options: {} }\n",
    """  if (pathname === '/api/v1/models/available') return [{
    path: '/models/Qwen3-Vision-8B-Q4_K_M.gguf', name: 'Qwen3 Vision 8B', total_bytes: 5_420_000_000,
    modified_at: new Date(now - 300_000).toISOString(), quantization: 'Q4_K_M',
    suggested_options: {
      mmproj: '/models/mmproj-Qwen3-Vision-F16.gguf',
      'spec-draft-model': '/models/Qwen3-0.6B-MTP-Q8_0.gguf',
      'spec-type': 'draft-mtp'
    }
  }]
  if (pathname === '/api/v1/models/inspect') return {
    id: 'local-qwen3-vision', name: 'Qwen3-Vision-8B-Q4_K_M.gguf', model_name: 'Qwen3 Vision 8B',
    architecture: 'qwen3', context_length: 32768, gguf_version: 3, metadata_count: 42,
    model_bytes: 5_420_000_000, total_bytes: 6_340_000_000, shard_count: 1, expected_shards: 1, complete: true,
    files: [{ path: '/models/Qwen3-Vision-8B-Q4_K_M.gguf', size: 5_420_000_000 }],
    suggested_options: {
      mmproj: '/models/mmproj-Qwen3-Vision-F16.gguf',
      'spec-draft-model': '/models/Qwen3-0.6B-MTP-Q8_0.gguf',
      'spec-type': 'draft-mtp'
    },
    dependencies: [
      { kind: 'mmproj', name: 'mmproj-Qwen3-Vision-F16.gguf', total_bytes: 620_000_000, files: [{ path: '/models/mmproj-Qwen3-Vision-F16.gguf', size: 620_000_000 }], option_path: '/models/mmproj-Qwen3-Vision-F16.gguf' },
      { kind: 'mtp', name: 'Qwen3-0.6B-MTP-Q8_0.gguf', quantization: 'Q8_0', total_bytes: 300_000_000, files: [{ path: '/models/Qwen3-0.6B-MTP-Q8_0.gguf', size: 300_000_000 }], option_path: '/models/Qwen3-0.6B-MTP-Q8_0.gguf' }
    ],
    dependency_candidates: [
      { kind: 'mmproj', name: 'mmproj-Qwen3-Vision-F16.gguf', total_bytes: 620_000_000, files: [{ path: '/models/mmproj-Qwen3-Vision-F16.gguf', size: 620_000_000 }], option_path: '/models/mmproj-Qwen3-Vision-F16.gguf' },
      { kind: 'mmproj', name: 'mmproj-Qwen3-Vision-Q8_0.gguf', quantization: 'Q8_0', total_bytes: 410_000_000, files: [{ path: '/models/mmproj-Qwen3-Vision-Q8_0.gguf', size: 410_000_000 }], option_path: '/models/mmproj-Qwen3-Vision-Q8_0.gguf' },
      { kind: 'mtp', name: 'Qwen3-0.6B-MTP-Q8_0.gguf', quantization: 'Q8_0', total_bytes: 300_000_000, files: [{ path: '/models/Qwen3-0.6B-MTP-Q8_0.gguf', size: 300_000_000 }], option_path: '/models/Qwen3-0.6B-MTP-Q8_0.gguf' },
      { kind: 'mtp', name: 'Qwen3-0.6B-MTP-Q4_K_M.gguf', quantization: 'Q4_K_M', total_bytes: 185_000_000, files: [{ path: '/models/Qwen3-0.6B-MTP-Q4_K_M.gguf', size: 185_000_000 }], option_path: '/models/Qwen3-0.6B-MTP-Q4_K_M.gguf' }
    ]
  }
""",
    "populated Add model fixture",
)

replace_once(
    """    const url = new URL(request.url())
    if (instancesStatePages.has(page) && url.pathname === '/api/v1/instances' && request.method() === 'GET') {
""",
    """    const url = new URL(request.url())
    if (modelInspectFailurePages.has(page) && url.pathname === '/api/v1/models/inspect' && request.method() === 'POST') {
      await route.fulfill({ status: 422, headers: corsHeaders, body: JSON.stringify({ error: 'Representative GGUF metadata inspection failure for visual QA.' }) })
      return
    }
    if (instancesStatePages.has(page) && url.pathname === '/api/v1/instances' && request.method() === 'GET') {
""",
    "metadata inspection failure route",
)

replace_once(
    """    await page.goto(path, { waitUntil: 'domcontentloaded' })
    await waitForManagerPanel(page)
    if (name === 'profile') {
""",
    """    await page.goto(path, { waitUntil: 'domcontentloaded' })
    await waitForManagerPanel(page)
    if (name === 'model-new') {
      const option = page.locator('[data-testid=\"gguf-option\"]').first()
      await expect(option).toBeVisible()
      await option.click()
      await expect(page.locator('[data-testid=\"model-name\"]')).toHaveValue('Qwen3 Vision 8B')
      await expect(page.locator('[data-testid^=\"companion-candidate-\"]')).toHaveCount(4)
    }
    if (name === 'profile') {
""",
    "Add model loaded-state readiness",
)

replace_once(
    """    await page.waitForTimeout(800)
    await page.screenshot({
""",
    """    if (name !== 'model-new') await page.waitForTimeout(800)
    await page.screenshot({
""",
    "generic model-new fixed wait",
)

anchor = """\ntest('downloads lifecycle and files screenshot', async ({ page }, testInfo) => {\n"""
addition = """

test('model-new metadata inspection failure screenshot', async ({ page }, testInfo) => {
  modelInspectFailurePages.add(page)
  await page.goto('/models/new', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  const option = page.locator('[data-testid="gguf-option"]').first()
  await expect(option).toBeVisible()
  await option.click()
  await expect(page.locator('[data-testid="metadata-warning"]')).toContainText('Representative GGUF metadata inspection failure for visual QA.')
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/model-new-metadata-failure.png`, fullPage: true, animations: 'disabled' })
})


test('model-new Hugging Face remote screenshot', async ({ page }, testInfo) => {
  await page.goto('/models/new?repo=Qwen%2FQwen3-8B-GGUF&artifact=q4_k_m', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  const artifact = page.locator('[data-testid="remote-artifact-summary"]')
  await expect(artifact).toContainText('Qwen/Qwen3-8B-GGUF')
  await expect(artifact).toContainText('Qwen3-8B-Q4_K_M.gguf')
  await expect(page.locator('[data-testid="model-name"]')).toHaveValue('Qwen3-8B-GGUF Q4_K_M')
  await expect(page.locator('[data-testid="model-form-first-instance"]')).toBeVisible()
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/model-new-remote.png`, fullPage: true, animations: 'disabled' })
})
"""
replace_once(anchor, addition + anchor, "dedicated Add model visual states")

path.write_text(source)
