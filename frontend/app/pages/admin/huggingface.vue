<script setup lang="ts">
type TokenStatus = { configured: boolean; prefix?: string }

const manager = useManager()
const tokenStatus = ref<TokenStatus>({ configured: false })
const tokenInput = ref('')
const busy = ref(false)
const error = ref('')
const saved = ref(false)

function normalizeTokenStatus(value: any): TokenStatus {
  if (!value || typeof value.configured !== 'boolean') return { configured: false }
  return { configured: value.configured, prefix: typeof value.prefix === 'string' ? value.prefix : undefined }
}

async function load() {
  if (!manager.user.value) return
  try {
    tokenStatus.value = normalizeTokenStatus(await manager.request('/api/v1/huggingface/token'))
  } catch (value: any) {
    tokenStatus.value = { configured: false }
    error.value = value?.data?.error || value?.message || 'Unable to load Hugging Face status'
  }
}
watch(manager.user, user => { if (user) void load() }, { immediate: true })

async function save() {
  if (!tokenInput.value.trim()) return
  busy.value = true
  error.value = ''
  saved.value = false
  try {
    tokenStatus.value = normalizeTokenStatus(await manager.request('/api/v1/huggingface/token', { method: 'PUT', body: { token: tokenInput.value } }))
    tokenInput.value = ''
    saved.value = true
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to save Hugging Face token'
  } finally {
    busy.value = false
  }
}

async function remove() {
  busy.value = true
  error.value = ''
  saved.value = false
  try {
    await manager.request('/api/v1/huggingface/token', { method: 'DELETE' })
    tokenStatus.value = { configured: false }
    tokenInput.value = ''
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to remove Hugging Face token'
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <AdminShell title="Hugging Face" description="Manage the global provider credential used for private and gated repositories.">
    <template #actions><AppButton intent="primary" :loading="busy" :disabled="!tokenInput.trim()" @click="save">Save token</AppButton></template>

    <Frame class="max-w-[720px] p-5" data-testid="admin-huggingface-card">
      <div>
        <h2 class="text-base font-semibold">Provider credential</h2>
        <p class="mt-1 text-xs text-[var(--neutral-700)]">The stored token is encrypted at rest and is never returned by the API.</p>
      </div>

      <UAlert v-if="error" class="mt-4" color="error" variant="subtle" :description="error" />
      <UAlert v-if="saved" class="mt-4" color="success" variant="subtle" description="Hugging Face token saved." />

      <div class="mt-5 flex flex-wrap items-end gap-2">
        <UFormField class="min-w-0 flex-1" :label="tokenStatus.configured ? `Replace token (${tokenStatus.prefix || 'configured'}…)` : 'Access token'">
          <UInput v-model="tokenInput" class="w-full" type="password" autocomplete="off" placeholder="hf_…" />
        </UFormField>
        <AppButton intent="primary" :loading="busy" :disabled="!tokenInput.trim()" @click="save">{{ tokenStatus.configured ? 'Replace' : 'Save token' }}</AppButton>
        <AppButton v-if="tokenStatus.configured" intent="secondary" :disabled="busy" @click="remove">Remove</AppButton>
      </div>

      <div class="mt-5 flex flex-wrap items-center gap-2 border-t border-[var(--color-divider)] pt-4 text-sm text-[var(--neutral-700)]">
        <StatusTag :variant="tokenStatus.configured ? 'ready' : 'neutral'">{{ tokenStatus.configured ? 'Configured' : 'Not configured' }}</StatusTag>
        <span>Credentials are sent only to the configured Hugging Face host.</span>
      </div>
    </Frame>
  </AdminShell>
</template>
