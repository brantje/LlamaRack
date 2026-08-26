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

function statusColor(state: string): 'primary' | 'error' | 'warning' | 'secondary' | 'neutral' {
  if (state === 'READY') return 'primary'
  if (state === 'FAILED') return 'error'
  if (state === 'STARTING' || state === 'LOADING') return 'warning'
  if (state === 'STOPPING') return 'secondary'
  return 'neutral'
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
  <div class="space-y-5">
    <div class="flex items-start justify-between gap-6">
      <UPageHeader
        class="min-w-0 flex-1"
        headline="MODEL REGISTRY"
        title="Models"
        description="Configure local GGUF models and control model workers."
      />
      <div class="flex flex-wrap justify-end gap-2">
        <UButton color="neutral" variant="soft" @click="manager.refresh">Refresh</UButton>
        <UButton v-if="canOperate" to="/models/new">Add model</UButton>
      </div>
    </div>

    <UAlert v-if="message" color="error" variant="subtle" :description="message" />

    <UCard>
      <template #header>
        <div>
          <p class="mb-1 text-xs font-extrabold tracking-[0.18em] text-dimmed">CONFIGURED</p>
          <h2 class="text-xl font-bold">Model fleet</h2>
        </div>
      </template>

      <UEmpty
        v-if="!models.length"
        variant="naked"
        title="No models configured"
        description="Add a GGUF model to get started."
      >
        <template v-if="canOperate" #actions>
          <UButton to="/models/new" size="sm">Add model</UButton>
        </template>
      </UEmpty>

      <div v-else class="divide-y divide-default">
        <article v-for="model in models" :key="model.id" class="py-5 first:pt-0 last:pb-0">
          <div class="flex flex-col gap-4 xl:flex-row xl:items-center xl:justify-between">
            <div class="min-w-0">
              <div class="flex flex-wrap items-center gap-2">
                <strong>{{ model.model_id }}</strong>
                <UBadge :color="statusColor(manager.modelState(model))" variant="subtle" size="sm">
                  {{ manager.modelState(model) }}
                </UBadge>
              </div>
              <p class="mt-1.5 text-sm text-muted">{{ model.name }}</p>
              <p class="mt-1 break-all font-mono text-xs text-dimmed">
                {{ model.gguf_path }}{{ model.quantization ? ` · ${model.quantization}` : '' }}
              </p>
              <p class="mt-1 text-xs text-dimmed">
                {{ model.priority }} · {{ model.routing_policy }} · {{ model.always_on ? 'always on' : model.autoload_enabled ? 'autoload' : 'manual' }}
              </p>
            </div>

            <UFieldGroup v-if="canOperate" class="flex-wrap justify-start xl:justify-end">
              <UButton
                color="neutral"
                variant="soft"
                size="sm"
                :loading="pending[model.id] === 'start'"
                :disabled="!!pending[model.id] || ['READY', 'STARTING', 'LOADING'].includes(manager.modelState(model))"
                @click="action(model.id, 'start')"
              >
                Start
              </UButton>
              <UButton
                color="neutral"
                variant="soft"
                size="sm"
                :loading="pending[model.id] === 'stop'"
                :disabled="!!pending[model.id] || manager.modelState(model) === 'UNLOADED'"
                @click="action(model.id, 'stop')"
              >
                Stop
              </UButton>
              <UButton
                size="sm"
                :loading="pending[model.id] === 'test'"
                :disabled="!!pending[model.id]"
                @click="testModel(model)"
              >
                Test
              </UButton>
              <UButton
                color="neutral"
                variant="soft"
                size="sm"
                :loading="pending[model.id] === 'logs'"
                :disabled="!!pending[model.id]"
                @click="loadLogs(model.id)"
              >
                {{ liveLogModels[model.id] ? 'Logs · Live' : 'Logs' }}
              </UButton>
              <UButton
                color="error"
                variant="soft"
                size="sm"
                :disabled="!!pending[model.id]"
                @click="remove(model.id)"
              >
                Delete
              </UButton>
            </UFieldGroup>
          </div>

          <UAlert
            v-if="testResults[model.id]"
            class="mt-4"
            :color="testResults[model.id].startsWith('PASS') ? 'primary' : 'error'"
            variant="subtle"
            :description="testResults[model.id]"
          />

          <section v-if="workerLogs[model.id] !== undefined" class="mt-4 border-t border-default pt-4">
            <div class="flex items-center justify-between gap-3 text-xs">
              <strong>Worker logs</strong>
              <span class="text-dimmed">{{ liveLogModels[model.id] ? 'LIVE · ' : '' }}{{ workerLogs[model.id]?.length || 0 }} lines</span>
            </div>
            <UScrollArea v-if="workerLogs[model.id]?.length" class="mt-3 max-h-80">
              <pre class="whitespace-pre-wrap break-words font-mono text-xs leading-5 text-muted">{{ workerLogs[model.id]?.join('\n') }}</pre>
            </UScrollArea>
            <p v-else class="mt-3 text-sm text-muted">Waiting for worker output…</p>
          </section>
        </article>
      </div>
    </UCard>
  </div>
</template>
