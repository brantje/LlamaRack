<script setup lang="ts">
import type { TableColumn } from '@nuxt/ui'
import type { ServiceAccount } from '~/composables/useManager'

const manager = useManager()
const accounts = ref<ServiceAccount[]>([])
const error = ref('')
const success = ref('')
const creating = ref(false)
const saving = ref(false)
const createOpen = ref(false)
const editOpen = ref(false)
const selected = ref<ServiceAccount | null>(null)
const draft = reactive({ name: '' })
const confirmation = ref<{ request: (options: Record<string, string>) => Promise<boolean> } | null>(null)

const columns: TableColumn<ServiceAccount>[] = [
  { accessorKey: 'name', header: 'Name' },
  { accessorKey: 'enabled', header: 'Status' },
  { accessorKey: 'created_at', header: 'Created' },
  { id: 'actions', header: 'Actions' }
]

const canCreate = computed(() => draft.name.trim().length >= 2)

function dateTime(value?: number) { return value ? new Date(value * 1000).toLocaleString() : 'Never' }

async function load() {
  if (!manager.user.value) return
  error.value = ''
  try { accounts.value = (await manager.request<ServiceAccount[]>('/api/v1/admin/service-accounts')) || [] }
  catch (value: any) { error.value = value?.data?.error || value?.message || 'Unable to load service accounts' }
}
watch(manager.user, user => { if (user) void load() }, { immediate: true })

function openCreate() {
  draft.name = ''
  createOpen.value = true
}

function openEdit(account: ServiceAccount) {
  selected.value = account
  draft.name = account.name
  editOpen.value = true
}

async function createAccount() {
  error.value = ''
  success.value = ''
  creating.value = true
  try {
    await manager.request('/api/v1/admin/service-accounts', { method: 'POST', body: { name: draft.name.trim() } })
    createOpen.value = false
    success.value = 'Service account created.'
    await load()
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to create service account'
  } finally {
    creating.value = false
  }
}

async function saveAccount() {
  if (!selected.value) return
  error.value = ''
  success.value = ''
  saving.value = true
  try {
    await manager.request(`/api/v1/admin/service-accounts/${selected.value.id}`, { method: 'PATCH', body: { name: draft.name.trim() } })
    editOpen.value = false
    success.value = 'Service account updated.'
    await load()
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to update service account'
  } finally {
    saving.value = false
  }
}

async function toggleAccount(account: ServiceAccount) {
  const action = account.enabled ? 'disable' : 'enable'
  const confirmed = await confirmation.value?.request({
    title: `${action === 'disable' ? 'Disable' : 'Enable'} service account`,
    description: action === 'disable'
      ? `Disable “${account.name}”? Keys owned by this service account will fail authentication until it is enabled again.`
      : `Enable “${account.name}”?`,
    confirmLabel: action === 'disable' ? 'Disable service account' : 'Enable service account',
    confirmTone: action === 'disable' ? 'destructive' : 'default'
  })
  if (!confirmed) return
  try {
    await manager.request(`/api/v1/admin/service-accounts/${account.id}`, { method: 'PATCH', body: { enabled: !account.enabled } })
    await load()
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || `Unable to ${action} service account`
  }
}

async function deleteAccount(account: ServiceAccount) {
  const confirmed = await confirmation.value?.request({
    title: 'Delete service account',
    description: `Delete “${account.name}”? API keys owned by this service account will be deleted with the account and will stop authenticating immediately.`,
    confirmLabel: 'Delete service account',
    confirmTone: 'destructive'
  })
  if (!confirmed) return
  try {
    await manager.request(`/api/v1/admin/service-accounts/${account.id}`, { method: 'DELETE' })
    success.value = `Deleted ${account.name}.`
    await load()
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to delete service account'
  }
}
</script>

<template>
  <AdminShell title="Service accounts" description="Non-user principals that can own API keys for automation.">
    <template #actions><AppButton intent="primary" @click="openCreate">Add service account</AppButton></template>

    <Frame v-if="error" class="mb-5 p-3"><div class="flex items-start gap-2"><StatusTag variant="failed">Error</StatusTag><p class="text-xs leading-5 text-[var(--neutral-800)]">{{ error }}</p></div></Frame>
    <Frame v-if="success" class="mb-5 p-3"><div class="flex items-start gap-2"><StatusTag variant="ready">Saved</StatusTag><p class="text-xs leading-5 text-[var(--neutral-800)]">{{ success }}</p></div></Frame>

    <Frame data-testid="admin-service-accounts-table" class="overflow-hidden p-0">
      <p v-if="accounts.length" class="border-b border-[var(--color-divider)] px-4 py-2 text-xs text-[var(--neutral-700)] md:hidden">Scroll horizontally for status, dates and actions.</p>
      <div v-if="accounts.length" class="overflow-x-auto" role="region" tabindex="0" aria-label="Administration service accounts. Scroll horizontally for status, dates and actions.">
        <UTable class="min-w-[720px]" :data="accounts" :columns="columns">
          <template #name-cell="{ row }">
            <NuxtLink :to="`/admin/service-accounts/${row.original.id}`" class="text-[length:var(--font-size-table-body)] font-semibold text-[var(--accent-700)] hover:underline">{{ row.original.name }}</NuxtLink>
          </template>
          <template #enabled-cell="{ row }"><StatusTag :variant="row.original.enabled ? 'ready' : 'neutral'">{{ row.original.enabled ? 'Enabled' : 'Disabled' }}</StatusTag></template>
          <template #created_at-cell="{ row }"><span class="font-mono text-xs tabular-nums text-[var(--neutral-700)]">{{ dateTime(row.original.created_at) }}</span></template>
          <template #actions-cell="{ row }">
            <div class="flex justify-end gap-1">
              <AppButton intent="ghost" size="xs" @click="openEdit(row.original)">Rename</AppButton>
              <AppButton intent="ghost" size="xs" @click="toggleAccount(row.original)">{{ row.original.enabled ? 'Disable' : 'Enable' }}</AppButton>
              <AppButton intent="ghost" size="xs" tone="destructive" @click="deleteAccount(row.original)">Delete</AppButton>
            </div>
          </template>
        </UTable>
      </div>
      <UEmpty v-else variant="naked" class="px-4 py-8" title="No service accounts" description="Create a service account to own automation API keys." />
    </Frame>

    <p class="mt-4 text-xs leading-5 text-[var(--neutral-700)]">Service accounts own API keys independently of management users. Create and rotate secrets under <NuxtLink to="/api" class="font-semibold text-[var(--accent-700)] hover:underline">API</NuxtLink>.</p>

    <UModal v-model:open="createOpen" title="Add service account">
      <template #body>
        <div class="space-y-4">
          <UFormField label="Name" required><UInput v-model="draft.name" class="w-full" autocomplete="off" minlength="2" required data-testid="service-account-name" /></UFormField>
        </div>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <AppButton type="button" intent="secondary" @click="createOpen = false">Cancel</AppButton>
          <AppButton intent="primary" :loading="creating" :disabled="!canCreate" data-testid="service-account-create" @click="createAccount">Add service account</AppButton>
        </div>
      </template>
    </UModal>

    <UModal v-model:open="editOpen" :title="selected ? `Rename ${selected.name}` : 'Rename service account'">
      <template #body>
        <div class="space-y-4">
          <UFormField label="Name" required><UInput v-model="draft.name" class="w-full" autocomplete="off" minlength="2" required data-testid="service-account-rename" /></UFormField>
        </div>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <AppButton intent="secondary" @click="editOpen = false">Cancel</AppButton>
          <AppButton intent="primary" :loading="saving" :disabled="!canCreate" data-testid="service-account-save" @click="saveAccount">Save</AppButton>
        </div>
      </template>
    </UModal>

    <AppConfirmationModal ref="confirmation" />
  </AdminShell>
</template>
