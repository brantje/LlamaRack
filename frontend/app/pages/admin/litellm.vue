<script setup lang="ts">
type KeyStatus = { configured: boolean; prefix?: string }
type GeneratedKey = { prefix?: string; name?: string }
type SyncCounts = { published?: number; unpublished?: number }
type LastSync = {
  at?: number | string
  ok?: boolean
  error?: string
  counts?: SyncCounts
  published?: number
  unpublished?: number
}
type LiteLLMStatus = {
  proxy_url?: string
  api_base?: string
  default_api_base?: string
  proxy_key?: KeyStatus
  generated_key?: GeneratedKey
  last_sync?: LastSync
  configured?: boolean
}

const manager = useManager()
const status = ref<LiteLLMStatus>({})
const proxyUrl = ref('')
const apiBase = ref('')
const proxyKeyInput = ref('')
const busy = ref(false)
const error = ref('')
const saved = ref(false)
const testMessage = ref('')
const testError = ref('')
const syncMessage = ref('')
const syncError = ref('')
const disconnectOpen = ref(false)
const disconnectUnpublish = ref(false)
const rotateOpen = ref(false)

function normalizeKeyStatus(value: unknown): KeyStatus {
  if (!value || typeof value !== 'object' || typeof (value as KeyStatus).configured !== 'boolean') {
    return { configured: false }
  }
  const candidate = value as KeyStatus
  return { configured: candidate.configured, prefix: typeof candidate.prefix === 'string' ? candidate.prefix : undefined }
}

function normalizeGeneratedKey(value: unknown): GeneratedKey {
  if (!value || typeof value !== 'object') return {}
  const candidate = value as GeneratedKey
  return {
    prefix: typeof candidate.prefix === 'string' ? candidate.prefix : undefined,
    name: typeof candidate.name === 'string' ? candidate.name : undefined
  }
}

function normalizeLastSync(value: unknown): LastSync | undefined {
  if (!value || typeof value !== 'object') return undefined
  const candidate = value as LastSync
  const counts = candidate.counts && typeof candidate.counts === 'object'
    ? {
        published: typeof candidate.counts.published === 'number' ? candidate.counts.published : undefined,
        unpublished: typeof candidate.counts.unpublished === 'number' ? candidate.counts.unpublished : undefined
      }
    : (typeof candidate.published === 'number' || typeof candidate.unpublished === 'number')
        ? {
            published: typeof candidate.published === 'number' ? candidate.published : undefined,
            unpublished: typeof candidate.unpublished === 'number' ? candidate.unpublished : undefined
          }
        : undefined
  return {
    at: typeof candidate.at === 'number' || typeof candidate.at === 'string' ? candidate.at : undefined,
    ok: typeof candidate.ok === 'boolean' ? candidate.ok : undefined,
    error: typeof candidate.error === 'string' ? candidate.error : undefined,
    counts
  }
}

function normalizeStatus(value: unknown): LiteLLMStatus {
  if (!value || typeof value !== 'object') return {}
  const candidate = value as LiteLLMStatus
  return {
    proxy_url: typeof candidate.proxy_url === 'string' ? candidate.proxy_url : '',
    api_base: typeof candidate.api_base === 'string' ? candidate.api_base : '',
    default_api_base: typeof candidate.default_api_base === 'string' ? candidate.default_api_base : '',
    proxy_key: normalizeKeyStatus(candidate.proxy_key),
    generated_key: normalizeGeneratedKey(candidate.generated_key),
    last_sync: normalizeLastSync(candidate.last_sync),
    configured: typeof candidate.configured === 'boolean' ? candidate.configured : undefined
  }
}

function applyStatus(value: LiteLLMStatus) {
  status.value = value
  proxyUrl.value = value.proxy_url || ''
  apiBase.value = value.api_base || ''
}

function clearActionFeedback() {
  error.value = ''
  saved.value = false
  testMessage.value = ''
  testError.value = ''
  syncMessage.value = ''
  syncError.value = ''
}

const configured = computed(() => {
  if (typeof status.value.configured === 'boolean') return status.value.configured
  return Boolean(status.value.proxy_url?.trim() && status.value.proxy_key?.configured)
})
const apiBasePlaceholder = computed(() => status.value.default_api_base || 'https://manager.example.com/v1')
const syncVariant = computed(() => {
  const sync = status.value.last_sync
  if (!sync || sync.ok == null) return 'neutral'
  return sync.ok ? 'ready' : 'failed'
})
const syncLabel = computed(() => {
  const sync = status.value.last_sync
  if (!sync) return 'Never synced'
  if (sync.ok === true) return 'Sync ok'
  if (sync.ok === false) return 'Sync failed'
  return 'Sync status unknown'
})
const syncDetail = computed(() => {
  const sync = status.value.last_sync
  if (!sync) return 'No catalog sync has run yet.'
  const parts: string[] = []
  if (sync.at != null) {
    const date = typeof sync.at === 'number' ? new Date(sync.at * 1000) : new Date(sync.at)
    if (!Number.isNaN(date.getTime())) parts.push(date.toLocaleString())
  }
  if (sync.error) parts.push(sync.error)
  if (sync.counts) {
    const countParts: string[] = []
    if (typeof sync.counts.published === 'number') countParts.push(`${sync.counts.published} published`)
    if (typeof sync.counts.unpublished === 'number') countParts.push(`${sync.counts.unpublished} unpublished`)
    if (countParts.length) parts.push(countParts.join(', '))
  }
  return parts.join(' · ') || 'Last sync details unavailable.'
})

async function load() {
  if (!manager.user.value) return
  try {
    applyStatus(normalizeStatus(await manager.request('/api/v1/litellm')))
  } catch (value: any) {
    status.value = {}
    proxyUrl.value = ''
    apiBase.value = ''
    error.value = value?.data?.error || value?.message || 'Unable to load LiteLLM status'
  }
}

watch(manager.user, user => { if (user) void load() }, { immediate: true })

function saveBody() {
  const body: Record<string, string> = {
    proxy_url: proxyUrl.value.trim(),
    api_base: apiBase.value.trim()
  }
  if (proxyKeyInput.value.trim()) body.proxy_key = proxyKeyInput.value.trim()
  return body
}

async function save() {
  if (!proxyUrl.value.trim()) return
  busy.value = true
  clearActionFeedback()
  try {
    applyStatus(normalizeStatus(await manager.request('/api/v1/litellm', { method: 'PUT', body: saveBody() })))
    proxyKeyInput.value = ''
    saved.value = true
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to save LiteLLM settings'
  } finally {
    busy.value = false
  }
}

async function testConnection() {
  busy.value = true
  clearActionFeedback()
  try {
    await manager.request('/api/v1/litellm/test', { method: 'POST' })
    testMessage.value = 'LiteLLM Proxy connection succeeded.'
  } catch (value: any) {
    testError.value = value?.data?.error || value?.message || 'Unable to test LiteLLM connection'
  } finally {
    busy.value = false
  }
}

async function syncNow() {
  busy.value = true
  clearActionFeedback()
  try {
    await manager.request('/api/v1/litellm/sync', { method: 'POST' })
    await load()
    syncMessage.value = 'Catalog sync completed.'
  } catch (value: any) {
    syncError.value = value?.data?.error || value?.message || 'Unable to sync LiteLLM catalog'
    await load()
  } finally {
    busy.value = false
  }
}

function openRotate() {
  clearActionFeedback()
  rotateOpen.value = true
}

async function confirmRotate() {
  busy.value = true
  clearActionFeedback()
  rotateOpen.value = false
  try {
    applyStatus(normalizeStatus(await manager.request('/api/v1/litellm/rotate', { method: 'POST' })))
    saved.value = true
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to rotate LiteLLM key'
  } finally {
    busy.value = false
  }
}

function openDisconnect() {
  clearActionFeedback()
  disconnectUnpublish.value = false
  disconnectOpen.value = true
}

async function confirmDisconnect() {
  busy.value = true
  clearActionFeedback()
  disconnectOpen.value = false
  try {
    await manager.request('/api/v1/litellm', {
      method: 'DELETE',
      body: { unpublish: disconnectUnpublish.value }
    })
    proxyKeyInput.value = ''
    applyStatus(normalizeStatus({}))
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to disconnect LiteLLM'
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <AdminShell title="LiteLLM" description="Publish enabled Instances to a self-hosted LiteLLM Proxy and keep the catalog in sync.">
    <Frame class="max-w-[720px] p-5" data-testid="admin-litellm-card">
      <div>
        <h2 class="text-base font-semibold">Proxy connection</h2>
        <p class="mt-1 text-xs text-[var(--neutral-700)]">LiteLLM Proxy credentials are encrypted at rest and are never returned by the API.</p>
      </div>

      <div v-if="error" class="mt-4 flex items-start gap-2 border border-[var(--color-divider)] px-3 py-2">
        <StatusTag variant="failed">Error</StatusTag><p class="text-xs leading-5 text-[var(--neutral-800)]">{{ error }}</p>
      </div>
      <div v-if="saved" class="mt-4 flex items-start gap-2 border border-[var(--color-divider)] px-3 py-2">
        <StatusTag variant="ready">Saved</StatusTag><p class="text-xs leading-5 text-[var(--neutral-800)]">LiteLLM settings saved.</p>
      </div>
      <div v-if="testError" class="mt-4 flex items-start gap-2 border border-[var(--color-divider)] px-3 py-2" data-testid="litellm-test-error">
        <StatusTag variant="failed">Test failed</StatusTag><p class="text-xs leading-5 text-[var(--neutral-800)]">{{ testError }}</p>
      </div>
      <div v-else-if="testMessage" class="mt-4 flex items-start gap-2 border border-[var(--color-divider)] px-3 py-2" data-testid="litellm-test-success">
        <StatusTag variant="ready">Test passed</StatusTag><p class="text-xs leading-5 text-[var(--neutral-800)]">{{ testMessage }}</p>
      </div>
      <div v-if="syncError" class="mt-4 flex items-start gap-2 border border-[var(--color-divider)] px-3 py-2" data-testid="litellm-sync-error">
        <StatusTag variant="failed">Sync failed</StatusTag><p class="text-xs leading-5 text-[var(--neutral-800)]">{{ syncError }}</p>
      </div>
      <div v-else-if="syncMessage" class="mt-4 flex items-start gap-2 border border-[var(--color-divider)] px-3 py-2" data-testid="litellm-sync-success">
        <StatusTag variant="ready">Sync complete</StatusTag><p class="text-xs leading-5 text-[var(--neutral-800)]">{{ syncMessage }}</p>
      </div>

      <div class="mt-5 space-y-4">
        <UFormField label="LiteLLM Proxy URL" description="Base URL of your LiteLLM Proxy, including scheme.">
          <UInput v-model="proxyUrl" class="w-full" placeholder="https://litellm.example.com" data-testid="litellm-proxy-url" />
        </UFormField>

        <UFormField
          :label="status.proxy_key?.configured ? `Replace LiteLLM API key (${status.proxy_key.prefix || 'configured'}…)` : 'LiteLLM API key'"
          description="Master or admin key used to manage models in the proxy."
        >
          <UInput v-model="proxyKeyInput" class="w-full" type="password" autocomplete="off" placeholder="sk-…" data-testid="litellm-proxy-key" />
        </UFormField>

        <UFormField :label="'LlamaRack API base'" :description="`LiteLLM will call this OpenAI-compatible base. Default: ${apiBasePlaceholder}`">
          <UInput v-model="apiBase" class="w-full" :placeholder="apiBasePlaceholder" data-testid="litellm-api-base" />
        </UFormField>
      </div>

      <div class="mt-5 flex flex-wrap gap-2">
        <AppButton intent="primary" :loading="busy" :disabled="!proxyUrl.trim()" data-testid="litellm-save" @click="save">Save</AppButton>
        <AppButton intent="secondary" :loading="busy" :disabled="!configured" data-testid="litellm-test" @click="testConnection">Test connection</AppButton>
        <AppButton intent="secondary" :loading="busy" :disabled="!configured" data-testid="litellm-sync" @click="syncNow">Sync now</AppButton>
        <AppButton intent="secondary" :loading="busy" :disabled="!configured" data-testid="litellm-rotate" @click="openRotate">Rotate LlamaRack key</AppButton>
        <AppButton v-if="configured" intent="secondary" tone="destructive" :loading="busy" data-testid="litellm-disconnect" @click="openDisconnect">Disconnect</AppButton>
      </div>

      <div class="mt-5 space-y-4 border-t border-[var(--color-divider)] pt-4">
        <div class="flex flex-wrap items-center gap-2 text-sm text-[var(--neutral-700)]">
          <StatusTag :variant="configured ? 'ready' : 'neutral'">{{ configured ? 'Configured' : 'Not configured' }}</StatusTag>
          <span>Proxy key and URL are required before testing, syncing, or rotating.</span>
        </div>

        <div v-if="status.generated_key?.prefix || status.generated_key?.name" class="flex flex-wrap items-center gap-2 text-sm text-[var(--neutral-700)]" data-testid="litellm-generated-key">
          <StatusTag variant="ready">Generated key</StatusTag>
          <span v-if="status.generated_key?.name">{{ status.generated_key.name }}</span>
          <span v-if="status.generated_key?.prefix" class="font-mono text-[length:var(--font-size-table-header)]">{{ status.generated_key.prefix }}…</span>
        </div>

        <div class="flex flex-col gap-1" data-testid="litellm-sync-status">
          <div class="flex flex-wrap items-center gap-2">
            <StatusTag :variant="syncVariant">{{ syncLabel }}</StatusTag>
            <span class="text-sm text-[var(--neutral-700)]">Last catalog sync</span>
          </div>
          <p class="text-xs leading-5 text-[var(--neutral-800)]">{{ syncDetail }}</p>
        </div>
      </div>
    </Frame>

    <UModal v-model:open="rotateOpen" title="Rotate LlamaRack key">
      <template #body>
        <p class="text-sm leading-6 text-muted">
          Rotate the generated LlamaRack inference key used by LiteLLM? The new secret will be republished on every LlamaRack-owned model in the proxy.
        </p>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <AppButton data-testid="litellm-rotate-cancel" intent="secondary" @click="rotateOpen = false">Cancel</AppButton>
          <AppButton data-testid="litellm-rotate-confirm" intent="primary" :loading="busy" @click="confirmRotate">Rotate key</AppButton>
        </div>
      </template>
    </UModal>

    <UModal v-model:open="disconnectOpen" title="Disconnect LiteLLM">
      <template #body>
        <div class="space-y-4">
          <p class="text-sm leading-6 text-muted">
            Disconnect LlamaRack from LiteLLM? This deletes the hidden LiteLLM service account, its generated LlamaRack key, and the stored LiteLLM Proxy API key.
          </p>
          <div data-testid="litellm-disconnect-unpublish">
            <UCheckbox
              v-model="disconnectUnpublish"
              label="Unpublish LlamaRack-owned models first"
              description="Remove managed models from the LiteLLM Proxy before deleting local credentials."
            />
          </div>
        </div>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <AppButton data-testid="litellm-disconnect-cancel" intent="secondary" @click="disconnectOpen = false">Cancel</AppButton>
          <AppButton data-testid="litellm-disconnect-confirm" intent="primary" tone="destructive" :loading="busy" @click="confirmDisconnect">Disconnect</AppButton>
        </div>
      </template>
    </UModal>
  </AdminShell>
</template>
