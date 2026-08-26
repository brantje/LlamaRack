<script setup lang="ts">
const manager = useManager()
const { apiBase, profile } = manager
const globalOptions = ref<Record<string, string>>({})
const loadingOptions = ref(false)
const savingOptions = ref(false)
const optionError = ref('')
const optionSaved = ref(false)

async function loadGlobalOptions() {
  loadingOptions.value = true
  optionError.value = ''
  try {
    const result = await manager.request<{ effective: { global: Record<string, string> } }>('/api/v1/llamacpp/config')
    globalOptions.value = { ...(result.effective.global || {}) }
  } catch (value: any) {
    optionError.value = value?.data?.error || value?.message || 'Unable to load global llama.cpp defaults'
  } finally {
    loadingOptions.value = false
  }
}

async function saveGlobalOptions() {
  savingOptions.value = true
  optionError.value = ''
  optionSaved.value = false
  try {
    await manager.request('/api/v1/llamacpp/config', { method: 'PUT', body: { options: globalOptions.value } })
    optionSaved.value = true
  } catch (value: any) {
    optionError.value = value?.data?.error || value?.message || 'Unable to save global llama.cpp defaults'
  } finally {
    savingOptions.value = false
  }
}

async function refreshAll() {
  await Promise.all([manager.refresh(), loadGlobalOptions()])
}

onMounted(() => void loadGlobalOptions())
</script>

<template>
  <div class="space-y-5">
    <div class="flex items-start justify-between gap-6">
      <UPageHeader class="min-w-0 flex-1" headline="RUNTIME" title="Settings" description="Detected backend, hardware and llama.cpp capabilities plus manager-wide defaults." />
      <UButton color="neutral" variant="soft" @click="refreshAll">Refresh</UButton>
    </div>

    <UCard>
      <p class="mb-1 text-xs font-extrabold tracking-[0.18em] text-dimmed">BACKEND</p>
      <h2 class="text-xl font-bold">Connection</h2>
      <dl class="mt-4 divide-y divide-default text-sm">
        <div class="grid gap-1 py-3 sm:grid-cols-[170px_1fr] sm:gap-5"><dt class="text-muted">Management API</dt><dd><code class="break-all font-mono">{{ apiBase }}/api/v1</code></dd></div>
        <div class="grid gap-1 py-3 sm:grid-cols-[170px_1fr] sm:gap-5"><dt class="text-muted">OpenAI endpoint</dt><dd><code class="break-all font-mono">{{ apiBase }}/v1</code></dd></div>
      </dl>
    </UCard>

    <UCard>
      <p class="mb-1 text-xs font-extrabold tracking-[0.18em] text-dimmed">LLAMA.CPP</p>
      <h2 class="text-xl font-bold">Binary capabilities</h2>
      <dl v-if="profile" class="mt-4 divide-y divide-default text-sm">
        <div class="grid gap-1 py-3 sm:grid-cols-[170px_1fr] sm:gap-5"><dt class="text-muted">Binary</dt><dd><code class="break-all font-mono">{{ profile.path }}</code></dd></div>
        <div class="grid gap-1 py-3 sm:grid-cols-[170px_1fr] sm:gap-5"><dt class="text-muted">Version</dt><dd><code class="font-mono">{{ profile.version || 'unknown' }}</code></dd></div>
        <div class="grid gap-1 py-3 sm:grid-cols-[170px_1fr] sm:gap-5"><dt class="text-muted">Fingerprint</dt><dd><code class="break-all font-mono">{{ profile.fingerprint.slice(0, 24) }}…</code></dd></div>
        <div class="grid gap-1 py-3 sm:grid-cols-[170px_1fr] sm:gap-5"><dt class="text-muted">Discovered options</dt><dd class="font-semibold">{{ profile.options.length }}</dd></div>
      </dl>
      <UAlert v-else class="mt-4" color="warning" variant="subtle" description="llama-server could not be discovered. Management features still work, but model workers cannot start until the binary path is correct." />
    </UCard>

    <UCard class="max-w-5xl">
      <div class="mb-5 flex flex-wrap items-start justify-between gap-3">
        <div><p class="mb-1 text-xs font-extrabold tracking-[0.18em] text-dimmed">GLOBAL DEFAULTS</p><h2 class="text-xl font-bold">llama.cpp configuration</h2><p class="text-sm text-muted">Inherited by Models and Instances unless a lower layer overrides the value.</p></div>
        <UButton :loading="savingOptions" :disabled="loadingOptions" @click="saveGlobalOptions">Save defaults</UButton>
      </div>
      <UAlert v-if="optionError" class="mb-4" color="error" variant="subtle" :description="optionError" />
      <UAlert v-if="optionSaved" class="mb-4" color="success" variant="subtle" description="Global llama.cpp defaults saved." />
      <div v-if="loadingOptions" class="space-y-2"><USkeleton v-for="n in 3" :key="n" class="h-20 w-full" /></div>
      <LlamaCppOptionsEditor v-else v-model="globalOptions" scope="global" />
    </UCard>
  </div>
</template>
