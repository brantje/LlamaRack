<script setup lang="ts">
import type { Artifact, Model, Runtime } from '~/composables/useManager'

const manager = useManager()
const { models, artifacts, canOperate } = manager
const formBusy = ref(false)
const message = ref('')
const pending = reactive<Record<string, 'start' | 'stop' | 'test' | 'logs' | undefined>>({})
const testResults = reactive<Record<string, string>>({})
const workerLogs = reactive<Record<string, string[] | undefined>>({})
const artifactForm = reactive({ path: '', display_name: '' })
const modelForm = reactive({ model_id: '', display_name: '', artifact_id: '', always_on: false, autoload_enabled: true, priority: 'normal', routing_policy: 'least_active' })

function errorMessage(error: any, fallback: string) {
  return error?.data?.error || error?.message || fallback
}

function runtimeFor(model: Model): Runtime[] {
  return manager.runtimes.value[model.id] || []
}

async function registerArtifact() {
  formBusy.value = true
  message.value = ''
  try {
    const artifact = await manager.request<Artifact>('/api/v1/artifacts/register', { method: 'POST', body: artifactForm })
    artifactForm.path = ''
    artifactForm.display_name = ''
    modelForm.artifact_id = artifact.id
    await manager.refresh()
  } catch (error: any) {
    message.value = errorMessage(error, 'Unable to register artifact')
  } finally {
    formBusy.value = false
  }
}

async function createModel() {
  formBusy.value = true
  message.value = ''
  try {
    await manager.request('/api/v1/models', { method: 'POST', body: modelForm })
    modelForm.model_id = ''
    modelForm.display_name = ''
    await manager.refresh()
  } catch (error: any) {
    message.value = errorMessage(error, 'Unable to create model')
  } finally {
    formBusy.value = false
  }
}

async function action(id: string, operation: 'start' | 'stop') {
  pending[id] = operation
  message.value = ''
  testResults[id] = ''
  try {
    await manager.request(`/api/v1/models/${id}/${operation}`, { method: 'POST' })
    await manager.refresh()
  } catch (error: any) {
    message.value = errorMessage(error, `Unable to ${operation} model`)
  } finally {
    pending[id] = undefined
  }
}

async function loadLogs(id: string) {
  const previous = pending[id]
  if (!previous) pending[id] = 'logs'
  message.value = ''
  try {
    await manager.refresh()
    const runtimes = runtimeFor(models.value.find(model => model.id === id) as Model)
    if (!runtimes.length) {
      workerLogs[id] = []
      return
    }
    const chunks = await Promise.all(runtimes.map(async runtime => {
      const result = await manager.request<{ lines: string[] }>(`/api/v1/instances/${runtime.instance_id}/logs`)
      return (result.lines || []).map(line => `[${runtime.instance_id.slice(0, 8)}] ${line}`)
    }))
    workerLogs[id] = chunks.flat()
  } catch (error: any) {
    message.value = errorMessage(error, 'Unable to load worker logs')
  } finally {
    if (!previous) pending[id] = undefined
  }
}

async function testModel(model: Model) {
  pending[model.id] = 'test'
  message.value = ''
  testResults[model.id] = ''
  try {
    await manager.request(`/api/v1/models/${model.id}/start`, { method: 'POST' })
    await manager.refresh()
    const runtimes = runtimeFor(model)
    const ready = runtimes.find(runtime => runtime.state === 'READY')
    if (!ready) {
      const failed = runtimes.find(runtime => runtime.state === 'FAILED')
      throw new Error(failed?.last_error || 'Worker did not reach READY state')
    }
    testResults[model.id] = `PASS · READY · PID ${ready.pid || 'n/a'} · port ${ready.port || 'n/a'}`
    await loadLogs(model.id)
  } catch (error: any) {
    testResults[model.id] = `FAIL · ${errorMessage(error, 'Model test failed')}`
    await manager.refresh().catch(() => undefined)
    await loadLogs(model.id).catch(() => undefined)
  } finally {
    pending[model.id] = undefined
  }
}

async function remove(id: string) {
  if (!confirm('Delete this model configuration? The GGUF file will not be deleted.')) return
  pending[id] = 'stop'
  message.value = ''
  try {
    await manager.request(`/api/v1/models/${id}`, { method: 'DELETE' })
    await manager.refresh()
  } catch (error: any) {
    message.value = errorMessage(error, 'Unable to delete model')
  } finally {
    pending[id] = undefined
  }
}
</script>

<template>
  <div>
    <header class="page-header">
      <div>
        <p class="eyebrow">MODEL REGISTRY</p>
        <h1>Models</h1>
        <p class="muted">Register local GGUF files and control model workers.</p>
      </div>
      <button class="ghost" @click="manager.refresh">Refresh</button>
    </header>

    <p v-if="message" class="alert error">{{ message }}</p>

    <div class="content-grid">
      <section class="panel grow-panel">
        <div class="panel-header">
          <div>
            <p class="eyebrow">CONFIGURED</p>
            <h2>Model fleet</h2>
          </div>
        </div>

        <div v-if="!models.length" class="empty-state">
          <strong>No models configured</strong>
          <p>Create your first model using a registered artifact.</p>
        </div>

        <article v-for="model in models" :key="model.id" class="model-card">
          <div class="model-row">
            <div class="model-main">
              <div class="model-title">
                <strong>{{ model.model_id }}</strong>
                <span class="status" :data-state="manager.modelState(model)">{{ manager.modelState(model) }}</span>
              </div>
              <p>{{ model.display_name || model.artifact_path }}</p>
              <small>{{ model.priority }} · {{ model.routing_policy }} · {{ model.always_on ? 'always on' : model.autoload_enabled ? 'autoload' : 'manual' }}</small>
            </div>

            <div v-if="canOperate" class="row-actions">
              <button
                class="ghost small"
                :disabled="!!pending[model.id] || ['READY', 'STARTING', 'LOADING'].includes(manager.modelState(model))"
                @click="action(model.id, 'start')"
              >
                {{ pending[model.id] === 'start' ? 'Starting…' : 'Start' }}
              </button>
              <button
                class="ghost small"
                :disabled="!!pending[model.id] || manager.modelState(model) === 'UNLOADED'"
                @click="action(model.id, 'stop')"
              >
                {{ pending[model.id] === 'stop' ? 'Stopping…' : 'Stop' }}
              </button>
              <button class="primary small" :disabled="!!pending[model.id]" @click="testModel(model)">
                {{ pending[model.id] === 'test' ? 'Testing…' : 'Test' }}
              </button>
              <button class="ghost small" :disabled="!!pending[model.id]" @click="loadLogs(model.id)">
                {{ pending[model.id] === 'logs' ? 'Loading…' : 'Logs' }}
              </button>
              <button class="danger small" :disabled="!!pending[model.id]" @click="remove(model.id)">Delete</button>
            </div>
          </div>

          <p v-if="testResults[model.id]" class="test-result" :data-pass="testResults[model.id].startsWith('PASS')">
            {{ testResults[model.id] }}
          </p>
          <div v-if="workerLogs[model.id] !== undefined" class="worker-log">
            <div class="worker-log-header">
              <strong>Worker logs</strong>
              <small>{{ workerLogs[model.id]?.length || 0 }} lines</small>
            </div>
            <pre v-if="workerLogs[model.id]?.length">{{ workerLogs[model.id]?.join('\n') }}</pre>
            <p v-else class="muted">No worker output captured yet.</p>
          </div>
        </article>
      </section>

      <div v-if="canOperate" class="side-stack">
        <section class="panel">
          <p class="eyebrow">LOCAL ARTIFACT</p>
          <h2>Register GGUF</h2>
          <p class="muted">The file must exist inside the backend models volume.</p>
          <form @submit.prevent="registerArtifact">
            <label>GGUF path<input v-model="artifactForm.path" placeholder="/models/qwen.gguf" required></label>
            <label>Display name<input v-model="artifactForm.display_name" placeholder="Optional"></label>
            <button class="primary" :disabled="formBusy">Register artifact</button>
          </form>
        </section>

        <section class="panel">
          <p class="eyebrow">NEW MODEL</p>
          <h2>Create model</h2>
          <form @submit.prevent="createModel">
            <label>Public model ID<input v-model="modelForm.model_id" placeholder="qwen-coder" required></label>
            <label>Display name<input v-model="modelForm.display_name" placeholder="Optional"></label>
            <label>Artifact
              <select v-model="modelForm.artifact_id" required>
                <option value="" disabled>Select artifact</option>
                <option v-for="artifact in artifacts" :key="artifact.id" :value="artifact.id">
                  {{ artifact.display_name }}{{ artifact.quantization ? ` · ${artifact.quantization}` : '' }}
                </option>
              </select>
            </label>
            <div class="field-row">
              <label>Priority
                <select v-model="modelForm.priority">
                  <option value="low">Low</option>
                  <option value="normal">Normal</option>
                  <option value="high">High</option>
                </select>
              </label>
              <label>Routing
                <select v-model="modelForm.routing_policy">
                  <option value="least_active">Least active</option>
                  <option value="round_robin">Round robin</option>
                </select>
              </label>
            </div>
            <label class="check"><input v-model="modelForm.autoload_enabled" type="checkbox"> Autoload on request</label>
            <label class="check"><input v-model="modelForm.always_on" type="checkbox"> Always on</label>
            <button class="primary" :disabled="formBusy || !artifacts.length">Create model</button>
          </form>
        </section>
      </div>
    </div>
  </div>
</template>
