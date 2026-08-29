<script setup lang="ts">
type ProfileUser = { id: number; username: string; enabled: boolean; created_at: number; last_login_at?: number }
type Session = { id: string; user_id: number; created_at: number; expires_at: number; remote_address: string; user_agent: string; current?: boolean }

const manager = useManager()
const profile = ref<ProfileUser | null>(null)
const sessions = ref<Session[]>([])
const error = ref('')
const success = ref('')
const busy = ref(false)
const password = reactive({ current: '', next: '', confirmation: '' })
const confirmation = ref<{ request: (options: Record<string, string>) => Promise<boolean> } | null>(null)

function dateTime(value?: number) {
  return value ? new Date(value * 1000).toLocaleString() : 'Never'
}

function initials(username: string) {
  const parts = username.trim().split(/[\s._-]+/).filter(Boolean)
  if (!parts.length) return '?'
  return parts.slice(0, 2).map(part => part[0]).join('').toUpperCase()
}

function clientLabel(userAgent: string) {
  const ua = userAgent || ''
  const browser = /Firefox\//.test(ua) ? 'Firefox' : /Edg\//.test(ua) ? 'Edge' : /Chrome\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) ? 'Safari' : 'Unknown client'
  const platform = /Windows/.test(ua) ? 'Windows' : /Mac OS X|Macintosh/.test(ua) ? 'macOS' : /Linux/.test(ua) ? 'Linux' : ''
  return platform && browser !== 'Unknown client' ? `${browser} on ${platform}` : browser
}

async function load() {
  if (!manager.user.value) return
  error.value = ''
  try {
    const [user, activeSessions] = await Promise.all([
      manager.request<ProfileUser>('/api/v1/me'),
      manager.request<Session[]>('/api/v1/me/sessions')
    ])
    profile.value = user
    sessions.value = activeSessions || []
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
    <UPageHeader headline="ACCOUNT" title="Profile" description="Manage your local account password and active management sessions." />

    <Frame v-if="error" class="flex items-start gap-3 p-4" data-testid="profile-error-note">
      <StatusTag variant="failed">Error</StatusTag>
      <p class="text-sm leading-5 text-[var(--neutral-900)]">{{ error }}</p>
    </Frame>
    <Frame v-if="success" class="flex items-start gap-3 p-4" data-testid="profile-success-note">
      <StatusTag variant="ready">Saved</StatusTag>
      <p class="text-sm leading-5 text-[var(--neutral-900)]">{{ success }}</p>
    </Frame>

    <Frame v-if="profile" class="p-5" data-testid="profile-account">
      <div class="flex flex-col gap-5 sm:flex-row sm:items-start">
        <Frame class="flex h-[66px] w-[66px] shrink-0 items-center justify-center p-0 font-mono text-xl font-semibold text-[var(--accent-700)]" data-testid="profile-avatar">
          {{ initials(profile.username) }}
        </Frame>
        <div class="min-w-0 flex-1">
          <div class="flex flex-wrap items-center gap-2">
            <h2 class="text-base font-semibold">Account</h2>
            <StatusTag :variant="profile.enabled ? 'ready' : 'neutral'">{{ profile.enabled ? 'Enabled' : 'Disabled' }}</StatusTag>
          </div>
          <dl class="mt-4 divide-y divide-[var(--color-divider)] text-sm">
            <div class="grid gap-1 py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Username</dt><dd class="font-mono text-[13px]">{{ profile.username }}</dd></div>
            <div class="grid gap-1 py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Created</dt><dd class="font-mono text-[12px]">{{ dateTime(profile.created_at) }}</dd></div>
            <div class="grid gap-1 py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Last login</dt><dd class="font-mono text-[12px]">{{ dateTime(profile.last_login_at) }}</dd></div>
          </dl>
        </div>
      </div>
    </Frame>

    <Frame class="max-w-[720px] p-5" data-testid="profile-password">
      <h2 class="text-base font-semibold">Change password</h2>
      <p class="mt-1 text-xs leading-5 text-[var(--neutral-700)]">Changing the password signs out every other session.</p>
      <UForm :state="password" class="mt-5 space-y-4" @submit="changePassword">
        <UFormField label="Current password" required><UInput v-model="password.current" class="w-full" type="password" autocomplete="current-password" required /></UFormField>
        <UFormField label="New password" required><UInput v-model="password.next" class="w-full" type="password" autocomplete="new-password" minlength="10" required /></UFormField>
        <UFormField label="Confirm new password" required><UInput v-model="password.confirmation" class="w-full" type="password" autocomplete="new-password" minlength="10" required /></UFormField>
        <AppButton type="submit" intent="primary" :loading="busy">Change password</AppButton>
      </UForm>
    </Frame>

    <Frame class="p-5" data-testid="profile-sessions">
      <div class="flex flex-col gap-4 md:flex-row md:items-start md:justify-between">
        <div>
          <h2 class="text-base font-semibold">Active sessions</h2>
          <p class="mt-1 text-xs leading-5 text-[var(--neutral-700)]">Session identifiers and cookies are never exposed here.</p>
        </div>
        <div class="flex flex-wrap gap-2 md:justify-end">
          <AppButton intent="secondary" size="sm" @click="revokeOthers">Revoke others</AppButton>
          <AppButton intent="destructive" size="sm" @click="revokeAll">Log out everywhere</AppButton>
        </div>
      </div>

      <div v-if="sessions.length" class="mt-4 divide-y divide-[var(--color-divider)]">
        <div v-for="session in sessions" :key="session.id" class="flex flex-col gap-3 py-4 md:flex-row md:items-center md:justify-between">
          <div class="min-w-0">
            <div class="flex flex-wrap items-center gap-2">
              <span class="text-[13.5px] font-semibold">{{ clientLabel(session.user_agent) }}</span>
              <StatusTag v-if="session.current" variant="ready">Current</StatusTag>
            </div>
            <p class="mt-1 break-words font-mono text-[10.5px] leading-5 text-[var(--neutral-700)]">
              {{ session.remote_address || 'Unknown address' }} · Created {{ dateTime(session.created_at) }} · Expires {{ dateTime(session.expires_at) }}
            </p>
          </div>
          <AppButton intent="ghost" size="sm" @click="revokeSession(session)">{{ session.current ? 'Sign out' : 'Revoke' }}</AppButton>
        </div>
      </div>
      <div v-else class="mt-4 border-t border-[var(--color-divider)] px-2 py-8 text-center text-sm text-[var(--neutral-700)]">No active sessions</div>
    </Frame>

    <AppConfirmationModal ref="confirmation" />
  </div>
</template>
