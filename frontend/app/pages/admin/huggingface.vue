<script setup lang="ts">
const manager = useManager()
const tokenStatus = ref<{ configured: boolean; prefix?: string }>({ configured: false })
const tokenInput = ref('')
const busy = ref(false)
const error = ref('')
const saved = ref(false)

async function load() {
  if (!manager.user.value) return
  try { tokenStatus.value = await manager.request('/api/v1/huggingface/token') } catch (value: any) { error.value = value?.data?.error || value?.message || 'Unable to load Hugging Face status' }
}
watch(manager.user, user => { if (user) void load() }, { immediate: true })

async function save() {
  busy.value = true; error.value = ''; saved.value = false
  try {
    tokenStatus.value = await manager.request('/api/v1/huggingface/token', { method: 'PUT', body: { token: tokenInput.value } })
    tokenInput.value = ''; saved.value = true
  } catch (value: any) { error.value = value?.data?.error || value?.message || 'Unable to save Hugging Face token' } finally { busy.value = false }
}

async function remove() {
  busy.value = true; error.value = ''; saved.value = false
  try { await manager.request('/api/v1/huggingface/token', { method: 'DELETE' }); tokenStatus.value = { configured: false }; tokenInput.value = '' }
  catch (value: any) { error.value = value?.data?.error || value?.message || 'Unable to remove Hugging Face token' } finally { busy.value = false }
}
</script>

<template>
  <div class="space-y-5">
    <UPageHeader headline="ADMINISTRATION" title="Hugging Face" description="Manage the global provider credential used for private and gated repositories." />
    <UCard class="max-w-3xl">
      <template #header><div><h2 class="text-xl font-bold">Provider authentication</h2><p class="text-sm text-muted">The stored token is encrypted at rest and is never returned by the API.</p></div></template>
      <UAlert v-if="error" class="mb-4" color="error" variant="subtle" :description="error" />
      <UAlert v-if="saved" class="mb-4" color="success" variant="subtle" description="Hugging Face token saved." />
      <div class="flex flex-wrap items-end gap-3">
        <UFormField class="min-w-0 flex-1" :label="tokenStatus.configured ? `Replace token (${tokenStatus.prefix || 'configured'}…)` : 'Access token'">
          <UInput v-model="tokenInput" class="w-full" type="password" autocomplete="off" placeholder="hf_…" />
        </UFormField>
        <UButton :loading="busy" :disabled="!tokenInput.trim()" @click="save">{{ tokenStatus.configured ? 'Replace' : 'Save token' }}</UButton>
        <UButton v-if="tokenStatus.configured" color="error" variant="soft" :disabled="busy" @click="remove">Remove</UButton>
      </div>
      <div class="mt-4 flex items-center gap-2 text-sm text-muted"><UBadge :color="tokenStatus.configured ? 'success' : 'neutral'" variant="subtle">{{ tokenStatus.configured ? 'Configured' : 'Not configured' }}</UBadge><span>Credentials are sent only to the configured Hugging Face host.</span></div>
    </UCard>
  </div>
</template>
