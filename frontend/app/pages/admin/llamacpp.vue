<script setup lang="ts">
import type { Profile } from '~/composables/useManager'

const manager = useManager()
const { profile } = manager
const globalOptions = ref<Record<string, string>>({})
const loading = ref(false)
const saving = ref(false)
const error = ref('')
const saved = ref(false)

async function load() {
  if (!manager.user.value) return
  loading.value = true
  error.value = ''
  try {
    const result = await manager.request<{ profile?: Profile; effective: { global: Record<string, string> } }>('/api/v1/llamacpp/config')
    globalOptions.value = { ...(result.effective.global || {}) }
    if (result.profile?.path && Array.isArray(result.profile.options)) profile.value = result.profile
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to load llama.cpp configuration'
  } finally {
    loading.value = false
  }
}
watch(manager.user, user => { if (user) void load() }, { immediate: true })

async function save() {
  saving.value = true
  error.value = ''
  saved.value = false
  try {
    await manager.request('/api/v1/llamacpp/config', { method: 'PUT', body: { options: globalOptions.value } })
    saved.value = true
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to save llama.cpp defaults'
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <AdminShell title="llama.cpp" description="Binary capabilities and manager-wide llama.cpp defaults.">
    <template #actions>
      <AppButton intent="secondary" :loading="loading" @click="load">Refresh</AppButton>
      <AppButton intent="primary" :loading="saving" :disabled="loading" @click="save">Save defaults</AppButton>
    </template>

    <Frame v-if="error" class="mb-5 p-3"><div class="flex items-start gap-2"><StatusTag variant="failed">Error</StatusTag><p class="text-xs leading-5 text-[var(--neutral-800)]">{{ error }}</p></div></Frame>
    <Frame v-if="saved" class="mb-5 p-3"><div class="flex items-start gap-2"><StatusTag variant="ready">Saved</StatusTag><p class="text-xs leading-5 text-[var(--neutral-800)]">Global llama.cpp defaults saved.</p></div></Frame>

    <div class="space-y-5">
      <Frame class="p-5" data-testid="admin-llamacpp-capabilities">
        <h2 class="text-base font-semibold">Binary capabilities</h2>
        <dl v-if="profile" class="mt-4 text-sm">
          <div class="grid gap-1 border-t border-[var(--color-divider)] py-3 first:border-t-0 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Binary</dt><dd><code class="break-all font-mono text-[12.5px]">{{ profile.path }}</code></dd></div>
          <div class="grid gap-1 border-t border-[var(--color-divider)] py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Version</dt><dd><code class="font-mono text-[12.5px]">{{ profile.version || 'unknown' }}</code></dd></div>
          <div class="grid gap-1 border-t border-[var(--color-divider)] py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Fingerprint</dt><dd><code class="break-all font-mono text-[12.5px]">{{ profile.fingerprint }}</code></dd></div>
          <div class="grid gap-1 border-t border-[var(--color-divider)] py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Discovered options</dt><dd>{{ profile.options.length }}</dd></div>
        </dl>
        <div v-else class="mt-4 flex items-start gap-2 border border-[var(--color-divider)] p-4" data-testid="llamacpp-unavailable-warning">
          <StatusTag variant="pending">Unavailable</StatusTag>
          <div><p class="text-sm font-semibold">llama-server could not be discovered.</p><p class="mt-1 text-xs leading-5 text-[var(--neutral-700)]">Management features remain available, but workers cannot start until the binary path is valid.</p></div>
        </div>
      </Frame>

      <Frame class="p-5" data-testid="admin-llamacpp-defaults">
        <h2 class="text-base font-semibold">Global defaults</h2>
        <p class="mt-1 text-xs text-[var(--neutral-700)]">Inherited by Models and Instances unless a lower layer overrides a value.</p>
        <div v-if="loading" class="mt-4 space-y-2"><USkeleton v-for="n in 3" :key="n" class="h-14 w-full" /></div>
        <AdminGlobalDefaultsEditor v-else v-model="globalOptions" class="mt-4" :profile="profile" />
      </Frame>
    </div>
  </AdminShell>
</template>
