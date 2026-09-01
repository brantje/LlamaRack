<script setup lang="ts">
type SettingValue<T> = { value: T; source: 'environment' | 'database' | 'default' | string; editable: boolean }
type GeneralSettings = {
  session_lifetime_seconds: SettingValue<number>
  login_protection_enabled: SettingValue<boolean>
  login_failure_threshold: SettingValue<number>
  login_lockout_seconds: SettingValue<number>
  trusted_proxies: SettingValue<string>
  allowed_origins: SettingValue<string>
  external_url: SettingValue<string>
  startup_timeout_seconds: SettingValue<number>
  idle_unload_seconds: SettingValue<number>
  always_on_reconcile_seconds: SettingValue<number>
  max_pending_requests_per_instance: SettingValue<number>
  max_pending_requests_global: SettingValue<number>
  observability_retention_days?: SettingValue<number>
  prometheus_auth_token?: SettingValue<string>
  runtime: { data_dir: string; models_dir: string; database_path: string; listen_addr: string; llama_server_path: string }
}
type DiscoverSettings = { hybrid_recommendations_enabled: SettingValue<boolean> }
type SystemNetwork = { network: { effective_scheme: string; secure_cookie: boolean } }

const manager = useManager()
const settings = ref<GeneralSettings | null>(null)
const discoverSettings = ref<DiscoverSettings | null>(null)
const network = ref<SystemNetwork['network'] | null>(null)
const form = reactive({
  session_lifetime_seconds: 86400,
  login_protection_enabled: true,
  login_failure_threshold: 5,
  login_lockout_seconds: 900,
  trusted_proxies: '',
  allowed_origins: '',
  external_url: '',
  startup_timeout_seconds: 180,
  idle_unload_seconds: 300,
  always_on_reconcile_seconds: 15,
  max_pending_requests_per_instance: 32,
  max_pending_requests_global: 128,
  observability_retention_days: 30,
  prometheus_auth_token: ''
})
const allowHybridDiscoverRecommendations = ref(true)
const error = ref('')
const saved = ref(false)
const busy = ref(false)
const baseline = ref('')
const legacySettingKeys = [
  'session_lifetime_seconds', 'login_protection_enabled', 'login_failure_threshold', 'login_lockout_seconds',
  'trusted_proxies', 'allowed_origins', 'external_url', 'startup_timeout_seconds', 'idle_unload_seconds', 'always_on_reconcile_seconds',
  'max_pending_requests_per_instance', 'max_pending_requests_global'
] as const

function isGeneralSettings(value: unknown): value is GeneralSettings {
  if (!value || typeof value !== 'object') return false
  const candidate = value as Partial<GeneralSettings>
  return Boolean(candidate.runtime && legacySettingKeys.every(key => candidate[key] && typeof candidate[key] === 'object'))
}
function isDiscoverSettings(value: unknown): value is DiscoverSettings {
  if (!value || typeof value !== 'object') return false
  const candidate = value as Partial<DiscoverSettings>
  return Boolean(candidate.hybrid_recommendations_enabled && typeof candidate.hybrid_recommendations_enabled.value === 'boolean')
}
function defaultDiscoverSettings(): DiscoverSettings {
  return { hybrid_recommendations_enabled: { value: true, source: 'default', editable: true } }
}
function normalizeDiscoverSettings(value: unknown): DiscoverSettings {
  return isDiscoverSettings(value) ? value : defaultDiscoverSettings()
}
function formSnapshot() {
  return JSON.stringify({ ...form, allowHybridDiscoverRecommendations: allowHybridDiscoverRecommendations.value })
}
function updateBaseline() {
  baseline.value = formSnapshot()
}
const hasChanges = computed(() => Boolean(settings.value && discoverSettings.value && baseline.value && formSnapshot() !== baseline.value))
const saveDisabledReason = computed(() => {
  if (!settings.value || !discoverSettings.value) return 'Settings are still loading.'
  if (!hasChanges.value) return 'No changes to save.'
  return ''
})
const canSave = computed(() => !busy.value && !saveDisabledReason.value)
function syncForm(value: GeneralSettings) {
  for (const key of Object.keys(form) as Array<keyof typeof form>) {
    const setting = value[key as keyof GeneralSettings] as SettingValue<unknown> | undefined
    if (setting && typeof setting === 'object' && 'value' in setting) (form[key] as any) = setting.value
  }
}

async function load() {
  if (!manager.user.value) return
  error.value = ''
  try {
    const [value, system, discoverValue] = await Promise.all([
      manager.request<GeneralSettings>('/api/v1/settings/general'),
      manager.request<SystemNetwork>('/api/v1/system'),
      manager.request<DiscoverSettings>('/api/v1/settings/discover')
    ])
    if (!isGeneralSettings(value) || !system?.network) throw new Error('Invalid manager settings response')
    const discover = normalizeDiscoverSettings(discoverValue)
    settings.value = value
    discoverSettings.value = discover
    network.value = system.network
    allowHybridDiscoverRecommendations.value = discover.hybrid_recommendations_enabled.value
    syncForm(value)
    updateBaseline()
  } catch (value: any) {
    settings.value = null
    discoverSettings.value = null
    network.value = null
    baseline.value = ''
    error.value = value?.data?.error || value?.message || 'Unable to load manager settings'
  }
}
watch(manager.user, user => { if (user) void load() }, { immediate: true })

async function save() {
  if (!settings.value || !discoverSettings.value || !hasChanges.value) return
  busy.value = true
  error.value = ''
  saved.value = false
  const body: Record<string, unknown> = {}
  for (const key of Object.keys(form) as Array<keyof typeof form>) {
    const setting = settings.value[key as keyof GeneralSettings] as SettingValue<unknown> | undefined
    if (setting?.editable) body[key] = form[key]
  }
  try {
    const [value, discoverValue] = await Promise.all([
      manager.request<GeneralSettings>('/api/v1/settings/general', { method: 'PUT', body }),
      manager.request<DiscoverSettings>('/api/v1/settings/discover', { method: 'PUT', body: { hybrid_recommendations_enabled: allowHybridDiscoverRecommendations.value } })
    ])
    if (!isGeneralSettings(value)) throw new Error('Invalid manager settings response')
    const discover = normalizeDiscoverSettings(discoverValue)
    settings.value = value
    discoverSettings.value = discover
    allowHybridDiscoverRecommendations.value = discover.hybrid_recommendations_enabled.value
    syncForm(value)
    updateBaseline()
    const system = await manager.request<SystemNetwork>('/api/v1/system')
    network.value = system?.network || null
    saved.value = true
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to save manager settings'
  } finally {
    busy.value = false
  }
}

function source(key: keyof typeof form) {
  const setting = settings.value?.[key as keyof GeneralSettings] as SettingValue<unknown> | undefined
  return setting?.source || 'default'
}
function editable(key: keyof typeof form) {
  const setting = settings.value?.[key as keyof GeneralSettings] as SettingValue<unknown> | undefined
  return setting?.editable !== false
}
</script>

<template>
  <AdminShell title="General" description="Manager security, network and lifecycle defaults.">
    <template #actions>
      <div class="flex w-full flex-col items-start gap-1 sm:w-auto sm:items-end">
        <AppButton data-testid="admin-general-save-top" intent="primary" :loading="busy" :disabled="!canSave" @click="save">Save changes</AppButton>
        <p v-if="saveDisabledReason" class="text-xs text-[var(--neutral-800)]" data-testid="admin-general-save-reason">{{ saveDisabledReason }}</p>
      </div>
    </template>

    <Frame v-if="error" class="mb-5 p-3"><div class="flex items-start gap-2"><StatusTag variant="failed">Error</StatusTag><p class="text-xs leading-5 text-[var(--neutral-800)]">{{ error }}</p></div></Frame>
    <Frame v-if="saved" class="mb-5 p-3"><div class="flex items-start gap-2"><StatusTag variant="ready">Saved</StatusTag><p class="text-xs leading-5 text-[var(--neutral-800)]">Manager settings saved.</p></div></Frame>

    <div v-if="settings && discoverSettings" class="space-y-5">
      <Frame class="p-5" data-testid="admin-general-authentication">
        <h2 class="text-base font-semibold">Authentication</h2>
        <p class="mt-1 text-xs leading-5 text-[var(--neutral-700)]">Hard session lifetime and bounded login protection. Sessions do not use idle or sliding expiration.</p>
        <div class="mt-5 grid gap-5 md:grid-cols-2">
          <AdminSettingField label="Session lifetime (seconds)" :source="source('session_lifetime_seconds')"><UInputNumber v-model="form.session_lifetime_seconds" class="w-full" :min="60" :disabled="!editable('session_lifetime_seconds')" /></AdminSettingField>
          <AdminSettingField label="Login failure threshold" :source="source('login_failure_threshold')"><UInputNumber v-model="form.login_failure_threshold" class="w-full" :min="2" :disabled="!editable('login_failure_threshold')" /></AdminSettingField>
          <AdminSettingField label="Lockout duration (seconds)" :source="source('login_lockout_seconds')"><UInputNumber v-model="form.login_lockout_seconds" class="w-full" :min="1" :disabled="!editable('login_lockout_seconds')" /></AdminSettingField>
          <AdminSettingField label="Login protection" :source="source('login_protection_enabled')"><USwitch v-model="form.login_protection_enabled" :disabled="!editable('login_protection_enabled')" label="Enable escalating login protection" /></AdminSettingField>
        </div>
      </Frame>

      <Frame class="p-5" data-testid="admin-general-network">
        <h2 class="text-base font-semibold">Network and reverse proxy</h2>
        <p class="mt-1 text-xs leading-5 text-[var(--neutral-700)]">Forwarded headers are trusted only when the direct peer matches an explicitly configured proxy address or CIDR.</p>
        <div class="mt-5 grid gap-5 md:grid-cols-2">
          <AdminSettingField label="Trusted proxies" :source="source('trusted_proxies')"><UInput v-model="form.trusted_proxies" class="w-full" :disabled="!editable('trusted_proxies')" placeholder="10.0.0.10, 172.16.0.0/12" /></AdminSettingField>
          <AdminSettingField label="Allowed origins" :source="source('allowed_origins')">
            <UInput v-model="form.allowed_origins" class="w-full" :disabled="!editable('allowed_origins')" placeholder="https://manager.example.com" />
            <template #help>A saved value here takes precedence over LLAMARACK_ALLOWED_ORIGIN.</template>
          </AdminSettingField>
          <AdminSettingField label="External/public URL" :source="source('external_url')"><UInput v-model="form.external_url" class="w-full" :disabled="!editable('external_url')" placeholder="https://manager.example.com" /></AdminSettingField>
        </div>
        <div class="mt-5 grid gap-4 border-t border-[var(--color-divider)] pt-4 text-sm sm:grid-cols-2">
          <div><p class="text-xs text-[var(--neutral-700)]">Effective external scheme</p><code class="mt-1 block font-mono text-[length:var(--font-size-h6)]">{{ network?.effective_scheme || 'unknown' }}</code></div>
          <div><p class="text-xs text-[var(--neutral-700)]">Secure session cookies</p><StatusTag class="mt-1" :variant="network?.secure_cookie ? 'ready' : 'neutral'">{{ network?.secure_cookie ? 'Enabled' : 'Disabled' }}</StatusTag></div>
        </div>
      </Frame>

      <Frame class="p-5" data-testid="admin-general-resources">
        <h2 class="text-base font-semibold">Resource defaults</h2>
        <p class="mt-1 text-xs leading-5 text-[var(--neutral-700)]">Global idle unload defaults to 300 seconds; set it to 0 to disable the global idle timeout. Streaming responses keep an Instance active until the proxied response completes. Pending-request limits default to 32 per Instance and 128 manager-wide; set either to 0 for unlimited. Instances may override the per-Instance default.</p>
        <div class="mt-5 grid gap-5 md:grid-cols-3">
          <AdminSettingField label="Worker startup timeout (seconds)" :source="source('startup_timeout_seconds')"><UInputNumber v-model="form.startup_timeout_seconds" class="w-full" :min="1" :disabled="!editable('startup_timeout_seconds')" /></AdminSettingField>
          <AdminSettingField label="Global idle unload (seconds)" :source="source('idle_unload_seconds')">
            <UInputNumber v-model="form.idle_unload_seconds" class="w-full" :min="0" :disabled="!editable('idle_unload_seconds')" />
            <template #help>Defaults to 300 seconds (5 minutes). Set to 0 to disable the global idle timeout.</template>
          </AdminSettingField>
          <AdminSettingField label="Always-on reconcile (seconds)" :source="source('always_on_reconcile_seconds')"><UInputNumber v-model="form.always_on_reconcile_seconds" class="w-full" :min="0" :disabled="!editable('always_on_reconcile_seconds')" /></AdminSettingField>
        </div>
        <div class="mt-5 grid gap-5 md:grid-cols-2">
          <AdminSettingField label="Max pending requests per Instance" :source="source('max_pending_requests_per_instance')">
            <UInputNumber v-model="form.max_pending_requests_per_instance" class="w-full" :min="0" :max="10000" :disabled="!editable('max_pending_requests_per_instance')" />
            <template #help>Default 32. Set to 0 for unlimited. Instances may override this default.</template>
          </AdminSettingField>
          <AdminSettingField label="Max pending requests global" :source="source('max_pending_requests_global')">
            <UInputNumber v-model="form.max_pending_requests_global" class="w-full" :min="0" :max="10000" :disabled="!editable('max_pending_requests_global')" />
            <template #help>Default 128 waiter ceiling across all Instances. Set to 0 for unlimited. Instance overrides cannot bypass this.</template>
          </AdminSettingField>
        </div>

        <div class="mt-5 grid gap-5 border-t border-[var(--color-divider)] pt-5 md:grid-cols-2">
          <AdminSettingField label="Discover recommendations" :source="discoverSettings.hybrid_recommendations_enabled.source" data-testid="discover-hybrid-policy">
            <USwitch v-model="allowHybridDiscoverRecommendations" :disabled="!discoverSettings.hybrid_recommendations_enabled.editable" label="Allow hybrid recommendations to outrank GPU-fit choices" />
          </AdminSettingField>
          <AdminSettingField v-if="settings.observability_retention_days" label="History retention (days)" :source="source('observability_retention_days')" data-testid="observability-settings">
            <UInputNumber v-model="form.observability_retention_days" class="w-full" :min="1" :max="3650" :disabled="!editable('observability_retention_days')" />
          </AdminSettingField>
          <AdminSettingField v-if="settings.prometheus_auth_token" label="Prometheus Bearer token" :source="source('prometheus_auth_token')">
            <UInput v-model="form.prometheus_auth_token" type="password" autocomplete="off" class="w-full" :disabled="!editable('prometheus_auth_token')" placeholder="Leave empty for unauthenticated /metrics" />
          </AdminSettingField>
        </div>
      </Frame>

      <Frame class="p-5" data-testid="admin-general-runtime">
        <h2 class="text-base font-semibold">Manager runtime</h2>
        <dl class="mt-4 text-sm">
          <div v-for="(value, key) in settings.runtime" :key="key" class="grid gap-1 border-t border-[var(--color-divider)] py-3 first:border-t-0 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">{{ String(key).replaceAll('_', ' ') }}</dt><dd><code class="break-all font-mono text-[length:var(--font-size-h6)]">{{ value }}</code></dd></div>
          <div class="grid gap-1 border-t border-[var(--color-divider)] py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Management API URL</dt><dd><code class="break-all font-mono text-[length:var(--font-size-h6)]">{{ manager.apiBase.value }}/api/v1</code></dd></div>
          <div class="grid gap-1 border-t border-[var(--color-divider)] py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">OpenAI API URL</dt><dd><code class="break-all font-mono text-[length:var(--font-size-h6)]">{{ manager.apiBase.value }}/v1</code></dd></div>
        </dl>
      </Frame>

      <div class="flex flex-col items-start gap-3 border-t border-[var(--color-divider)] pt-4 sm:hidden" data-testid="admin-general-mobile-actions">
        <p class="text-xs text-[var(--neutral-800)]">{{ saveDisabledReason || 'Unsaved changes' }}</p>
        <AppButton data-testid="admin-general-save-bottom" intent="primary" :loading="busy" :disabled="!canSave" @click="save">Save changes</AppButton>
      </div>
    </div>
  </AdminShell>
</template>
