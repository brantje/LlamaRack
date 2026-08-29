<script setup lang="ts">
type ProfileUser = { id: number; username: string; enabled: boolean; created_at: number; last_login_at?: number }
type Session = { id: string; user_id: number; created_at: number; expires_at: number; remote_address: string; user_agent: string; current?: boolean }
type ExternalIdentity = { id: string; provider_id: string; issuer: string; subject: string; user_id: number; created_at: number }
type PublicOIDCProvider = { id: string; name: string }
type AuthProvidersResponse = { providers?: PublicOIDCProvider[] }

const manager = useManager()
const profile = ref<ProfileUser | null>(null)
const sessions = ref<Session[]>([])
const identities = ref<ExternalIdentity[]>([])
const authProviders = ref<PublicOIDCProvider[]>([])
const error = ref('')
const success = ref('')
const busy = ref(false)
const password = reactive({ current: '', next: '', confirmation: '' })
const confirmation = ref<{ request: (options: Record<string, string>) => Promise<boolean> } | null>(null)

function dateTime(value?: number) {
  return value ? new Date(value * 1000).toLocaleString() : 'Never'
}

function clientLabel(userAgent: string) {
  const ua = userAgent || ''
  const browser = /Firefox\//.test(ua) ? 'Firefox' : /Edg\//.test(ua) ? 'Edge' : /Chrome\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) ? 'Safari' : 'Unknown client'
  const platform = /Windows/.test(ua) ? 'Windows' : /Mac OS X|Macintosh/.test(ua) ? 'macOS' : /Linux/.test(ua) ? 'Linux' : ''
  return platform && browser !== 'Unknown client' ? `${browser} on ${platform}` : browser
}

function providerName(identity: ExternalIdentity) {
  return authProviders.value.find(provider => provider.id === identity.provider_id)?.name || identity.issuer
}

async function load() {
  if (!manager.user.value) return
  error.value = ''
  try {
    const [user, activeSessions, linkedIdentities, providers] = await Promise.all([
      manager.request<ProfileUser>('/api/v1/me'),
      manager.request<Session[]>('/api/v1/me/sessions'),
      manager.request<ExternalIdentity[]>('/api/v1/me/identities'),
      manager.request<AuthProvidersResponse>('/api/v1/auth/providers')
    ])
    profile.value = user
    sessions.value = activeSessions || []
    identities.value = linkedIdentities || []
    authProviders.value = providers?.providers || []
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to load profile'
  }
}

watch(manager.user, user => {
  if (user) void load()
}, { immediate: true })

async function changePassword() {
  error.value = ''
  success.value = ''
  if (password.next !== password.confirmation) {
    error.value = 'New password confirmation does not match.'
    return
  }
  busy.value = true
  try {
    await manager.request('/api/v1/me/password', {
      method: 'POST',
      body: { current_password: password.current, new_password: password.next, new_password_confirmation: password.confirmation }
    })
    password.current = ''
    password.next = ''
    password.confirmation = ''
    success.value = 'Password changed. Other sessions were signed out.'
    await load()
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to change password'
  } finally {
    busy.value = false
  }
}

async function unlinkIdentity(identity: ExternalIdentity) {
  error.value = ''
  success.value = ''
  const name = providerName(identity)
  const confirmed = await confirmation.value?.request({
    title: 'Unlink authentication source',
    description: `Unlink ${name} from this account? You will no longer be able to sign in with this source unless it is linked again.`,
    confirmLabel: 'Unlink source',
    color: 'error'
  })
  if (!confirmed) return
  try {
    await manager.request(`/api/v1/admin/auth/identities/${encodeURIComponent(identity.id)}`, { method: 'DELETE' })
    success.value = `${name} unlinked.`
    await load()
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to unlink authentication source'
  }
}

async function revokeSession(session: Session) {
  if (session.current) {
    await manager.logout()
    return
  }
  const confirmed = await confirmation.value?.request({ title: 'Revoke session', description: 'Sign this session out immediately?', confirmLabel: 'Revoke session', color: 'error' })
  if (!confirmed) return
  try {
    await manager.request(`/api/v1/sessions/${encodeURIComponent(session.id)}`, { method: 'DELETE' })
    await load()
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to revoke session'
  }
}

async function revokeOthers() {
  const confirmed = await confirmation.value?.request({ title: 'Revoke other sessions', description: 'Sign out every session except this browser?', confirmLabel: 'Revoke others', color: 'error' })
  if (!confirmed) return
  try {
    await manager.request('/api/v1/me/sessions/revoke-others', { method: 'POST' })
    await load()
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to revoke other sessions'
  }
}

async function revokeAll() {
  const confirmed = await confirmation.value?.request({ title: 'Log out everywhere', description: 'Revoke every session, including this browser?', confirmLabel: 'Log out everywhere', color: 'error' })
  if (!confirmed) return
  try {
    await manager.request('/api/v1/me/sessions/revoke-all', { method: 'POST' })
    await manager.initialize()
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to log out all sessions'
  }
}
</script>

<template>
  <div class="space-y-5">
    <UPageHeader headline="ACCOUNT" title="Profile" description="Manage your local account password, linked authentication sources and active management sessions." />
    <UAlert v-if="error" color="error" variant="subtle" :description="error" />
    <UAlert v-if="success" color="success" variant="subtle" :description="success" />

    <UCard v-if="profile">
      <template #header><h2 class="text-xl font-bold">Account</h2></template>
      <dl class="divide-y divide-default text-sm">
        <div class="grid gap-1 py-3 sm:grid-cols-[180px_1fr]"><dt class="text-muted">Username</dt><dd>{{ profile.username }}</dd></div>
        <div class="grid gap-1 py-3 sm:grid-cols-[180px_1fr]"><dt class="text-muted">Created</dt><dd>{{ dateTime(profile.created_at) }}</dd></div>
        <div class="grid gap-1 py-3 sm:grid-cols-[180px_1fr]"><dt class="text-muted">Last login</dt><dd>{{ dateTime(profile.last_login_at) }}</dd></div>
      </dl>
    </UCard>

    <div class="grid gap-5 lg:grid-cols-2">
      <UCard>
        <template #header><h2 class="text-xl font-bold">Change password</h2></template>
        <UForm :state="password" class="space-y-4" @submit="changePassword">
          <UFormField label="Current password" required><UInput v-model="password.current" class="w-full" type="password" autocomplete="current-password" required /></UFormField>
          <UFormField label="New password" required><UInput v-model="password.next" class="w-full" type="password" autocomplete="new-password" minlength="10" required /></UFormField>
          <UFormField label="Confirm new password" required><UInput v-model="password.confirmation" class="w-full" type="password" autocomplete="new-password" minlength="10" required /></UFormField>
          <UButton type="submit" :loading="busy">Change password</UButton>
        </UForm>
      </UCard>

      <UCard>
        <template #header>
          <div>
            <h2 class="text-xl font-bold">Authentication sources</h2>
            <p class="text-sm text-muted">External sign-in providers linked to this account.</p>
          </div>
        </template>
        <div v-if="identities.length" class="divide-y divide-default">
          <div v-for="identity in identities" :key="identity.id" class="flex flex-col gap-3 py-4 sm:flex-row sm:items-center sm:justify-between">
            <div class="min-w-0 text-sm">
              <div class="flex items-center gap-2">
                <span class="font-semibold">{{ providerName(identity) }}</span>
                <UBadge color="neutral" variant="subtle">OIDC</UBadge>
              </div>
              <p class="mt-1 truncate text-muted">{{ identity.issuer }}</p>
              <p class="mt-1 text-xs text-muted">Linked {{ dateTime(identity.created_at) }}</p>
            </div>
            <UButton color="error" variant="soft" size="sm" @click="unlinkIdentity(identity)">Unlink</UButton>
          </div>
        </div>
        <UEmpty v-else variant="naked" title="No linked authentication sources" description="This account is not linked to an external sign-in provider." />
      </UCard>
    </div>

    <UCard>
      <template #header>
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div><h2 class="text-xl font-bold">Active sessions</h2><p class="text-sm text-muted">Session identifiers and cookies are never exposed here.</p></div>
          <UFieldGroup><UButton color="neutral" variant="soft" @click="revokeOthers">Revoke others</UButton><UButton color="error" variant="soft" @click="revokeAll">Log out everywhere</UButton></UFieldGroup>
        </div>
      </template>
      <div v-if="sessions.length" class="divide-y divide-default">
        <div v-for="session in sessions" :key="session.id" class="flex flex-col gap-3 py-4 md:flex-row md:items-center md:justify-between">
          <div class="min-w-0 text-sm">
            <div class="flex items-center gap-2"><span class="font-semibold">{{ clientLabel(session.user_agent) }}</span><UBadge v-if="session.current" color="primary" variant="subtle">Current</UBadge></div>
            <p class="mt-1 text-muted">{{ session.remote_address || 'Unknown address' }} · Created {{ dateTime(session.created_at) }} · Expires {{ dateTime(session.expires_at) }}</p>
          </div>
          <UButton :color="session.current ? 'neutral' : 'error'" variant="soft" size="sm" @click="revokeSession(session)">{{ session.current ? 'Sign out' : 'Revoke' }}</UButton>
        </div>
      </div>
      <UEmpty v-else variant="naked" title="No active sessions" />
    </UCard>

    <AppConfirmationModal ref="confirmation" />
  </div>
</template>
