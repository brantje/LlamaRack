<script setup lang="ts">
type TokenStatus = { configured: boolean; prefix?: string }
type SettingValue<T> = { value: T; source: 'environment' | 'database' | 'default' | string; editable: boolean }
type HuggingFaceSettings = { max_download_bytes: SettingValue<number> }
type SizeUnit = 'MiB' | 'GiB' | 'TiB'

const MIB = 1024 ** 2
const GIB = 1024 ** 3
const TIB = 1024 ** 4
const DEFAULT_MAX_DOWNLOAD_BYTES = TIB
const UNIT_BYTES: Record<SizeUnit, number> = { MiB: MIB, GiB: GIB, TiB: TIB }
const UNIT_ITEMS = [
  { label: 'MiB', value: 'MiB' },
  { label: 'GiB', value: 'GiB' },
  { label: 'TiB', value: 'TiB' }
]

const manager = useManager()
const tokenStatus = ref<TokenStatus>({ configured: false })
const tokenInput = ref('')
const busy = ref(false)
const error = ref('')
const saved = ref(false)
const settingsBusy = ref(false)
const settingsError = ref('')
const settingsSaved = ref(false)
const settings = ref<HuggingFaceSettings | null>(null)
const limitMagnitude = ref(1)
const limitUnit = ref<SizeUnit>('TiB')

function normalizeTokenStatus(value: any): TokenStatus {
  if (!value || typeof value.configured !== 'boolean') return { configured: false }
  return { configured: value.configured, prefix: typeof value.prefix === 'string' ? value.prefix : undefined }
}

function normalizeSettings(value: any): HuggingFaceSettings {
  const raw = value && typeof value === 'object' ? value.max_download_bytes : null
  const parsed = typeof raw?.value === 'number' && Number.isFinite(raw.value) ? Math.trunc(raw.value) : DEFAULT_MAX_DOWNLOAD_BYTES
  return {
    max_download_bytes: {
      value: parsed > 0 ? parsed : DEFAULT_MAX_DOWNLOAD_BYTES,
      source: typeof raw?.source === 'string' ? raw.source : 'default',
      editable: raw?.editable !== false
    }
  }
}

function splitBytes(bytes: number): { magnitude: number, unit: SizeUnit } {
  if (bytes % TIB === 0 && bytes >= TIB) return { magnitude: bytes / TIB, unit: 'TiB' }
  if (bytes % GIB === 0 && bytes >= GIB) return { magnitude: bytes / GIB, unit: 'GiB' }
  return { magnitude: bytes / MIB, unit: 'MiB' }
}

function applySettings(next: HuggingFaceSettings) {
  settings.value = next
  const split = splitBytes(next.max_download_bytes.value)
  limitMagnitude.value = split.magnitude
  limitUnit.value = split.unit
}

const limitBytes = computed(() => {
  const magnitude = Number(limitMagnitude.value)
  if (!Number.isFinite(magnitude) || magnitude <= 0) return 0
  return Math.round(magnitude * UNIT_BYTES[limitUnit.value])
})
const settingsEditable = computed(() => settings.value?.max_download_bytes.editable === true)
const settingsChanged = computed(() => Boolean(settings.value && limitBytes.value !== settings.value.max_download_bytes.value))
const canSaveLimit = computed(() => settingsEditable.value && settingsChanged.value && limitBytes.value >= 1)

function formatExactBytes(bytes: number) {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B'
  return `${new Intl.NumberFormat('en-US').format(bytes)} bytes`
}

async function load() {
  if (!manager.user.value) return
  error.value = ''
  settingsError.value = ''
  try {
    tokenStatus.value = normalizeTokenStatus(await manager.request('/api/v1/huggingface/token'))
  } catch (value: any) {
    tokenStatus.value = { configured: false }
    error.value = value?.data?.error || value?.message || 'Unable to load Hugging Face status'
  }
  try {
    applySettings(normalizeSettings(await manager.request('/api/v1/huggingface/settings')))
  } catch (value: any) {
    settings.value = null
    settingsError.value = value?.data?.error || value?.message || 'Unable to load Hugging Face settings'
  }
}
watch(manager.user, user => { if (user) void load() }, { immediate: true })

async function save() {
  if (!tokenInput.value.trim()) return
  busy.value = true
  error.value = ''
  saved.value = false
  try {
    tokenStatus.value = normalizeTokenStatus(await manager.request('/api/v1/huggingface/token', { method: 'PUT', body: { token: tokenInput.value } }))
    tokenInput.value = ''
    saved.value = true
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to save Hugging Face token'
  } finally {
    busy.value = false
  }
}

async function remove() {
  busy.value = true
  error.value = ''
  saved.value = false
  try {
    await manager.request('/api/v1/huggingface/token', { method: 'DELETE' })
    tokenStatus.value = { configured: false }
    tokenInput.value = ''
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to remove Hugging Face token'
  } finally {
    busy.value = false
  }
}

async function saveDownloadLimit() {
  if (!canSaveLimit.value) return
  settingsBusy.value = true
  settingsError.value = ''
  settingsSaved.value = false
  try {
    applySettings(normalizeSettings(await manager.request('/api/v1/huggingface/settings', {
      method: 'PUT',
      body: { max_download_bytes: limitBytes.value }
    })))
    settingsSaved.value = true
  } catch (value: any) {
    settingsError.value = value?.data?.error || value?.message || 'Unable to save download limit'
  } finally {
    settingsBusy.value = false
  }
}
</script>

<template>
  <AdminShell title="Hugging Face" description="Manage the global provider credential and Hugging Face download size limit.">
    <div class="max-w-[720px] space-y-5">
      <Frame class="p-5" data-testid="admin-huggingface-card">
        <div>
          <h2 class="text-base font-semibold">Provider credential</h2>
          <p class="mt-1 text-xs text-[var(--neutral-700)]">The stored token is encrypted at rest and is never returned by the API.</p>
        </div>

        <div v-if="error" class="mt-4 flex items-start gap-2 border border-[var(--color-divider)] px-3 py-2">
          <StatusTag variant="failed">Error</StatusTag><p class="text-xs leading-5 text-[var(--neutral-800)]">{{ error }}</p>
        </div>
        <div v-if="saved" class="mt-4 flex items-start gap-2 border border-[var(--color-divider)] px-3 py-2">
          <StatusTag variant="ready">Saved</StatusTag><p class="text-xs leading-5 text-[var(--neutral-800)]">Hugging Face token saved.</p>
        </div>

        <div class="mt-5 flex flex-wrap items-end gap-2">
          <UFormField class="min-w-0 flex-1" :label="tokenStatus.configured ? `Replace token (${tokenStatus.prefix || 'configured'}…)` : 'Access token'">
            <UInput v-model="tokenInput" class="w-full" type="password" autocomplete="off" placeholder="hf_…" />
          </UFormField>
          <AppButton intent="primary" :loading="busy" :disabled="!tokenInput.trim()" @click="save">{{ tokenStatus.configured ? 'Replace' : 'Save token' }}</AppButton>
          <AppButton v-if="tokenStatus.configured" intent="secondary" :disabled="busy" @click="remove">Remove</AppButton>
        </div>

        <div class="mt-5 flex flex-wrap items-center gap-2 border-t border-[var(--color-divider)] pt-4 text-sm text-[var(--neutral-700)]">
          <StatusTag :variant="tokenStatus.configured ? 'ready' : 'neutral'">{{ tokenStatus.configured ? 'Configured' : 'Not configured' }}</StatusTag>
          <span>Credentials are sent only to the configured Hugging Face host.</span>
        </div>
      </Frame>

      <Frame class="p-5" data-testid="admin-huggingface-download-limit">
        <div>
          <h2 class="text-base font-semibold">Download limits</h2>
          <p class="mt-1 text-xs text-[var(--neutral-700)]">Hugging Face downloads abort before writing more than this ceiling. The default is 1 TiB and the setting cannot be unlimited.</p>
        </div>

        <div v-if="settingsError" class="mt-4 flex items-start gap-2 border border-[var(--color-divider)] px-3 py-2">
          <StatusTag variant="failed">Error</StatusTag><p class="text-xs leading-5 text-[var(--neutral-800)]">{{ settingsError }}</p>
        </div>
        <div v-if="settingsSaved" class="mt-4 flex items-start gap-2 border border-[var(--color-divider)] px-3 py-2">
          <StatusTag variant="ready">Saved</StatusTag><p class="text-xs leading-5 text-[var(--neutral-800)]">Download limit saved.</p>
        </div>

        <div class="mt-5">
          <AdminSettingField label="Max download size" :source="settings?.max_download_bytes.source || 'default'">
            <UFieldGroup class="w-full">
              <UInputNumber v-model="limitMagnitude" class="w-full" :min="1" :disabled="!settingsEditable" data-testid="hf-max-download-magnitude" />
              <USelect v-model="limitUnit" class="w-28" :items="UNIT_ITEMS" value-key="value" label-key="label" :disabled="!settingsEditable" aria-label="Download size unit" data-testid="hf-max-download-unit" />
            </UFieldGroup>
            <template #help>
              Persisted as {{ formatExactBytes(limitBytes) }}. Raise this for multi-terabyte models; values at or below zero are rejected.
            </template>
          </AdminSettingField>
        </div>

        <div class="mt-5 flex flex-wrap items-center gap-2 border-t border-[var(--color-divider)] pt-4">
          <AppButton intent="primary" :loading="settingsBusy" :disabled="!canSaveLimit" data-testid="hf-max-download-save" @click="saveDownloadLimit">Save download limit</AppButton>
          <code class="font-mono text-xs text-[var(--neutral-700)]" data-testid="hf-max-download-bytes">{{ formatExactBytes(limitBytes) }}</code>
        </div>
      </Frame>
    </div>
  </AdminShell>
</template>
