<script setup lang="ts">
import type { TableColumn } from '@nuxt/ui'
import type { APIKey } from '~/composables/useManager'

type ManagedAPIKey = APIKey & { revoked_at?: number; created_by_user_id?: number }

const manager = useManager()
const { apiBase } = manager
const keys = ref<ManagedAPIKey[]>([])
const name = ref('default')
const secret = ref('')
const error = ref('')
const pending = reactive<Record<string, 'toggle' | 'revoke' | 'rotate' | undefined>>({})
const confirmation = ref<{ request: (options: Record<string, string>) => Promise<boolean> } | null>(null)

const columns: TableColumn<ManagedAPIKey>[] = [
  { accessorKey: 'name', header: 'Name' },
  { accessorKey: 'prefix', header: 'Prefix' },
  { accessorKey: 'enabled', header: 'Status' },
  { id: 'actions', header: '' }
]

async function load() {
  if (!manager.user.value) return
  try {
    keys.value = (await manager.request<ManagedAPIKey[]>('/api/v1/api-keys')) || []
  } catch (e: any) {
    error.value = e?.data?.error || e?.message || 'Unable to load API keys'
  }
}

watch(manager.user, user => {
  if (user) void load()
}, { immediate: true })

async function createKey() {
  error.value = ''
  try {
    const result = await manager.request<{ key: ManagedAPIKey; secret: string }>('/api/v1/api-keys', { method: 'POST', body: { name: name.value } })
    secret.value = result.secret
    await load()
  } catch (e: any) {
    error.value = e?.data?.error || e?.message || 'Unable to create API key'
  }
}

async function setEnabled(key: ManagedAPIKey) {
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

async function revoke(key: ManagedAPIKey) {
  const confirmed = await confirmation.value?.request({
    title: 'Revoke API key',
    description: `Revoke API key “${key.name}”? Existing clients using it will fail immediately. Revoked metadata is retained for history.`,
    confirmLabel: 'Revoke key',
    color: 'error'
  })
  if (!confirmed) return
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

async function rotate(key: ManagedAPIKey) {
  const confirmed = await confirmation.value?.request({
    title: 'Rotate API key',
    description: `Rotate “${key.name}”? The current secret will be revoked immediately and a replacement secret will be shown once.`,
    confirmLabel: 'Rotate key',
    color: 'warning'
  })
  if (!confirmed) return
  pending[key.id] = 'rotate'
  error.value = ''
  try {
    const result = await manager.request<{ key: ManagedAPIKey; secret: string }>(`/api/v1/api-keys/${key.id}/rotate`, { method: 'POST' })
    secret.value = result.secret
    await load()
  } catch (e: any) {
    error.value = e?.data?.error || e?.message || 'Unable to rotate API key'
  } finally {
    pending[key.id] = undefined
  }
}
</script>

<template>
  <div class="space-y-5">
    <UPageHeader headline="OPENAI COMPATIBILITY" title="API" description="Connect OpenAI-compatible SDKs and LiteLLM to the unified manager endpoint." />

    <UCard>
      <p class="mb-1 text-xs font-extrabold tracking-[0.18em] text-dimmed">BASE URL</p>
      <h2 class="text-xl font-bold">Unified endpoint</h2>
      <code class="my-4 block break-all border-y border-default py-3 font-mono text-sm text-primary">{{ apiBase }}/v1</code>
      <p class="text-sm leading-6 text-muted">Supported initial routes: models, chat completions, completions, Responses and embeddings.</p>
    </UCard>

    <UCard>
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

      <p class="mb-4 text-sm text-muted">Disable keeps a key for later. Revoke invalidates it permanently while retaining safe metadata. Rotation returns replacement plaintext once.</p>
      <UAlert v-if="error" class="mb-4" color="error" variant="subtle" :description="error" />

      <section v-if="secret" class="mb-4 border-y border-default py-4">
        <div class="space-y-3">
          <strong class="text-sm">Copy this key now. It will not be shown again.</strong>
          <code class="block break-all font-mono text-sm text-primary">{{ secret }}</code>
          <AppCopyButton
            :text="secret"
            color="neutral"
            variant="soft"
            size="sm"
            error-message="Unable to copy API key. Select the key and copy it manually."
            data-testid="copy-key"
            @copied="error = ''"
            @error="message => error = message"
          />
        </div>
      </section>

      <UTable v-if="keys.length" :data="keys" :columns="columns">
        <template #prefix-cell="{ row }"><span class="font-mono text-xs">{{ row.original.prefix }}…</span></template>
        <template #enabled-cell="{ row }">
          <UBadge v-if="row.original.revoked_at" color="error" variant="subtle" size="sm">Revoked</UBadge>
          <UBadge v-else :color="row.original.enabled ? 'success' : 'neutral'" variant="subtle" size="sm">{{ row.original.enabled ? 'Enabled' : 'Disabled' }}</UBadge>
        </template>
        <template #actions-cell="{ row }">
          <div class="flex justify-end">
            <UFieldGroup v-if="!row.original.revoked_at">
              <UButton color="neutral" variant="soft" size="sm" :loading="pending[row.original.id] === 'toggle'" :disabled="!!pending[row.original.id]" @click="setEnabled(row.original)">{{ row.original.enabled ? 'Disable' : 'Enable' }}</UButton>
              <UButton color="warning" variant="soft" size="sm" :loading="pending[row.original.id] === 'rotate'" :disabled="!!pending[row.original.id]" @click="rotate(row.original)">Rotate</UButton>
              <UButton color="error" variant="soft" size="sm" :loading="pending[row.original.id] === 'revoke'" :disabled="!!pending[row.original.id]" @click="revoke(row.original)">Revoke</UButton>
            </UFieldGroup>
          </div>
        </template>
      </UTable>

      <UEmpty v-else variant="naked" title="No API keys created yet." description="Create a key to authenticate OpenAI-compatible clients." />
    </UCard>
    <AppConfirmationModal ref="confirmation" />
  </div>
</template>
