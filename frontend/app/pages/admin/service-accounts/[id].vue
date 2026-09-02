<script setup lang="ts">
import type { TableColumn } from '@nuxt/ui'
import type { APIKey, ServiceAccount } from '~/composables/useManager'
import { apiKeyStatus, apiKeyTypeLabel, formatAPIKeyPrefix, formatAPIKeyTimestamp } from '~/utils/apiKeys'

type ServiceAccountDetail = ServiceAccount & { keys?: APIKey[] }

const manager = useManager()
const route = useRoute()
const router = useRouter()
const id = computed(() => String(route.params.id || ''))
const account = ref<ServiceAccountDetail | null>(null)
const error = ref('')
const success = ref('')
const saving = ref(false)
const editOpen = ref(false)
const draft = reactive({ name: '' })
const confirmation = ref<{ request: (options: Record<string, string>) => Promise<boolean> } | null>(null)

const columns: TableColumn<APIKey>[] = [
  { accessorKey: 'name', header: 'Name' },
  { accessorKey: 'key_type', header: 'Type' },
  { accessorKey: 'prefix', header: 'Prefix' },
  { id: 'status', header: 'Status' },
  { accessorKey: 'expires_on', header: 'Expires' },
  { accessorKey: 'last_used_at', header: 'Last used' }
]

const keys = computed(() => account.value?.keys || [])
const canSave = computed(() => draft.name.trim().length >= 2)

function dateTime(value?: number) { return value ? new Date(value * 1000).toLocaleString() : 'Never' }

function normalizeDetail(value: ServiceAccountDetail | { account?: ServiceAccountDetail; keys?: APIKey[] } | null): ServiceAccountDetail | null {
  if (!value) return null
  if ('account' in value && value.account) return { ...value.account, keys: value.keys || value.account.keys || [] }
  return { ...value, keys: value.keys || [] }
}

async function load() {
  if (!manager.user.value || !id.value) return
  error.value = ''
  try {
    account.value = normalizeDetail(await manager.request<ServiceAccountDetail>(`/api/v1/admin/service-accounts/${encodeURIComponent(id.value)}`))
  } catch (value: any) {
    account.value = null
    error.value = value?.data?.error || value?.message || 'Unable to load service account'
  }
}
watch([manager.user, id], ([user]) => { if (user) void load() }, { immediate: true })

function openEdit() {
  if (!account.value) return
  draft.name = account.value.name
  editOpen.value = true
}

async function saveAccount() {
  if (!account.value) return
  error.value = ''
  success.value = ''
  saving.value = true
  try {
    await manager.request(`/api/v1/admin/service-accounts/${account.value.id}`, { method: 'PATCH', body: { name: draft.name.trim() } })
    editOpen.value = false
    success.value = 'Service account updated.'
    await load()
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to update service account'
  } finally {
    saving.value = false
  }
}

async function toggleAccount() {
  if (!account.value) return
  const action = account.value.enabled ? 'disable' : 'enable'
  const confirmed = await confirmation.value?.request({
    title: `${action === 'disable' ? 'Disable' : 'Enable'} service account`,
    description: action === 'disable'
      ? `Disable “${account.value.name}”? Keys owned by this service account will fail authentication until it is enabled again.`
      : `Enable “${account.value.name}”?`,
    confirmLabel: action === 'disable' ? 'Disable service account' : 'Enable service account',
    confirmTone: action === 'disable' ? 'destructive' : 'default'
  })
  if (!confirmed) return
  try {
    await manager.request(`/api/v1/admin/service-accounts/${account.value.id}`, { method: 'PATCH', body: { enabled: !account.value.enabled } })
    await load()
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || `Unable to ${action} service account`
  }
}

async function deleteAccount() {
  if (!account.value) return
  const confirmed = await confirmation.value?.request({
    title: 'Delete service account',
    description: `Delete “${account.value.name}”? API keys owned by this service account will be deleted with the account and will stop authenticating immediately.`,
    confirmLabel: 'Delete service account',
    confirmTone: 'destructive'
  })
  if (!confirmed) return
  try {
    await manager.request(`/api/v1/admin/service-accounts/${account.value.id}`, { method: 'DELETE' })
    await router.push('/admin/service-accounts')
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to delete service account'
  }
}
</script>

<template>
  <AdminShell :title="account?.name || 'Service account'" description="Account fields and the keys owned by this service account.">
    <template #actions>
      <AppButton intent="secondary" to="/admin/service-accounts">All service accounts</AppButton>
      <AppButton v-if="account" intent="secondary" @click="openEdit">Rename</AppButton>
      <AppButton v-if="account" intent="secondary" @click="toggleAccount">{{ account.enabled ? 'Disable' : 'Enable' }}</AppButton>
      <AppButton v-if="account" intent="secondary" tone="destructive" @click="deleteAccount">Delete</AppButton>
    </template>

    <Frame v-if="error" class="mb-5 p-3"><div class="flex items-start gap-2"><StatusTag variant="failed">Error</StatusTag><p class="text-xs leading-5 text-[var(--neutral-800)]">{{ error }}</p></div></Frame>
    <Frame v-if="success" class="mb-5 p-3"><div class="flex items-start gap-2"><StatusTag variant="ready">Saved</StatusTag><p class="text-xs leading-5 text-[var(--neutral-800)]">{{ success }}</p></div></Frame>

    <Frame v-if="account" class="mb-5 p-5" data-testid="service-account-detail">
      <p class="text-[length:var(--font-size-kicker)] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">ACCOUNT</p>
      <div class="mt-3 grid gap-3 sm:grid-cols-2">
        <div>
          <p class="text-xs text-[var(--neutral-700)]">Name</p>
          <p class="mt-1 text-sm font-semibold">{{ account.name }}</p>
        </div>
        <div>
          <p class="text-xs text-[var(--neutral-700)]">Status</p>
          <div class="mt-1"><StatusTag :variant="account.enabled ? 'ready' : 'neutral'">{{ account.enabled ? 'Enabled' : 'Disabled' }}</StatusTag></div>
        </div>
        <div>
          <p class="text-xs text-[var(--neutral-700)]">Created</p>
          <p class="mt-1 font-mono text-xs tabular-nums text-[var(--neutral-700)]">{{ dateTime(account.created_at) }}</p>
        </div>
      </div>
    </Frame>

    <Frame v-if="account" data-testid="service-account-keys" class="overflow-hidden p-0">
      <div class="border-b border-[var(--color-divider)] px-4 py-3">
        <p class="text-[length:var(--font-size-kicker)] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">KEYS</p>
        <p class="mt-1 text-xs text-[var(--neutral-700)]">Create, edit and rotate secrets on <NuxtLink to="/api" class="font-semibold text-[var(--accent-700)] hover:underline">API</NuxtLink>.</p>
      </div>
      <div v-if="keys.length" class="overflow-x-auto" role="region" tabindex="0" aria-label="Service account API keys. Scroll horizontally for type, status and expiry.">
        <UTable class="min-w-[720px]" :data="keys" :columns="columns">
          <template #name-cell="{ row }"><span class="text-[length:var(--font-size-table-body)] font-semibold">{{ row.original.name }}</span></template>
          <template #key_type-cell="{ row }"><span class="text-[length:var(--font-size-table-body)]">{{ apiKeyTypeLabel(row.original.key_type) }}</span></template>
          <template #prefix-cell="{ row }"><span class="font-mono text-[length:var(--font-size-h6)] text-[var(--neutral-700)]">{{ formatAPIKeyPrefix(row.original.prefix) }}</span></template>
          <template #status-cell="{ row }"><StatusTag :variant="apiKeyStatus(row.original).variant">{{ apiKeyStatus(row.original).label }}</StatusTag></template>
          <template #expires_on-cell="{ row }"><span class="font-mono text-xs tabular-nums text-[var(--neutral-700)]">{{ row.original.expires_on || '—' }}</span></template>
          <template #last_used_at-cell="{ row }"><span class="font-mono text-xs tabular-nums text-[var(--neutral-700)]">{{ formatAPIKeyTimestamp(row.original.last_used_at) }}</span></template>
        </UTable>
      </div>
      <UEmpty v-else variant="naked" class="px-4 py-8" title="No keys for this account" description="Create a key owned by this service account on the API page." />
    </Frame>

    <UModal v-model:open="editOpen" title="Rename service account">
      <template #body>
        <div class="space-y-4">
          <UFormField label="Name" required><UInput v-model="draft.name" class="w-full" autocomplete="off" minlength="2" required /></UFormField>
        </div>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <AppButton intent="secondary" @click="editOpen = false">Cancel</AppButton>
          <AppButton intent="primary" :loading="saving" :disabled="!canSave" @click="saveAccount">Save</AppButton>
        </div>
      </template>
    </UModal>

    <AppConfirmationModal ref="confirmation" />
  </AdminShell>
</template>
