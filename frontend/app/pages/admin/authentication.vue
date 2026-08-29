<script setup lang="ts">
import AppConfirmationModal from '~/components/AppConfirmationModal.vue'

type SettingValue<T> = { value: T; source: string; editable: boolean }
type AuthSettings = {
  local_login_enabled: SettingValue<boolean>
  oidc_jit_provisioning_enabled: SettingValue<boolean>
  oidc_auto_link_enabled: SettingValue<boolean>
  external_url: SettingValue<string>
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
const settingsForm = reactive({ local_login_enabled: true, oidc_jit_provisioning_enabled: true, oidc_auto_link_enabled: false, external_url: '' })
const providers = ref<Provider[]>([])
const providerModalOpen = ref(false)
const editingProvider = ref<Provider | null>(null)
const providerForm = reactive<ProviderForm>(emptyProvider())
const confirmation = ref<InstanceType<typeof AppConfirmationModal> | null>(null)

function emptyProvider(): ProviderForm {
  return { name: '', enabled: true, issuer: '', discovery_url: '', client_id: '', client_secret: '', scopes: 'openid profile email', username_claim: 'preferred_username', authorization_endpoint: '', token_endpoint: '', jwks_url: '' }
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
    settingsForm.local_login_enabled = authSettings.local_login_enabled.value
    settingsForm.oidc_jit_provisioning_enabled = authSettings.oidc_jit_provisioning_enabled.value
    settingsForm.oidc_auto_link_enabled = authSettings.oidc_auto_link_enabled.value
    settingsForm.external_url = authSettings.external_url.value
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
    await manager.request('/api/v1/admin/auth/providers/test', { method: 'POST', body: providerPayload() })
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
    color: 'error'
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
  <div class="space-y-8">
    <UPageHeader title="Authentication" description="Configure local management login and OpenID Connect providers." />
    <UAlert v-if="errorMessage" color="error" variant="subtle" :description="errorMessage" />
    <UAlert v-if="successMessage" color="success" variant="subtle" :description="successMessage" />

    <USkeleton v-if="loading" class="h-64 w-full" />
    <template v-else>
      <UCard>
        <template #header><h2 class="text-lg font-semibold">Login policy</h2></template>
        <div class="space-y-5">
          <UFormField label="External URL" description="Public base URL used to generate stable OIDC callbacks.">
            <UInput v-model="settingsForm.external_url" class="w-full" placeholder="https://manager.example.com" />
          </UFormField>
          <USeparator />
          <USwitch v-model="settingsForm.local_login_enabled" label="Allow local username/password login" />
          <USwitch v-model="settingsForm.oidc_jit_provisioning_enabled" label="Provision unknown OIDC users automatically" />
          <USwitch v-model="settingsForm.oidc_auto_link_enabled" label="Automatically link an exact matching username" />
          <UAlert color="warning" variant="subtle" description="Automatic linking is disabled by default. With it disabled, username collisions require explicit identity linking." />
        </div>
        <template #footer><UButton :loading="savingSettings" @click="saveSettings">Save authentication settings</UButton></template>
      </UCard>

      <div class="flex flex-wrap items-end justify-between gap-4">
        <div><h2 class="text-xl font-semibold">OIDC providers</h2><p class="mt-1 text-sm text-muted">Multiple providers can be enabled at the same time.</p></div>
        <UButton icon="i-lucide-plus" @click="setProviderForm()">Add provider</UButton>
      </div>

      <UEmpty v-if="providers.length === 0" variant="naked" icon="i-lucide-shield" title="No OIDC providers" description="Add a provider to offer external login." />
      <div v-else class="divide-y divide-default rounded-lg border border-default">
        <div v-for="provider in providers" :key="provider.id" class="grid gap-4 p-5 lg:grid-cols-[1fr_auto] lg:items-center">
          <div class="min-w-0 space-y-2">
            <div class="flex flex-wrap items-center gap-2">
              <h3 class="font-semibold">{{ provider.name }}</h3>
              <UBadge :color="provider.enabled ? 'success' : 'neutral'" variant="subtle">{{ provider.enabled ? 'Enabled' : 'Disabled' }}</UBadge>
              <UBadge :color="provider.last_test_succeeded ? 'success' : 'warning'" variant="subtle">{{ provider.last_test_succeeded ? 'Tested' : 'Needs test' }}</UBadge>
            </div>
            <p class="break-all text-sm text-muted">{{ provider.issuer }}</p>
            <p class="break-all font-mono text-xs text-dimmed">Callback: {{ callbackURL(provider) }}</p>
          </div>
          <div class="flex flex-wrap gap-2">
            <UButton color="neutral" variant="soft" :loading="providerBusy === provider.id" @click="testProvider(provider)">Test</UButton>
            <UButton color="neutral" variant="soft" @click="setProviderForm(provider)">Edit</UButton>
            <UButton color="error" variant="soft" @click="deleteProvider(provider)">Delete</UButton>
          </div>
        </div>
      </div>
    </template>

    <UModal v-model:open="providerModalOpen" :title="editingProvider ? 'Edit OIDC provider' : 'Add OIDC provider'">
      <template #body>
        <div class="space-y-4">
          <UAlert v-if="providerTestError" color="error" variant="subtle" :description="providerTestError" />
          <UAlert v-else-if="providerTestMessage" color="success" variant="subtle" :description="providerTestMessage" />
          <UFormField label="Display name"><UInput v-model="providerForm.name" class="w-full" /></UFormField>
          <USwitch v-model="providerForm.enabled" label="Enabled" />
          <UFormField label="Issuer URL"><UInput v-model="providerForm.issuer" class="w-full" placeholder="https://id.example.com/application/o/manager/" /></UFormField>
          <UFormField label="Discovery URL" description="Optional. Defaults to issuer + /.well-known/openid-configuration."><UInput v-model="providerForm.discovery_url" class="w-full" /></UFormField>
          <UFormField label="Client ID"><UInput v-model="providerForm.client_id" class="w-full" /></UFormField>
          <UFormField :label="editingProvider?.secret_configured ? 'Replace client secret' : 'Client secret'"><UInput v-model="providerForm.client_secret" class="w-full" type="password" autocomplete="new-password" /></UFormField>
          <UFormField label="Scopes"><UInput v-model="providerForm.scopes" class="w-full" /></UFormField>
          <UFormField label="Username claim"><UInput v-model="providerForm.username_claim" class="w-full" /></UFormField>
          <USeparator label="Manual endpoints (optional)" />
          <UFormField label="Authorization endpoint"><UInput v-model="providerForm.authorization_endpoint" class="w-full" /></UFormField>
          <UFormField label="Token endpoint"><UInput v-model="providerForm.token_endpoint" class="w-full" /></UFormField>
          <UFormField label="JWKS endpoint"><UInput v-model="providerForm.jwks_url" class="w-full" /></UFormField>
        </div>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton color="neutral" variant="soft" @click="providerModalOpen = false">Cancel</UButton>
          <UButton v-if="!editingProvider" color="neutral" variant="soft" :loading="providerBusy === 'draft-test'" @click="testProviderForm">Test configuration</UButton>
          <UButton :loading="providerBusy === (editingProvider?.id || 'new')" @click="saveProvider">Save provider</UButton>
        </div>
      </template>
    </UModal>
    <AppConfirmationModal ref="confirmation" />
  </div>
</template>