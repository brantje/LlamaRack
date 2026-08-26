<script setup lang="ts">
import type { APIKey } from '~/composables/useManager'

const manager = useManager()
const { isAdmin, apiBase } = manager
const keys = ref<APIKey[]>([])
const name = ref('default')
const secret = ref('')
const error = ref('')
const pending = reactive<Record<string, 'toggle' | 'delete' | undefined>>({})

async function load() {
  if (!isAdmin.value) return
  try {
    keys.value = await manager.request<APIKey[]>('/api/v1/api-keys')
  } catch (e: any) {
    error.value = e?.data?.error || e?.message || 'Unable to load API keys'
  }
}

async function createKey() {
  error.value = ''
  try {
    const result = await manager.request<{ key: APIKey; secret: string }>('/api/v1/api-keys', { method: 'POST', body: { name: name.value } })
    secret.value = result.secret
    await load()
  } catch (e: any) {
    error.value = e?.data?.error || e?.message || 'Unable to create API key'
  }
}

async function setEnabled(key: APIKey) {
  pending[key.id] = 'toggle'
  error.value = ''
  try {
    await manager.request(`/api/v1/api-keys/${key.id}`, { method: 'PATCH', body: { enabled: !key.enabled } })
    await load()
  } catch (e: any) {
    error.value = e?.data?.error || e?.message || 'Unable to update API key'
  } finally {
    pending[key.id] = undefined
  }
}

async function deleteKey(key: APIKey) {
  if (!confirm(`Delete API key "${key.name}"? This cannot be undone.`)) return
  pending[key.id] = 'delete'
  error.value = ''
  try {
    await manager.request(`/api/v1/api-keys/${key.id}`, { method: 'DELETE' })
    await load()
  } catch (e: any) {
    error.value = e?.data?.error || e?.message || 'Unable to delete API key'
  } finally {
    pending[key.id] = undefined
  }
}

async function copySecret() {
  if (secret.value) await navigator.clipboard.writeText(secret.value)
}

onMounted(load)
</script>

<template>
  <div>
    <header class="page-header">
      <div>
        <p class="eyebrow">OPENAI COMPATIBILITY</p>
        <h1>API</h1>
        <p class="muted">Connect OpenAI-compatible SDKs and LiteLLM to the unified manager endpoint.</p>
      </div>
    </header>

    <section class="panel">
      <p class="eyebrow">BASE URL</p>
      <h2>Unified endpoint</h2>
      <div class="endpoint">{{ apiBase }}/v1</div>
      <p class="muted">Supported initial routes: models, chat completions, completions, Responses and embeddings.</p>
    </section>

    <section v-if="isAdmin" class="panel">
      <div class="panel-header">
        <div>
          <p class="eyebrow">CREDENTIALS</p>
          <h2>Inference API keys</h2>
        </div>
        <div class="inline-create">
          <input v-model="name" placeholder="Key name">
          <button class="primary small" @click="createKey">Create key</button>
        </div>
      </div>

      <p v-if="error" class="alert error">{{ error }}</p>
      <div v-if="secret" class="secret-box">
        <strong>Copy this key now. It will not be shown again.</strong>
        <code>{{ secret }}</code>
        <button class="ghost small" @click="copySecret">Copy</button>
      </div>

      <div class="table-wrap">
        <table v-if="keys.length">
          <thead>
            <tr><th>Name</th><th>Prefix</th><th>Status</th><th></th></tr>
          </thead>
          <tbody>
            <tr v-for="key in keys" :key="key.id">
              <td>{{ key.name }}</td>
              <td class="mono">{{ key.prefix }}…</td>
              <td>{{ key.enabled ? 'Enabled' : 'Disabled' }}</td>
              <td>
                <div class="row-actions">
                  <button class="ghost small" :disabled="!!pending[key.id]" @click="setEnabled(key)">
                    {{ pending[key.id] === 'toggle' ? 'Saving…' : key.enabled ? 'Disable' : 'Enable' }}
                  </button>
                  <button class="danger small" :disabled="!!pending[key.id]" @click="deleteKey(key)">
                    {{ pending[key.id] === 'delete' ? 'Deleting…' : 'Delete' }}
                  </button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
        <div v-else class="empty-state">No API keys created yet.</div>
      </div>
    </section>

    <section v-else class="panel"><p class="muted">Only administrators can manage inference API keys.</p></section>
  </div>
</template>
