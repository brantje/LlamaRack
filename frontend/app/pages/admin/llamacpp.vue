<script setup lang="ts">
const manager = useManager()
const { profile } = manager
const globalOptions = ref<Record<string, string>>({})
const loading = ref(false)
const saving = ref(false)
const error = ref('')
const saved = ref(false)

async function load() {
  if (!manager.user.value) return
  loading.value = true; error.value = ''
  try {
    const result = await manager.request<{ effective: { global: Record<string, string> } }>('/api/v1/llamacpp/config')
    globalOptions.value = { ...(result.effective.global || {}) }
    await manager.refresh()
  } catch (value: any) { error.value = value?.data?.error || value?.message || 'Unable to load llama.cpp configuration' } finally { loading.value = false }
}
watch(manager.user, user => { if (user) void load() }, { immediate: true })

async function save() {
  saving.value = true; error.value = ''; saved.value = false
  try { await manager.request('/api/v1/llamacpp/config', { method: 'PUT', body: { options: globalOptions.value } }); saved.value = true }
  catch (value: any) { error.value = value?.data?.error || value?.message || 'Unable to save llama.cpp defaults' } finally { saving.value = false }
}
</script>

<template>
  <div class="space-y-5">
    <div class="flex items-start justify-between gap-4"><UPageHeader class="min-w-0 flex-1" headline="ADMINISTRATION" title="llama.cpp" description="Detected backend binary capabilities and manager-wide llama.cpp defaults." /><UButton color="neutral" variant="soft" @click="load">Refresh</UButton></div>
    <UAlert v-if="error" color="error" variant="subtle" :description="error" />
    <UAlert v-if="saved" color="success" variant="subtle" description="Global llama.cpp defaults saved." />

    <UCard>
      <template #header><h2 class="text-xl font-bold">Binary capabilities</h2></template>
      <dl v-if="profile" class="divide-y divide-default text-sm">
        <div class="grid gap-1 py-3 sm:grid-cols-[180px_1fr]"><dt class="text-muted">Binary</dt><dd><code class="break-all font-mono">{{ profile.path }}</code></dd></div>
        <div class="grid gap-1 py-3 sm:grid-cols-[180px_1fr]"><dt class="text-muted">Version</dt><dd><code class="font-mono">{{ profile.version || 'unknown' }}</code></dd></div>
        <div class="grid gap-1 py-3 sm:grid-cols-[180px_1fr]"><dt class="text-muted">Fingerprint</dt><dd><code class="break-all font-mono">{{ profile.fingerprint }}</code></dd></div>
        <div class="grid gap-1 py-3 sm:grid-cols-[180px_1fr]"><dt class="text-muted">Discovered options</dt><dd>{{ profile.options.length }}</dd></div>
      </dl>
      <UAlert v-else color="warning" variant="subtle" description="llama-server could not be discovered. Management features remain available, but workers cannot start until the binary path is valid." />
    </UCard>

    <UCard>
      <template #header><div class="flex flex-wrap items-start justify-between gap-3"><div><h2 class="text-xl font-bold">Global defaults</h2><p class="text-sm text-muted">Inherited by Models and Instances unless a lower layer overrides a value.</p></div><UButton :loading="saving" :disabled="loading" @click="save">Save defaults</UButton></div></template>
      <div v-if="loading" class="space-y-2"><USkeleton v-for="n in 3" :key="n" class="h-20 w-full" /></div>
      <LlamaCppOptionsEditor v-else v-model="globalOptions" scope="global" />
    </UCard>
  </div>
</template>
