<script setup lang="ts">
import { profileClientLabel, profileDateTime } from '~/utils/profileDisplay'

type Session = { id: string; user_id: number; created_at: number; expires_at: number; remote_address: string; user_agent: string; current?: boolean }

const manager = useManager()
const sessions = ref<Session[]>([])
const error = ref('')
const otherSessionCount = computed(() => sessions.value.filter(session => !session.current).length)
const confirmation = ref<{ request: (options: Record<string, string>) => Promise<boolean> } | null>(null)

async function load() {
  if (!manager.user.value) return
  error.value = ''
  try {
    sessions.value = await manager.request<Session[]>('/api/v1/me/sessions') || []
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to load sessions'
  }
}

watch(manager.user, user => {
  if (user) void load()
}, { immediate: true })

async function revokeSession(session: Session) {
  if (session.current) {
    await manager.logout()
    return
  }
  const confirmed = await confirmation.value?.request({ title: 'Revoke session', description: 'Sign this session out immediately?', confirmLabel: 'Revoke session', confirmTone: 'destructive' })
  if (!confirmed) return
  try {
    await manager.request(`/api/v1/sessions/${encodeURIComponent(session.id)}`, { method: 'DELETE' })
    await load()
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to revoke session'
  }
}

async function revokeOthers() {
  const confirmed = await confirmation.value?.request({ title: 'Revoke other sessions', description: 'Sign out every session except this browser?', confirmLabel: 'Revoke others', confirmTone: 'destructive' })
  if (!confirmed) return
  try {
    await manager.request('/api/v1/me/sessions/revoke-others', { method: 'POST' })
    await load()
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to revoke other sessions'
  }
}

async function revokeAll() {
  const confirmed = await confirmation.value?.request({ title: 'Log out everywhere', description: 'Revoke every session, including this browser?', confirmLabel: 'Log out everywhere', confirmTone: 'destructive' })
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
  <UserShell title="Active sessions" description="Review and revoke management sessions.">
    <div class="space-y-5">
      <Frame v-if="error" class="flex items-start gap-3 p-4" data-testid="profile-error-note">
        <StatusTag variant="failed">Error</StatusTag>
        <p class="text-sm leading-5 text-[var(--neutral-900)]">{{ error }}</p>
      </Frame>

      <Frame class="p-5" data-testid="profile-sessions">
        <div class="flex flex-col gap-4 md:flex-row md:items-start md:justify-between">
          <div>
            <h2 class="text-base font-semibold">Active sessions</h2>
            <p class="mt-1 text-xs leading-5 text-[var(--neutral-700)]">Session identifiers and cookies are never exposed here.</p>
          </div>
          <div class="flex flex-wrap gap-2 md:justify-end">
            <AppButton intent="secondary" size="sm" :disabled="otherSessionCount === 0" @click="revokeOthers">Revoke others</AppButton>
            <AppButton intent="secondary" tone="destructive" size="sm" :disabled="sessions.length === 0" @click="revokeAll">Log out everywhere</AppButton>
          </div>
        </div>

        <div v-if="sessions.length" class="mt-4 divide-y divide-[var(--color-divider)]">
          <div v-for="session in sessions" :key="session.id" class="flex flex-col gap-3 py-4 md:flex-row md:items-center md:justify-between">
            <div class="min-w-0">
              <div class="flex flex-wrap items-center gap-2">
                <span class="text-[length:var(--font-size-table-body)] font-semibold">{{ profileClientLabel(session.user_agent) }}</span>
                <StatusTag v-if="session.current" variant="ready">Current</StatusTag>
              </div>
              <p class="mt-1 break-words font-mono text-[length:var(--font-size-table-header)] leading-5 tabular-nums text-[var(--neutral-700)]">
                {{ session.remote_address || 'Unknown address' }} · Created {{ profileDateTime(session.created_at) }} · Expires {{ profileDateTime(session.expires_at) }}
              </p>
            </div>
            <AppButton intent="ghost" size="sm" @click="revokeSession(session)">{{ session.current ? 'Sign out' : 'Revoke' }}</AppButton>
          </div>
        </div>
        <div v-else class="mt-4 border-t border-[var(--color-divider)] px-2 py-8 text-center text-sm text-[var(--neutral-700)]">No active sessions</div>
      </Frame>

      <AppConfirmationModal ref="confirmation" />
    </div>
  </UserShell>
</template>
