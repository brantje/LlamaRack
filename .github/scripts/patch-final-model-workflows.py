from pathlib import Path

page = Path('frontend/app/pages/models/new.vue')
text = page.read_text()
replacements = [
    (
        '<div class="flex items-start justify-between gap-6">',
        '<div class="flex flex-wrap items-start justify-between gap-4" data-testid="model-add-header">'
    ),
    (
        '<p class="mt-2 text-[10.5px] text-[var(--neutral-700)]">value cleared — the flag is not passed</p>',
        '<p class="mt-2 text-[10.5px] text-[var(--neutral-800)]" :data-testid="`companion-disabled-${definition.kind}`">value cleared — the flag is not passed</p>'
    ),
    (
        '<p v-else class="mt-4 text-xs text-[var(--neutral-700)]">No compatible {{ definition.title.toLowerCase() }} was detected in this artifact scope.</p>',
        '<p v-else class="mt-4 text-xs text-[var(--neutral-800)]" :data-testid="`companion-empty-${definition.kind}`">No compatible {{ definition.title.toLowerCase() }} was detected in this artifact scope.</p>'
    ),
    (
        '<AppButton v-if="!remoteMode" type="button" intent="secondary" :loading="scanning" @click="scanGGUFs">Rescan</AppButton>',
        '<AppButton v-if="!remoteMode" type="button" intent="ghost" :loading="scanning" @click="scanGGUFs">Rescan</AppButton>'
    ),
]
for old, new in replacements:
    if old not in text:
        raise SystemExit(f'missing Add Model marker: {old[:100]!r}')
    text = text.replace(old, new, 1)
page.write_text(text)

test = Path('frontend/test/model-add-redesign.nuxt.test.ts')
text = test.read_text()
old = "import ModelOverridesEditor from '~/components/ModelOverridesEditor.vue'"
new = old + "\nimport AppButton from '~/components/AppButton.vue'"
if old not in text or "import AppButton from '~/components/AppButton.vue'" in text:
    raise SystemExit('unexpected Add Model test import state')
text = text.replace(old, new, 1)
old = "    expect(wrapper.get('[data-testid=\"model-submit-requirements\"]').text()).toContain('Required: a GGUF artifact and Model name.')"
new = """    expect(wrapper.get('[data-testid=\"model-submit-requirements\"]').text()).toContain('Required: a GGUF artifact and Model name.')
    expect(wrapper.get('[data-testid=\"model-add-header\"]').classes()).toContain('flex-wrap')
    const rescans = wrapper.findAllComponents(AppButton).filter(button => button.text() === 'Rescan')
    expect(rescans.map(button => button.props('intent'))).toEqual(['secondary', 'ghost'])"""
if old not in text:
    raise SystemExit('missing Add Model hierarchy test marker')
text = text.replace(old, new, 1)
old = "    expect(wrapper.get('[data-testid=\"companion-mtp\"]').text()).toContain('None found')"
new = """    expect(wrapper.get('[data-testid=\"companion-mtp\"]').text()).toContain('None found')
    expect(wrapper.get('[data-testid=\"companion-empty-mmproj\"]').classes()).toContain('text-[var(--neutral-800)]')
    expect(wrapper.get('[data-testid=\"companion-empty-mtp\"]').classes()).toContain('text-[var(--neutral-800)]')"""
if old not in text:
    raise SystemExit('missing Add Model empty companion test marker')
text = text.replace(old, new, 1)
old = "    expect(projectorSlot.text()).toContain('value cleared — the flag is not passed')"
new = """    expect(projectorSlot.text()).toContain('value cleared — the flag is not passed')
    expect(projectorSlot.get('[data-testid=\"companion-disabled-mmproj\"]').classes()).toContain('text-[var(--neutral-800)]')"""
if old not in text:
    raise SystemExit('missing Add Model disabled companion test marker')
test.write_text(text.replace(old, new, 1))

models = Path('frontend/app/pages/models/index.vue')
text = models.read_text()
old = "const message = ref('')\nconst pending = ref<string | null>(null)"
new = """const message = ref('')
const messageTitle = ref('')
const pending = ref<string | null>(null)

function clearMessage() {
  message.value = ''
  messageTitle.value = ''
}

function showCopyError(value: string) {
  messageTitle.value = 'Unable to copy model path'
  message.value = value
}"""
if old not in text:
    raise SystemExit('missing Models message state marker')
text = text.replace(old, new, 1)
old = "  pending.value = id\n  message.value = ''"
new = "  pending.value = id\n  clearMessage()"
if old not in text:
    raise SystemExit('missing Models remove clear marker')
text = text.replace(old, new, 1)
old = "  } catch (error: any) {\n    message.value = error?.data?.error || error?.message || 'Unable to delete model'"
new = "  } catch (error: any) {\n    messageTitle.value = 'Unable to delete model'\n    message.value = error?.data?.error || error?.message || 'Unable to delete model'"
if old not in text:
    raise SystemExit('missing Models remove error marker')
text = text.replace(old, new, 1)
old = '<p class="text-sm font-semibold text-[var(--accent-900)]">Unable to delete model</p>'
new = '<p class="text-sm font-semibold text-[var(--accent-900)]">{{ messageTitle || \'Model operation failed\' }}</p>'
if old not in text:
    raise SystemExit('missing Models error title marker')
text = text.replace(old, new, 1)
old = '<td class="min-w-[260px] max-w-[420px] break-words px-4 py-3 font-mono text-[11.5px] text-[var(--neutral-700)]">{{ model.gguf_path }}</td>'
new = '''<td class="min-w-[260px] max-w-[420px] break-words px-4 py-3 font-mono text-[11.5px] text-[var(--neutral-700)]">
                <div class="flex min-w-0 items-start gap-1">
                  <span class="min-w-0 flex-1">{{ model.gguf_path }}</span>
                  <AppCopyButton
                    :text="model.gguf_path"
                    label="Copy model path"
                    copied-label="Copied model path"
                    error-message="Unable to copy model path. Select the path and copy it manually."
                    icon-only
                    color="neutral"
                    variant="ghost"
                    size="xs"
                    :data-testid="`copy-model-path-${model.id}`"
                    @copied="clearMessage"
                    @error="showCopyError"
                  />
                </div>
              </td>'''
if old not in text:
    raise SystemExit('missing Models path cell marker')
models.write_text(text.replace(old, new, 1))

test = Path('frontend/test/models-redesign.nuxt.test.ts')
text = test.read_text()
old = "import AppButton from '~/components/AppButton.vue'"
new = old + "\nimport AppCopyButton from '~/components/AppCopyButton.vue'"
if old not in text or "import AppCopyButton from '~/components/AppCopyButton.vue'" in text:
    raise SystemExit('unexpected Models test import state')
text = text.replace(old, new, 1)
old = """    expect(pathCell.classes()).toContain('text-[11.5px]')

    const rowActions"""
new = """    expect(pathCell.classes()).toContain('text-[11.5px]')

    const copyPath = wrapper.getComponent(AppCopyButton)
    expect(copyPath.props('text')).toBe('nested/models/coder/coder-q4.gguf')
    expect(copyPath.props('iconOnly')).toBe(true)
    copyPath.vm.$emit('error', 'clipboard blocked')
    await wrapper.vm.$nextTick()
    const copyError = wrapper.get('[data-testid=\"models-error-state\"]')
    expect(copyError.text()).toContain('Unable to copy model path')
    expect(copyError.text()).toContain('clipboard blocked')
    copyPath.vm.$emit('copied', 'nested/models/coder/coder-q4.gguf')
    await wrapper.vm.$nextTick()
    expect(wrapper.find('[data-testid=\"models-error-state\"]').exists()).toBe(false)

    const rowActions"""
if old not in text:
    raise SystemExit('missing Models path test marker')
test.write_text(text.replace(old, new, 1))
