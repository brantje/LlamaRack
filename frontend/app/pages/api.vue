<script setup lang="ts">
import type { TableColumn } from '@nuxt/ui'
import type { APIKey, ServiceAccount, User } from '~/composables/useManager'
import APIKeyModal from '~/components/APIKeyModal.vue'
import type { APIKeyDraft } from '~/utils/apiKeys'
import {
  apiKeyStatus,
  apiKeyTypeLabel,
  formatAPIKeyPrefix,
  formatAPIKeyTimestamp
} from '~/utils/apiKeys'

type AdminUser = User & { created_at?: number; last_login_at?: number }

const manager = useManager()
const { apiBase } = manager
const keys = ref<APIKey[]>([])
const users = ref<AdminUser[]>([])
const serviceAccounts = ref<ServiceAccount[]>([])
const error = ref('')
const pending = reactive<Record<string, 'toggle' | 'rotate' | 'edit' | undefined>>({})
const confirmation = ref<{ request: (options: Record<string, string>) => Promise<boolean> } | null>(null)
const modalOpen = ref(false)
const modalPhase = ref<'form' | 'secret'>('form')
const editingKey = ref<APIKey | null>(null)
const secret = ref('')
const submitting = ref(false)

const columns: TableColumn<APIKey>[] = [
  { accessorKey: 'name', header: 'Name' },
  { accessorKey: 'owner_name', header: 'Owner' },
  { accessorKey: 'key_type', header: 'Type' },
  { accessorKey: 'prefix', header: 'Prefix' },
  { id: 'status', header: 'Status' },
  { accessorKey: 'expires_on', header: 'Expires' },
  { accessorKey: 'last_used_at', header: 'Last used' },
  { id: 'actions', header: 'Actions' }
]

async function load() {
  if (!manager.user.value) return
  error.value = ''
  try {
    keys.value = (await manager.request<APIKey[]>('/api/v1/api-keys')) || []
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to load API keys'
  }
}

async function loadOwners() {
  try {
    users.value = (await manager.request<AdminUser[]>('/api/v1/users')) || []
  } catch {
    users.value = manager.user.value ? [manager.user.value] : []
  }
  try {
    serviceAccounts.value = (await manager.request<ServiceAccount[]>('/api/v1/admin/service-accounts')) || []
  } catch {
    serviceAccounts.value = []
  }
}

watch(manager.user, user => {
  if (user) void load()
}, { immediate: true })

async function openCreate() {
  error.value = ''
  secret.value = ''
  editingKey.value = null
  modalPhase.value = 'form'
  await loadOwners()
  modalOpen.value = true
}

async function openEdit(key: APIKey) {
  error.value = ''
  secret.value = ''
  editingKey.value = key
  modalPhase.value = 'form'
  await loadOwners()
  modalOpen.value = true
}

function closeModal() {
  modalOpen.value = false
  modalPhase.value = 'form'
  editingKey.value = null
  submitting.value = false
}

function createBody(draft: APIKeyDraft) {
  const body: Record<string, unknown> = { name: draft.name, key_type: draft.key_type }
  if (draft.owner_user_id != null) body.owner_user_id = draft.owner_user_id
  if (draft.owner_service_account_id) body.owner_service_account_id = draft.owner_service_account_id
  if (draft.key_type === 'inference' && draft.instance_ids?.length) body.instance_ids = draft.instance_ids
  if (draft.expires_on) body.expires_on = draft.expires_on
  return body
}

function patchBody(draft: APIKeyDraft) {
  if (editingKey.value?.managed) {
    return {
      expires_on: draft.expires_on ?? null,
      instance_ids: draft.instance_ids || []
    }
  }
  const body: Record<string, unknown> = {
    name: draft.name,
    owner_user_id: draft.owner_user_id ?? null,
    owner_service_account_id: draft.owner_service_account_id ?? null,
    expires_on: draft.expires_on ?? null
  }
  if (editingKey.value?.key_type === 'inference') body.instance_ids = draft.instance_ids || []
  return body
}

async function saveKey(draft: APIKeyDraft) {
  const editingId = editingKey.value?.id
  submitting.value = true
  error.value = ''
  if (editingId) pending[editingId] = 'edit'
  try {
    if (editingId) {
      await manager.request(`/api/v1/api-keys/${editingId}`, { method: 'PATCH', body: patchBody(draft) })
      closeModal()
      await load()
      return
    }
    const result = await manager.request<{ key: APIKey; secret: string }>('/api/v1/api-keys', { method: 'POST', body: createBody(draft) })
    secret.value = result.secret
    modalPhase.value = 'secret'
    await load()
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || (editingId ? 'Unable to update API key' : 'Unable to create API key')
  } finally {
    submitting.value = false
    if (editingId) pending[editingId] = undefined
  }
}

async function setEnabled(key: APIKey) {
  if (key.enabled) {
    const confirmed = await confirmation.value?.request({
      title: 'Disable API key',
      description: `Disable “${key.name}”? Clients using this key will fail until it is enabled again.`,
      confirmLabel: 'Disable key',
      confirmTone: 'destructive'
    })
    if (!confirmed) return
  }
  pending[key.id] = 'toggle'
  error.value = ''
  try {
    await manager.request(`/api/v1/api-keys/${key.id}`, { method: 'PATCH', body: { enabled: !key.enabled } })
    await load()
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to update API key'
  } finally {
    pending[key.id] = undefined
  }
}

async function rotate(key: APIKey) {
  const confirmed = await confirmation.value?.request({
    title: 'Rotate API key',
    description: `Rotate “${key.name}”? The current secret will stop working immediately and a replacement secret will be shown once.`,
    confirmLabel: 'Rotate key',
    confirmTone: 'destructive'
  })
  if (!confirmed) return
  pending[key.id] = 'rotate'
  error.value = ''
  try {
    const result = await manager.request<{ key: APIKey; secret: string }>(`/api/v1/api-keys/${key.id}/rotate`, { method: 'POST' })
    secret.value = result.secret
    editingKey.value = null
    modalPhase.value = 'secret'
    modalOpen.value = true
    await load()
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to rotate API key'
  } finally {
    pending[key.id] = undefined
  }
}
</script>

<template>
  <div class="space-y-5">
    <UPageHeader headline="OPENAI COMPATIBILITY" title="API" description="Connect OpenAI-compatible SDKs and LiteLLM to the unified manager endpoint." />

    <Frame class="p-5" data-testid="api-base-url">
      <p class="text-[length:var(--font-size-kicker)] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">BASE URL</p>
      <h2 class="mt-1 text-base font-semibold">Unified endpoint</h2>
      <div class="my-4 flex items-center gap-2 border-y border-[var(--color-divider)] py-3">
        <code class="min-w-0 flex-1 break-all font-mono text-[length:var(--font-size-table-body)] text-[var(--accent-700)]">{{ apiBase }}/v1</code>
        <AppCopyButton :text="`${apiBase}/v1`" icon-only color="neutral" variant="ghost" size="sm" error-message="Unable to copy Base URL. Select it and copy it manually." data-testid="copy-api-base-url" />
      </div>
      <p class="text-sm leading-6 text-[var(--neutral-700)]">Supported routes: models, chat completions, completions, Responses and embeddings.</p>
    </Frame>

    <Frame v-if="error" class="p-3" data-testid="api-key-error">
      <div class="flex items-start gap-2">
        <StatusTag variant="failed">Error</StatusTag>
        <p class="text-xs leading-5 text-[var(--neutral-800)]">{{ error }}</p>
      </div>
    </Frame>

    <Frame class="p-5" data-testid="api-keys-card">
      <div class="flex flex-col gap-4 md:flex-row md:items-end md:justify-between">
        <div>
          <p class="text-[length:var(--font-size-kicker)] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">CREDENTIALS</p>
          <h2 class="mt-1 text-base font-semibold">API keys</h2>
          <p class="mt-2 text-sm text-[var(--neutral-700)]">Keys are owned by a user or service account. Disable keeps a key for later. Rotation replaces the secret in place and shows it once.</p>
        </div>
        <AppButton data-testid="create-key" intent="primary" size="sm" @click="openCreate">Create key</AppButton>
      </div>

      <p v-if="keys.length" class="mt-4 text-xs text-[var(--neutral-700)] md:hidden">Scroll horizontally for owner, type, status, expiry and actions.</p>
      <div v-if="keys.length" class="mt-2 overflow-x-auto border border-[var(--color-divider)]" data-testid="api-keys-table" role="region" tabindex="0" aria-label="API keys. Scroll horizontally for owner, type, status, expiry and actions.">
        <UTable class="min-w-[960px]" :data="keys" :columns="columns">
          <template #name-cell="{ row }"><span class="text-[length:var(--font-size-table-body)] font-semibold">{{ row.original.name }}</span></template>
          <template #owner_name-cell="{ row }"><span class="text-[length:var(--font-size-table-body)]">{{ row.original.owner_name || '—' }}</span></template>
          <template #key_type-cell="{ row }"><span class="text-[length:var(--font-size-table-body)]">{{ apiKeyTypeLabel(row.original.key_type) }}</span></template>
          <template #prefix-cell="{ row }"><span class="font-mono text-[length:var(--font-size-h6)] text-[var(--neutral-700)]">{{ formatAPIKeyPrefix(row.original.prefix) }}</span></template>
          <template #status-cell="{ row }"><StatusTag :variant="apiKeyStatus(row.original).variant">{{ apiKeyStatus(row.original).label }}</StatusTag></template>
          <template #expires_on-cell="{ row }"><span class="font-mono text-xs tabular-nums text-[var(--neutral-700)]">{{ row.original.expires_on || '—' }}</span></template>
          <template #last_used_at-cell="{ row }"><span class="font-mono text-xs tabular-nums text-[var(--neutral-700)]">{{ formatAPIKeyTimestamp(row.original.last_used_at) }}</span></template>
          <template #actions-cell="{ row }">
            <div class="flex justify-end gap-1">
              <AppButton intent="ghost" size="xs" :disabled="!!pending[row.original.id]" @click="openEdit(row.original)">Edit</AppButton>
              <AppButton intent="ghost" size="xs" :loading="pending[row.original.id] === 'toggle'" :disabled="!!pending[row.original.id]" @click="setEnabled(row.original)">{{ row.original.enabled ? 'Disable' : 'Enable' }}</AppButton>
              <AppButton v-if="!row.original.managed" intent="ghost" size="xs" :loading="pending[row.original.id] === 'rotate'" :disabled="!!pending[row.original.id]" @click="rotate(row.original)">Rotate</AppButton>
            </div>
          </template>
        </UTable>
      </div>

      <div v-else data-testid="api-keys-empty">
        <UEmpty
          variant="naked"
          title="No API keys created yet."
          description="Create a key to authenticate OpenAI-compatible clients or management automation."
        />
      </div>
    </Frame>

    <APIKeyModal
      v-model:open="modalOpen"
      :phase="modalPhase"
      :editing="!!editingKey"
      :submitting="submitting"
      :secret="secret"
      :users="users"
      :service-accounts="serviceAccounts"
      :instances="manager.instances.value"
      :current-user-id="manager.user.value?.id"
      :initial-key="editingKey"
      @save="saveKey"
      @close="closeModal"
    />
    <AppConfirmationModal ref="confirmation" />
  </div>
</template>
