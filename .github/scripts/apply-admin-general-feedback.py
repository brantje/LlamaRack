from pathlib import Path


def replace_once(path: str, old: str, new: str):
    file = Path(path)
    source = file.read_text()
    count = source.count(old)
    if count != 1:
        raise SystemExit(f'{path}: expected one match, found {count}: {old[:140]!r}')
    file.write_text(source.replace(old, new, 1))


replace_once(
    'frontend/app/components/AdminSettingField.vue',
    '<span class="flex w-full items-center justify-between gap-3 text-xs font-semibold">\n        <span>{{ label }}</span>\n        <span class="font-mono text-[11.5px] font-normal" :class="sourceClass" data-testid="setting-source">{{ source }}</span>\n      </span>',
    '<span class="flex w-full flex-col items-start gap-1 text-xs font-semibold sm:flex-row sm:items-center sm:justify-between sm:gap-3">\n        <span>{{ label }}</span>\n        <span class="font-mono text-xs font-normal" :class="sourceClass" data-testid="setting-source">source: {{ source }}</span>\n      </span>'
)

path = Path('frontend/app/pages/admin/general.vue')
source = path.read_text()
source = source.replace("const busy = ref(false)\n", "const busy = ref(false)\nconst baseline = ref('')\n", 1)
marker = "function normalizeDiscoverSettings(value: unknown): DiscoverSettings {\n  return isDiscoverSettings(value) ? value : defaultDiscoverSettings()\n}\n"
addition = marker + "function formSnapshot() {\n  return JSON.stringify({ ...form, allowHybridDiscoverRecommendations: allowHybridDiscoverRecommendations.value })\n}\nfunction updateBaseline() {\n  baseline.value = formSnapshot()\n}\nconst hasChanges = computed(() => Boolean(settings.value && discoverSettings.value && baseline.value && formSnapshot() !== baseline.value))\nconst saveDisabledReason = computed(() => {\n  if (!settings.value || !discoverSettings.value) return 'Settings are still loading.'\n  if (!hasChanges.value) return 'No changes to save.'\n  return ''\n})\nconst canSave = computed(() => !busy.value && !saveDisabledReason.value)\n"
if source.count(marker) != 1:
    raise SystemExit('general normalize marker mismatch')
source = source.replace(marker, addition, 1)
source = source.replace("    syncForm(value)\n  } catch", "    syncForm(value)\n    updateBaseline()\n  } catch", 1)
source = source.replace("    network.value = null\n    error.value", "    network.value = null\n    baseline.value = ''\n    error.value", 1)
source = source.replace("  if (!settings.value || !discoverSettings.value) return\n  busy.value = true", "  if (!settings.value || !discoverSettings.value || !hasChanges.value) return\n  busy.value = true", 1)
source = source.replace("    syncForm(value)\n    const system", "    syncForm(value)\n    updateBaseline()\n    const system", 1)

editable_keys = [
    'session_lifetime_seconds', 'login_failure_threshold', 'login_lockout_seconds', 'login_protection_enabled',
    'trusted_proxies', 'allowed_origins', 'external_url', 'startup_timeout_seconds', 'idle_unload_seconds',
    'always_on_reconcile_seconds', 'observability_retention_days', 'prometheus_auth_token'
]
removed = 0
for key in editable_keys:
    token = f' :class="!editable(\'{key}\') ? \'opacity-45\' : \'\'"'
    count = source.count(token)
    if count != 1:
        raise SystemExit(f'general {key}: expected one opacity class, found {count}')
    source = source.replace(token, '', 1)
    removed += 1
if removed != 12:
    raise SystemExit(f'expected 12 locked-field opacity removals, got {removed}')

old_actions = '    <template #actions><AppButton intent="primary" :loading="busy" :disabled="!settings || !discoverSettings" @click="save">Save changes</AppButton></template>'
new_actions = '''    <template #actions>
      <div class="flex w-full flex-col items-start gap-1 sm:w-auto sm:items-end">
        <AppButton data-testid="admin-general-save-top" intent="primary" :loading="busy" :disabled="!canSave" @click="save">Save changes</AppButton>
        <p v-if="saveDisabledReason" class="text-xs text-[var(--neutral-800)]" data-testid="admin-general-save-reason">{{ saveDisabledReason }}</p>
      </div>
    </template>'''
if source.count(old_actions) != 1:
    raise SystemExit('general header actions marker mismatch')
source = source.replace(old_actions, new_actions, 1)

old_end = '''      </Frame>
    </div>
  </AdminShell>
</template>'''
new_end = '''      </Frame>

      <div class="flex flex-col items-start gap-3 border-t border-[var(--color-divider)] pt-4 sm:hidden" data-testid="admin-general-mobile-actions">
        <p class="text-xs text-[var(--neutral-800)]">{{ saveDisabledReason || 'Unsaved changes' }}</p>
        <AppButton data-testid="admin-general-save-bottom" intent="primary" :loading="busy" :disabled="!canSave" @click="save">Save changes</AppButton>
      </div>
    </div>
  </AdminShell>
</template>'''
if source.count(old_end) != 1:
    raise SystemExit('general footer marker mismatch')
source = source.replace(old_end, new_end, 1)
path.write_text(source)

replace_once(
    'frontend/test/phase10-admin.nuxt.test.ts',
    "    expect(wrapper.text()).toContain('Disabled')\n    saveFailure = true\n    await button(wrapper, 'Save changes').trigger('click')",
    "    expect(wrapper.text()).toContain('Disabled')\n    expect(wrapper.text()).toContain('No changes to save.')\n    const failureIdle = components(wrapper, ['InputNumber', 'UInputNumber']).find(component => component.props('modelValue') === 300)\n    expect(failureIdle).toBeTruthy()\n    failureIdle!.vm.$emit('update:modelValue', 301)\n    await flushPromises()\n    saveFailure = true\n    await button(wrapper, 'Save changes').trigger('click')"
)
replace_once(
    'frontend/test/phase10-admin.nuxt.test.ts',
    "    expect(wrapper.findAll('[data-testid=\"setting-source\"]').length).toBeGreaterThanOrEqual(10)\n\n    const numbers",
    "    expect(wrapper.findAll('[data-testid=\"setting-source\"]').length).toBeGreaterThanOrEqual(10)\n    expect(wrapper.text()).toContain('source: environment')\n    expect(button(wrapper, 'Save changes').attributes('disabled')).toBeDefined()\n    expect(wrapper.text()).toContain('No changes to save.')\n\n    const numbers"
)
replace_once(
    'frontend/test/phase10-admin.nuxt.test.ts',
    "    idle!.vm.$emit('update:modelValue', 600)\n    await flushPromises()\n    await button(wrapper, 'Save changes').trigger('click')",
    "    idle!.vm.$emit('update:modelValue', 600)\n    await flushPromises()\n    expect(button(wrapper, 'Save changes').attributes('disabled')).toBeUndefined()\n    expect(wrapper.text()).toContain('Unsaved changes')\n    await button(wrapper, 'Save changes').trigger('click')"
)

replace_once(
    'frontend/test/admin-redesign-branches.nuxt.test.ts',
    "        expect(options.body.prometheus_auth_token).toBe('metrics-secret')",
    "        expect(options.body.prometheus_auth_token).toBe('metrics-secret-2')"
)
replace_once(
    'frontend/test/admin-redesign-branches.nuxt.test.ts',
    "    system = {}\n    await button(wrapper, 'Save changes').trigger('click')",
    "    system = {}\n    await wrapper.find('input[type=\"password\"]').setValue('metrics-secret-2')\n    await flushPromises()\n    await button(wrapper, 'Save changes').trigger('click')"
)
replace_once(
    'frontend/test/admin-redesign-branches.nuxt.test.ts',
    "      const wrapper = await mountSuspended(AdminGeneralPage, { route: false })\n      await flushPromises()\n      await button(wrapper, 'Save changes').trigger('click')\n      await flushPromises()\n      expect(wrapper.text()).toContain(message)",
    "      const wrapper = await mountSuspended(AdminGeneralPage, { route: false })\n      await flushPromises()\n      await wrapper.find('input[placeholder=\"https://manager.example.com\"]').setValue('https://changed.test')\n      await flushPromises()\n      await button(wrapper, 'Save changes').trigger('click')\n      await flushPromises()\n      expect(wrapper.text()).toContain(message)"
)

replace_once(
    'frontend/test/phase10-deep-branches.nuxt.test.ts',
    "    const general = await mountSuspended(AdminGeneralPage, { route: false })\n    await flushPromises(); await button(general, 'Save changes').trigger('click'); await flushPromises()",
    "    const general = await mountSuspended(AdminGeneralPage, { route: false })\n    await flushPromises()\n    const generalStartup = components(general, ['InputNumber', 'UInputNumber']).find(component => component.props('modelValue') === 120)\n    expect(generalStartup).toBeTruthy()\n    generalStartup!.vm.$emit('update:modelValue', 121)\n    await flushPromises(); await button(general, 'Save changes').trigger('click'); await flushPromises()"
)

replace_once(
    'frontend/test/phase10-branch-coverage.nuxt.test.ts',
    "    settings = { ...settingsResponse(), allowed_origins: { value: 'https://locked', source: 'environment', editable: false } }\n    await button(wrapper, 'Save changes').trigger('click')\n    await flushPromises()\n\n    settings = []\n    await button(wrapper, 'Save changes').trigger('click')",
    "    settings = { ...settingsResponse(), allowed_origins: { value: 'https://locked', source: 'environment', editable: false } }\n    const sessionLifetime = components(wrapper, ['InputNumber', 'UInputNumber']).find(component => component.props('modelValue') === 3600)\n    expect(sessionLifetime).toBeTruthy()\n    sessionLifetime!.vm.$emit('update:modelValue', 3601)\n    await flushPromises()\n    await button(wrapper, 'Save changes').trigger('click')\n    await flushPromises()\n\n    settings = []\n    sessionLifetime!.vm.$emit('update:modelValue', 3602)\n    await flushPromises()\n    await button(wrapper, 'Save changes').trigger('click')"
)

Path('frontend/test/admin-general-feedback.nuxt.test.ts').write_text('''import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

describe('Administration General feedback rules', () => {
  it('keeps locked provenance readable and provides explicit save-state affordances', () => {
    const field = readFileSync(resolve(process.cwd(), 'app/components/AdminSettingField.vue'), 'utf8')
    expect(field).toContain('flex-col items-start gap-1')
    expect(field).toContain('source: {{ source }}')

    const page = readFileSync(resolve(process.cwd(), 'app/pages/admin/general.vue'), 'utf8')
    expect(page).not.toContain("? 'opacity-45' : ''")
    expect(page).toContain('No changes to save.')
    expect(page).toContain('data-testid="admin-general-save-top"')
    expect(page).toContain('data-testid="admin-general-save-bottom"')
    expect(page).toContain('data-testid="admin-general-mobile-actions"')
  })
})
''')
