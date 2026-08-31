<script setup lang="ts">
type LogSource = 'stdout' | 'stderr' | 'manager'
type LogEntry = { source: LogSource; timestamp: string; text: string }
type LogResponse = { instance_id: string; entries: LogEntry[] }

const props = defineProps<{ instanceId: string; embedded?: boolean }>()
const manager = useManager()
const entries = ref<LogEntry[]>([])
const source = ref<'all' | LogSource>('all')
const search = ref('')
const loading = ref(false)
const live = ref(false)
const error = ref('')
const output = ref<HTMLElement | null>(null)
let stream: EventSource | null = null
let connectionGeneration = 0

const sourceItems = [
  { label: 'All sources', value: 'all' },
  { label: 'stdout', value: 'stdout' },
  { label: 'stderr', value: 'stderr' },
  { label: 'Manager lifecycle', value: 'manager' }
]

const aggregateLogsTo = computed(() => ({
  path: '/admin/logs',
  query: { source: props.instanceId }
}))

const visibleEntries = computed(() => {
  const needle = search.value.trim().toLowerCase()
  return entries.value.filter((entry) => {
    if (source.value !== 'all' && entry.source !== source.value) return false
    return needle === '' || entry.text.toLowerCase().includes(needle)
  })
})

function formatTimestamp(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toISOString().replace('T', ' ').replace('Z', ' UTC')
}

function validEntry(value: unknown): value is LogEntry {
  if (!value || typeof value !== 'object') return false
  const candidate = value as Partial<LogEntry>
  return ['stdout', 'stderr', 'manager'].includes(String(candidate.source))
    && typeof candidate.text === 'string'
    && typeof candidate.timestamp === 'string'
    && !Number.isNaN(Date.parse(candidate.timestamp))
}

function append(entry: LogEntry) {
  entries.value = [...entries.value, entry].slice(-2000)
  void nextTick(() => {
    if (output.value) output.value.scrollTop = output.value.scrollHeight
  })
}

function closeStream() {
  connectionGeneration++
  stream?.close()
  stream = null
  live.value = false
}

async function loadSnapshot() {
  loading.value = true
  error.value = ''
  try {
    const response = await manager.request<LogResponse>(`/api/v1/logs?instance_id=${encodeURIComponent(props.instanceId)}&limit=2000`)
    entries.value = Array.isArray(response?.entries) ? response.entries.filter(validEntry) : []
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to load Instance logs'
  } finally {
    loading.value = false
  }
}

async function connectStream() {
  closeStream()
  const generation = connectionGeneration
  entries.value = []
  error.value = ''
  loading.value = true
  if (typeof EventSource === 'undefined') {
    await loadSnapshot()
    return
  }

  let ticket = ''
  try {
    const response = await manager.request<{ ticket: string }>('/api/v1/auth/ws-ticket', { method: 'POST' })
    ticket = String(response?.ticket || '')
  } catch (value: any) {
    if (generation !== connectionGeneration) return
    loading.value = false
    error.value = value?.data?.error || value?.message || 'Unable to authenticate live log stream'
    return
  }
  if (generation !== connectionGeneration) return
  if (!ticket) {
    loading.value = false
    error.value = 'Unable to authenticate live log stream'
    return
  }

  const url = `${manager.apiBase.value}/api/v1/logs/stream?instance_id=${encodeURIComponent(props.instanceId)}&limit=2000&ticket=${encodeURIComponent(ticket)}`
  const nextStream = new EventSource(url)
  stream = nextStream
  nextStream.onopen = () => {
    if (stream !== nextStream) return
    live.value = true
    loading.value = false
  }
  nextStream.addEventListener('log', (event) => {
    if (stream !== nextStream) return
    loading.value = false
    try {
      const entry = JSON.parse((event as MessageEvent).data)
      if (validEntry(entry)) append(entry)
    } catch {
      // Ignore malformed live log frames. The connection stays usable.
    }
  })
  nextStream.onerror = () => {
    if (stream !== nextStream) return
    nextStream.close()
    stream = null
    live.value = false
    loading.value = false
    error.value = 'Live log stream disconnected. Reconnect to continue tailing.'
  }
}

function clearView() {
  entries.value = []
  error.value = ''
}

watch(() => props.instanceId, () => { void connectStream() })
onMounted(() => { void connectStream() })
onBeforeUnmount(closeStream)
</script>

<template>
  <Frame
    id="logs"
    data-testid="instance-log-viewer"
    :class="props.embedded ? '!border-0 !bg-transparent !p-0' : 'p-4'"
  >
    <div class="flex flex-wrap items-start justify-between gap-3 border-b border-[var(--color-divider)] pb-4">
      <div>
        <p class="text-[length:var(--font-size-kicker)] font-semibold uppercase tracking-[.1em] text-[var(--neutral-700)]">INSTANCE OUTPUT</p>
        <h3 class="mt-1 text-sm font-semibold text-[var(--color-text)]">Current-session logs</h3>
        <p class="mt-1 max-w-3xl text-xs leading-5 text-[var(--neutral-700)]">Live llama-server stdout/stderr plus manager lifecycle events. Raw log lines stay in memory and are not persisted across manager restarts.</p>
      </div>
      <div class="flex items-center gap-2">
        <StatusTag :variant="live ? 'ready' : 'neutral'">{{ live ? 'Live' : 'Snapshot / disconnected' }}</StatusTag>
        <AppButton :to="aggregateLogsTo" intent="ghost" size="xs" data-testid="open-aggregate-logs">Open diagnostics</AppButton>
      </div>
    </div>

    <div class="space-y-4 pt-4">
      <div class="grid gap-3 md:grid-cols-[minmax(11rem,14rem)_minmax(14rem,1fr)_auto_auto]">
        <USelectMenu v-model="source" :items="sourceItems" label-key="label" value-key="value" aria-label="Log source" />
        <UInput v-model="search" icon="i-lucide-search" placeholder="Search current-session logs" aria-label="Search logs" />
        <AppButton intent="secondary" :loading="loading" @click="connectStream">{{ live ? 'Reload' : 'Reconnect' }}</AppButton>
        <AppButton intent="ghost" :disabled="!entries.length" @click="clearView">Clear view</AppButton>
      </div>

      <div v-if="error" class="border-l-2 border-[var(--color-accent)] pl-3" data-testid="instance-log-error">
        <p class="text-sm font-semibold text-[var(--color-text)]">Log stream unavailable</p>
        <p class="mt-1 text-xs leading-5 text-[var(--neutral-800)]">{{ error }}</p>
      </div>

      <div
        v-if="visibleEntries.length"
        ref="output"
        data-testid="instance-log-output"
        class="max-h-[34rem] min-h-64 overflow-auto border border-[var(--color-divider)] bg-[var(--neutral-100)] font-mono text-xs"
      >
        <div v-for="(entry, index) in visibleEntries" :key="`${index}-${entry.timestamp}-${entry.source}-${entry.text}`" class="grid grid-cols-[12rem_6rem_minmax(0,1fr)] gap-3 border-b border-[var(--color-divider)] px-3 py-1.5 last:border-b-0">
          <time :datetime="entry.timestamp" class="whitespace-nowrap text-[var(--neutral-700)]">{{ formatTimestamp(entry.timestamp) }}</time>
          <div><StatusTag :variant="entry.source === 'stderr' ? 'failed' : entry.source === 'manager' ? 'pending' : 'neutral'">{{ entry.source }}</StatusTag></div>
          <pre class="whitespace-pre-wrap break-words text-[var(--color-text)]">{{ entry.text }}</pre>
        </div>
      </div>
      <div v-else class="border border-[var(--color-divider)] px-4 py-8 text-center" data-testid="instance-log-empty">
        <p class="text-sm font-semibold text-[var(--color-text)]">{{ entries.length ? 'No logs match the current filters' : 'No logs in the current view' }}</p>
        <p class="mt-1 text-xs text-[var(--neutral-700)]">{{ entries.length ? 'Change the source or search filter to show more lines.' : 'Start or use the Instance to produce log output. Clearing the view does not stop live tailing.' }}</p>
      </div>

      <p class="text-xs text-[var(--neutral-700)]">Showing {{ visibleEntries.length }} of {{ entries.length }} lines kept in this browser view. The manager retains at most its bounded in-memory session ring.</p>
    </div>
  </Frame>
</template>