<script setup lang="ts">
type SystemInfo = {
  manager: { uptime_seconds: number; runtime: Record<string, string> }
  network: { effective_scheme: string; secure_cookie: boolean; allowed_origins: unknown; trusted_proxies: unknown; external_url: unknown }
  llamacpp: { available: boolean; path?: string; version?: string; fingerprint?: string; options?: number }
}
type ResolvedSetting = { value?: unknown }

const runtimeLabels: Record<string, string> = {
  data_dir: 'Data directory',
  models_dir: 'Models directory',
  database_path: 'Database path',
  listen_addr: 'Listen address',
  llama_server_path: 'llama server path'
}
const diagnosticLabels: Record<string, string> = {
  cidr: 'CIDR',
  url: 'URL'
}

const manager = useManager()
const info = ref<SystemInfo | null>(null)
const error = ref('')
const loading = ref(false)
const updatedAt = ref<number | null>(null)

function labelFor(key: string, labels: Record<string, string> = {}) {
  if (labels[key]) return labels[key]
  const text = key.replaceAll('_', ' ')
  return text.charAt(0).toUpperCase() + text.slice(1)
}

function resolvedValue(value: unknown): unknown {
  if (value && typeof value === 'object' && !Array.isArray(value) && 'value' in value) {
    return resolvedValue((value as ResolvedSetting).value)
  }
  return value
}

function diagnosticValues(value: unknown): string[] {
  const candidate = resolvedValue(value)
  if (candidate == null) return []
  if (Array.isArray(candidate)) return candidate.flatMap(item => diagnosticValues(item))
  if (typeof candidate === 'object') {
    return Object.entries(candidate).flatMap(([key, item]) =>
      diagnosticValues(item).map(text => `${labelFor(key, diagnosticLabels)}: ${text}`)
    )
  }
  if (typeof candidate === 'string') {
    return candidate.split(/[\n,]+/).map(item => item.trim()).filter(Boolean)
  }
  const text = String(candidate).trim()
  return text ? [text] : []
}

const allowedOrigins = computed(() => diagnosticValues(info.value?.network.allowed_origins))
const trustedProxies = computed(() => diagnosticValues(info.value?.network.trusted_proxies))
const freshness = computed(() => {
  if (updatedAt.value == null) return 'Not updated yet'
  const time = new Intl.DateTimeFormat('en-GB', {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false
  }).format(updatedAt.value)
  return `Updated ${time}`
})

function isSystemInfo(value: unknown): value is SystemInfo {
  if (!value || typeof value !== 'object') return false
  const candidate = value as Partial<SystemInfo>
  return Boolean(candidate.manager && candidate.network && candidate.llamacpp)
}
async function load() {
  if (!manager.user.value || loading.value) return
  loading.value = true
  error.value = ''
  try {
    const result = await manager.request<SystemInfo>('/api/v1/system')
    info.value = isSystemInfo(result) ? result : null
    if (info.value) updatedAt.value = Date.now()
  } catch (value: any) {
    info.value = null
    error.value = value?.data?.error || value?.message || 'Unable to load system diagnostics'
  } finally {
    loading.value = false
  }
}
watch(manager.user, user => { if (user) void load() }, { immediate: true })
</script>

<template>
  <AdminShell title="System" description="Read-only manager, network and llama.cpp diagnostics.">
    <template #actions>
      <div class="flex flex-col items-end gap-1">
        <AppButton class="min-w-[104px]" intent="secondary" :disabled="loading" :aria-busy="loading" @click="load">{{ loading ? 'Refreshing…' : 'Refresh' }}</AppButton>
        <p class="text-[10.5px] leading-4 text-[var(--neutral-800)]" aria-live="polite" data-testid="system-freshness">{{ loading ? 'Refreshing diagnostics…' : freshness }}</p>
      </div>
    </template>

    <Frame v-if="error" class="mb-5 p-3"><div class="flex items-start gap-2"><StatusTag variant="failed">Error</StatusTag><p class="text-xs leading-5 text-[var(--neutral-800)]">{{ error }}</p></div></Frame>

    <div v-if="info" class="space-y-5">
      <Frame class="p-5" data-testid="admin-system-manager">
        <h2 class="text-base font-semibold">Manager</h2>
        <dl class="mt-4 text-sm">
          <div class="grid gap-1 py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Uptime</dt><dd class="min-w-0"><code class="font-mono text-[12.5px] tabular-nums">{{ info.manager.uptime_seconds }} seconds</code></dd></div>
          <div v-for="(value, key) in info.manager.runtime" :key="key" class="grid gap-1 border-t border-[var(--color-divider)] py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">{{ labelFor(String(key), runtimeLabels) }}</dt><dd class="min-w-0"><code class="whitespace-normal font-mono text-[12.5px] [overflow-wrap:anywhere]">{{ value }}</code></dd></div>
        </dl>
      </Frame>

      <Frame class="p-5" data-testid="admin-system-network">
        <h2 class="text-base font-semibold">Network security</h2>
        <dl class="mt-4 text-sm">
          <div class="grid gap-1 py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Effective scheme</dt><dd class="min-w-0"><code class="font-mono text-[12.5px]">{{ info.network.effective_scheme }}</code></dd></div>
          <div class="grid gap-1 border-t border-[var(--color-divider)] py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Secure session cookie</dt><dd class="min-w-0"><StatusTag :variant="info.network.secure_cookie ? 'ready' : 'neutral'">{{ info.network.secure_cookie ? 'Enabled' : 'Disabled' }}</StatusTag></dd></div>
          <div class="grid gap-1 border-t border-[var(--color-divider)] py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Allowed origins</dt><dd class="min-w-0"><ul v-if="allowedOrigins.length" class="space-y-1"><li v-for="(origin, index) in allowedOrigins" :key="`${origin}-${index}`"><code class="whitespace-normal font-mono text-[12.5px] [overflow-wrap:anywhere]" data-testid="allowed-origin-value">{{ origin }}</code></li></ul><code v-else class="font-mono text-[12.5px] text-[var(--neutral-800)]">None configured</code></dd></div>
          <div class="grid gap-1 border-t border-[var(--color-divider)] py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Trusted proxies</dt><dd class="min-w-0"><ul v-if="trustedProxies.length" class="space-y-1"><li v-for="(proxy, index) in trustedProxies" :key="`${proxy}-${index}`"><code class="whitespace-normal font-mono text-[12.5px] [overflow-wrap:anywhere]" data-testid="trusted-proxy-value">{{ proxy }}</code></li></ul><code v-else class="font-mono text-[12.5px] text-[var(--neutral-800)]">None configured</code></dd></div>
        </dl>
      </Frame>

      <Frame class="p-5" data-testid="admin-system-llamacpp">
        <h2 class="text-base font-semibold">llama.cpp</h2>
        <div v-if="!info.llamacpp.available" class="mt-4 flex items-start gap-2 border border-[var(--color-divider)] p-4" data-testid="system-llamacpp-warning">
          <StatusTag variant="pending">Unavailable</StatusTag><p class="text-sm font-semibold">llama-server is unavailable.</p>
        </div>
        <dl v-else class="mt-4 text-sm">
          <div class="grid gap-1 py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Path</dt><dd class="min-w-0"><code class="whitespace-normal font-mono text-[12.5px] [overflow-wrap:anywhere]">{{ info.llamacpp.path || 'unknown' }}</code></dd></div>
          <div class="grid gap-1 border-t border-[var(--color-divider)] py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Version</dt><dd class="min-w-0"><code class="font-mono text-[12.5px]">{{ info.llamacpp.version || 'unknown' }}</code></dd></div>
          <div class="grid gap-1 border-t border-[var(--color-divider)] py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Discovered options</dt><dd class="min-w-0"><code class="font-mono text-[12.5px] tabular-nums">{{ info.llamacpp.options || 0 }}</code></dd></div>
        </dl>
      </Frame>
    </div>
  </AdminShell>
</template>
