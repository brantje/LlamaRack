<script setup lang="ts">
import { profileDateTime, profileProviderName } from '~/utils/profileDisplay'

type ExternalIdentity = { id: string; provider_id: string; issuer: string; subject: string; user_id: number; created_at: number }
type PublicOIDCProvider = { id: string; name: string }
type AuthProvidersResponse = { providers?: PublicOIDCProvider[] }

const manager = useManager()
const identities = ref<ExternalIdentity[]>([])
const authProviders = ref<PublicOIDCProvider[]>([])
const error = ref('')
const success = ref('')
const confirmation = ref<{ request: (options: Record<string, string>) => Promise<boolean> } | null>(null)

async function load() {
  if (!manager.user.value) return
  error.value = ''
  try {
    const [linkedIdentities, providers] = await Promise.all([
      manager.request<ExternalIdentity[]>('/api/v1/me/identities'),
      manager.request<AuthProvidersResponse>('/api/v1/auth/providers')
    ])
    identities.value = linkedIdentities || []
    authProviders.value = providers?.providers || []
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to load authentication sources'
  }
}

watch(manager.user, user => {
  if (user) void load()
}, { immediate: true })

async function unlinkIdentity(identity: ExternalIdentity) {
  error.value = ''
  success.value = ''
  const name = profileProviderName(identity, authProviders.value)
  const confirmed = await confirmation.value?.request({
    title: 'Unlink authentication source',
    description: `Unlink ${name} from this account? You will no longer be able to sign in with this source unless it is linked again.`,
    confirmLabel: 'Unlink source',
    confirmTone: 'destructive'
  })
  if (!confirmed) return
  try {
    await manager.request(`/api/v1/me/identities/${encodeURIComponent(identity.id)}`, { method: 'DELETE' })
    success.value = `${name} unlinked.`
    await load()
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to unlink authentication source'
  }
}
</script>

<template>
  <UserShell title="Authentication sources" description="External sign-in providers linked to this account.">
    <div class="space-y-5">
      <Frame v-if="error" class="flex items-start gap-3 p-4" data-testid="profile-error-note">
        <StatusTag variant="failed">Error</StatusTag>
        <p class="text-sm leading-5 text-[var(--neutral-900)]">{{ error }}</p>
      </Frame>
      <Frame v-if="success" class="flex items-start gap-3 p-4" data-testid="profile-success-note">
        <StatusTag variant="ready">Saved</StatusTag>
        <p class="text-sm leading-5 text-[var(--neutral-900)]">{{ success }}</p>
      </Frame>

      <Frame class="p-5" data-testid="profile-authentication-sources">
        <div>
          <p class="text-[length:var(--font-size-kicker)] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">SIGN-IN</p>
          <h2 class="mt-1 text-base font-semibold">Authentication sources</h2>
          <p class="mt-1 text-xs leading-5 text-[var(--neutral-700)]">External sign-in providers linked to this account. Provider configuration is managed in <NuxtLink to="/admin/authentication" class="font-semibold text-[var(--accent-700)] hover:underline">Administration → Authentication</NuxtLink>.</p>
        </div>
        <div v-if="identities.length" class="mt-4 divide-y divide-[var(--color-divider)] border-t border-[var(--color-divider)]">
          <div v-for="identity in identities" :key="identity.id" class="flex flex-col gap-3 py-4 sm:flex-row sm:items-center sm:justify-between">
            <div class="min-w-0">
              <div class="flex flex-wrap items-center gap-2">
                <span class="text-[length:var(--font-size-table-body)] font-semibold">{{ profileProviderName(identity, authProviders) }}</span>
                <StatusTag variant="neutral">OIDC</StatusTag>
              </div>
              <p class="mt-1 truncate font-mono text-[length:var(--font-size-table-header)] text-[var(--neutral-700)]">{{ identity.issuer }}</p>
              <p class="mt-1 font-mono text-[length:var(--font-size-table-header)] tabular-nums text-[var(--neutral-700)]">Linked {{ profileDateTime(identity.created_at) }}</p>
            </div>
            <AppButton intent="ghost" size="sm" @click="unlinkIdentity(identity)">Unlink</AppButton>
          </div>
        </div>
        <div v-else class="mt-4 border-t border-[var(--color-divider)] px-2 py-8 text-center">
          <p class="text-sm font-semibold">No linked authentication sources</p>
          <p class="mt-1 text-xs text-[var(--neutral-700)]">This account is not linked to an external sign-in provider.</p>
        </div>
      </Frame>

      <AppConfirmationModal ref="confirmation" />
    </div>
  </UserShell>
</template>
