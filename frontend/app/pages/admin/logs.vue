<script setup lang="ts">
type LogLevel = 'INFO' | 'WARN' | 'DEBUG' | 'ERROR'
type SystemLogEntry = {
  timestamp: string
  level: LogLevel
  source: string
  message: string
}

type LogResponse = { entries: SystemLogEntry[] }

const manager = useManager()
const route = useRoute()
const entries = ref<SystemLogEntry[]>([])
const selectedLevel = ref<'ALL' | 'INFO' | 'WARN' | 'DEBUG'>('ALL')
const selectedSource = ref('all')
const grep = ref('')
const follow = ref(true)
const loading = ref(false)
const error = ref('')
const output = ref<HTMLElement | null>(null)
let stream: EventSource | null = null
let connectionGeneration = 0
let programmaticScroll = false

const fixedSources = ['manager', 'gateway', 'telemetry']
const levels = ['ALL', 'INFO', 'WARN', 'DEBUG'] as const

const instanceSources = computed(() => Array.from(new Set(
  entries.value
    .map((entry) => entry.source)
    .filter((source) => source && !fixedSources.includes(source))
)).sort())

const sourceItems = computed(() => {
  const items = ['all', ...fixedSources, ...instanceSources.value]
  if (selectedSource.value !== 'all' && !items.includes(selectedSource.value)) items.push(selectedSource.value)
  return items
})

const visibleEntries = computed(() => {
  const needle = grep.value.trim().toLowerCase()
  return entries.value.filter((entry) => {
    if (selectedLevel.value === 'WARN') {
      if (entry.level !== 'WARN' && entry.level !== 'ERROR') return false
    } else if (selectedLevel.value !== 'ALL' && entry.level !== selectedLevel.value) {
      return false
    }
    if (selectedSource.value !== 'all' && entry.source !== selectedSource.value) return false
    return needle === '' || entry.message.toLowerCase().includes(needle)
  })
})

function validEntry(value: unknown): value is SystemLogEntry {
  if (!value || typeof value !== 'object') return false
  const candidate = value as Partial<SystemLogEntry>
  return ['INFO', 'WARN', 'DEBUG', 'ERROR'].includes(String(candidate.level))
    && typeof candidate.source === 'string'
    && candidate.source.trim() !== ''
    && typeof candidate.message === 'string'
    && typeof candidate.timestamp === 'string'
    && !Number.isNaN(Date.parse(candidate.timestamp))
}

function formatTime(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleTimeString([], { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

function levelClass(level: LogLevel) {
  if (level === 'INFO') return 'text-[var(--accent-700)]'
  if (level === 'DEBUG') return 'opacity-45'
  if (level === 'WARN') return 'font-bold text-[var(--accent-800)]'
  return 'font-bold text-[var(--accent-900)]'
}

async function scrollToTail() {
  if (!follow.value) return
  await nextTick()
  if (!output.value) return
  programmaticScroll = true
  output.value.scrollTop = output.value.scrollHeight
  requestAnimationFrame(() => { programmaticScroll = false })
}

function append(entry: SystemLogEntry) {
  entries.value = [...entries.value, entry].slice(-4000)
  void scrollToTail()
}

function onScroll() {
  if (!output.value || programmaticScroll) return
  const remaining = output.value.scrollHeight - output.value.scrollTop - output.value.clientHeight
  if (remaining > 24) follow.value = false
}

function closeStream() {
  connectionGeneration++
  stream?.close()
  stream = null
}

async function loadSnapshot() {
  loading.value = true
  error.value = ''
  try {
    const response = await manager.request<LogResponse>('/api/v1/logs?scope=system&limit=4000')
    entries.value = Array.isArray(response?.entries) ? response.entries.filter(validEntry) : []
    await scrollToTail()
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to load manager logs'
  } finally {
    loading.value = false
  }
}

async function connectStream() {
  closeStream()
  const generation = connectionGeneration
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

  const url = `${manager.apiBase.value}/api/v1/logs/stream?scope=system&limit=4000&ticket=${encodeURIComponent(ticket)}`
  const nextStream = new EventSource(url)
  stream = nextStream
  nextStream.onopen = () => {
    if (stream !== nextStream) return
    loading.value = false
  }
  nextStream.addEventListener('log', (event) => {
    if (stream !== nextStream) return
    loading.value = false
    try {
      const entry = JSON.parse((event as MessageEvent).data)
      if (validEntry(entry)) append(entry)
    } catch {
      // Ignore malformed frames without interrupting the diagnostic stream.
    }
  })
  nextStream.onerror = () => {
    if (stream !== nextStream) return
    nextStream.close()
    stream = null
    loading.value = false
    error.value = 'Live log stream disconnected. Reconnect to continue tailing.'
  }
}

watch(follow, (enabled) => {
  if (enabled) void scrollToTail()
})

watch(() => route.query.source, (value) => {
  const source = Array.isArray(value) ? value[0] : value
  selectedSource.value = typeof source === 'string' && source.trim() ? source : 'all'
}, { immediate: true })

onMounted(() => { void connectStream() })
onBeforeUnmount(closeStream)
</script>

<template>
  <AdminShell
    headline="DIAGNOSTICS"
    title="Logs"
    description="Live manager, gateway, telemetry and Instance diagnostics from this manager process."
  >
    <template #actions>
      <div class="flex flex-wrap items-center justify-end gap-3" data-testid="system-log-header-controls">
        <div class="flex items-center gap-1" aria-label="Log level">
          <button
            v-for="level in levels"
            :key="level"
            type="button"
            class="border px-3 py-1.5 text-xs font-semibold transition-colors"
            :class="selectedLevel === level
              ? 'border-[var(--color-accent)] bg-[var(--accent-100)] text-[var(--accent-800)]'
              : 'border-[var(--color-divider)] bg-transparent text-[var(--neutral-700)] hover:bg-[var(--neutral-100)]'"
            :aria-pressed="selectedLevel === level"
            @click="selectedLevel = level"
          >
            {{ level === 'ALL' ? 'All' : level }}
          </button>
        </div>
        <UCheckbox v-model="follow" label="Follow" data-testid="system-log-follow" />
      </div>
    </template>

    <div class="flex min-h-[calc(100vh-10rem)] flex-col gap-4" data-testid="system-log-viewer">
      <div class="flex flex-wrap items-center gap-2 border-y border-[var(--color-divider)] py-3">
        <button
          v-for="source in sourceItems"
          :key="source"
          type="button"
          class="border px-2.5 py-1 text-xs font-semibold transition-colors"
          :class="selectedSource === source
            ? 'border-[var(--color-accent)] bg-[var(--color-accent)] text-[var(--color-bg)]'
            : 'border-[var(--color-divider)] bg-transparent text-[var(--neutral-700)] hover:bg-[var(--neutral-100)]'"
          :aria-pressed="selectedSource === source"
          @click="selectedSource = source"
        >
          {{ source === 'all' ? 'All sources' : source }}
        </button>
        <UInput
          v-model="grep"
          class="ml-auto min-w-[14rem] font-mono"
          icon="i-lucide-search"
          placeholder="grep message"
          aria-label="grep log messages"
        />
      </div>

      <p v-if="error" class="border border-[var(--accent-300)] bg-[var(--accent-100)] px-3 py-2 text-xs text-[var(--accent-900)]">
        {{ error }}
        <button type="button" class="ml-2 font-semibold underline" @click="connectStream">Reconnect</button>
      </p>

      <Frame class="min-h-0 flex-1 overflow-hidden bg-[var(--neutral-100)] p-0">
        <div
          ref="output"
          data-testid="system-log-output"
          class="h-full min-h-[28rem] max-h-[calc(100vh-18rem)] overflow-auto font-mono text-[12px] leading-[1.6]"
          @scroll="onScroll"
        >
          <div
            v-for="(entry, index) in visibleEntries"
            :key="`${entry.timestamp}-${entry.source}-${index}-${entry.message}`"
            class="grid grid-cols-[auto_52px_150px_minmax(0,1fr)] gap-3 border-b border-[var(--color-divider)] px-3 py-1 last:border-b-0"
            data-testid="system-log-row"
          >
            <time :datetime="entry.timestamp" class="whitespace-nowrap opacity-45">{{ formatTime(entry.timestamp) }}</time>
            <span :class="levelClass(entry.level)">{{ entry.level }}</span>
            <span class="truncate opacity-55">{{ entry.source }}</span>
            <span class="min-w-0 whitespace-pre-wrap break-words text-[var(--neutral-900)]">{{ entry.message }}</span>
          </div>
          <div v-if="!visibleEntries.length && !loading" class="px-4 py-8 text-center text-sm text-[var(--neutral-700)]">
            No log lines match this filter.
          </div>
          <div v-if="loading && !entries.length" class="px-4 py-8 text-center text-sm text-[var(--neutral-700)]">
            Loading diagnostics…
          </div>
        </div>
      </Frame>
    </div>
  </AdminShell>
</template>
