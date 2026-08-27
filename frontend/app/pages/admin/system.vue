<script setup lang="ts">
type SystemInfo = {
  manager: { uptime_seconds: number; runtime: Record<string, string> }
  network: { effective_scheme: string; secure_cookie: boolean; allowed_origins: unknown; trusted_proxies: unknown; external_url: unknown }
  llamacpp: { available: boolean; path?: string; version?: string; fingerprint?: string; options?: number }
}

const manager = useManager()
const info = ref<SystemInfo | null>(null)
const error = ref('')
async function load() {
  if (!manager.user.value) return
  error.value = ''
  try { info.value = await manager.request<SystemInfo>('/api/v1/system') } catch (value: any) { error.value = value?.data?.error || value?.message || 'Unable to load system diagnostics' }
}
watch(manager.user, user => { if (user) void load() }, { immediate: true })
</script>

<template>
  <div class="space-y-5">
    <div class="flex items-start justify-between gap-4"><UPageHeader class="min-w-0 flex-1" headline="ADMINISTRATION" title="System" description="Read-only manager, storage, network and llama.cpp diagnostics. Secret values and environment dumps are intentionally excluded." /><UButton color="neutral" variant="soft" @click="load">Refresh</UButton></div>
    <UAlert v-if="error" color="error" variant="subtle" :description="error" />
    <template v-if="info">
      <UCard><template #header><h2 class="text-xl font-bold">Manager</h2></template><dl class="divide-y divide-default text-sm"><div class="grid gap-1 py-3 sm:grid-cols-[180px_1fr]"><dt class="text-muted">Uptime</dt><dd>{{ info.manager.uptime_seconds }} seconds</dd></div><div v-for="(value, key) in info.manager.runtime" :key="key" class="grid gap-1 py-3 sm:grid-cols-[180px_1fr]"><dt class="text-muted">{{ String(key).replaceAll('_', ' ') }}</dt><dd><code class="break-all font-mono">{{ value }}</code></dd></div></dl></UCard>
      <UCard><template #header><h2 class="text-xl font-bold">Network security</h2></template><dl class="divide-y divide-default text-sm"><div class="grid gap-1 py-3 sm:grid-cols-[180px_1fr]"><dt class="text-muted">Effective scheme</dt><dd>{{ info.network.effective_scheme }}</dd></div><div class="grid gap-1 py-3 sm:grid-cols-[180px_1fr]"><dt class="text-muted">Secure session cookie</dt><dd><UBadge :color="info.network.secure_cookie ? 'success' : 'warning'" variant="subtle">{{ info.network.secure_cookie ? 'Enabled' : 'Disabled' }}</UBadge></dd></div><div class="grid gap-1 py-3 sm:grid-cols-[180px_1fr]"><dt class="text-muted">Allowed origins</dt><dd><code>{{ (info.network.allowed_origins as any)?.value ?? info.network.allowed_origins }}</code></dd></div><div class="grid gap-1 py-3 sm:grid-cols-[180px_1fr]"><dt class="text-muted">Trusted proxies</dt><dd><code>{{ (info.network.trusted_proxies as any)?.value ?? info.network.trusted_proxies || 'None' }}</code></dd></div></dl></UCard>
      <UCard><template #header><h2 class="text-xl font-bold">llama.cpp</h2></template><UAlert v-if="!info.llamacpp.available" color="warning" variant="subtle" description="llama-server is unavailable." /><dl v-else class="divide-y divide-default text-sm"><div class="grid gap-1 py-3 sm:grid-cols-[180px_1fr]"><dt class="text-muted">Path</dt><dd><code class="break-all">{{ info.llamacpp.path }}</code></dd></div><div class="grid gap-1 py-3 sm:grid-cols-[180px_1fr]"><dt class="text-muted">Version</dt><dd>{{ info.llamacpp.version || 'unknown' }}</dd></div><div class="grid gap-1 py-3 sm:grid-cols-[180px_1fr]"><dt class="text-muted">Discovered options</dt><dd>{{ info.llamacpp.options || 0 }}</dd></div></dl></UCard>
    </template>
  </div>
</template>
