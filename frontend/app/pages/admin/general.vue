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
  runtime: { data_dir: string; models_dir: string; database_path: string; listen_addr: string; llama_server_path: string }
}
type SystemNetwork = { network: { effective_scheme: string; secure_cookie: boolean } }

const manager = useManager()
const settings = ref<GeneralSettings | null>(null)
const network = ref<SystemNetwork['network'] | null>(null)
const form = reactive({ session_lifetime_seconds: 86400, login_protection_enabled: true, login_failure_threshold: 5, login_lockout_seconds: 900, trusted_proxies: '', allowed_origins: '', external_url: '', startup_timeout_seconds: 180, idle_unload_seconds: 300, always_on_reconcile_seconds: 15 })
const error = ref('')
const saved = ref(false)
const busy = ref(false)

function isGeneralSettings(value: unknown): value is GeneralSettings {
  if (!value || typeof value !== 'object') return false
  const candidate = value as Partial<GeneralSettings>
  return Boolean(candidate.runtime && Object.keys(form).every(key => candidate[key as keyof typeof form] && typeof candidate[key as keyof typeof form] === 'object'))
}

function syncForm(value: GeneralSettings) {
  for (const key of Object.keys(form) as Array<keyof typeof form>) (form[key] as any) = value[key].value
}

async function load() {
  if (!manager.user.value) return
  error.value = ''
  try {
    const [value, system] = await Promise.all([
      manager.request<GeneralSettings>('/api/v1/settings/general'),
      manager.request<SystemNetwork>('/api/v1/system')
    ])
    if (!isGeneralSettings(value) || !system?.network) throw new Error('Invalid manager settings response')
    settings.value = value
    network.value = system.network
    syncForm(value)
  } catch (value: any) {
    settings.value = null
    network.value = null
    error.value = value?.data?.error || value?.message || 'Unable to load manager settings'
  }
}
watch(manager.user, user => { if (user) void load() }, { immediate: true })

async function save() {
  if (!settings.value) return
  busy.value = true
  error.value = ''
  saved.value = false
  const body: Record<string, unknown> = {}
  for (const key of Object.keys(form) as Array<keyof typeof form>) if (settings.value[key]?.editable) body[key] = form[key]
  try {
    const value = await manager.request<GeneralSettings>('/api/v1/settings/general', { method: 'PUT', body })
    if (!isGeneralSettings(value)) throw new Error('Invalid manager settings response')
    settings.value = value
    syncForm(value)
    const system = await manager.request<SystemNetwork>('/api/v1/system')
    network.value = system?.network || null
    saved.value = true
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to save manager settings'
  } finally {
    busy.value = false
  }
}

function source(key: keyof typeof form) { return settings.value?.[key]?.source || 'default' }
function editable(key: keyof typeof form) { return settings.value?.[key]?.editable !== false }
</script>

<template>
  <div class="space-y-5">
    <div class="flex items-start justify-between gap-4"><UPageHeader class="min-w-0 flex-1" headline="ADMINISTRATION" title="General" description="Manager security, network and lifecycle defaults. Environment values normally override database values; Allowed Origins is explicitly overrideable from this page." /><UButton :loading="busy" :disabled="!settings" @click="save">Save changes</UButton></div>
    <UAlert v-if="error" color="error" variant="subtle" :description="error" />
    <UAlert v-if="saved" color="success" variant="subtle" description="Manager settings saved." />

    <template v-if="settings">
      <UCard>
        <template #header><div><h2 class="text-xl font-bold">Authentication</h2><p class="text-sm text-muted">Hard session lifetime and bounded login protection. Sessions do not use idle or sliding expiration.</p></div></template>
        <div class="grid gap-5 md:grid-cols-2">
          <UFormField label="Session lifetime (seconds)" :hint="source('session_lifetime_seconds')"><UInputNumber v-model="form.session_lifetime_seconds" class="w-full" :min="60" :disabled="!editable('session_lifetime_seconds')" /></UFormField>
          <UFormField label="Login failure threshold" :hint="source('login_failure_threshold')"><UInputNumber v-model="form.login_failure_threshold" class="w-full" :min="2" :disabled="!editable('login_failure_threshold')" /></UFormField>
          <UFormField label="Lockout duration (seconds)" :hint="source('login_lockout_seconds')"><UInputNumber v-model="form.login_lockout_seconds" class="w-full" :min="1" :disabled="!editable('login_lockout_seconds')" /></UFormField>
          <UFormField label="Login protection" :hint="source('login_protection_enabled')"><USwitch v-model="form.login_protection_enabled" :disabled="!editable('login_protection_enabled')" label="Enable escalating login protection" /></UFormField>
        </div>
      </UCard>

      <UCard>
        <template #header><div><h2 class="text-xl font-bold">Network and reverse proxy</h2><p class="text-sm text-muted">Forwarded headers are trusted only when the direct peer matches an explicitly configured proxy address or CIDR.</p></div></template>
        <div class="grid gap-5 md:grid-cols-2">
          <UFormField label="Trusted proxies" :hint="source('trusted_proxies')"><UInput v-model="form.trusted_proxies" class="w-full" :disabled="!editable('trusted_proxies')" placeholder="10.0.0.10, 172.16.0.0/12" /></UFormField>
          <UFormField label="Allowed origins" :hint="source('allowed_origins')">
            <UInput v-model="form.allowed_origins" class="w-full" :disabled="!editable('allowed_origins')" placeholder="https://manager.example.com" />
            <template #help>A saved value here takes precedence over LCM_ALLOWED_ORIGIN.</template>
          </UFormField>
          <UFormField label="External/public URL" :hint="source('external_url')"><UInput v-model="form.external_url" class="w-full" :disabled="!editable('external_url')" placeholder="https://manager.example.com" /></UFormField>
        </div>
        <USeparator class="my-5" />
        <dl class="grid gap-4 text-sm sm:grid-cols-2">
          <div><dt class="text-muted">Effective external scheme</dt><dd class="mt-1 font-semibold">{{ network?.effective_scheme || 'unknown' }}</dd></div>
          <div><dt class="text-muted">Secure session cookies</dt><dd class="mt-1"><UBadge :color="network?.secure_cookie ? 'success' : 'warning'" variant="subtle">{{ network?.secure_cookie ? 'Enabled' : 'Disabled' }}</UBadge></dd></div>
        </dl>
      </UCard>

      <UCard>
        <template #header><div><h2 class="text-xl font-bold">Resource defaults</h2><p class="text-sm text-muted">Persisted lifecycle defaults are applied when the manager starts.</p></div></template>
        <div class="grid gap-5 md:grid-cols-3">
          <UFormField label="Worker startup timeout (seconds)" :hint="source('startup_timeout_seconds')"><UInputNumber v-model="form.startup_timeout_seconds" class="w-full" :min="1" :disabled="!editable('startup_timeout_seconds')" /></UFormField>
          <UFormField label="Global idle unload (seconds)" :hint="source('idle_unload_seconds')">
            <UInputNumber v-model="form.idle_unload_seconds" class="w-full" :min="0" :disabled="!editable('idle_unload_seconds')" />
            <template #help>Defaults to 300 seconds (5 minutes). Set to 0 to disable the global idle timeout.</template>
          </UFormField>
          <UFormField label="Always-on reconcile (seconds)" :hint="source('always_on_reconcile_seconds')"><UInputNumber v-model="form.always_on_reconcile_seconds" class="w-full" :min="0" :disabled="!editable('always_on_reconcile_seconds')" /></UFormField>
        </div>
      </UCard>

      <UCard>
        <template #header><h2 class="text-xl font-bold">Manager runtime</h2></template>
        <dl class="divide-y divide-default text-sm">
          <div v-for="(value, key) in settings.runtime" :key="key" class="grid gap-1 py-3 sm:grid-cols-[180px_1fr]"><dt class="text-muted">{{ String(key).replaceAll('_', ' ') }}</dt><dd><code class="break-all font-mono">{{ value }}</code></dd></div>
          <div class="grid gap-1 py-3 sm:grid-cols-[180px_1fr]"><dt class="text-muted">Management API URL</dt><dd><code class="break-all font-mono">{{ manager.apiBase.value }}/api/v1</code></dd></div>
          <div class="grid gap-1 py-3 sm:grid-cols-[180px_1fr]"><dt class="text-muted">OpenAI API URL</dt><dd><code class="break-all font-mono">{{ manager.apiBase.value }}/v1</code></dd></div>
        </dl>
      </UCard>
    </template>
  </div>
</template>
