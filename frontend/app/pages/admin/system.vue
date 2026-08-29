<script setup lang="ts">
type SystemInfo = {
  manager: { uptime_seconds: number; runtime: Record<string, string> }
  network: { effective_scheme: string; secure_cookie: boolean; allowed_origins: unknown; trusted_proxies: unknown; external_url: unknown }
  llamacpp: { available: boolean; path?: string; version?: string; fingerprint?: string; options?: number }
}
type ResolvedSetting = { value?: unknown }

const manager = useManager()
const info = ref<SystemInfo | null>(null)
const error = ref('')

function settingValue(value: unknown, fallback = '') {
  if (value && typeof value === 'object' && 'value' in value) {
    const candidate = (value as ResolvedSetting).value
    if (candidate != null && String(candidate).trim() !== '') return String(candidate)
  }
  if (value == null || String(value).trim() === '') return fallback
  return String(value)
}
function isSystemInfo(value: unknown): value is SystemInfo {
  if (!value || typeof value !== 'object') return false
  const candidate = value as Partial<SystemInfo>
  return Boolean(candidate.manager && candidate.network && candidate.llamacpp)
}
async function load() {
  if (!manager.user.value) return
  error.value = ''
  try {
    const result = await manager.request<SystemInfo>('/api/v1/system')
    info.value = isSystemInfo(result) ? result : null
  } catch (value: any) {
    info.value = null
    error.value = value?.data?.error || value?.message || 'Unable to load system diagnostics'
  }
}
watch(manager.user, user => { if (user) void load() }, { immediate: true })
</script>

<template>
  <AdminShell title="System" description="Read-only manager, network and llama.cpp diagnostics.">
    <template #actions><AppButton intent="secondary" @click="load">Refresh</AppButton></template>

    <UAlert v-if="error" class="mb-5" color="error" variant="subtle" :description="error" />

    <div v-if="info" class="space-y-5">
      <Frame class="p-5" data-testid="admin-system-manager">
        <h2 class="text-base font-semibold">Manager</h2>
        <dl class="mt-4 text-sm">
          <div class="grid gap-1 py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Uptime</dt><dd>{{ info.manager.uptime_seconds }} seconds</dd></div>
          <div v-for="(value, key) in info.manager.runtime" :key="key" class="grid gap-1 border-t border-[var(--color-divider)] py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">{{ String(key).replaceAll('_', ' ') }}</dt><dd><code class="break-all font-mono text-[12.5px]">{{ value }}</code></dd></div>
        </dl>
      </Frame>

      <Frame class="p-5" data-testid="admin-system-network">
        <h2 class="text-base font-semibold">Network security</h2>
        <dl class="mt-4 text-sm">
          <div class="grid gap-1 py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Effective scheme</dt><dd><code class="font-mono text-[12.5px]">{{ info.network.effective_scheme }}</code></dd></div>
          <div class="grid gap-1 border-t border-[var(--color-divider)] py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Secure session cookie</dt><dd><StatusTag :variant="info.network.secure_cookie ? 'ready' : 'neutral'">{{ info.network.secure_cookie ? 'Enabled' : 'Disabled' }}</StatusTag></dd></div>
          <div class="grid gap-1 border-t border-[var(--color-divider)] py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Allowed origins</dt><dd><code class="break-all font-mono text-[12.5px]">{{ settingValue(info.network.allowed_origins) }}</code></dd></div>
          <div class="grid gap-1 border-t border-[var(--color-divider)] py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Trusted proxies</dt><dd><code class="break-all font-mono text-[12.5px]">{{ settingValue(info.network.trusted_proxies, 'None') }}</code></dd></div>
        </dl>
      </Frame>

      <Frame class="p-5" data-testid="admin-system-llamacpp">
        <h2 class="text-base font-semibold">llama.cpp</h2>
        <div v-if="!info.llamacpp.available" class="mt-4 border border-[var(--color-divider)] p-4" data-testid="system-llamacpp-warning">
          <p class="text-sm font-semibold">llama-server is unavailable.</p>
        </div>
        <dl v-else class="mt-4 text-sm">
          <div class="grid gap-1 py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Path</dt><dd><code class="break-all font-mono text-[12.5px]">{{ info.llamacpp.path }}</code></dd></div>
          <div class="grid gap-1 border-t border-[var(--color-divider)] py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Version</dt><dd>{{ info.llamacpp.version || 'unknown' }}</dd></div>
          <div class="grid gap-1 border-t border-[var(--color-divider)] py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Discovered options</dt><dd>{{ info.llamacpp.options || 0 }}</dd></div>
        </dl>
      </Frame>
    </div>
  </AdminShell>
</template>
