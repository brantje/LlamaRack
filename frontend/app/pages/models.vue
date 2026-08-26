<script setup lang="ts">
import type { Model, Runtime } from '~/composables/useManager'

const manager = useManager()
const { models, canOperate } = manager
const message = ref('')
const pending = reactive<Record<string, 'start' | 'stop' | 'test' | 'logs' | undefined>>({})
const testResults = reactive<Record<string, string>>({})
const workerLogs = reactive<Record<string, string[] | undefined>>({})
const liveLogModels = reactive<Record<string, boolean>>({})
const liveSources = new Map<string, EventSource>()

function errorMessage(error: any, fallback: string) {
  return error?.data?.error || error?.message || fallback
}

function runtimeFor(model: Model): Runtime[] {
  return manager.runtimes.value[model.id] || []
}

function appendWorkerLog(modelId: string, instanceId: string, line: string) {
  const lines = workerLogs[modelId] || (workerLogs[modelId] = [])
  lines.push(`[${instanceId.slice(0, 8)}] ${line}`)
  if (lines.length > 2000) lines.splice(0, lines.length - 2000)
}

function ensureLogStream(modelId: string, runtime: Runtime) {
  if (liveSources.has(runtime.instance_id)) return
  const url = `${manager.apiBase.value}/api/v1/instances/${encodeURIComponent(runtime.instance_id)}/logs/stream`
  const source = new EventSource(url, { withCredentials: true })
  source.onmessage = (event) => {
    let line = event.data
    try {
      line = JSON.parse(event.data)
    } catch {
      // Keep raw SSE data if it was not JSON encoded.
    }
    appendWorkerLog(modelId, runtime.instance_id, String(line))
  }
  liveSources.set(runtime.instance_id, source)
  liveLogModels[modelId] = true
}

function closeLogStreams() {
  for (const source of liveSources.values()) source.close()
  liveSources.clear()
  for (const modelId of Object.keys(liveLogModels)) liveLogModels[modelId] = false
}

onBeforeUnmount(closeLogStreams)

async function action(id: string, operation: 'start' | 'stop') {
  pending[id] = operation
  message.value = ''
  testResults[id] = ''
  try {
    await loadLogs(id)
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
    const model = models.value.find(item => item.id === id)
    const runtimes = model ? runtimeFor(model) : []
    if (!runtimes.length) {
      workerLogs[id] = []
      return
    }
    if (workerLogs[id] === undefined) workerLogs[id] = []
    for (const runtime of runtimes) ensureLogStream(id, runtime)
  } catch (error: any) {
    message.value = errorMessage(error, 'Unable to open live worker logs')
  } finally {
    if (!previous) pending[id] = undefined
  }
}

async function testModel(model: Model) {
  pending[model.id] = 'test'
  message.value = ''
  testResults[model.id] = ''
  try {
    await loadLogs(model.id)
    await manager.request(`/api/v1/models/${model.id}/start`, { method: 'POST' })
    await manager.refresh()
    const runtimes = runtimeFor(model)
    const ready = runtimes.find(runtime => runtime.state === 'READY')
    if (!ready) {
      const failed = runtimes.find(runtime => runtime.state === 'FAILED')
      throw new Error(failed?.last_error || 'Worker did not reach READY state')
    }
    testResults[model.id] = `PASS · READY · PID ${ready.pid || 'n/a'} · port ${ready.port || 'n/a'}`
  } catch (error: any) {
    testResults[model.id] = `FAIL · ${errorMessage(error, 'Model test failed')}`
    await manager.refresh().catch(() => undefined)
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
        <p class="muted">Configure local GGUF models and control model workers.</p>
      </div>
      <div class="row-actions">
        <button class="ghost" @click="manager.refresh">Refresh</button>
        <NuxtLink v-if="canOperate" to="/models/new" class="primary">Add model</NuxtLink>
      </div>
    </header>

    <p v-if="message" class="alert error">{{ message }}</p>

    <section class="panel grow-panel">
      <div class="panel-header">
        <div>
          <p class="eyebrow">CONFIGURED</p>
          <h2>Model fleet</h2>
        </div>
      </div>

      <div v-if="!models.length" class="empty-state">
        <strong>No models configured</strong>
        <p>Add a GGUF model to get started.</p>
        <NuxtLink v-if="canOperate" to="/models/new" class="primary small">Add model</NuxtLink>
      </div>

      <article v-for="model in models" :key="model.id" class="model-card">
        <div class="model-row">
          <div class="model-main">
            <div class="model-title">
              <strong>{{ model.model_id }}</strong>
              <span class="status" :data-state="manager.modelState(model)">{{ manager.modelState(model) }}</span>
            </div>
            <p>{{ model.name }}</p>
            <small>{{ model.gguf_path }}{{ model.quantization ? ` · ${model.quantization}` : '' }}</small>
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
              {{ pending[model.id] === 'logs' ? 'Opening…' : liveLogModels[model.id] ? 'Logs · Live' : 'Logs' }}
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
            <small>{{ liveLogModels[model.id] ? 'LIVE · ' : '' }}{{ workerLogs[model.id]?.length || 0 }} lines</small>
          </div>
          <pre v-if="workerLogs[model.id]?.length">{{ workerLogs[model.id]?.join('\n') }}</pre>
          <p v-else class="muted">Waiting for worker output…</p>
        </div>
      </article>
    </section>
  </div>
</template>
