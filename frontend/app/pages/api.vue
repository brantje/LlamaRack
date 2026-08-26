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
  <div class="space-y-5">
    <UPageHeader
      headline="OPENAI COMPATIBILITY"
      title="API"
      description="Connect OpenAI-compatible SDKs and LiteLLM to the unified manager endpoint."
    />

    <UCard>
      <p class="mb-1 text-xs font-extrabold tracking-[0.18em] text-dimmed">BASE URL</p>
      <h2 class="text-xl font-bold">Unified endpoint</h2>
      <div class="my-4 rounded-lg border border-default bg-default px-4 py-3 font-mono text-sm text-primary">{{ apiBase }}/v1</div>
      <p class="text-sm leading-6 text-muted">Supported initial routes: models, chat completions, completions, Responses and embeddings.</p>
    </UCard>

    <UCard v-if="isAdmin">
      <template #header>
        <div class="flex flex-col gap-4 md:flex-row md:items-end md:justify-between">
          <div>
            <p class="mb-1 text-xs font-extrabold tracking-[0.18em] text-dimmed">CREDENTIALS</p>
            <h2 class="text-xl font-bold">Inference API keys</h2>
          </div>
          <UFieldGroup class="w-full md:w-auto">
            <UInput v-model="name" data-testid="key-name" class="min-w-0 flex-1 md:w-48" placeholder="Key name" />
            <UButton data-testid="create-key" size="sm" @click="createKey">Create key</UButton>
          </UFieldGroup>
        </div>
      </template>

      <p class="mb-4 text-sm text-muted">Disable keeps a key for later. Revoked keys are permanently removed.</p>
      <UAlert v-if="error" class="mb-4" color="error" variant="subtle" :description="error" />

      <section v-if="secret" class="mb-4 border-y border-default py-4">
        <div class="space-y-3">
          <strong class="text-sm">Copy this key now. It will not be shown again.</strong>
          <code class="block break-all font-mono text-sm text-primary">{{ secret }}</code>
          <UButton data-testid="copy-key" color="neutral" variant="soft" size="sm" @click="copySecret">Copy</UButton>
        </div>
      </section>

      <UTable v-if="keys.length" :data="keys" :columns="columns">
        <template #prefix-cell="{ row }">
          <span class="font-mono text-xs">{{ row.original.prefix }}…</span>
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
                variant="soft"
                size="sm"
                :loading="pending[row.original.id] === 'toggle'"
                :disabled="!!pending[row.original.id]"
                @click="setEnabled(row.original)"
              >
                {{ row.original.enabled ? 'Disable' : 'Enable' }}
              </UButton>
              <UButton
                color="error"
                variant="soft"
                size="sm"
                :loading="pending[row.original.id] === 'revoke'"
                :disabled="!!pending[row.original.id]"
                @click="revoke(row.original)"
              >
                Revoke
              </UButton>
            </UFieldGroup>
          </div>
        </template>
      </UTable>

      <UEmpty v-else title="No API keys created yet." description="Create a key to authenticate OpenAI-compatible clients." />
    </UCard>

    <UAlert v-else color="neutral" variant="subtle" description="Only administrators can manage inference API keys." />
  </div>
</template>
