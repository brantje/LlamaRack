<script setup lang="ts">
type BuildIdentity = {
  version: string
  commit?: string
  build_time?: string
  channel: string
  variant: string
  dirty?: boolean
  llama_cpp: { release?: string; build?: string; image?: string }
}
type SystemInfo = {
  identity?: BuildIdentity
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

function formatUptimeSeconds(seconds: number) {
  if (!Number.isFinite(seconds) || seconds < 0) return '—'
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  const rest = Math.floor(seconds % 60)
  const parts: string[] = []
  if (days) parts.push(`${days}d`)
  if (hours) parts.push(`${hours}h`)
  if (minutes) parts.push(`${minutes}m`)
  if (!days || rest) parts.push(`${rest}s`)
  return parts.join(' ') || '0s'
}

function formatBuildTime(value?: string) {
  if (!value) return 'Unknown'
  const timestamp = Date.parse(value)
  if (!Number.isFinite(timestamp)) return value
  return `${new Intl.DateTimeFormat('en-GB', {
    dateStyle: 'medium',
    timeStyle: 'medium',
    timeZone: 'UTC'
  }).format(timestamp)} UTC`
}

function shortCommit(value?: string) {
  const commit = value?.trim()
  return commit ? commit.slice(0, 12) : 'Unknown'
}

function channelLabel(value?: string) {
  const channel = value?.trim().toLowerCase()
  if (!channel) return 'Unknown'
  return channel.charAt(0).toUpperCase() + channel.slice(1)
}

function channelVariant(value?: string): 'ready' | 'pending' | 'neutral' {
  switch (value?.trim().toLowerCase()) {
    case 'release': return 'ready'
    case 'development': return 'pending'
    default: return 'neutral'
  }
}

function variantLabel(value?: string) {
  const variant = value?.trim().toLowerCase()
  if (!variant || variant === 'unknown') return 'Unknown'
  if (['cpu', 'cuda', 'rocm'].includes(variant)) return variant.toUpperCase()
  return variant.charAt(0).toUpperCase() + variant.slice(1)
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
  <AdminShell title="System" description="Read-only manager, build, network and llama.cpp diagnostics.">
    <template #actions>
      <div class="flex flex-col items-end gap-1">
        <AppButton class="min-w-[104px]" intent="secondary" :disabled="loading" :aria-busy="loading" @click="load">{{ loading ? 'Refreshing…' : 'Refresh' }}</AppButton>
        <p class="text-[length:var(--font-size-table-header)] leading-4 text-[var(--neutral-800)]" aria-live="polite" data-testid="system-freshness">{{ loading ? 'Refreshing diagnostics…' : freshness }}</p>
      </div>
    </template>

    <Frame v-if="error" class="mb-5 p-3"><div class="flex items-start gap-2"><StatusTag variant="failed">Error</StatusTag><p class="text-xs leading-5 text-[var(--neutral-800)]">{{ error }}</p></div></Frame>

    <div v-if="info" class="space-y-5">
      <UCard v-if="info.identity" data-testid="admin-system-identity">
        <h2 class="text-base font-semibold">Build identity</h2>
        <p class="mt-1 text-xs leading-5 text-[var(--neutral-800)]">Exact application and bundled runtime metadata for support and release verification.</p>
        <dl class="mt-4 text-sm">
          <div class="grid gap-1 py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">LlamaRack version</dt><dd class="min-w-0 flex flex-wrap items-center gap-2"><code class="font-mono text-[length:var(--font-size-h6)]">{{ info.identity.version || 'Unknown' }}</code><StatusTag :variant="channelVariant(info.identity.channel)">{{ channelLabel(info.identity.channel) }}</StatusTag></dd></div>
          <div class="grid gap-1 border-t border-[var(--color-divider)] py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Runtime variant</dt><dd class="min-w-0"><code class="font-mono text-[length:var(--font-size-h6)]">{{ variantLabel(info.identity.variant) }}</code></dd></div>
          <div class="grid gap-1 border-t border-[var(--color-divider)] py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Git commit</dt><dd class="min-w-0 flex items-center gap-2"><code class="font-mono text-[length:var(--font-size-h6)]" :title="info.identity.commit || undefined" data-testid="build-commit">{{ shortCommit(info.identity.commit) }}</code><AppCopyButton v-if="info.identity.commit" :text="info.identity.commit" label="Copy commit" icon-only size="xs" color="neutral" variant="ghost" /></dd></div>
          <div class="grid gap-1 border-t border-[var(--color-divider)] py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Built</dt><dd class="min-w-0"><code class="whitespace-normal font-mono text-[length:var(--font-size-h6)] [overflow-wrap:anywhere]">{{ formatBuildTime(info.identity.build_time) }}</code></dd></div>
          <div v-if="info.identity.dirty" class="grid gap-1 border-t border-[var(--color-divider)] py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Working tree</dt><dd class="min-w-0"><StatusTag variant="pending">Modified</StatusTag></dd></div>
          <div class="grid gap-1 border-t border-[var(--color-divider)] py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">llama.cpp release</dt><dd class="min-w-0"><code class="font-mono text-[length:var(--font-size-h6)]">{{ info.identity.llama_cpp.release || 'Unknown' }}</code></dd></div>
          <div class="grid gap-1 border-t border-[var(--color-divider)] py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">llama.cpp build</dt><dd class="min-w-0"><code class="font-mono text-[length:var(--font-size-h6)]">{{ info.identity.llama_cpp.build || 'Unknown' }}</code></dd></div>
          <div v-if="info.identity.llama_cpp.image" class="grid gap-1 border-t border-[var(--color-divider)] py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Bundled runtime image</dt><dd class="min-w-0"><code class="whitespace-normal font-mono text-[length:var(--font-size-h6)] [overflow-wrap:anywhere]">{{ info.identity.llama_cpp.image }}</code></dd></div>
        </dl>
      </UCard>

      <Frame class="p-5" data-testid="admin-system-manager">
        <h2 class="text-base font-semibold">Manager</h2>
        <dl class="mt-4 text-sm">
          <div class="grid gap-1 py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Uptime</dt><dd class="min-w-0 flex flex-wrap items-baseline gap-x-2"><code class="font-mono text-[length:var(--font-size-h6)] tabular-nums">{{ formatUptimeSeconds(info.manager.uptime_seconds) }}</code><code class="font-mono text-[length:var(--font-size-h6)] tabular-nums text-[var(--neutral-700)]">{{ info.manager.uptime_seconds.toLocaleString() }} s</code></dd></div>
          <div v-for="(value, key) in info.manager.runtime" :key="key" class="grid gap-1 border-t border-[var(--color-divider)] py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">{{ labelFor(String(key), runtimeLabels) }}</dt><dd class="min-w-0"><code class="whitespace-normal font-mono text-[length:var(--font-size-h6)] [overflow-wrap:anywhere]">{{ value }}</code></dd></div>
        </dl>
      </Frame>

      <Frame class="p-5" data-testid="admin-system-network">
        <h2 class="text-base font-semibold">Network security</h2>
        <dl class="mt-4 text-sm">
          <div class="grid gap-1 py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Effective scheme</dt><dd class="min-w-0"><code class="font-mono text-[length:var(--font-size-h6)]">{{ info.network.effective_scheme }}</code></dd></div>
          <div class="grid gap-1 border-t border-[var(--color-divider)] py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Secure session cookie</dt><dd class="min-w-0"><StatusTag :variant="info.network.secure_cookie ? 'ready' : 'neutral'">{{ info.network.secure_cookie ? 'Enabled' : 'Disabled' }}</StatusTag></dd></div>
          <div class="grid gap-1 border-t border-[var(--color-divider)] py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Allowed origins</dt><dd class="min-w-0"><ul v-if="allowedOrigins.length" class="space-y-1"><li v-for="(origin, index) in allowedOrigins" :key="`${origin}-${index}`"><code class="whitespace-normal font-mono text-[length:var(--font-size-h6)] [overflow-wrap:anywhere]" data-testid="allowed-origin-value">{{ origin }}</code></li></ul><code v-else class="font-mono text-[length:var(--font-size-h6)] text-[var(--neutral-800)]">None configured</code></dd></div>
          <div class="grid gap-1 border-t border-[var(--color-divider)] py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Trusted proxies</dt><dd class="min-w-0"><ul v-if="trustedProxies.length" class="space-y-1"><li v-for="(proxy, index) in trustedProxies" :key="`${proxy}-${index}`"><code class="whitespace-normal font-mono text-[length:var(--font-size-h6)] [overflow-wrap:anywhere]" data-testid="trusted-proxy-value">{{ proxy }}</code></li></ul><code v-else class="font-mono text-[length:var(--font-size-h6)] text-[var(--neutral-800)]">None configured</code></dd></div>
        </dl>
      </Frame>

      <Frame class="p-5" data-testid="admin-system-llamacpp">
        <h2 class="text-base font-semibold">llama.cpp</h2>
        <div v-if="!info.llamacpp.available" class="mt-4 flex items-start gap-2 border border-[var(--color-divider)] p-4" data-testid="system-llamacpp-warning">
          <StatusTag variant="pending">Unavailable</StatusTag><p class="text-sm font-semibold">llama-server is unavailable.</p>
        </div>
        <dl v-else class="mt-4 text-sm">
          <div class="grid gap-1 py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Path</dt><dd class="min-w-0"><code class="whitespace-normal font-mono text-[length:var(--font-size-h6)] [overflow-wrap:anywhere]">{{ info.llamacpp.path || 'unknown' }}</code></dd></div>
          <div class="grid gap-1 border-t border-[var(--color-divider)] py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Version</dt><dd class="min-w-0"><code class="font-mono text-[length:var(--font-size-h6)]">{{ info.llamacpp.version || 'unknown' }}</code></dd></div>
          <div class="grid gap-1 border-t border-[var(--color-divider)] py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Discovered options</dt><dd class="min-w-0"><code class="font-mono text-[length:var(--font-size-h6)] tabular-nums">{{ info.llamacpp.options || 0 }}</code></dd></div>
        </dl>
      </Frame>
    </div>
  </AdminShell>
</template>
