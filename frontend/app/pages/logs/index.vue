<script setup lang="ts">
import type { TableColumn } from '@nuxt/ui'

const manager = useManager()
const route = useRoute()
const router = useRouter()

type APIKeyRef = { id: string; name: string; prefix: string }
type RequestRecord = {
  id: number
  request_id: string
  trace_id?: string
  session_id?: string
  session_total_count?: number
  model_id?: string
  model_name?: string
  call_type?: string
  started_at: number
  finished_at: number
  instance_id?: string
  endpoint: string
  api_key?: APIKeyRef
  client_ip?: string
  user_agent?: string
  streaming: boolean
  status_code: number
  result: string
  duration_ms: number
  ttft_ms?: number
  prompt_tokens: number
  generated_tokens: number
  total_tokens: number
  prompt_tokens_per_second?: number
  generation_tokens_per_second?: number
  queue_duration_ms: number
  load_duration_ms: number
  autoloaded: boolean
  error?: string
}
type RequestDetail = RequestRecord & { request_body?: string; response_body?: string }
type RequestPage = { items: RequestRecord[]; has_more: boolean }
type SessionSortMode = 'duration' | 'start_time'
type RequestSelection = { current: boolean; sessionID: string }

const pageSize = 25
const sessionPageSize = 100
const maxSessionPages = 50
const requests = ref<RequestRecord[]>([])
const offset = ref(0)
const hasMore = ref(false)
const loading = ref(false)
const error = ref('')
const traceID = ref(String(route.query.trace_id || '').trim())
const routeReady = ref(false)
const liveStreamingEnabled = ref(true)
const detailOpen = ref(false)
const detailLoading = ref(false)
const detailError = ref('')
const detail = ref<RequestDetail | null>(null)
const detailMode = ref<'pretty' | 'json'>('pretty')
const activeSessionID = ref('')
const sessionRequests = ref<RequestRecord[]>([])
const sessionLoading = ref(false)
const sessionError = ref('')
const sessionSortMode = ref<SessionSortMode>('duration')
const sessionSidebarOpen = ref(true)
const filters = reactive({ window: '1h', instance_id: '', endpoint: '', api_key_id: '', result: '', status_code: '', streaming: '', search: '' })
let loadGeneration = 0
let sessionLoadGeneration = 0
let detailLoadGeneration = 0
let detailSelectionGeneration = 0

const windowItems = [
  { label: 'Last 15 minutes', value: '15m' }, { label: 'Last hour', value: '1h' },
  { label: 'Last 24 hours', value: '24h' }, { label: 'Last 7 days', value: '7d' },
  { label: 'All retained history', value: 'all' }
]
const endpointItems = [
  { label: 'All endpoints', value: '' }, { label: 'Chat completions', value: '/v1/chat/completions' },
  { label: 'Completions', value: '/v1/completions' }, { label: 'Responses', value: '/v1/responses' },
  { label: 'Embeddings', value: '/v1/embeddings' }
]
const resultItems = [{ label: 'All results', value: '' }, { label: 'Success', value: 'success' }, { label: 'Error', value: 'error' }]
const streamingItems = [{ label: 'Streaming + non-streaming', value: '' }, { label: 'Streaming', value: 'true' }, { label: 'Non-streaming', value: 'false' }]
const sessionSortItems = [{ label: 'Duration', value: 'duration' }, { label: 'Start time', value: 'start_time' }]
const instanceItems = computed(() => [{ label: 'All Instances', value: '' }, ...manager.instances.value.map(item => ({ label: `${item.name} (${item.id})`, value: item.id }))])
const columns: TableColumn<RequestRecord>[] = [
  { accessorKey: 'started_at', header: 'Time' }, { accessorKey: 'result', header: 'Status' },
  { accessorKey: 'model_name', header: 'Model' }, { accessorKey: 'instance_id', header: 'Instance ID' },
  { accessorKey: 'api_key', header: 'Key alias' }, { accessorKey: 'duration_ms', header: 'Duration' },
  { accessorKey: 'ttft_ms', header: 'TTFT' }, { accessorKey: 'total_tokens', header: 'Tokens' },
  { accessorKey: 'prompt_tokens_per_second', header: 'Prompt tok/s' }, { accessorKey: 'generation_tokens_per_second', header: 'Gen tok/s' },
  { accessorKey: 'call_type', header: 'Call Type' }, { accessorKey: 'request_id', header: 'Request ID' },
  { accessorKey: 'session_id', header: 'Session' }, { accessorKey: 'endpoint', header: 'Endpoint' }
]
const displayRequests = computed(() => requests.value)
const sortedSessionRequests = computed(() => {
  const rows = [...sessionRequests.value]
  if (sessionSortMode.value === 'start_time') return rows.sort((a, b) => a.started_at - b.started_at)
  return rows.sort((a, b) => (b.duration_ms || 0) - (a.duration_ms || 0))
})
const sessionTotalCount = computed(() => sessionRequests.value[0]?.session_total_count || sessionRequests.value.length)
const sessionTruncated = computed(() => sessionTotalCount.value > sessionRequests.value.length)
const sessionDuration = computed(() => {
  if (!sessionRequests.value.length) return 0
  const started = Math.min(...sessionRequests.value.map(item => item.started_at).filter(Boolean))
  const finished = Math.max(...sessionRequests.value.map(item => item.finished_at || item.started_at).filter(Boolean))
  return Math.max(0, finished - started)
})
const sidebarRequests = computed<RequestRecord[]>(() => sessionRequests.value.length ? sortedSessionRequests.value : detail.value ? [detail.value] : [])
const sidebarTotalCount = computed(() => activeSessionID.value ? sessionTotalCount.value : sidebarRequests.value.length)
const sidebarDuration = computed(() => activeSessionID.value ? sessionDuration.value : (detail.value?.duration_ms || 0))
const liveRequestFingerprint = computed(() => {
  const items = (manager.observabilityLive.value?.requests || []) as RequestRecord[]
  return items.map(item => [
    item.id, item.request_id, item.started_at, item.finished_at, item.status_code,
    item.result, item.total_tokens, item.duration_ms, item.ttft_ms ?? ''
  ].join(':')).join('|')
})
const liveState = computed(() => {
  if (!liveStreamingEnabled.value) return { label: 'Live off', color: 'neutral' as const }
  if (!manager.runtimeEventsConnected.value) return { label: 'Disconnected', color: 'neutral' as const }
  if (offset.value > 0) return { label: 'Live paused on older page', color: 'warning' as const }
  return { label: 'Live', color: 'success' as const }
})

function callTypeLabel(value?: string) {
  return ({ chat_completion: 'Chat Completion', completion: 'Completion', response: 'Responses', embedding: 'Embedding' } as Record<string, string>)[value || ''] || '—'
}
function requestKeyAlias(item: RequestRecord) { return item.api_key ? item.api_key.name || item.api_key.prefix || item.api_key.id || '—' : '—' }
function requestModelName(item: RequestRecord) {
  if (item.model_name) return item.model_name
  if (item.model_id) return manager.models.value.find(model => model.id === item.model_id)?.name || item.model_id
  const instance = manager.instances.value.find(candidate => candidate.id === item.instance_id)
  if (!instance) return '—'
  return manager.models.value.find(model => model.id === instance.model_id)?.name || instance.model_id || '—'
}
function isPending(item: RequestRecord) { return item.finished_at === 0 || !item.result || item.result === 'pending' }
function resultLabel(item: RequestRecord) { return isPending(item) ? 'pending' : String(item.status_code || item.result) }
function sessionCount(item: RequestRecord) { return item.session_id ? Math.max(1, item.session_total_count || 1) : 0 }
function shortID(value?: string, length = 16) { return value && value.length > length ? `${value.slice(0, length - 1)}…` : value || '—' }
function formatDuration(value?: number) {
  if (value === undefined || !Number.isFinite(value)) return '—'
  return value < 1000 ? `${Math.round(value)} ms` : `${(value / 1000).toFixed(value >= 10_000 ? 1 : 2)} s`
}
function formatRate(value?: number) { return value === undefined || !Number.isFinite(value) ? '—' : `${value.toFixed(1)} tok/s` }
function formatTime(value: number) { return value ? new Date(value).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit' }) : '—' }
function sinceForWindow() {
  const windows: Record<string, number> = { '15m': 900_000, '1h': 3_600_000, '24h': 86_400_000, '7d': 604_800_000 }
  return windows[filters.window] ? Date.now() - windows[filters.window]! : 0
}
function listPath() {
  const query = new URLSearchParams({ limit: String(pageSize), offset: String(offset.value) })
  const since = sinceForWindow()
  if (since) query.set('since', String(since))
  if (traceID.value) query.set('trace_id', traceID.value)
  for (const key of ['instance_id', 'endpoint', 'api_key_id', 'result', 'status_code', 'streaming', 'search'] as const) {
    const value = String(filters[key] ?? '').trim()
    if (value) query.set(key, value)
  }
  return `/api/v1/observability/requests?${query}`
}
async function loadRequests() {
  const generation = ++loadGeneration
  loading.value = true
  error.value = ''
  try {
    const payload = await manager.request<RequestPage>(listPath())
    if (generation !== loadGeneration) return
    requests.value = payload.items || []
    hasMore.value = Boolean(payload.has_more)
  } catch (value: any) {
    if (generation !== loadGeneration) return
    error.value = value?.data?.error || value?.message || 'Unable to load request logs'
    requests.value = []
    hasMore.value = false
  } finally {
    if (generation === loadGeneration) loading.value = false
  }
}
async function loadSessionRequests(sessionID: string) {
  const generation = ++sessionLoadGeneration
  sessionLoading.value = true
  sessionError.value = ''
  sessionRequests.value = []
  try {
    const rows: RequestRecord[] = []
    for (let page = 0; page < maxSessionPages; page++) {
      const query = new URLSearchParams({ session_id: sessionID, limit: String(sessionPageSize), offset: String(page * sessionPageSize) })
      const payload = await manager.request<RequestPage>(`/api/v1/observability/requests?${query}`)
      if (generation !== sessionLoadGeneration) return
      rows.push(...(payload.items || []))
      if (!payload.has_more) break
    }
    sessionRequests.value = rows
  } catch (value: any) {
    if (generation !== sessionLoadGeneration) return
    sessionError.value = value?.data?.error || value?.message || 'Unable to load session requests'
  } finally {
    if (generation === sessionLoadGeneration) sessionLoading.value = false
  }
}
async function applyFilters() { offset.value = 0; await loadRequests() }
async function previousPage() { offset.value = Math.max(0, offset.value - pageSize); await loadRequests() }
async function nextPage() { offset.value += pageSize; await loadRequests() }
async function clearTrace() {
  traceID.value = ''
  offset.value = 0
  const query = { ...route.query }; delete query.trace_id
  await router.replace({ path: '/logs', query })
  await loadRequests()
}
async function toggleLiveStreaming() {
  liveStreamingEnabled.value = !liveStreamingEnabled.value
  if (liveStreamingEnabled.value && routeReady.value && manager.user.value && offset.value === 0) await loadRequests()
}
async function loadRequestDetail(requestID: string): Promise<RequestDetail | null> {
  const generation = ++detailLoadGeneration
  detailLoading.value = true
  detailError.value = ''
  detail.value = null
  detailMode.value = 'pretty'
  try {
    const payload = await manager.request<RequestDetail>(`/api/v1/observability/requests/${encodeURIComponent(requestID)}`)
    if (generation !== detailLoadGeneration) return null
    detail.value = payload
    return payload
  } catch (value: any) {
    if (generation !== detailLoadGeneration) return null
    detailError.value = value?.data?.error || value?.message || 'Unable to load request details'
    return null
  } finally {
    if (generation === detailLoadGeneration) detailLoading.value = false
  }
}
async function showRequest(requestID: string, routeSessionID: string): Promise<RequestSelection> {
  if (!requestID) return { current: false, sessionID: '' }
  const selectionGeneration = ++detailSelectionGeneration
  detailOpen.value = true
  sessionSidebarOpen.value = true
  const loadedDetail = detail.value?.request_id === requestID && !detailError.value
    ? detail.value
    : await loadRequestDetail(requestID)
  if (selectionGeneration !== detailSelectionGeneration) return { current: false, sessionID: '' }

  if (!loadedDetail) {
    if (routeSessionID !== activeSessionID.value) {
      ++sessionLoadGeneration
      activeSessionID.value = ''
      sessionRequests.value = []
      sessionError.value = ''
    }
    return { current: true, sessionID: '' }
  }

  const resolvedSessionID = sessionCount(loadedDetail) > 1 ? (loadedDetail.session_id || '') : ''
  if (resolvedSessionID !== activeSessionID.value) {
    ++sessionLoadGeneration
    activeSessionID.value = resolvedSessionID
    sessionRequests.value = []
    sessionError.value = ''
    sessionSortMode.value = 'duration'
    if (resolvedSessionID) await loadSessionRequests(resolvedSessionID)
  }
  if (selectionGeneration !== detailSelectionGeneration) return { current: false, sessionID: '' }
  return { current: true, sessionID: resolvedSessionID }
}
async function openRequest(item: RequestRecord) {
  if (!item.request_id) return
  const selection = await showRequest(item.request_id, item.session_id || '')
  if (!selection.current) return
  const query: Record<string, string> = Object.fromEntries(Object.entries(route.query).flatMap(([key, value]) => typeof value === 'string' ? [[key, value]] : []))
  query.request_id = item.request_id
  if (selection.sessionID) query.session_id = selection.sessionID
  else delete query.session_id
  await router.push({ path: '/logs', query })
}
async function selectSessionRequest(item: RequestRecord) {
  if (!item.request_id || !activeSessionID.value) return
  const selection = await showRequest(item.request_id, activeSessionID.value)
  if (!selection.current) return
  const query: Record<string, string> = Object.fromEntries(Object.entries(route.query).flatMap(([key, value]) => typeof value === 'string' ? [[key, value]] : []))
  query.request_id = item.request_id
  if (selection.sessionID) query.session_id = selection.sessionID
  else delete query.session_id
  await router.replace({ path: '/logs', query })
}
async function syncDetailFromRoute() {
  if (!routeReady.value) return
  const requestID = String(route.query.request_id || '').trim()
  const routeSessionID = String(route.query.session_id || '').trim()
  if (!requestID) {
    if (detailOpen.value) detailOpen.value = false
    return
  }
  if (detailOpen.value && detail.value?.request_id === requestID && activeSessionID.value === routeSessionID && !detailError.value) return
  const selection = await showRequest(requestID, routeSessionID)
  if (!selection.current || detail.value?.request_id !== requestID || selection.sessionID === routeSessionID) return
  const query: Record<string, string> = Object.fromEntries(Object.entries(route.query).flatMap(([key, value]) => typeof value === 'string' ? [[key, value]] : []))
  query.request_id = requestID
  if (selection.sessionID) query.session_id = selection.sessionID
  else delete query.session_id
  await router.replace({ path: '/logs', query })
}
async function initializePage() {
  if (routeReady.value || !manager.initialized.value || !manager.user.value) return
  traceID.value = String(route.query.trace_id || '').trim()
  await loadRequests()
  routeReady.value = true
  await syncDetailFromRoute()
}
function parseBody(raw?: string) { if (!raw) return null; try { return JSON.parse(raw) } catch { return raw } }
function prettyBody(raw?: string) { const value = parseBody(raw); return value === null ? '' : typeof value === 'string' ? value : JSON.stringify(value, null, 2) }
const requestObject = computed<any>(() => parseBody(detail.value?.request_body))
const responseObject = computed<any>(() => parseBody(detail.value?.response_body))
const requestMessages = computed<any[]>(() => Array.isArray(requestObject.value?.messages) ? requestObject.value.messages : [])
const requestTools = computed<any[]>(() => Array.isArray(requestObject.value?.tools) ? requestObject.value.tools : [])
const responseToolCalls = computed<any[]>(() => Array.isArray(responseObject.value?.choices) ? responseObject.value.choices.flatMap((choice: any) => choice?.message?.tool_calls || []) : [])

watch(() => route.query.trace_id, async (value) => {
  const next = String(value || '').trim()
  if (next === traceID.value) return
  traceID.value = next
  offset.value = 0
  if (routeReady.value) await loadRequests()
})
watch([() => route.query.request_id, () => route.query.session_id], () => { void syncDetailFromRoute() })
watch(detailOpen, async (open) => {
  if (open) return
  ++detailSelectionGeneration
  ++detailLoadGeneration
  ++sessionLoadGeneration
  activeSessionID.value = ''
  sessionRequests.value = []
  sessionError.value = ''
  sessionSortMode.value = 'duration'
  sessionSidebarOpen.value = true
  detail.value = null
  detailError.value = ''
  detailLoading.value = false
  sessionLoading.value = false
  if (!route.query.request_id && !route.query.session_id) return
  const query = { ...route.query }; delete query.request_id; delete query.session_id
  await router.replace({ path: '/logs', query })
})
watch(
  [() => manager.initialized.value, () => manager.user.value],
  ([initialized, user]) => {
    if (!initialized || !user) {
      routeReady.value = false
      return
    }
    void initializePage()
  },
  { immediate: true }
)
watch(liveRequestFingerprint, (next, previous) => {
  if (!liveStreamingEnabled.value || !routeReady.value || !manager.user.value || offset.value !== 0 || !next || next === previous) return
  void loadRequests()
})
</script>

<template>
  <div class="space-y-5" data-testid="request-logs-page">
    <div class="flex flex-wrap items-start justify-between gap-4">
      <UPageHeader class="min-w-0 flex-1" headline="OBSERVABILITY" title="Request logs" description="Persistent inference request history with LiteLLM-compatible session grouping, correlation and performance metadata." />
      <div class="flex flex-wrap items-center justify-end gap-2">
        <UBadge data-testid="request-logs-live-state" :color="liveState.color" variant="subtle">{{ liveState.label }}</UBadge>
        <UButton data-testid="request-logs-live-toggle" color="neutral" variant="soft" :icon="liveStreamingEnabled ? 'i-lucide-pause' : 'i-lucide-play'" @click="toggleLiveStreaming">{{ liveStreamingEnabled ? 'Pause live' : 'Enable live' }}</UButton>
        <UButton color="neutral" variant="soft" :loading="loading" icon="i-lucide-refresh-cw" @click="loadRequests">Refresh</UButton>
      </div>
    </div>

    <UAlert v-if="traceID" data-testid="trace-filter" color="info" variant="subtle" title="Trace filter active" :description="`Showing requests in chronological order for ${traceID}.`">
      <template #actions><UButton size="xs" color="neutral" variant="soft" @click="clearTrace">Clear trace</UButton></template>
    </UAlert>
    <UAlert v-if="error" color="error" variant="subtle" title="Request history unavailable" :description="error" />

    <UCard data-testid="request-log-filters">
      <template #header><p class="text-xs font-extrabold tracking-[0.18em] text-dimmed">FILTERS</p><p class="mt-1 text-xs text-muted">Filters are applied server-side to retained request history. Multi-request sessions collapse to one representative row.</p></template>
      <div class="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
        <UFormField label="Time window"><USelectMenu v-model="filters.window" :items="windowItems" label-key="label" value-key="value" class="w-full" /></UFormField>
        <UFormField label="Instance"><USelectMenu v-model="filters.instance_id" :items="instanceItems" label-key="label" value-key="value" class="w-full" /></UFormField>
        <UFormField label="Endpoint"><USelectMenu v-model="filters.endpoint" :items="endpointItems" label-key="label" value-key="value" class="w-full" /></UFormField>
        <UFormField label="API key ID"><UInput v-model="filters.api_key_id" class="w-full" placeholder="Key ID" /></UFormField>
        <UFormField label="Result"><USelectMenu v-model="filters.result" :items="resultItems" label-key="label" value-key="value" class="w-full" /></UFormField>
        <UFormField label="HTTP status"><UInput v-model="filters.status_code" type="number" min="100" max="599" class="w-full" placeholder="Any status" /></UFormField>
        <UFormField label="Streaming"><USelectMenu v-model="filters.streaming" :items="streamingItems" label-key="label" value-key="value" class="w-full" /></UFormField>
        <UFormField label="Search"><UInput v-model="filters.search" class="w-full" icon="i-lucide-search" placeholder="Request ID, session, trace, model…" @keyup.enter="applyFilters" /></UFormField>
      </div>
      <div class="mt-4 flex justify-end"><UButton data-testid="apply-request-log-filters" :loading="loading" @click="applyFilters">Apply filters</UButton></div>
    </UCard>

    <UCard data-testid="request-log-table">
      <template #header><div class="flex items-center justify-between gap-3"><div><p class="text-xs font-extrabold tracking-[0.18em] text-dimmed">REQUEST HISTORY</p><p class="mt-1 text-xs text-muted">{{ traceID ? 'Oldest first for this trace.' : 'Newest first.' }} Full payloads load only for the selected request.</p></div><UBadge color="neutral" variant="soft">{{ requests.length }} rows</UBadge></div></template>
      <UEmpty v-if="!loading && !requests.length" variant="naked" title="No matching requests" description="Adjust the filters or send inference traffic through the gateway." />
      <div v-else class="overflow-x-auto">
        <UTable :data="displayRequests" :columns="columns" class="min-w-[1580px]">
          <template #started_at-cell="{ row }"><span class="whitespace-nowrap font-mono text-xs">{{ formatTime(row.original.started_at) }}</span></template>
          <template #result-cell="{ row }"><UBadge :color="isPending(row.original) ? 'neutral' : row.original.result === 'success' ? 'success' : 'error'" variant="subtle" size="sm">{{ resultLabel(row.original) }}</UBadge></template>
          <template #model_name-cell="{ row }"><span class="text-xs font-semibold">{{ requestModelName(row.original) }}</span></template>
          <template #instance_id-cell="{ row }"><span class="font-mono text-xs text-muted">{{ row.original.instance_id || '—' }}</span></template>
          <template #api_key-cell="{ row }"><span class="text-xs text-muted">{{ requestKeyAlias(row.original) }}</span></template>
          <template #duration_ms-cell="{ row }"><span class="whitespace-nowrap font-mono text-xs">{{ formatDuration(row.original.duration_ms) }}</span></template>
          <template #ttft_ms-cell="{ row }"><span class="whitespace-nowrap font-mono text-xs text-muted">{{ formatDuration(row.original.ttft_ms) }}</span></template>
          <template #total_tokens-cell="{ row }"><span class="font-mono text-xs">{{ row.original.total_tokens || '—' }}</span></template>
          <template #prompt_tokens_per_second-cell="{ row }"><span class="whitespace-nowrap font-mono text-xs text-muted">{{ formatRate(row.original.prompt_tokens_per_second) }}</span></template>
          <template #generation_tokens_per_second-cell="{ row }"><span class="whitespace-nowrap font-mono text-xs text-muted">{{ formatRate(row.original.generation_tokens_per_second) }}</span></template>
          <template #call_type-cell="{ row }"><span class="text-xs text-muted">{{ callTypeLabel(row.original.call_type) }}</span></template>
          <template #request_id-cell="{ row }"><UButton v-if="row.original.request_id" data-testid="request-detail-trigger" color="neutral" variant="link" size="xs" class="font-mono" @click="openRequest(row.original)">{{ shortID(row.original.request_id, 20) }}</UButton><span v-else>—</span></template>
          <template #session_id-cell="{ row }"><UButton v-if="sessionCount(row.original) > 1" color="neutral" variant="soft" size="xs" @click="openRequest(row.original)">{{ sessionCount(row.original) }} requests</UButton><span v-else-if="row.original.session_id" class="font-mono text-xs text-muted">{{ shortID(row.original.session_id) }}</span><span v-else>—</span></template>
          <template #endpoint-cell="{ row }"><span class="font-mono text-xs text-muted">{{ row.original.endpoint }}</span></template>
        </UTable>
      </div>
      <template #footer><div class="flex items-center justify-between"><span class="text-xs text-muted">Rows {{ requests.length ? offset + 1 : 0 }}–{{ offset + requests.length }}</span><div class="flex gap-2"><UButton color="neutral" variant="soft" size="sm" :disabled="offset === 0 || loading" @click="previousPage">Previous</UButton><UButton color="neutral" variant="soft" size="sm" :disabled="!hasMore || loading" @click="nextPage">Next</UButton></div></div></template>
    </UCard>

    <USlideover v-model:open="detailOpen" side="right" title="Request Details" data-testid="request-detail-slideover" :ui="{ content: 'sm:max-w-[min(92vw,1400px)]' }">
      <template #body>
        <div class="flex min-h-[76vh] min-w-0">
          <aside v-if="(detail || sessionRequests.length) && sessionSidebarOpen" data-testid="request-sidebar" class="w-72 shrink-0 border-r border-default pr-4">
            <div class="mb-4 flex items-start justify-between gap-2">
              <div class="min-w-0">
                <p class="text-sm font-semibold">Request</p>
                <p class="mt-1 text-xs text-muted">{{ sidebarTotalCount }} {{ sidebarTotalCount === 1 ? 'request' : 'requests' }} · {{ formatDuration(sidebarDuration) }}</p>
                <p v-if="activeSessionID" class="mt-1 truncate font-mono text-[10px] text-dimmed" :title="activeSessionID">Session ID: {{ activeSessionID }}</p>
              </div>
              <UButton color="neutral" variant="ghost" size="xs" icon="i-lucide-chevron-right" aria-label="Collapse session sidebar" @click="sessionSidebarOpen = false" />
            </div>
            <USelectMenu v-if="activeSessionID && sessionRequests.length > 1" v-model="sessionSortMode" :items="sessionSortItems" label-key="label" value-key="value" class="mb-3 w-full" />
            <UAlert v-if="sessionError" color="error" variant="subtle" title="Session unavailable" :description="sessionError" class="mb-3" />
            <UAlert v-if="sessionTruncated" color="warning" variant="subtle" title="Session truncated" :description="`Showing ${sessionRequests.length} of ${sessionTotalCount} retained requests.`" class="mb-3" />
            <USkeleton v-if="activeSessionID && sessionLoading" class="h-32 w-full" />
            <UScrollArea v-else class="h-[calc(100vh-13rem)] pr-1">
              <div class="space-y-1">
                <UButton
                  v-for="item in sidebarRequests"
                  :key="item.request_id || item.id"
                  color="neutral"
                  :variant="detail?.request_id === item.request_id ? 'soft' : 'ghost'"
                  class="h-auto w-full justify-start px-2 py-2 text-left"
                  @click="selectSessionRequest(item)"
                >
                  <div class="min-w-0 flex-1">
                    <div class="flex items-center gap-2">
                      <span class="truncate text-xs font-medium">{{ item.call_type || 'request' }}</span>
                      <UBadge :color="isPending(item) ? 'neutral' : item.result === 'success' ? 'success' : 'error'" variant="subtle" size="sm" class="ml-auto">{{ resultLabel(item) }}</UBadge>
                    </div>
                    <p class="mt-1 truncate font-mono text-[10px] text-dimmed">{{ shortID(item.request_id, 26) }}</p>
                    <p class="mt-1 truncate text-[10px] text-muted">{{ requestModelName(item) }}</p>
                    <p class="mt-1 text-[10px] text-dimmed">{{ formatDuration(item.duration_ms) }} · {{ item.total_tokens || 0 }} tok · {{ formatTime(item.started_at) }}</p>
                  </div>
                </UButton>
              </div>
            </UScrollArea>
          </aside>

          <div class="min-w-0 flex-1" :class="(detail || sessionRequests.length) && sessionSidebarOpen ? 'pl-6' : ''">
            <div v-if="(detail || sessionRequests.length) && !sessionSidebarOpen" class="mb-3"><UButton color="neutral" variant="soft" size="xs" icon="i-lucide-chevron-left" @click="sessionSidebarOpen = true">{{ activeSessionID ? 'Show session requests' : 'Show requests' }}</UButton></div>
            <div class="space-y-6">
              <USkeleton v-if="detailLoading" class="h-40 w-full" />
              <UAlert v-else-if="detailError" color="error" variant="subtle" title="Request details unavailable" :description="detailError" />
              <template v-else-if="detail">
                <section>
                  <div class="mb-4 flex items-center justify-between gap-3">
                    <h3 class="text-lg font-semibold">Request Overview</h3>
                    <UBadge :color="isPending(detail) ? 'neutral' : detail.result === 'success' ? 'success' : 'error'" variant="subtle">{{ resultLabel(detail) }}</UBadge>
                  </div>
                  <p v-if="isPending(detail)" class="mb-4 text-xs text-muted">Request pending · The request is still in progress.</p>
                  <p v-else-if="detail.result === 'error'" class="mb-4 text-xs text-error">Request failed · {{ detail.error || 'The request failed.' }}</p>
                  <dl class="space-y-3 text-sm">
                    <div class="grid grid-cols-[9rem_minmax(0,1fr)] gap-4"><dt class="text-muted">Model</dt><dd>{{ requestModelName(detail) }}</dd></div>
                    <div class="grid grid-cols-[9rem_minmax(0,1fr)] gap-4"><dt class="text-muted">Instance ID</dt><dd class="break-all font-mono text-xs text-muted">{{ detail.instance_id || 'Unresolved' }}</dd></div>
                    <div class="grid grid-cols-[9rem_minmax(0,1fr)] gap-4"><dt class="text-muted">Call Type</dt><dd>{{ detail.call_type || '—' }}</dd></div>
                    <div class="grid grid-cols-[9rem_minmax(0,1fr)] gap-4"><dt class="text-muted">Endpoint</dt><dd class="break-all font-mono text-xs text-muted">{{ detail.endpoint }}</dd></div>
                    <div class="grid grid-cols-[9rem_minmax(0,1fr)] gap-4"><dt class="text-muted">Streaming</dt><dd>{{ detail.streaming ? 'True' : 'False' }}</dd></div>
                    <div class="grid grid-cols-[9rem_minmax(0,1fr)] gap-4"><dt class="text-muted">Key Alias</dt><dd class="text-muted">{{ requestKeyAlias(detail) }}</dd></div>
                    <div class="grid grid-cols-[9rem_minmax(0,1fr)] gap-4"><dt class="text-muted">Start Time</dt><dd>{{ formatTime(detail.started_at) }}</dd></div>
                    <div class="grid grid-cols-[9rem_minmax(0,1fr)] gap-4"><dt class="text-muted">End Time</dt><dd>{{ formatTime(detail.finished_at) }}</dd></div>
                    <div class="grid grid-cols-[9rem_minmax(0,1fr)] gap-4"><dt class="text-muted">Model ID</dt><dd class="break-all font-mono text-xs text-muted">{{ detail.model_id || '—' }}</dd></div>
                    <div class="grid grid-cols-[9rem_minmax(0,1fr)] gap-4"><dt class="text-muted">Request ID</dt><dd class="break-all font-mono text-xs text-muted">{{ detail.request_id }}</dd></div>
                    <div class="grid grid-cols-[9rem_minmax(0,1fr)] gap-4"><dt class="text-muted">Session ID</dt><dd class="break-all font-mono text-xs text-muted">{{ detail.session_id || '—' }}</dd></div>
                    <div class="grid grid-cols-[9rem_minmax(0,1fr)] gap-4"><dt class="text-muted">Trace ID</dt><dd class="break-all font-mono text-xs text-muted">{{ detail.trace_id || '—' }}</dd></div>
                  </dl>
                </section>

                <USeparator />

                <section data-testid="request-detail-metrics">
                  <h3 class="mb-4 text-base font-semibold">Metrics</h3>
                  <dl class="space-y-3 text-sm">
                    <div class="grid grid-cols-[9rem_minmax(0,1fr)] gap-4"><dt class="text-muted">Total Latency</dt><dd class="font-mono">{{ formatDuration(detail.duration_ms) }}</dd></div>
                    <div class="grid grid-cols-[9rem_minmax(0,1fr)] gap-4"><dt class="text-muted">TTFT</dt><dd class="font-mono">{{ formatDuration(detail.ttft_ms) }}</dd></div>
                    <div class="grid grid-cols-[9rem_minmax(0,1fr)] gap-4"><dt class="text-muted">Input Tokens</dt><dd class="font-mono">{{ detail.prompt_tokens }}</dd></div>
                    <div class="grid grid-cols-[9rem_minmax(0,1fr)] gap-4"><dt class="text-muted">Output Tokens</dt><dd class="font-mono">{{ detail.generated_tokens }}</dd></div>
                    <div class="grid grid-cols-[9rem_minmax(0,1fr)] gap-4"><dt class="text-muted">Total Tokens</dt><dd class="font-mono">{{ detail.total_tokens }}</dd></div>
                    <div class="grid grid-cols-[9rem_minmax(0,1fr)] gap-4"><dt class="text-muted">Prompt Processing</dt><dd class="font-mono">{{ formatRate(detail.prompt_tokens_per_second) }}</dd></div>
                    <div class="grid grid-cols-[9rem_minmax(0,1fr)] gap-4"><dt class="text-muted">Generation Speed</dt><dd class="font-mono">{{ formatRate(detail.generation_tokens_per_second) }}</dd></div>
                    <div class="grid grid-cols-[9rem_minmax(0,1fr)] gap-4"><dt class="text-muted">Queue Time</dt><dd class="font-mono">{{ formatDuration(detail.queue_duration_ms) }}</dd></div>
                    <div class="grid grid-cols-[9rem_minmax(0,1fr)] gap-4"><dt class="text-muted">Load Time</dt><dd class="font-mono">{{ formatDuration(detail.load_duration_ms) }}</dd></div>
                  </dl>
                </section>

                <USeparator />

                <section class="space-y-3">
                  <div class="flex items-center justify-between"><h3 class="text-base font-semibold">Request & response content</h3><div v-if="detail.request_body || detail.response_body" class="flex gap-1"><UButton size="xs" :variant="detailMode === 'pretty' ? 'solid' : 'soft'" @click="detailMode = 'pretty'">Pretty</UButton><UButton size="xs" :variant="detailMode === 'json' ? 'solid' : 'soft'" @click="detailMode = 'json'">JSON</UButton></div></div>
                  <UAlert v-if="!detail.request_body && !detail.response_body" color="neutral" variant="subtle" title="Content not recorded" description="This request used metadata-only logging, so request and response payloads were not retained." />
                  <template v-else-if="detailMode === 'pretty'">
                    <div v-if="requestMessages.length"><p class="text-xs font-semibold uppercase text-dimmed">Messages</p><div v-for="(message, index) in requestMessages" :key="index" class="mt-2"><UBadge color="neutral" variant="soft" size="sm">{{ message.role || 'message' }}</UBadge><pre class="mt-1 whitespace-pre-wrap text-xs text-muted">{{ typeof message.content === 'string' ? message.content : JSON.stringify(message.content, null, 2) }}</pre></div></div>
                    <div v-if="requestTools.length"><p class="text-xs font-semibold uppercase text-dimmed">Tools</p><pre class="whitespace-pre-wrap text-xs text-muted">{{ JSON.stringify(requestTools, null, 2) }}</pre></div>
                    <div v-if="responseToolCalls.length"><p class="text-xs font-semibold uppercase text-dimmed">Response tool calls</p><pre class="whitespace-pre-wrap text-xs text-muted">{{ JSON.stringify(responseToolCalls, null, 2) }}</pre></div>
                    <div v-if="!requestMessages.length"><p class="text-xs font-semibold uppercase text-dimmed">Request</p><pre class="whitespace-pre-wrap text-xs text-muted">{{ prettyBody(detail.request_body) || '—' }}</pre></div>
                    <div><p class="text-xs font-semibold uppercase text-dimmed">Response</p><pre class="whitespace-pre-wrap text-xs text-muted">{{ prettyBody(detail.response_body) || '—' }}</pre></div>
                  </template>
                  <template v-else><div><p class="text-xs font-semibold uppercase text-dimmed">Request JSON</p><pre class="whitespace-pre-wrap text-xs text-muted">{{ prettyBody(detail.request_body) || '—' }}</pre></div><div><p class="text-xs font-semibold uppercase text-dimmed">Response JSON</p><pre class="whitespace-pre-wrap text-xs text-muted">{{ prettyBody(detail.response_body) || '—' }}</pre></div></template>
                </section>

                <USeparator />

                <section>
                  <h3 class="mb-4 text-base font-semibold">Client Metadata</h3>
                  <dl class="space-y-3 text-sm">
                    <div class="grid grid-cols-[9rem_minmax(0,1fr)] gap-4"><dt class="text-muted">Client IP</dt><dd class="font-mono text-xs text-muted">{{ detail.client_ip || '—' }}</dd></div>
                    <div class="grid grid-cols-[9rem_minmax(0,1fr)] gap-4"><dt class="text-muted">User-Agent</dt><dd class="break-words text-xs text-muted">{{ detail.user_agent || '—' }}</dd></div>
                    <div class="grid grid-cols-[9rem_minmax(0,1fr)] gap-4"><dt class="text-muted">Autoloaded</dt><dd>{{ detail.autoloaded ? 'True' : 'False' }}</dd></div>
                  </dl>
                </section>
              </template>
            </div>
          </div>
        </div>
      </template>
    </USlideover>
  </div>
</template>
