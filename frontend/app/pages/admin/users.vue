<script setup lang="ts">
import type { TableColumn } from '@nuxt/ui'

type UserRow = { id: number; username: string; enabled: boolean; created_at: number; last_login_at?: number; bootstrap_admin?: boolean }

const manager = useManager()
const users = ref<UserRow[]>([])
const error = ref('')
const success = ref('')
const creating = ref(false)
const createOpen = ref(false)
const newUser = reactive({ username: '', password: '', passwordConfirmation: '' })
const resetOpen = ref(false)
const selectedUser = ref<UserRow | null>(null)
const resetPassword = ref('')
const confirmation = ref<{ request: (options: Record<string, string>) => Promise<boolean> } | null>(null)

const columns: TableColumn<UserRow>[] = [
  { accessorKey: 'username', header: 'Username' },
  { accessorKey: 'enabled', header: 'Status' },
  { accessorKey: 'created_at', header: 'Created' },
  { accessorKey: 'last_login_at', header: 'Last login' },
  { id: 'actions', header: 'Actions' }
]

const canCreate = computed(() => newUser.username.trim().length >= 2 && newUser.password.length >= 10 && newUser.password === newUser.passwordConfirmation)

function dateTime(value?: number) { return value ? new Date(value * 1000).toLocaleString() : 'Never' }

async function load() {
  if (!manager.user.value) return
  error.value = ''
  try { users.value = (await manager.request<UserRow[]>('/api/v1/users')) || [] }
  catch (value: any) { error.value = value?.data?.error || value?.message || 'Unable to load users' }
}
watch(manager.user, user => { if (user) void load() }, { immediate: true })

function openCreate() {
  newUser.username = ''
  newUser.password = ''
  newUser.passwordConfirmation = ''
  createOpen.value = true
}

async function createUser() {
  error.value = ''
  success.value = ''
  if (newUser.password !== newUser.passwordConfirmation) {
    error.value = 'Password confirmation does not match.'
    return
  }
  creating.value = true
  try {
    await manager.request('/api/v1/users', { method: 'POST', body: { username: newUser.username, password: newUser.password } })
    createOpen.value = false
    success.value = 'Management user created.'
    await load()
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to create user'
  } finally {
    creating.value = false
  }
}

async function toggleUser(user: UserRow) {
  const action = user.enabled ? 'disable' : 'enable'
  const confirmed = await confirmation.value?.request({
    title: `${action === 'disable' ? 'Disable' : 'Enable'} user`,
    description: action === 'disable' ? `Disable “${user.username}”? All of that user’s active sessions will be revoked.` : `Enable “${user.username}”?`,
    confirmLabel: action === 'disable' ? 'Disable user' : 'Enable user',
    color: action === 'disable' ? 'warning' : 'primary'
  })
  if (!confirmed) return
  try {
    await manager.request(`/api/v1/users/${user.id}`, { method: 'PATCH', body: { enabled: !user.enabled } })
    await load()
    if (user.id === manager.user.value?.id && user.enabled) await manager.initialize()
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || `Unable to ${action} user`
  }
}

function openReset(user: UserRow) {
  selectedUser.value = user
  resetPassword.value = ''
  resetOpen.value = true
}

async function submitReset() {
  if (!selectedUser.value) return
  try {
    await manager.request(`/api/v1/users/${selectedUser.value.id}/password`, { method: 'POST', body: { password: resetPassword.value } })
    resetOpen.value = false
    success.value = `Password reset for ${selectedUser.value.username}. Their sessions were revoked.`
    if (selectedUser.value.id === manager.user.value?.id) await manager.initialize()
    else await load()
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to reset password'
  }
}
</script>

<template>
  <AdminShell title="Users" description="Local management accounts with equivalent manager access.">
    <template #actions><AppButton intent="primary" @click="openCreate">Add user</AppButton></template>

    <Frame v-if="error" class="mb-5 p-3"><div class="flex items-start gap-2"><StatusTag variant="failed">Error</StatusTag><p class="text-xs leading-5 text-[var(--neutral-800)]">{{ error }}</p></div></Frame>
    <Frame v-if="success" class="mb-5 p-3"><div class="flex items-start gap-2"><StatusTag variant="ready">Saved</StatusTag><p class="text-xs leading-5 text-[var(--neutral-800)]">{{ success }}</p></div></Frame>

    <Frame data-testid="admin-users-table">
      <UTable :data="users" :columns="columns">
        <template #username-cell="{ row }">
          <div>
            <span class="text-[13.5px] font-semibold">{{ row.original.username }}</span>
            <span v-if="row.original.bootstrap_admin" class="mt-0.5 block text-[10.5px] text-[var(--neutral-700)]">bootstrap admin</span>
          </div>
        </template>
        <template #enabled-cell="{ row }"><StatusTag :variant="row.original.enabled ? 'ready' : 'neutral'">{{ row.original.enabled ? 'Enabled' : 'Disabled' }}</StatusTag></template>
        <template #created_at-cell="{ row }"><span class="text-xs text-[var(--neutral-700)]">{{ dateTime(row.original.created_at) }}</span></template>
        <template #last_login_at-cell="{ row }"><span class="text-xs text-[var(--neutral-700)]">{{ dateTime(row.original.last_login_at) }}</span></template>
        <template #actions-cell="{ row }">
          <div class="flex justify-end gap-1">
            <AppButton intent="ghost" size="xs" @click="openReset(row.original)">Reset password</AppButton>
            <AppButton intent="ghost" size="xs" @click="toggleUser(row.original)">{{ row.original.enabled ? 'Disable' : 'Enable' }}</AppButton>
          </div>
        </template>
      </UTable>
      <div v-if="!users.length" class="px-4 py-8 text-center text-sm text-[var(--neutral-700)]">No users</div>
    </Frame>

    <p class="mt-4 text-xs leading-5 text-[var(--neutral-700)]">Accounts are equivalent local management users. Inference API keys are managed separately under <NuxtLink to="/api" class="font-semibold text-[var(--accent-700)] hover:underline">API</NuxtLink> and are not owned by a user.</p>

    <UModal v-model:open="createOpen" title="Add user">
      <template #body>
        <UForm :state="newUser" class="space-y-4" @submit="createUser">
          <UFormField label="Username" required><UInput v-model="newUser.username" class="w-full" autocomplete="off" minlength="2" required /></UFormField>
          <UFormField label="Password" required><UInput v-model="newUser.password" class="w-full" type="password" minlength="10" autocomplete="new-password" required /></UFormField>
          <UFormField label="Confirm password" required><UInput v-model="newUser.passwordConfirmation" class="w-full" type="password" minlength="10" autocomplete="new-password" required /></UFormField>
          <div class="flex justify-end gap-2"><AppButton type="button" intent="secondary" @click="createOpen = false">Cancel</AppButton><AppButton type="submit" intent="primary" :loading="creating" :disabled="!canCreate">Add user</AppButton></div>
        </UForm>
      </template>
    </UModal>

    <UModal v-model:open="resetOpen" :title="`Reset password${selectedUser ? ` for ${selectedUser.username}` : ''}`">
      <template #body><UFormField label="New password" required><UInput v-model="resetPassword" class="w-full" type="password" minlength="10" autocomplete="new-password" required /></UFormField><p class="mt-3 text-sm text-[var(--neutral-700)]">Resetting a password revokes all sessions for that user.</p></template>
      <template #footer><div class="flex w-full justify-end gap-2"><AppButton intent="secondary" @click="resetOpen = false">Cancel</AppButton><AppButton intent="primary" :disabled="resetPassword.length < 10" @click="submitReset">Reset password</AppButton></div></template>
    </UModal>

    <AppConfirmationModal ref="confirmation" />
  </AdminShell>
</template>
