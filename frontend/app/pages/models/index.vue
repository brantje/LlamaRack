<script setup lang="ts">
import type { Model, Runtime } from '~/composables/useManager'

type WorkerLogLine = {
  timestamp: string
  text: string
}

const manager = useManager()
const { models } = manager
const message = ref('')
const pending = reactive<Record<string, 'start' | 'stop' | 'test' | 'logs' | 'delete' | undefined>>({})
const testResults = reactive<Record<string, string>>({})
const workerLogs = reactive<Record<string, WorkerLogLine[] | undefined>>({})
const liveLogModels = reactive<Record<string, boolean>>({})
const liveSources = new Map<string, EventSource>()
const logsOpen = ref(false)
const logModelId = ref<string | null>(null)

const logModel = computed(() => models.value.find(model => model.id === logModelId.value) || null)
const activeLogLines = computed(() => logModelId.value ? workerLogs[logModelId.value] || [] : [])

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

function logTimestamp(date = new Date()) {
  return [date.getHours(), date.getMinutes(), date.getSeconds()]
    .map(value => String(value).padStart(2, '0'))
    .join(':')
}

function appendWorkerLog(modelId: string, instanceId: string, line: string) {
  const lines = workerLogs[modelId] || (workerLogs[modelId] = [])
  lines.push({ timestamp: logTimestamp(), text: `[${instanceId.slice(0, 8)}] ${line}` })
  if (lines.length > 2000) lines.splice(0, lines.length - 2000)
}

function logStream(line: string): 'stderr' | 'stdout' | 'log' {
  if (line.includes('[stderr]')) return 'stderr'
  if (line.includes('[stdout]')) return 'stdout'
  return 'log'
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

async function openLogs(model: Model) {
  logModelId.value = model.id
  logsOpen.value = true
  await loadLogs(model.id)
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
  pending[id] = 'delete'
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
        title="Model fleet"
        description="Configure local GGUF models and control model workers."
      />
      <div class="flex flex-wrap justify-end gap-2">
        <UButton color="neutral" variant="soft" @click="manager.refresh">Refresh</UButton>
        <UButton to="/models/new">Add model</UButton>
      </div>
    </div>

    <UAlert v-if="message" color="error" variant="subtle" :description="message" />

    <UEmpty
      v-if="!models.length"
      title="No models configured"
      description="Add a GGUF model to get started."
    >
      <template #actions>
        <UButton to="/models/new" size="sm">Add model</UButton>
      </template>
    </UEmpty>

    <div v-else class="grid gap-4 md:grid-cols-2 2xl:grid-cols-3">
      <UCard
        v-for="model in models"
        :key="model.id"
        data-testid="model-card"
        class="min-w-0"
      >
        <template #header>
          <div class="flex items-start justify-between gap-3">
            <div class="min-w-0">
              <div class="flex flex-wrap items-center gap-2">
                <h2 class="truncate text-lg font-bold">{{ model.model_id }}</h2>
                <UBadge :color="statusColor(manager.modelState(model))" variant="subtle" size="sm">
                  {{ manager.modelState(model) }}
                </UBadge>
              </div>
              <p class="mt-1 truncate text-sm text-muted">{{ model.name }}</p>
            </div>

            <UButton
              icon="i-lucide-pencil"
              color="neutral"
              variant="ghost"
              size="sm"
              disabled
              aria-label="Edit model (coming soon)"
              title="Edit model (coming soon)"
            />
          </div>
        </template>

        <dl class="space-y-3 text-sm">
          <div>
            <dt class="text-xs font-semibold uppercase tracking-wide text-dimmed">GGUF</dt>
            <dd class="mt-1 break-all font-mono text-xs text-muted">
              {{ model.gguf_path }}{{ model.quantization ? ` · ${model.quantization}` : '' }}
            </dd>
          </div>
          <div class="grid grid-cols-2 gap-3">
            <div>
              <dt class="text-xs font-semibold uppercase tracking-wide text-dimmed">Priority</dt>
              <dd class="mt-1 capitalize">{{ model.priority }}</dd>
            </div>
            <div>
              <dt class="text-xs font-semibold uppercase tracking-wide text-dimmed">Routing</dt>
              <dd class="mt-1">{{ model.routing_policy === 'round_robin' ? 'Round robin' : 'Least active' }}</dd>
            </div>
          </div>
          <div>
            <dt class="text-xs font-semibold uppercase tracking-wide text-dimmed">Lifecycle</dt>
            <dd class="mt-1">{{ model.always_on ? 'always on' : model.autoload_enabled ? 'Autoload on request' : 'Manual' }}</dd>
          </div>
        </dl>

        <UAlert
          v-if="testResults[model.id]"
          class="mt-4"
          :color="testResults[model.id].startsWith('PASS') ? 'primary' : 'error'"
          variant="subtle"
          :description="testResults[model.id]"
        />

        <template #footer>
          <div class="flex flex-wrap gap-2">
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
              @click="openLogs(model)"
            >
              Logs
            </UButton>
            <UButton
              color="error"
              variant="soft"
              size="sm"
              :loading="pending[model.id] === 'delete'"
              :disabled="!!pending[model.id]"
              @click="remove(model.id)"
            >
              Delete
            </UButton>
          </div>
        </template>
      </UCard>
    </div>

    <UModal
      v-model:open="logsOpen"
      :title="logModel ? `worker://${logModel.model_id}` : 'Worker logs'"
      :ui="{
        content: 'sm:max-w-5xl overflow-hidden bg-[#05080a]',
        header: 'border-b border-slate-800 bg-slate-950/90',
        title: 'font-mono text-xs font-normal text-slate-400',
        body: 'p-0'
      }"
    >
      <template #actions>
        <span class="font-mono text-[11px] text-slate-500">
          {{ logModelId && liveLogModels[logModelId] ? 'LIVE' : 'WAITING' }} · {{ activeLogLines.length }} lines
        </span>
      </template>

      <template #body>
        <UScrollArea data-testid="log-terminal" class="h-[min(65vh,38rem)] bg-[#05080a]">
          <div class="min-h-full p-4 font-mono text-xs leading-[1.65]">
            <div v-if="activeLogLines.length" class="space-y-0.5">
              <div
                v-for="(line, index) in activeLogLines"
                :key="`${index}-${line.timestamp}-${line.text}`"
                :data-stream="logStream(line.text)"
                class="grid grid-cols-[4.75rem_minmax(0,1fr)] gap-3 break-words"
              >
                <time class="select-none text-slate-600" :datetime="line.timestamp" data-log-time>{{ line.timestamp }}</time>
                <span
                  :class="logStream(line.text) === 'stderr'
                    ? 'text-error-300'
                    : logStream(line.text) === 'stdout'
                      ? 'text-slate-200'
                      : 'text-slate-400'"
                >{{ line.text }}</span>
              </div>
            </div>

            <p v-else class="text-slate-500">Waiting for worker output…</p>
          </div>
        </UScrollArea>
      </template>
    </UModal>
  </div>
</template>
