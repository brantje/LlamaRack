<script setup lang="ts">
import { profileDateTime, profileInitials } from '~/utils/profileDisplay'

type ProfileUser = { id: number; username: string; enabled: boolean; created_at: number; last_login_at?: number }

const manager = useManager()
const profile = ref<ProfileUser | null>(null)
const error = ref('')
const success = ref('')
const busy = ref(false)
const password = reactive({ current: '', next: '', confirmation: '' })
const passwordVisible = reactive({ current: false, next: false, confirmation: false })
const passwordMismatch = computed(() => Boolean(password.confirmation && password.next !== password.confirmation))
const passwordReady = computed(() => Boolean(password.current && password.next.length >= 10 && password.confirmation.length >= 10 && !passwordMismatch.value))

async function load() {
  if (!manager.user.value) return
  error.value = ''
  try {
    profile.value = await manager.request<ProfileUser>('/api/v1/me')
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
</script>

<template>
  <UserShell title="Account" description="View your local account details and change your password.">
    <div class="space-y-5">
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
          <UAvatar :text="profileInitials(profile.username)" size="xl" class="h-[66px] w-[66px] shrink-0 font-mono text-xl font-semibold text-[var(--accent-700)]" data-testid="profile-avatar" />
          <div class="min-w-0 flex-1">
            <div class="flex flex-wrap items-center gap-2">
              <h2 class="text-base font-semibold">Account</h2>
              <StatusTag :variant="profile.enabled ? 'ready' : 'neutral'">{{ profile.enabled ? 'Enabled' : 'Disabled' }}</StatusTag>
            </div>
            <dl class="mt-4 divide-y divide-[var(--color-divider)] text-sm">
              <div class="grid gap-1 py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Username</dt><dd class="font-mono text-[length:var(--font-size-h6)]">{{ profile.username }}</dd></div>
              <div class="grid gap-1 py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Created</dt><dd class="font-mono text-[length:var(--font-size-h6)] tabular-nums">{{ profileDateTime(profile.created_at) }}</dd></div>
              <div class="grid gap-1 py-3 sm:grid-cols-[180px_1fr]"><dt class="text-[var(--neutral-700)]">Last login</dt><dd class="font-mono text-[length:var(--font-size-h6)] tabular-nums">{{ profileDateTime(profile.last_login_at) }}</dd></div>
            </dl>
          </div>
        </div>
      </Frame>

      <Frame class="max-w-[720px] p-5" data-testid="profile-password">
        <h2 class="text-base font-semibold">Change password</h2>
        <p class="mt-1 text-xs leading-5 text-[var(--neutral-700)]">Changing the password signs out every other session. Use at least 10 characters for the new password.</p>
        <UForm :state="password" class="mt-5 space-y-4" @submit="changePassword">
          <UFormField label="Current password" required>
            <UInput v-model="password.current" data-testid="profile-current-password" class="w-full" :type="passwordVisible.current ? 'text' : 'password'" autocomplete="current-password" required>
              <template #trailing><UButton type="button" color="neutral" variant="link" size="xs" :icon="passwordVisible.current ? 'i-lucide-eye-off' : 'i-lucide-eye'" :aria-label="passwordVisible.current ? 'Hide current password' : 'Show current password'" :aria-pressed="passwordVisible.current" data-testid="toggle-current-password" @click="passwordVisible.current = !passwordVisible.current" /></template>
            </UInput>
          </UFormField>
          <UFormField label="New password" help="At least 10 characters." required>
            <UInput v-model="password.next" data-testid="profile-new-password" class="w-full" :type="passwordVisible.next ? 'text' : 'password'" autocomplete="new-password" minlength="10" required>
              <template #trailing><UButton type="button" color="neutral" variant="link" size="xs" :icon="passwordVisible.next ? 'i-lucide-eye-off' : 'i-lucide-eye'" :aria-label="passwordVisible.next ? 'Hide new password' : 'Show new password'" :aria-pressed="passwordVisible.next" data-testid="toggle-new-password" @click="passwordVisible.next = !passwordVisible.next" /></template>
            </UInput>
          </UFormField>
          <UFormField label="Confirm new password" :error="passwordMismatch ? 'New password confirmation does not match.' : undefined" required>
            <UInput v-model="password.confirmation" data-testid="profile-confirm-password" class="w-full" :type="passwordVisible.confirmation ? 'text' : 'password'" autocomplete="new-password" minlength="10" required>
              <template #trailing><UButton type="button" color="neutral" variant="link" size="xs" :icon="passwordVisible.confirmation ? 'i-lucide-eye-off' : 'i-lucide-eye'" :aria-label="passwordVisible.confirmation ? 'Hide password confirmation' : 'Show password confirmation'" :aria-pressed="passwordVisible.confirmation" data-testid="toggle-confirm-password" @click="passwordVisible.confirmation = !passwordVisible.confirmation" /></template>
            </UInput>
          </UFormField>
          <AppButton type="submit" intent="primary" :loading="busy" :disabled="!passwordReady">Change password</AppButton>
        </UForm>
      </Frame>
    </div>
  </UserShell>
</template>
