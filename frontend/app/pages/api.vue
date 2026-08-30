<script setup lang="ts">
import type { TableColumn } from '@nuxt/ui'
import type { APIKey } from '~/composables/useManager'

type ManagedAPIKey = APIKey & { revoked_at?: number }

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
  { id: 'status', header: 'Status' },
  { accessorKey: 'created_at', header: 'Created' },
  { accessorKey: 'last_used_at', header: 'Last used' },
  { id: 'actions', header: 'Actions' }
]

function formatTimestamp(value?: number) {
  if (!value) return '—'
  const date = new Date(value * 1000)
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString()
}

function statusVariant(key: ManagedAPIKey) {
  if (key.revoked_at) return 'failed' as const
  return key.enabled ? 'ready' as const : 'neutral' as const
}

function statusLabel(key: ManagedAPIKey) {
  if (key.revoked_at) return 'Revoked'
  return key.enabled ? 'Enabled' : 'Disabled'
}

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
    confirmTone: 'destructive'
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
    confirmTone: 'destructive'
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

    <Frame class="p-5" data-testid="api-base-url">
      <p class="text-[9.5px] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">BASE URL</p>
      <h2 class="mt-1 text-base font-semibold">Unified endpoint</h2>
      <div class="my-4 flex items-center gap-2 border-y border-[var(--color-divider)] py-3">
        <code class="min-w-0 flex-1 break-all font-mono text-[13.5px] text-[var(--accent-700)]">{{ apiBase }}/v1</code>
        <AppCopyButton :text="`${apiBase}/v1`" icon-only color="neutral" variant="ghost" size="sm" error-message="Unable to copy Base URL. Select it and copy it manually." data-testid="copy-api-base-url" />
      </div>
      <p class="text-sm leading-6 text-[var(--neutral-700)]">Supported routes: models, chat completions, completions, Responses and embeddings.</p>
    </Frame>

    <Frame class="p-5" data-testid="api-keys-card">
      <div class="flex flex-col gap-4 md:flex-row md:items-end md:justify-between">
        <div>
          <p class="text-[9.5px] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">CREDENTIALS</p>
          <h2 class="mt-1 text-base font-semibold">Inference API keys</h2>
        </div>
        <UFieldGroup class="w-full rounded-none md:w-auto">
          <UInput v-model="name" data-testid="key-name" class="min-w-0 flex-1 md:w-48" placeholder="Key name" />
          <AppButton data-testid="create-key" intent="primary" size="sm" @click="createKey">Create key</AppButton>
        </UFieldGroup>
      </div>

      <p class="mt-4 text-sm text-[var(--neutral-700)]">Disable keeps a key for later. Revoke invalidates it permanently while retaining safe metadata. Rotation returns replacement plaintext once. Keys authenticate clients, not users.</p>
      <Frame v-if="error" class="mt-4 border-[var(--accent-800)] p-3" data-testid="api-key-error">
        <p class="text-sm font-semibold text-[var(--accent-900)]">API key operation failed</p>
        <p class="mt-1 text-xs text-[var(--neutral-800)]">{{ error }}</p>
      </Frame>

      <section v-if="secret" class="mt-4 border-y border-[var(--color-divider)] py-4" data-testid="fresh-api-key">
        <div class="space-y-3">
          <strong class="text-sm font-semibold">Copy this key now. It will not be shown again.</strong>
          <code class="block break-all font-mono text-[13.5px] text-[var(--accent-700)]">{{ secret }}</code>
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

      <p v-if="keys.length" class="mt-4 text-xs text-[var(--neutral-700)] md:hidden">Scroll horizontally for key status, timestamps and actions.</p>
      <div v-if="keys.length" class="mt-2 overflow-x-auto border border-[var(--color-divider)]" data-testid="api-keys-table" role="region" tabindex="0" aria-label="API keys. Scroll horizontally for status, timestamps and actions.">
        <UTable class="min-w-[760px]" :data="keys" :columns="columns">
          <template #name-cell="{ row }"><span class="text-[13.5px] font-semibold">{{ row.original.name }}</span></template>
          <template #prefix-cell="{ row }"><span class="font-mono text-[11.5px] text-[var(--neutral-700)]">{{ row.original.prefix }}…</span></template>
          <template #status-cell="{ row }"><StatusTag :variant="statusVariant(row.original)">{{ statusLabel(row.original) }}</StatusTag></template>
          <template #created_at-cell="{ row }"><span class="text-xs text-[var(--neutral-700)]">{{ formatTimestamp(row.original.created_at) }}</span></template>
          <template #last_used_at-cell="{ row }"><span class="text-xs text-[var(--neutral-700)]">{{ formatTimestamp(row.original.last_used_at) }}</span></template>
          <template #actions-cell="{ row }">
            <div v-if="!row.original.revoked_at" class="flex justify-end gap-1">
              <AppButton intent="ghost" size="xs" :loading="pending[row.original.id] === 'toggle'" :disabled="!!pending[row.original.id]" @click="setEnabled(row.original)">{{ row.original.enabled ? 'Disable' : 'Enable' }}</AppButton>
              <AppButton intent="ghost" size="xs" :loading="pending[row.original.id] === 'rotate'" :disabled="!!pending[row.original.id]" @click="rotate(row.original)">Rotate</AppButton>
              <AppButton intent="ghost" size="xs" :loading="pending[row.original.id] === 'revoke'" :disabled="!!pending[row.original.id]" @click="revoke(row.original)">Revoke</AppButton>
            </div>
          </template>
        </UTable>
      </div>

      <div v-else class="mt-4 border border-[var(--color-divider)] px-4 py-8 text-center" data-testid="api-keys-empty">
        <p class="text-sm font-semibold">No API keys created yet.</p>
        <p class="mt-1 text-xs text-[var(--neutral-700)]">Create a key to authenticate OpenAI-compatible clients.</p>
      </div>
    </Frame>
    <AppConfirmationModal ref="confirmation" />
  </div>
</template>