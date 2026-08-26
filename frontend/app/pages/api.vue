<script setup lang="ts">
import type { TableColumn } from '@nuxt/ui'
import type { APIKey } from '~/composables/useManager'

const manager = useManager()
const { isAdmin, apiBase } = manager
const keys = ref<APIKey[]>([])
const name = ref('default')
const secret = ref('')
const error = ref('')
const pending = reactive<Record<string, 'toggle' | 'revoke' | undefined>>({})

const columns: TableColumn<APIKey>[] = [
  { accessorKey: 'name', header: 'Name' },
  { accessorKey: 'prefix', header: 'Prefix' },
  { accessorKey: 'enabled', header: 'Status' },
  { id: 'actions', header: '' }
]

async function load() {
  if (!isAdmin.value) return
  try {
    keys.value = await manager.request<APIKey[]>('/api/v1/api-keys')
  } catch (e: any) {
    error.value = e?.data?.error || e?.message || 'Unable to load API keys'
  }
}

async function createKey() {
  error.value = ''
  try {
    const result = await manager.request<{ key: APIKey; secret: string }>('/api/v1/api-keys', { method: 'POST', body: { name: name.value } })
    secret.value = result.secret
    await load()
  } catch (e: any) {
    error.value = e?.data?.error || e?.message || 'Unable to create API key'
  }
}

async function setEnabled(key: APIKey) {
  pending[key.id] = 'toggle'
  error.value = ''
  try {
    await manager.request(`/api/v1/api-keys/${key.id}`, { method: 'PATCH', body: { enabled: !key.enabled } })
    await load()
  } catch (e: any) {
    error.value = e?.data?.error || e?.message || 'Unable to update API key'
  } finally {
    pending[key.id] = undefined
  }
}

async function revoke(key: APIKey) {
  if (!confirm(`Revoke API key "${key.name}"? It will be permanently removed.`)) return
  pending[key.id] = 'revoke'
  error.value = ''
  try {
    await manager.request(`/api/v1/api-keys/${key.id}/revoke`, { method: 'POST' })
    await load()
  } catch (e: any) {
    error.value = e?.data?.error || e?.message || 'Unable to revoke API key'
  } finally {
    pending[key.id] = undefined
  }
}

async function copySecret() {
  if (secret.value) await navigator.clipboard.writeText(secret.value)
}

onMounted(load)
</script>

<template>
  <div class="space-y-4">
    <UPageHeader
      headline="OPENAI COMPATIBILITY"
      title="API"
      description="Connect OpenAI-compatible SDKs and LiteLLM to the unified manager endpoint."
      class="mb-7"
    />

    <UCard>
      <template #header>
        <div>
          <p class="mb-2 text-[11px] font-extrabold tracking-[0.18em] text-muted">BASE URL</p>
          <h2 class="text-xl font-bold text-highlighted">Unified endpoint</h2>
        </div>
      </template>
      <code class="block rounded-lg border border-default bg-muted px-4 py-3.5 text-sm text-primary [overflow-wrap:anywhere]">{{ apiBase }}/v1</code>
      <p class="mt-4 text-sm leading-6 text-muted">Supported initial routes: models, chat completions, completions, Responses and embeddings.</p>
    </UCard>

    <UCard v-if="isAdmin">
      <template #header>
        <div class="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
          <div>
            <p class="mb-2 text-[11px] font-extrabold tracking-[0.18em] text-muted">CREDENTIALS</p>
            <h2 class="text-xl font-bold text-highlighted">Inference API keys</h2>
          </div>
          <UFieldGroup class="w-full sm:w-auto">
            <UInput v-model="name" placeholder="Key name" class="min-w-0 flex-1 sm:w-48" />
            <UButton size="sm" @click="createKey">Create key</UButton>
          </UFieldGroup>
        </div>
      </template>

      <p class="mb-4 text-sm leading-6 text-muted">Disable keeps a key for later. Revoked keys are permanently removed.</p>
      <UAlert v-if="error" color="error" variant="subtle" icon="i-lucide-circle-alert" :description="error" class="mb-4" />

      <UCard v-if="secret" variant="subtle" class="mb-4 bg-primary/5">
        <div class="grid gap-3">
          <strong class="text-sm text-highlighted">Copy this key now. It will not be shown again.</strong>
          <code class="text-sm text-primary [overflow-wrap:anywhere]">{{ secret }}</code>
          <UButton color="neutral" variant="outline" size="sm" icon="i-lucide-copy" class="w-fit" @click="copySecret">Copy</UButton>
        </div>
      </UCard>

      <UTable v-if="keys.length" :data="keys" :columns="columns">
        <template #prefix-cell="{ row }">
          <code class="font-mono text-xs">{{ row.original.prefix }}…</code>
        </template>
        <template #enabled-cell="{ row }">
          <UBadge :color="row.original.enabled ? 'success' : 'neutral'" variant="subtle" size="sm">
            {{ row.original.enabled ? 'Enabled' : 'Disabled' }}
          </UBadge>
        </template>
        <template #actions-cell="{ row }">
          <div class="flex justify-end">
            <UFieldGroup>
              <UButton
                color="neutral"
                variant="outline"
                size="sm"
                :loading="pending[row.original.id] === 'toggle'"
                :disabled="!!pending[row.original.id]"
                @click="setEnabled(row.original)"
              >
                {{ pending[row.original.id] === 'toggle' ? 'Saving…' : row.original.enabled ? 'Disable' : 'Enable' }}
              </UButton>
              <UButton
                color="error"
                variant="subtle"
                size="sm"
                :loading="pending[row.original.id] === 'revoke'"
                :disabled="!!pending[row.original.id]"
                @click="revoke(row.original)"
              >
                {{ pending[row.original.id] === 'revoke' ? 'Revoking…' : 'Revoke' }}
              </UButton>
            </UFieldGroup>
          </div>
        </template>
      </UTable>
      <UEmpty
        v-else
        icon="i-lucide-key-round"
        title="No API keys created yet."
        description="Create a key to authenticate OpenAI-compatible inference clients."
      />
    </UCard>

    <UAlert
      v-else
      color="neutral"
      variant="subtle"
      icon="i-lucide-shield"
      title="API key management unavailable"
      description="Only administrators can manage inference API keys."
    />
  </div>
</template>
