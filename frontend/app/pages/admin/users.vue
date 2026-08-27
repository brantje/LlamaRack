<script setup lang="ts">
import type { TableColumn } from '@nuxt/ui'

type UserRow = { id: number; username: string; enabled: boolean; created_at: number; last_login_at?: number }
type SessionRow = { id: string; user_id: number; created_at: number; expires_at: number; remote_address: string; user_agent: string; current?: boolean }

const manager = useManager()
const users = ref<UserRow[]>([])
const sessions = ref<SessionRow[]>([])
const error = ref('')
const success = ref('')
const creating = ref(false)
const newUser = reactive({ username: '', password: '' })
const resetOpen = ref(false)
const sessionsOpen = ref(false)
const selectedUser = ref<UserRow | null>(null)
const resetPassword = ref('')
const confirmation = ref<{ request: (options: Record<string, string>) => Promise<boolean> } | null>(null)

const columns: TableColumn<UserRow>[] = [
  { accessorKey: 'username', header: 'Username' },
  { accessorKey: 'enabled', header: 'Status' },
  { accessorKey: 'last_login_at', header: 'Last login' },
  { id: 'actions', header: '' }
]

function dateTime(value?: number) {
  return value ? new Date(value * 1000).toLocaleString() : 'Never'
}

async function load() {
  if (!manager.user.value) return
  error.value = ''
  try {
    users.value = await manager.request<UserRow[]>('/api/v1/users')
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to load users'
  }
}

watch(manager.user, user => {
  if (user) void load()
}, { immediate: true })

async function createUser() {
  creating.value = true
  error.value = ''
  success.value = ''
  try {
    await manager.request('/api/v1/users', { method: 'POST', body: newUser })
    newUser.username = ''
    newUser.password = ''
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

async function deleteUser(user: UserRow) {
  const confirmed = await confirmation.value?.request({
    title: 'Delete user',
    description: `Delete “${user.username}” and all of their sessions? This cannot be undone.`,
    confirmLabel: 'Delete user',
    color: 'error'
  })
  if (!confirmed) return
  try {
    await manager.request(`/api/v1/users/${user.id}`, { method: 'DELETE' })
    await load()
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to delete user'
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
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to reset password'
  }
}

async function openSessions(user: UserRow) {
  selectedUser.value = user
  try {
    sessions.value = await manager.request<SessionRow[]>(`/api/v1/users/${user.id}/sessions`)
    sessionsOpen.value = true
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to load sessions'
  }
}

async function revokeSession(session: SessionRow) {
  await manager.request(`/api/v1/sessions/${encodeURIComponent(session.id)}`, { method: 'DELETE' })
  if (selectedUser.value) sessions.value = await manager.request<SessionRow[]>(`/api/v1/users/${selectedUser.value.id}/sessions`)
  if (session.current) await manager.initialize()
}
</script>

<template>
  <div class="space-y-5">
    <UPageHeader headline="ADMINISTRATION" title="Users" description="All local management users have the same full management access in v1. There are no roles or permission tiers." />
    <UAlert v-if="error" color="error" variant="subtle" :description="error" />
    <UAlert v-if="success" color="success" variant="subtle" :description="success" />

    <UCard class="max-w-3xl">
      <template #header><h2 class="text-xl font-bold">Create user</h2></template>
      <UForm :state="newUser" class="grid gap-4 md:grid-cols-[1fr_1fr_auto] md:items-end" @submit="createUser">
        <UFormField label="Username" required><UInput v-model="newUser.username" class="w-full" autocomplete="off" required /></UFormField>
        <UFormField label="Initial password" required><UInput v-model="newUser.password" class="w-full" type="password" minlength="10" autocomplete="new-password" required /></UFormField>
        <UButton type="submit" :loading="creating">Create user</UButton>
      </UForm>
    </UCard>

    <UCard>
      <template #header><h2 class="text-xl font-bold">Management users</h2></template>
      <UTable :data="users" :columns="columns">
        <template #username-cell="{ row }">
          <div class="flex items-center gap-2"><span class="font-semibold">{{ row.original.username }}</span><UBadge v-if="row.original.id === manager.user.value?.id" color="primary" variant="subtle">You</UBadge></div>
        </template>
        <template #enabled-cell="{ row }"><UBadge :color="row.original.enabled ? 'success' : 'neutral'" variant="subtle">{{ row.original.enabled ? 'Enabled' : 'Disabled' }}</UBadge></template>
        <template #last_login_at-cell="{ row }">{{ dateTime(row.original.last_login_at) }}</template>
        <template #actions-cell="{ row }">
          <div class="flex justify-end"><UFieldGroup>
            <UButton color="neutral" variant="soft" size="sm" @click="openSessions(row.original)">Sessions</UButton>
            <UButton color="neutral" variant="soft" size="sm" @click="openReset(row.original)">Reset password</UButton>
            <UButton :color="row.original.enabled ? 'warning' : 'primary'" variant="soft" size="sm" @click="toggleUser(row.original)">{{ row.original.enabled ? 'Disable' : 'Enable' }}</UButton>
            <UButton color="error" variant="soft" size="sm" :disabled="row.original.id === manager.user.value?.id" @click="deleteUser(row.original)">Delete</UButton>
          </UFieldGroup></div>
        </template>
      </UTable>
      <UEmpty v-if="!users.length" variant="naked" title="No users" />
    </UCard>

    <UModal v-model:open="resetOpen" :title="`Reset password${selectedUser ? ` for ${selectedUser.username}` : ''}`">
      <template #body><UFormField label="New password" required><UInput v-model="resetPassword" class="w-full" type="password" minlength="10" autocomplete="new-password" required /></UFormField><p class="mt-3 text-sm text-muted">Resetting a password revokes all sessions for that user.</p></template>
      <template #footer><div class="flex w-full justify-end gap-2"><UButton color="neutral" variant="soft" @click="resetOpen = false">Cancel</UButton><UButton color="warning" :disabled="resetPassword.length < 10" @click="submitReset">Reset password</UButton></div></template>
    </UModal>

    <UModal v-model:open="sessionsOpen" :title="`Sessions${selectedUser ? ` for ${selectedUser.username}` : ''}`">
      <template #body>
        <div v-if="sessions.length" class="divide-y divide-default">
          <div v-for="session in sessions" :key="session.id" class="flex items-start justify-between gap-4 py-3">
            <div class="min-w-0 text-sm"><p class="font-semibold">{{ session.user_agent || 'Unknown client' }}</p><p class="text-muted">{{ session.remote_address || 'Unknown address' }} · {{ dateTime(session.created_at) }}</p></div>
            <UButton color="error" variant="soft" size="sm" @click="revokeSession(session)">Revoke</UButton>
          </div>
        </div>
        <UEmpty v-else variant="naked" title="No active sessions" />
      </template>
    </UModal>

    <AppConfirmationModal ref="confirmation" />
  </div>
</template>
