<script setup lang="ts">
import AppConfirmationModal from '~/components/AppConfirmationModal.vue'

type SettingValue<T> = { value: T; source: string; editable: boolean }
type AuthSettings = {
  local_login_enabled: SettingValue<boolean>
  oidc_jit_provisioning_enabled: SettingValue<boolean>
  oidc_auto_link_enabled: SettingValue<boolean>
  external_url: SettingValue<string>
  frontend_url?: SettingValue<string>
}
type Provider = {
  id: string
  name: string
  enabled: boolean
  issuer: string
  discovery_url?: string
  client_id: string
  scopes: string[]
  username_claim: string
  authorization_endpoint?: string
  token_endpoint?: string
  jwks_url?: string
  secret_configured: boolean
  last_tested_at?: number
  last_test_succeeded: boolean
}

type ProviderForm = {
  name: string
  enabled: boolean
  issuer: string
  discovery_url: string
  client_id: string
  client_secret: string
  scopes: string
  username_claim: string
  authorization_endpoint: string
  token_endpoint: string
  jwks_url: string
}

const manager = useManager()
const loading = ref(true)
const savingSettings = ref(false)
const providerBusy = ref('')
const providerTestMessage = ref('')
const providerTestError = ref('')
const errorMessage = ref('')
const successMessage = ref('')
const settings = ref<AuthSettings | null>(null)
const settingsForm = reactive({ local_login_enabled: true, oidc_jit_provisioning_enabled: true, oidc_auto_link_enabled: false, external_url: '', frontend_url: '' })
const providers = ref<Provider[]>([])
const providerModalOpen = ref(false)
const editingProvider = ref<Provider | null>(null)
const providerForm = reactive<ProviderForm>(emptyProvider())
const confirmation = ref<InstanceType<typeof AppConfirmationModal> | null>(null)

function emptyProvider(): ProviderForm {
  return { name: '', enabled: true, issuer: '', discovery_url: '', client_id: '', client_secret: '', scopes: 'openid profile email', username_claim: 'preferred_username', authorization_endpoint: '', token_endpoint: '', jwks_url: '' }
}

function sourceClass(source?: string) {
  return source === 'environment' ? 'text-[var(--accent-700)]' : 'text-[var(--neutral-700)]'
}

function setProviderForm(provider?: Provider) {
  editingProvider.value = provider || null
  providerTestMessage.value = ''
  providerTestError.value = ''
  Object.assign(providerForm, provider ? {
    name: provider.name,
    enabled: provider.enabled,
    issuer: provider.issuer,
    discovery_url: provider.discovery_url || '',
    client_id: provider.client_id,
    client_secret: '',
    scopes: provider.scopes.join(' '),
    username_claim: provider.username_claim,
    authorization_endpoint: provider.authorization_endpoint || '',
    token_endpoint: provider.token_endpoint || '',
    jwks_url: provider.jwks_url || ''
  } : emptyProvider())
  providerModalOpen.value = true
}

async function load() {
  loading.value = true
  errorMessage.value = ''
  try {
    const [authSettings, providerItems] = await Promise.all([
      manager.request<AuthSettings>('/api/v1/admin/auth/settings'),
      manager.request<Provider[]>('/api/v1/admin/auth/providers')
    ])
    settings.value = authSettings
    settingsForm.local_login_enabled = authSettings.local_login_enabled.value
    settingsForm.oidc_jit_provisioning_enabled = authSettings.oidc_jit_provisioning_enabled.value
    settingsForm.oidc_auto_link_enabled = authSettings.oidc_auto_link_enabled.value
    settingsForm.external_url = authSettings.external_url.value
    settingsForm.frontend_url = authSettings.frontend_url?.value || ''
    providers.value = providerItems || []
  } catch (error: any) {
    errorMessage.value = error?.data?.error || error?.message || 'Failed to load authentication settings'
  } finally {
    loading.value = false
  }
}

async function saveSettings() {
  savingSettings.value = true
  errorMessage.value = ''
  successMessage.value = ''
  try {
    await manager.request('/api/v1/admin/auth/settings', { method: 'PUT', body: { ...settingsForm } })
    successMessage.value = 'Authentication settings saved.'
    await manager.refreshAuthProviders()
    await load()
  } catch (error: any) {
    errorMessage.value = error?.data?.error || error?.message || 'Failed to save authentication settings'
  } finally {
    savingSettings.value = false
  }
}

function providerPayload() {
  return {
    name: providerForm.name,
    enabled: providerForm.enabled,
    issuer: providerForm.issuer,
    discovery_url: providerForm.discovery_url,
    client_id: providerForm.client_id,
    ...(providerForm.client_secret ? { client_secret: providerForm.client_secret } : {}),
    scopes: providerForm.scopes.split(/[\s,]+/).filter(Boolean),
    username_claim: providerForm.username_claim,
    authorization_endpoint: providerForm.authorization_endpoint,
    token_endpoint: providerForm.token_endpoint,
    jwks_url: providerForm.jwks_url
  }
}

async function testProviderForm() {
  providerBusy.value = 'draft-test'
  providerTestMessage.value = ''
  providerTestError.value = ''
  try {
    if (editingProvider.value && !providerForm.client_secret) {
      await manager.request(`/api/v1/admin/auth/providers/${encodeURIComponent(editingProvider.value.id)}/test`, { method: 'POST' })
    } else {
      await manager.request('/api/v1/admin/auth/providers/test', { method: 'POST', body: providerPayload() })
    }
    providerTestMessage.value = 'Provider configuration test passed.'
  } catch (error: any) {
    providerTestError.value = error?.data?.error || error?.message || 'Provider configuration test failed'
  } finally {
    providerBusy.value = ''
  }
}

async function saveProvider() {
  providerBusy.value = editingProvider.value?.id || 'new'
  errorMessage.value = ''
  try {
    if (editingProvider.value) {
      await manager.request(`/api/v1/admin/auth/providers/${encodeURIComponent(editingProvider.value.id)}`, { method: 'PUT', body: providerPayload() })
    } else {
      await manager.request('/api/v1/admin/auth/providers', { method: 'POST', body: providerPayload() })
    }
    providerModalOpen.value = false
    successMessage.value = editingProvider.value ? 'Provider updated.' : 'Provider added.'
    await load()
    await manager.refreshAuthProviders()
  } catch (error: any) {
    errorMessage.value = error?.data?.error || error?.message || 'Failed to save provider'
  } finally {
    providerBusy.value = ''
  }
}

async function testProvider(provider: Provider) {
  providerBusy.value = provider.id
  errorMessage.value = ''
  successMessage.value = ''
  try {
    await manager.request(`/api/v1/admin/auth/providers/${encodeURIComponent(provider.id)}/test`, { method: 'POST' })
    successMessage.value = `${provider.name} configuration test passed.`
    await load()
  } catch (error: any) {
    const message = error?.data?.error || error?.message || `${provider.name} configuration test failed`
    await load()
    errorMessage.value = message
  } finally {
    providerBusy.value = ''
  }
}

async function deleteProvider(provider: Provider) {
  const confirmed = await confirmation.value?.request({
    title: `Delete ${provider.name}?`,
    description: 'External identities linked to this provider will also be removed. This action can affect sign-in availability.',
    confirmLabel: 'Delete provider',
    confirmTone: 'destructive'
  })
  if (!confirmed) return
  providerBusy.value = provider.id
  errorMessage.value = ''
  try {
    await manager.request(`/api/v1/admin/auth/providers/${encodeURIComponent(provider.id)}`, { method: 'DELETE' })
    await load()
    await manager.refreshAuthProviders()
  } catch (error: any) {
    errorMessage.value = error?.data?.error || error?.message || 'Failed to delete provider'
  } finally {
    providerBusy.value = ''
  }
}

function callbackURL(provider: Provider) {
  const base = settingsForm.external_url.replace(/\/$/, '')
  return base ? `${base}/api/v1/auth/oidc/${encodeURIComponent(provider.id)}/callback` : 'Configure External URL to generate callback URI'
}

watch(() => manager.user.value, (value) => { if (value) void load() }, { immediate: true })
</script>

<template>
  <AdminShell title="Authentication" description="Configure local management login and OpenID Connect providers.">
    <template #actions>
      <AppButton intent="primary" :loading="savingSettings" @click="saveSettings">Save settings</AppButton>
    </template>

    <div class="space-y-5">
      <Frame v-if="errorMessage" class="px-4 py-3" data-testid="authentication-error">
        <div class="flex flex-wrap items-start gap-2"><StatusTag variant="failed">Authentication error</StatusTag><p class="min-w-0 flex-1 text-xs leading-5 text-[var(--neutral-800)]">{{ errorMessage }}</p></div>
      </Frame>
      <Frame v-if="successMessage" class="px-4 py-3" data-testid="authentication-success">
        <div class="flex flex-wrap items-start gap-2"><StatusTag variant="ready">Updated</StatusTag><p class="min-w-0 flex-1 text-xs leading-5 text-[var(--neutral-800)]">{{ successMessage }}</p></div>
      </Frame>

      <div v-if="loading" class="space-y-3">
        <USkeleton class="h-44 w-full" />
        <USkeleton class="h-36 w-full" />
      </div>

      <template v-else>
        <Frame class="p-5" data-testid="authentication-sign-in-policy">
          <div class="mb-5">
            <p class="text-[10px] font-semibold uppercase tracking-[.1em] text-[var(--neutral-700)]">SIGN-IN POLICY</p>
            <h2 class="mt-1 text-xl font-semibold">Management login</h2>
            <p class="mt-1 text-sm text-[var(--neutral-800)]">Configure browser destinations and which local or external sign-in paths are available.</p>
          </div>

          <div class="grid gap-5 lg:grid-cols-2">
            <UFormField description="Public backend URL used to generate stable OIDC callback URIs.">
              <template #label>
                <div class="flex w-full items-center justify-between gap-3"><span>External URL</span><span :class="sourceClass(settings?.external_url.source)" class="text-[10px] font-mono uppercase">{{ settings?.external_url.source }}</span></div>
              </template>
              <UInput v-model="settingsForm.external_url" class="w-full" :disabled="settings?.external_url.editable === false" placeholder="https://manager.example.com" />
            </UFormField>
            <UFormField description="Optional browser destination after OIDC sign-in. Leave empty to use External URL.">
              <template #label>
                <div class="flex w-full items-center justify-between gap-3"><span>Frontend URL</span><span :class="sourceClass(settings?.frontend_url?.source)" class="text-[10px] font-mono uppercase">{{ settings?.frontend_url?.source || 'default' }}</span></div>
              </template>
              <UInput v-model="settingsForm.frontend_url" class="w-full" :disabled="settings?.frontend_url?.editable === false" placeholder="https://manager.example.com" />
            </UFormField>
          </div>

          <div class="mt-5 border-t border-[var(--color-divider)] pt-5 space-y-4">
            <div class="grid gap-1 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center sm:gap-5"><USwitch v-model="settingsForm.local_login_enabled" :disabled="settings?.local_login_enabled.editable === false" label="Allow local username/password login" /><span :class="sourceClass(settings?.local_login_enabled.source)" class="pl-9 text-[10px] font-mono uppercase sm:pl-0">source: {{ settings?.local_login_enabled.source }}</span></div>
            <div class="grid gap-1 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center sm:gap-5"><USwitch v-model="settingsForm.oidc_jit_provisioning_enabled" :disabled="settings?.oidc_jit_provisioning_enabled.editable === false" label="Provision unknown OIDC users automatically" /><span :class="sourceClass(settings?.oidc_jit_provisioning_enabled.source)" class="pl-9 text-[10px] font-mono uppercase sm:pl-0">source: {{ settings?.oidc_jit_provisioning_enabled.source }}</span></div>
            <div class="grid gap-1 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center sm:gap-5"><USwitch v-model="settingsForm.oidc_auto_link_enabled" :disabled="settings?.oidc_auto_link_enabled.editable === false" label="Automatically link an exact matching username" /><span :class="sourceClass(settings?.oidc_auto_link_enabled.source)" class="pl-9 text-[10px] font-mono uppercase sm:pl-0">source: {{ settings?.oidc_auto_link_enabled.source }}</span></div>
          </div>

          <div class="mt-5 flex flex-col items-start gap-2 border border-[var(--color-divider)] px-4 py-3 sm:flex-row" data-testid="authentication-auto-link-note">
            <StatusTag variant="pending">Explicit linking required</StatusTag>
            <p class="min-w-0 flex-1 text-xs leading-5 text-[var(--neutral-800)]">Automatic linking is disabled by default. With it disabled, username collisions require explicit identity linking.</p>
          </div>
        </Frame>

        <section data-testid="authentication-providers">
          <div class="mb-3 flex flex-wrap items-end justify-between gap-4">
            <div>
              <p class="text-[10px] font-semibold uppercase tracking-[.1em] text-[var(--neutral-700)]">OIDC PROVIDERS</p>
              <h2 class="mt-1 text-xl font-semibold">External login providers</h2>
              <p class="mt-1 text-sm text-[var(--neutral-800)]">Multiple providers can be enabled at the same time.</p>
            </div>
            <AppButton intent="secondary" icon="i-lucide-plus" @click="setProviderForm()">Add provider</AppButton>
          </div>

          <Frame v-if="providers.length === 0" class="p-8 text-center">
            <h3 class="text-base font-semibold">No OIDC providers</h3>
            <p class="mt-1 text-sm text-[var(--neutral-800)]">Add a provider to offer external login.</p>
            <AppButton class="mt-4" intent="primary" @click="setProviderForm()">Add provider</AppButton>
          </Frame>

          <Frame v-else class="overflow-x-auto" role="region" tabindex="0" aria-label="OIDC providers. Scroll horizontally for issuer, callback and actions.">
            <p class="border-b border-[var(--color-divider)] px-4 py-2 text-xs text-[var(--neutral-700)] sm:hidden">Scroll horizontally for issuer, callback and actions.</p>
            <div class="min-w-[760px]">
              <div class="grid grid-cols-[minmax(180px,1fr)_minmax(220px,1.3fr)_180px_220px] gap-4 border-b border-[var(--color-divider)] px-4 py-2 text-[11px] uppercase tracking-[.08em] text-[var(--neutral-700)]">
                <span>Provider</span><span>Issuer</span><span>Callback</span><span class="text-right">Actions</span>
              </div>
              <div v-for="provider in providers" :key="provider.id" class="grid grid-cols-[minmax(180px,1fr)_minmax(220px,1.3fr)_180px_220px] items-center gap-4 border-b border-[var(--color-divider)] px-4 py-3 last:border-b-0">
                <div class="min-w-0">
                  <div class="flex flex-wrap items-center gap-2"><span class="font-semibold">{{ provider.name }}</span><StatusTag :variant="provider.enabled ? 'ready' : 'neutral'">{{ provider.enabled ? 'Enabled' : 'Disabled' }}</StatusTag><StatusTag :variant="provider.last_test_succeeded ? 'ready' : 'pending'">{{ provider.last_test_succeeded ? 'Tested' : 'Needs test' }}</StatusTag></div>
                </div>
                <p class="break-all text-sm text-[var(--neutral-800)]">{{ provider.issuer }}</p>
                <p class="break-all font-mono text-[11px] text-[var(--neutral-700)]">{{ callbackURL(provider) }}</p>
                <div class="flex justify-end gap-1">
                  <AppButton intent="ghost" :loading="providerBusy === provider.id" @click="testProvider(provider)">Test</AppButton>
                  <AppButton intent="ghost" @click="setProviderForm(provider)">Edit</AppButton>
                  <AppButton intent="secondary" tone="destructive" @click="deleteProvider(provider)">Delete</AppButton>
                </div>
              </div>
            </div>
          </Frame>
        </section>
      </template>
    </div>

    <UModal v-model:open="providerModalOpen" :title="editingProvider ? 'Edit OIDC provider' : 'Add OIDC provider'">
      <template #body>
        <div class="space-y-4">
          <Frame v-if="providerTestError" class="px-4 py-3" data-testid="provider-test-error">
            <div class="flex flex-wrap items-start gap-2"><StatusTag variant="failed">Test failed</StatusTag><p class="min-w-0 flex-1 text-xs leading-5 text-[var(--neutral-800)]">{{ providerTestError }}</p></div>
          </Frame>
          <Frame v-else-if="providerTestMessage" class="px-4 py-3" data-testid="provider-test-success">
            <div class="flex flex-wrap items-start gap-2"><StatusTag variant="ready">Test passed</StatusTag><p class="min-w-0 flex-1 text-xs leading-5 text-[var(--neutral-800)]">{{ providerTestMessage }}</p></div>
          </Frame>
          <UFormField label="Display name"><UInput v-model="providerForm.name" class="w-full" /></UFormField>
          <USwitch v-model="providerForm.enabled" label="Enabled" />
          <UFormField label="Issuer URL"><UInput v-model="providerForm.issuer" class="w-full" placeholder="https://id.example.com/application/o/manager/" /></UFormField>
          <UFormField label="Discovery URL" description="Optional. Defaults to issuer + /.well-known/openid-configuration."><UInput v-model="providerForm.discovery_url" class="w-full" /></UFormField>
          <UFormField label="Client ID"><UInput v-model="providerForm.client_id" class="w-full" /></UFormField>
          <UFormField :label="editingProvider?.secret_configured ? 'Replace client secret' : 'Client secret'"><UInput v-model="providerForm.client_secret" class="w-full" type="password" autocomplete="new-password" /></UFormField>
          <UFormField label="Scopes"><UInput v-model="providerForm.scopes" class="w-full" /></UFormField>
          <UFormField label="Username claim"><UInput v-model="providerForm.username_claim" class="w-full" /></UFormField>
          <div class="border-t border-[var(--color-divider)] pt-4">
            <p class="mb-3 text-[10px] font-semibold uppercase tracking-[.1em] text-[var(--neutral-700)]">MANUAL ENDPOINTS · OPTIONAL</p>
            <div class="space-y-4">
              <UFormField label="Authorization endpoint"><UInput v-model="providerForm.authorization_endpoint" class="w-full" /></UFormField>
              <UFormField label="Token endpoint"><UInput v-model="providerForm.token_endpoint" class="w-full" /></UFormField>
              <UFormField label="JWKS endpoint"><UInput v-model="providerForm.jwks_url" class="w-full" /></UFormField>
            </div>
          </div>
        </div>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <AppButton intent="secondary" @click="providerModalOpen = false">Cancel</AppButton>
          <AppButton intent="secondary" :loading="providerBusy === 'draft-test'" @click="testProviderForm">Test configuration</AppButton>
          <AppButton intent="primary" :loading="providerBusy === (editingProvider?.id || 'new')" @click="saveProvider">Save provider</AppButton>
        </div>
      </template>
    </UModal>
    <AppConfirmationModal ref="confirmation" />
  </AdminShell>
</template>
