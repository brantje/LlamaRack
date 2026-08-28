<script setup lang="ts">
type LogSource = 'stdout' | 'stderr' | 'manager'
type LogEntry = { source: LogSource; timestamp: string; text: string }
type LogResponse = { instance_id: string; entries: LogEntry[] }

const props = defineProps<{ instanceId: string }>()
const manager = useManager()
const entries = ref<LogEntry[]>([])
const source = ref<'all' | LogSource>('all')
const search = ref('')
const loading = ref(false)
const live = ref(false)
const error = ref('')
const output = ref<HTMLElement | null>(null)
let stream: EventSource | null = null

const sourceItems = [
  { label: 'All sources', value: 'all' },
  { label: 'stdout', value: 'stdout' },
  { label: 'stderr', value: 'stderr' },
  { label: 'Manager lifecycle', value: 'manager' }
]

const visibleEntries = computed(() => {
  const needle = search.value.trim().toLowerCase()
  return entries.value.filter((entry) => {
    if (source.value !== 'all' && entry.source !== source.value) return false
    return needle === '' || entry.text.toLowerCase().includes(needle)
  })
})

function sourceColor(value: LogSource) {
  if (value === 'stderr') return 'error'
  if (value === 'manager') return 'primary'
  return 'neutral'
}

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

function connectStream() {
  closeStream()
  entries.value = []
  error.value = ''
  loading.value = true
  if (typeof EventSource === 'undefined') {
    void loadSnapshot()
    return
  }
  const url = `${manager.apiBase.value}/api/v1/logs/stream?instance_id=${encodeURIComponent(props.instanceId)}&limit=2000`
  const nextStream = new EventSource(url, { withCredentials: true })
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

watch(() => props.instanceId, () => connectStream())
onMounted(connectStream)
onBeforeUnmount(closeStream)
</script>

<template>
  <UCard id="logs" data-testid="instance-log-viewer">
    <template #header>
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div>
          <p class="text-sm font-semibold text-highlighted">Current-session logs</p>
          <p class="mt-1 text-xs text-muted">Live llama-server stdout/stderr plus manager lifecycle events. Raw log lines stay in memory and are not persisted across manager restarts.</p>
        </div>
        <UBadge :color="live ? 'success' : 'neutral'" variant="subtle">{{ live ? 'Live' : 'Snapshot / disconnected' }}</UBadge>
      </div>
    </template>

    <div class="space-y-4">
      <div class="grid gap-3 md:grid-cols-[minmax(11rem,14rem)_minmax(14rem,1fr)_auto_auto]">
        <USelectMenu v-model="source" :items="sourceItems" label-key="label" value-key="value" aria-label="Log source" />
        <UInput v-model="search" icon="i-lucide-search" placeholder="Search current-session logs" aria-label="Search logs" />
        <UButton color="neutral" variant="soft" :loading="loading" @click="connectStream">{{ live ? 'Reload' : 'Reconnect' }}</UButton>
        <UButton color="neutral" variant="ghost" :disabled="!entries.length" @click="clearView">Clear view</UButton>
      </div>

      <UAlert v-if="error" color="warning" variant="subtle" :description="error" />

      <div
        v-if="visibleEntries.length"
        ref="output"
        data-testid="instance-log-output"
        class="max-h-[34rem] min-h-64 overflow-auto rounded-md bg-elevated p-3 font-mono text-xs"
      >
        <div v-for="(entry, index) in visibleEntries" :key="`${index}-${entry.timestamp}-${entry.source}-${entry.text}`" class="grid grid-cols-[12rem_5rem_minmax(0,1fr)] gap-3 border-b border-default py-1.5 last:border-b-0">
          <time :datetime="entry.timestamp" class="whitespace-nowrap text-muted">{{ formatTimestamp(entry.timestamp) }}</time>
          <div><UBadge :color="sourceColor(entry.source)" variant="soft" size="xs">{{ entry.source }}</UBadge></div>
          <pre class="whitespace-pre-wrap break-words text-highlighted">{{ entry.text }}</pre>
        </div>
      </div>
      <UEmpty v-else variant="naked" :title="entries.length ? 'No logs match the current filters' : 'No logs in the current view'" :description="entries.length ? 'Change the source or search filter to show more lines.' : 'Start or use the Instance to produce log output. Clearing the view does not stop live tailing.'" />

      <p class="text-xs text-muted">Showing {{ visibleEntries.length }} of {{ entries.length }} lines kept in this browser view. The manager retains at most its bounded in-memory session ring.</p>
    </div>
  </UCard>
</template>
