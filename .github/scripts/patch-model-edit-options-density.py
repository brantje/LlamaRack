from pathlib import Path

editor = Path('frontend/app/components/LlamaCppOptionsEditor.vue')
source = editor.read_text()


def replace_once(old: str, new: str, label: str) -> None:
    global source
    count = source.count(old)
    if count != 1:
        raise SystemExit(f'{label}: expected one match, found {count}')
    source = source.replace(old, new, 1)

replace_once(
    "  if (count === 0) return 'No overrides configured · inheriting all values'",
    "  if (count === 0) return 'No overrides configured · inherited options available below'",
    'editor summary clarity',
)

replace_once(
    """const visibleOptions = computed(() => {
  const term = search.value.trim().toLowerCase()
  return allOptions.value.filter((option) => {
    if (mode.value === 'basic' && (!basicKeys.has(option.key) || isProtected(option))) return false
    if (!term) return true
    return option.key.toLowerCase().includes(term) || (option.description || '').toLowerCase().includes(term)
  })
})
""",
    """const visibleOptions = computed(() => {
  const term = search.value.trim().toLowerCase()
  return allOptions.value.filter((option) => {
    if (mode.value === 'basic' && (!basicKeys.has(option.key) || isProtected(option))) return false
    if (!term) return true
    return option.key.toLowerCase().includes(term) || (option.description || '').toLowerCase().includes(term)
  })
})
const visibleOverrides = computed(() => visibleOptions.value.filter(option => isOverridden(option.key)))
const visibleInheritedOptions = computed(() => visibleOptions.value.filter(option => !isOverridden(option.key)))
""",
    'visible option groups',
)

replace_once(
    """            <UButton type="button" :variant="mode === 'basic' ? 'solid' : 'soft'" size="sm" @click="mode = 'basic'">Basic</UButton>
            <UButton type="button" :variant="mode === 'advanced' ? 'solid' : 'soft'" size="sm" @click="mode = 'advanced'">Advanced</UButton>
""",
    """            <UButton type="button" :variant="mode === 'basic' ? 'solid' : 'soft'" size="sm" :aria-pressed="mode === 'basic'" @click="mode = 'basic'">Basic</UButton>
            <UButton type="button" :variant="mode === 'advanced' ? 'solid' : 'soft'" size="sm" :aria-pressed="mode === 'advanced'" @click="mode = 'advanced'">Advanced</UButton>
""",
    'mode pressed semantics',
)

old_block = """        <div v-else class="space-y-2">
          <div v-for="option in visibleOptions" :key="option.key" class="border border-[var(--color-divider)] bg-[var(--color-surface)] p-4">
            <div class="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
              <div class="min-w-0 flex-1">
                <div class="flex flex-wrap items-center gap-2">
                  <code class="font-mono text-sm font-semibold">--{{ option.key }}</code>
                  <StatusTag :variant="sourceVariant(effectiveSource(option))">{{ effectiveSource(option) }}</StatusTag>
                  <StatusTag v-if="isProtected(option)" variant="neutral">Manager controlled</StatusTag>
                  <StatusTag v-if="option.unsupported" variant="failed">Unsupported · retained</StatusTag>
                </div>
                <p v-if="option.description" class="mt-1 text-xs text-muted">{{ option.description }}</p>
                <p v-if="!isOverridden(option.key) && effectiveValue(option.key) !== undefined" class="mt-1 text-xs text-dimmed">Effective inherited value: <code>{{ effectiveValue(option.key) }}</code></p>
                <p v-else-if="!isOverridden(option.key)" class="mt-1 text-xs text-dimmed">Using llama.cpp upstream default.</p>
              </div>

              <div class="w-full space-y-2 lg:w-80">
                <template v-if="isOverridden(option.key)">
                  <UCheckbox
                    v-if="kind(option) === 'boolean' && !option.unsupported"
                    :model-value="overrides[option.key] === 'true'"
                    :label="overrides[option.key] === 'true' ? 'Enabled' : 'Disabled'"
                    @update:model-value="updateValue(option.key, $event ? 'true' : 'false')"
                  />
                  <USelectMenu
                    v-else-if="kind(option) === 'enum' && !option.unsupported"
                    :model-value="overrides[option.key]"
                    class="w-full"
                    :items="choices(option)"
                    @update:model-value="updateValue(option.key, String($event || ''))"
                  />
                  <UInput
                    v-else
                    :model-value="overrides[option.key]"
                    class="w-full font-mono"
                    :disabled="option.unsupported"
                    :placeholder="option.value_hint || 'value'"
                    @update:model-value="updateValue(option.key, String($event || ''))"
                  />
                  <UButton type="button" size="xs" color="neutral" variant="ghost" @click="removeOverride(option.key)">Remove override</UButton>
                </template>
                <UButton v-else-if="!isProtected(option)" type="button" size="xs" color="neutral" variant="soft" @click="enableOverride(option)">Override here</UButton>
              </div>
            </div>
          </div>
          <Frame v-if="!visibleOptions.length" class="p-3">
            <div class="flex items-start gap-2"><StatusTag variant="neutral">No options</StatusTag><p class="text-xs leading-5 text-[var(--neutral-800)]">No options match this view. Switch to Advanced to see every detected option.</p></div>
          </Frame>
        </div>
"""
new_block = """        <div v-else class="space-y-4">
          <section v-if="visibleOverrides.length" data-testid="llamacpp-configured-overrides">
            <div class="mb-2 flex items-center justify-between gap-3">
              <h3 class="text-xs font-semibold uppercase tracking-[.08em] text-[var(--neutral-800)]">Configured overrides</h3>
              <span class="font-mono text-xs tabular-nums text-[var(--neutral-700)]">{{ visibleOverrides.length }}</span>
            </div>
            <div class="border border-[var(--color-divider)]">
              <div v-for="option in visibleOverrides" :key="option.key" class="border-t border-[var(--color-divider)] p-3 first:border-t-0" data-testid="llamacpp-option-row" data-option-state="override">
                <div class="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
                  <div class="min-w-0 flex-1">
                    <div class="flex flex-wrap items-center gap-2">
                      <code class="font-mono text-sm font-semibold">--{{ option.key }}</code>
                      <StatusTag :variant="sourceVariant(effectiveSource(option))">{{ effectiveSource(option) }}</StatusTag>
                      <StatusTag v-if="isProtected(option)" variant="neutral">Manager controlled</StatusTag>
                      <StatusTag v-if="option.unsupported" variant="failed">Unsupported · retained</StatusTag>
                    </div>
                    <p v-if="option.description" class="mt-1 text-xs text-muted">{{ option.description }}</p>
                  </div>

                  <div class="w-full space-y-2 lg:w-72">
                    <UCheckbox
                      v-if="kind(option) === 'boolean' && !option.unsupported"
                      :model-value="overrides[option.key] === 'true'"
                      :label="overrides[option.key] === 'true' ? 'Enabled' : 'Disabled'"
                      @update:model-value="updateValue(option.key, $event ? 'true' : 'false')"
                    />
                    <USelectMenu
                      v-else-if="kind(option) === 'enum' && !option.unsupported"
                      :model-value="overrides[option.key]"
                      class="w-full"
                      :items="choices(option)"
                      @update:model-value="updateValue(option.key, String($event || ''))"
                    />
                    <UInput
                      v-else
                      :model-value="overrides[option.key]"
                      class="w-full font-mono"
                      :disabled="option.unsupported"
                      :placeholder="option.value_hint || 'value'"
                      @update:model-value="updateValue(option.key, String($event || ''))"
                    />
                    <AppButton type="button" size="xs" intent="ghost" @click="removeOverride(option.key)">Remove override</AppButton>
                  </div>
                </div>
              </div>
            </div>
          </section>

          <section v-if="visibleInheritedOptions.length" data-testid="llamacpp-inherited-options">
            <div class="mb-2 flex items-center justify-between gap-3">
              <div>
                <h3 class="text-xs font-semibold uppercase tracking-[.08em] text-[var(--neutral-800)]">Available inherited options</h3>
                <p class="mt-1 text-xs text-dimmed">These values are not stored here until you choose Override.</p>
              </div>
              <span class="font-mono text-xs tabular-nums text-[var(--neutral-700)]">{{ visibleInheritedOptions.length }}</span>
            </div>
            <div class="border border-[var(--color-divider)]">
              <div v-for="option in visibleInheritedOptions" :key="option.key" class="flex flex-col gap-2 border-t border-[var(--color-divider)] p-3 first:border-t-0 sm:flex-row sm:items-center sm:justify-between" data-testid="llamacpp-option-row" data-option-state="inherited">
                <div class="min-w-0 flex-1">
                  <div class="flex flex-wrap items-center gap-2">
                    <code class="font-mono text-sm font-semibold">--{{ option.key }}</code>
                    <StatusTag :variant="sourceVariant(effectiveSource(option))">{{ effectiveSource(option) }}</StatusTag>
                    <StatusTag v-if="isProtected(option)" variant="neutral">Manager controlled</StatusTag>
                  </div>
                  <p v-if="option.description" class="mt-1 text-xs text-muted">{{ option.description }}</p>
                  <p v-if="effectiveValue(option.key) !== undefined" class="mt-1 text-xs text-dimmed">Inherited value: <code>{{ effectiveValue(option.key) }}</code></p>
                  <p v-else class="mt-1 text-xs text-dimmed">Using llama.cpp upstream default.</p>
                </div>
                <AppButton v-if="!isProtected(option) && !option.unsupported" type="button" size="xs" intent="secondary" class="shrink-0 self-start sm:self-center" @click="enableOverride(option)">Override</AppButton>
              </div>
            </div>
          </section>

          <Frame v-if="!visibleOptions.length" class="p-3">
            <div class="flex items-start gap-2"><StatusTag variant="neutral">No options</StatusTag><p class="text-xs leading-5 text-[var(--neutral-800)]">No options match this view. Switch to Advanced to see every detected option.</p></div>
          </Frame>
        </div>
"""
replace_once(old_block, new_block, 'compact option sections')
editor.write_text(source)

unit = Path('frontend/test/llamacpp-override-order.nuxt.test.ts')
text = unit.read_text()
marker = """    expect(optionKeys(wrapper)).toEqual([
      '--batch-size',
      '--threads',
      '--ctx-size',
      '--flash-attn'
    ])
"""
addition = marker + """
    expect(wrapper.get('[data-testid=\"llamacpp-configured-overrides\"]').text()).toContain('Configured overrides')
    expect(wrapper.get('[data-testid=\"llamacpp-inherited-options\"]').text()).toContain('Available inherited options')
    expect(wrapper.findAll('[data-testid=\"llamacpp-option-row\"][data-option-state=\"override\"]')).toHaveLength(2)
    expect(wrapper.findAll('[data-testid=\"llamacpp-option-row\"][data-option-state=\"inherited\"]')).toHaveLength(2)
    const modeButtons = wrapper.findAll('button').filter(button => ['Basic', 'Advanced'].includes(button.text()))
    expect(modeButtons.find(button => button.text() === 'Basic')?.attributes('aria-pressed')).toBe('true')
    expect(modeButtons.find(button => button.text() === 'Advanced')?.attributes('aria-pressed')).toBe('false')
"""
if text.count(marker) != 1:
    raise SystemExit(f'override-order test marker: expected one match, found {text.count(marker)}')
unit.write_text(text.replace(marker, addition, 1))

e2e = Path('frontend/e2e/redesign-screenshots.spec.ts')
text = e2e.read_text()

old = "const dashboardFailurePages = new WeakSet<Page>()\n"
new = "const dashboardFailurePages = new WeakSet<Page>()\nconst modelEditSaveFailurePages = new WeakSet<Page>()\n"
if text.count(old) != 1:
    raise SystemExit(f'model edit failure state marker: expected one match, found {text.count(old)}')
text = text.replace(old, new, 1)

marker = "  if (pathname === '/api/v1/models/inspect') return { dependencies: [], suggested_options: {} }\n"
fixture = """  if (pathname === '/api/v1/models/inspect') return { dependencies: [], suggested_options: {} }
  if (pathname === '/api/v1/models/qwen3-8b-q4km') return models[0]
  if (pathname === '/api/v1/models/qwen3-8b-q4km/options') return { 'ctx-size': '24576', 'flash-attn': 'true', parallel: '2' }
"""
if text.count(marker) != 1:
    raise SystemExit(f'model edit fixture marker: expected one match, found {text.count(marker)}')
text = text.replace(marker, fixture, 1)

old = "  if (pathname === '/api/v1/llamacpp/config') return { profile: llamaProfile, effective: { global: {}, values: {}, sources: {} } }"
new = "  if (pathname === '/api/v1/llamacpp/config') return { profile: llamaProfile, effective: { global: { threads: '64' }, values: { threads: '64' }, sources: { threads: 'global' } } }"
if text.count(old) != 1:
    raise SystemExit(f'llamacpp config fixture marker: expected one match, found {text.count(old)}')
text = text.replace(old, new, 1)

marker = """    const url = new URL(request.url())
    if (instancesStatePages.has(page) && url.pathname === '/api/v1/instances' && request.method() === 'GET') {
"""
route_patch = """    const url = new URL(request.url())
    if (modelEditSaveFailurePages.has(page) && url.pathname === '/api/v1/models/qwen3-8b-q4km' && request.method() === 'PUT') {
      await route.fulfill({ status: 422, headers: corsHeaders, body: JSON.stringify({ error: 'Representative Model option validation failure for visual QA.' }) })
      return
    }
    if (instancesStatePages.has(page) && url.pathname === '/api/v1/instances' && request.method() === 'GET') {
"""
if text.count(marker) != 1:
    raise SystemExit(f'model edit failure route marker: expected one match, found {text.count(marker)}')
text = text.replace(marker, route_patch, 1)

old = """    if (name === 'profile') {
"""
new = """    if (name === 'model-edit') {
      await expect(page.locator('[data-testid=\"llamacpp-option-row\"][data-option-state=\"override\"]')).toHaveCount(3)
      await expect(page.locator('[data-testid=\"llamacpp-inherited-options\"]')).toBeVisible()
      await expect(page.locator('[data-testid=\"model-edit-submit-hint\"]')).toContainText('No changes to save.')
    }
    if (name === 'profile') {
"""
if text.count(old) != 1:
    raise SystemExit(f'model edit readiness marker: expected one match, found {text.count(old)}')
text = text.replace(old, new, 1)

anchor = "\ntest('downloads lifecycle and files screenshot', async ({ page }, testInfo) => {\n"
visual = """

test('model edit dirty and save failure screenshots', async ({ page }, testInfo) => {
  modelEditSaveFailurePages.add(page)
  await page.goto('/models/qwen3-8b-q4km/edit', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  await expect(page.locator('[data-testid="llamacpp-option-row"][data-option-state="override"]')).toHaveCount(3)
  const nameInput = page.getByRole('textbox', { name: 'Model name' })
  await nameInput.fill('Qwen3 8B tuned')
  await expect(page.locator('[data-testid="model-edit-submit-hint"]')).toContainText('Unsaved changes.')
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/model-edit-dirty.png`, fullPage: true, animations: 'disabled' })

  await page.getByRole('button', { name: 'Save Model' }).click()
  await expect(page.locator('[data-testid="model-edit-error"]')).toContainText('Representative Model option validation failure for visual QA.')
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/model-edit-save-failure.png`, fullPage: true, animations: 'disabled' })
})
"""
if text.count(anchor) != 1:
    raise SystemExit(f'model edit visual anchor: expected one match, found {text.count(anchor)}')
text = text.replace(anchor, visual + anchor, 1)
e2e.write_text(text)
