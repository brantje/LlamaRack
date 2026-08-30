from pathlib import Path

page = Path('frontend/app/pages/admin/huggingface.vue')
source = page.read_text()


def replace_once(old: str, new: str, label: str) -> None:
    global source
    count = source.count(old)
    if count != 1:
        raise SystemExit(f'{label}: expected one match, found {count}')
    source = source.replace(old, new, 1)

replace_once(
    "const saved = ref(false)\n",
    """const saved = ref(false)
const confirmation = ref<{ request: (options: { title: string; description: string; confirmLabel?: string; confirmTone?: 'default' | 'destructive' }) => Promise<boolean> } | null>(null)
""",
    'confirmation ref',
)

replace_once(
    """async function remove() {
  busy.value = true
""",
    """async function remove() {
  const confirmed = await confirmation.value?.request({
    title: 'Remove Hugging Face credential?',
    description: 'Private and gated Hugging Face repository access will stop until another credential is configured.',
    confirmLabel: 'Remove credential',
    confirmTone: 'destructive'
  })
  if (!confirmed) return
  busy.value = true
""",
    'remove confirmation',
)

replace_once(
    "<AppButton v-if=\"tokenStatus.configured\" intent=\"secondary\" :disabled=\"busy\" @click=\"remove\">Remove</AppButton>",
    "<AppButton v-if=\"tokenStatus.configured\" intent=\"secondary\" tone=\"destructive\" :disabled=\"busy\" @click=\"remove\">Remove</AppButton>",
    'destructive remove tone',
)

replace_once(
    """    </Frame>
  </AdminShell>
</template>
""",
    """    </Frame>
    <AppConfirmationModal ref="confirmation" />
  </AdminShell>
</template>
""",
    'confirmation modal',
)
page.write_text(source)

unit = Path('frontend/test/phase8.nuxt.test.ts')
text = unit.read_text()

marker = """function button(wrapper: any, text: string) {
  const found = wrapper.findAll('button').find((item: any) => item.text().trim() === text)
  if (!found) throw new Error(`Missing button ${text}`)
  return found
}
"""
addition = marker + """
async function confirmRemove(confirm = true) {
  await flushPromises()
  const selector = confirm ? '[data-testid="confirmation-confirm"]' : '[data-testid="confirmation-cancel"]'
  const control = [...document.body.querySelectorAll<HTMLButtonElement>(selector)].at(-1)
  if (!control) throw new Error(`Missing ${confirm ? 'confirmation' : 'cancellation'} button`)
  control.click()
  await flushPromises()
}
"""
if text.count(marker) != 1:
    raise SystemExit(f'phase8 helper marker: expected one match, found {text.count(marker)}')
text = text.replace(marker, addition, 1)

old = """    await button(wrapper, 'Remove').trigger('click')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/huggingface/token', { method: 'DELETE' })
"""
new = """    await button(wrapper, 'Remove').trigger('click')
    await confirmRemove()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/huggingface/token', { method: 'DELETE' })
"""
if text.count(old) != 1:
    raise SystemExit(f'phase8 success remove marker: expected one match, found {text.count(old)}')
text = text.replace(old, new, 1)

old = """      await button(candidate, 'Remove').trigger('click')
      await flushPromises()
      expect(candidate.text()).toContain(expected)
"""
new = """      await button(candidate, 'Remove').trigger('click')
      await confirmRemove()
      expect(candidate.text()).toContain(expected)
"""
if text.count(old) != 1:
    raise SystemExit(f'phase8 remove error marker: expected one match, found {text.count(old)}')
text = text.replace(old, new, 1)

anchor = """  it('surfaces load, save and remove error variants', async () => {
"""
cancel_test = """  it('keeps the configured credential when removal is cancelled', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/huggingface/token') return { configured: true, prefix: 'hf_abc' }
      return []
    })
    const wrapper = await mountSuspended(AdminHuggingFacePage, { route: false })
    await flushPromises()
    mocks.request.mockClear()
    await button(wrapper, 'Remove').trigger('click')
    await confirmRemove(false)
    expect(mocks.request).not.toHaveBeenCalledWith('/api/v1/huggingface/token', { method: 'DELETE' })
    expect(wrapper.text()).toContain('Configured')
    wrapper.unmount()
  })

"""
if text.count(anchor) != 1:
    raise SystemExit(f'phase8 cancel insertion marker: expected one match, found {text.count(anchor)}')
text = text.replace(anchor, cancel_test + anchor, 1)
unit.write_text(text)

e2e = Path('frontend/e2e/redesign-screenshots.spec.ts')
text = e2e.read_text()

old = "const dashboardFailurePages = new WeakSet<Page>()\n"
new = "const dashboardFailurePages = new WeakSet<Page>()\nconst huggingFaceTokenSaveFailurePages = new WeakSet<Page>()\n"
if text.count(old) != 1:
    raise SystemExit(f'HF failure state marker: expected one match, found {text.count(old)}')
text = text.replace(old, new, 1)

marker = """    const url = new URL(request.url())
    if (instancesStatePages.has(page) && url.pathname === '/api/v1/instances' && request.method() === 'GET') {
"""
route_patch = """    const url = new URL(request.url())
    if (huggingFaceTokenSaveFailurePages.has(page) && url.pathname === '/api/v1/huggingface/token' && request.method() === 'PUT') {
      await route.fulfill({ status: 422, headers: corsHeaders, body: JSON.stringify({ error: 'Representative Hugging Face credential save failure for visual QA.' }) })
      return
    }
    if (instancesStatePages.has(page) && url.pathname === '/api/v1/instances' && request.method() === 'GET') {
"""
if text.count(marker) != 1:
    raise SystemExit(f'HF failure route marker: expected one match, found {text.count(marker)}')
text = text.replace(marker, route_patch, 1)

anchor = "\ntest('downloads lifecycle and files screenshot', async ({ page }, testInfo) => {\n"
visual = """

test('Hugging Face configured credential and removal confirmation screenshots', async ({ page }, testInfo) => {
  await page.goto('/admin/huggingface', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  await expect(page.locator('[data-testid="admin-huggingface-card"]')).toContainText('Configured')
  await expect(page.getByRole('button', { name: 'Replace' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Remove' })).toBeVisible()
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/admin-huggingface-configured.png`, fullPage: true, animations: 'disabled' })

  await page.getByRole('button', { name: 'Remove' }).click()
  await expect(page.locator('[data-testid="confirmation-confirm"]')).toContainText('Remove credential')
  await expect(page.getByText('Private and gated Hugging Face repository access will stop', { exact: false })).toBeVisible()
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/admin-huggingface-remove-confirmation.png`, fullPage: true, animations: 'disabled' })
  await page.locator('[data-testid="confirmation-cancel"]').click()
})


test('Hugging Face credential save success and failure screenshots', async ({ page }, testInfo) => {
  await page.goto('/admin/huggingface', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  const input = page.locator('input[placeholder="hf_…"]')
  await input.fill('hf_visual_replacement')
  await page.getByRole('button', { name: 'Replace' }).click()
  await expect(page.locator('[data-testid="admin-huggingface-card"]')).toContainText('Hugging Face token saved.')
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/admin-huggingface-save-success.png`, fullPage: true, animations: 'disabled' })

  huggingFaceTokenSaveFailurePages.add(page)
  await input.fill('hf_visual_failure')
  await page.getByRole('button', { name: 'Replace' }).click()
  await expect(page.locator('[data-testid="admin-huggingface-card"]')).toContainText('Representative Hugging Face credential save failure for visual QA.')
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/admin-huggingface-save-failure.png`, fullPage: true, animations: 'disabled' })
})
"""
if text.count(anchor) != 1:
    raise SystemExit(f'HF visual insertion marker: expected one match, found {text.count(anchor)}')
text = text.replace(anchor, visual + anchor, 1)
e2e.write_text(text)
