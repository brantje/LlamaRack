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
  model_slug?: string
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
const filtersOpen = ref(true)
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
const instanceItems = computed(() => [{ label: 'All Instances', value: '' }, ...manager.instances.value.map(item => ({ label: `${item.name} (${item.slug})`, value: item.id }))])
const columns: TableColumn<RequestRecord>[] = [
  { accessorKey: 'started_at', header: 'Time' }, { accessorKey: 'result', header: 'Status' },
  { accessorKey: 'model_name', header: 'Model' }, { accessorKey: 'model_slug', header: 'Model slug' },
  { accessorKey: 'api_key', header: 'Key alias' }, { accessorKey: 'duration_ms', header: 'Duration' },
  { accessorKey: 'ttft_ms', header: 'TTFT' }, { accessorKey: 'total_tokens', header: 'Tokens' },
  { accessorKey: 'prompt_tokens_per_second', header: 'Prompt tok/s' }, { accessorKey: 'generation_tokens_per_second', header: 'Gen tok/s' },
  { accessorKey: 'call_type', header: 'Call Type' }, { accessorKey: 'request_id', header: 'Request ID' },
  { accessorKey: 'session_id', header: 'Session' }, { accessorKey: 'endpoint', header: 'Endpoint' }
]
const displayRequests = computed(() => requests.value)
const activeFilterCount = computed(() => [
  filters.window !== '1h', filters.instance_id, filters.endpoint, filters.api_key_id,
  filters.result, filters.status_code, filters.streaming, filters.search
].filter(Boolean).length)
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
const liveAction = computed(() => {
  if (!liveStreamingEnabled.value) return { label: 'Enable live', icon: 'i-lucide-play' }
  if (!manager.runtimeEventsConnected.value) return { label: 'Reconnect', icon: 'i-lucide-refresh-cw' }
  if (offset.value > 0) return { label: 'Return to live', icon: 'i-lucide-arrow-up' }
  return { label: 'Pause live', icon: 'i-lucide-pause' }
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
function requestModelSlug(item: RequestRecord) {
  if (item.model_slug) return item.model_slug
  return manager.instances.value.find(candidate => candidate.id === item.instance_id)?.slug || '—'
}
function currentInstanceTarget(item: RequestRecord) {
  const instance = manager.instances.value.find(candidate => candidate.id === item.instance_id)
  return instance ? `/instances/${encodeURIComponent(instance.slug)}/detail` : ''
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
async function applyFilters() { offset.value = 0; await loadRequests(); filtersOpen.value = false }
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
async function handleLiveAction() {
  if (!liveStreamingEnabled.value) {
    await toggleLiveStreaming()
    return
  }
  if (!manager.runtimeEventsConnected.value) {
    await manager.connectRuntimeEvents()
    return
  }
  if (offset.value > 0) {
    offset.value = 0
    await loadRequests()
    return
  }
  await toggleLiveStreaming()
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
function onRequestRowSelect(_event: Event, row: { original: RequestRecord }) {
  return openRequest(row.original)
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
      <UPageHeader class="min-w-0 flex-1" headline="OBSERVABILITY" title="Request logs" description="Persistent inference request history with request/session correlation and performance metadata." />
      <div class="flex w-full flex-wrap items-center justify-start gap-2 sm:w-auto sm:justify-end">
        <StatusTag data-testid="request-logs-live-state" :variant="liveState.label === 'Live' ? 'ready' : liveState.label.includes('paused') ? 'pending' : 'neutral'">{{ liveState.label }}</StatusTag>
        <AppButton data-testid="request-logs-live-toggle" intent="secondary" :icon="liveAction.icon" @click="handleLiveAction">{{ liveAction.label }}</AppButton>
        <AppButton intent="secondary" :loading="loading" icon="i-lucide-refresh-cw" @click="loadRequests">Refresh</AppButton>
      </div>
    </div>

    <div v-if="traceID" data-testid="trace-filter" class="flex flex-wrap items-center justify-between gap-3 border-y border-[var(--color-divider)] bg-[var(--neutral-100)] px-4 py-3">
      <div class="min-w-0">
        <p class="text-[length:var(--font-size-kicker)] font-semibold uppercase tracking-[.1em] text-[var(--neutral-700)]">TRACE FILTER ACTIVE</p>
        <p class="mt-1 break-all font-mono text-xs text-[var(--color-text)]">{{ traceID }}</p>
        <p class="mt-1 text-xs text-[var(--neutral-800)]">Showing requests in chronological order for this trace.</p>
      </div>
      <AppButton intent="secondary" size="xs" @click="clearTrace">Clear trace</AppButton>
    </div>

    <Frame v-if="error" class="p-3" data-testid="request-log-error">
      <div class="flex flex-wrap items-start gap-2">
        <StatusTag variant="failed">Request history unavailable</StatusTag>
        <p class="min-w-0 flex-1 text-xs leading-5 text-[var(--neutral-800)]">{{ error }}</p>
      </div>
    </Frame>

    <Frame class="p-4" data-testid="request-log-filters">
      <div class="mb-3 flex flex-wrap items-start justify-between gap-3">
        <div>
          <p class="text-[length:var(--font-size-kicker)] font-semibold uppercase tracking-[.1em] text-[var(--neutral-700)]">FILTERS</p>
          <p class="mt-1 text-xs text-[var(--neutral-800)]">Filters are applied server-side to retained request history. Sessions are grouped only in the request inspector.</p>
        </div>
        <StatusTag variant="neutral" data-testid="request-log-active-filter-count">{{ activeFilterCount }} active</StatusTag>
      </div>
      <UCollapsible v-model:open="filtersOpen" data-testid="request-log-filter-collapsible">
        <AppButton
          data-testid="request-log-filters-toggle"
          intent="secondary"
          size="sm"
          :trailing-icon="filtersOpen ? 'i-lucide-chevron-up' : 'i-lucide-chevron-down'"
        >{{ filtersOpen ? 'Hide filters' : 'Edit filters' }}</AppButton>
        <template #content>
          <div class="mt-4 grid gap-3 md:grid-cols-2 xl:grid-cols-4">
            <UFormField label="Time window"><USelectMenu v-model="filters.window" :items="windowItems" label-key="label" value-key="value" class="w-full" /></UFormField>
            <UFormField label="Instance"><USelectMenu v-model="filters.instance_id" :items="instanceItems" label-key="label" value-key="value" class="w-full" /></UFormField>
            <UFormField label="Endpoint"><USelectMenu v-model="filters.endpoint" :items="endpointItems" label-key="label" value-key="value" class="w-full" /></UFormField>
            <UFormField label="API key ID"><UInput v-model="filters.api_key_id" class="w-full font-mono" placeholder="Key ID" /></UFormField>
            <UFormField label="Result"><USelectMenu v-model="filters.result" :items="resultItems" label-key="label" value-key="value" class="w-full" /></UFormField>
            <UFormField label="HTTP status"><UInput v-model="filters.status_code" type="number" min="100" max="599" class="w-full font-mono tabular-nums" placeholder="Any status" /></UFormField>
            <UFormField label="Streaming"><USelectMenu v-model="filters.streaming" :items="streamingItems" label-key="label" value-key="value" class="w-full" /></UFormField>
            <UFormField label="Search"><UInput v-model="filters.search" class="w-full font-mono" icon="i-lucide-search" placeholder="Request ID, session, trace, model…" @keyup.enter="applyFilters" /></UFormField>
          </div>
          <div class="mt-4 flex justify-end"><AppButton data-testid="apply-request-log-filters" intent="primary" :loading="loading" @click="applyFilters">Apply filters</AppButton></div>
        </template>
      </UCollapsible>
    </Frame>

    <Frame data-testid="request-log-table">
      <div class="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--color-divider)] px-4 py-3">
        <div>
          <p class="text-[length:var(--font-size-kicker)] font-semibold uppercase tracking-[.1em] text-[var(--neutral-700)]">REQUEST HISTORY</p>
          <p class="mt-1 text-xs text-[var(--neutral-800)]">{{ traceID ? 'Oldest first for this trace.' : 'Newest first.' }} Full payloads load only for the selected request.</p>
        </div>
        <StatusTag variant="neutral"><span class="font-mono tabular-nums">{{ requests.length }} rows</span></StatusTag>
      </div>

      <div v-if="!loading && !requests.length" class="p-8 text-center">
        <p class="font-heading text-lg font-semibold text-[var(--color-text)]">No matching requests</p>
        <p class="mt-2 text-sm text-[var(--neutral-800)]">Adjust the filters or send inference traffic through the gateway.</p>
      </div>
      <div v-else class="overflow-x-auto" role="region" aria-label="Request history table. Scroll horizontally to view all columns on small screens." tabindex="0">
        <UTable :data="displayRequests" :columns="columns" class="min-w-[1580px]" :ui="{ tbody: '[&>tr]:cursor-pointer' }" @select="onRequestRowSelect">
          <template #started_at-cell="{ row }"><span class="whitespace-nowrap font-mono text-xs tabular-nums">{{ formatTime(row.original.started_at) }}</span></template>
          <template #result-cell="{ row }"><StatusTag :variant="isPending(row.original) ? 'pending' : row.original.result === 'success' ? 'ready' : 'failed'">{{ resultLabel(row.original) }}</StatusTag></template>
          <template #model_name-cell="{ row }"><span class="text-xs font-semibold">{{ requestModelName(row.original) }}</span></template>
          <template #model_slug-cell="{ row }"><span class="font-mono text-xs text-[var(--neutral-800)]">{{ requestModelSlug(row.original) }}</span></template>
          <template #api_key-cell="{ row }"><span class="text-xs text-[var(--neutral-800)]">{{ requestKeyAlias(row.original) }}</span></template>
          <template #duration_ms-cell="{ row }"><span class="whitespace-nowrap font-mono text-xs tabular-nums">{{ formatDuration(row.original.duration_ms) }}</span></template>
          <template #ttft_ms-cell="{ row }"><span class="whitespace-nowrap font-mono text-xs tabular-nums text-[var(--neutral-800)]">{{ formatDuration(row.original.ttft_ms) }}</span></template>
          <template #total_tokens-cell="{ row }"><span class="font-mono text-xs tabular-nums">{{ row.original.total_tokens || '—' }}</span></template>
          <template #prompt_tokens_per_second-cell="{ row }"><span class="whitespace-nowrap font-mono text-xs tabular-nums text-[var(--neutral-800)]">{{ formatRate(row.original.prompt_tokens_per_second) }}</span></template>
          <template #generation_tokens_per_second-cell="{ row }"><span class="whitespace-nowrap font-mono text-xs tabular-nums text-[var(--neutral-800)]">{{ formatRate(row.original.generation_tokens_per_second) }}</span></template>
          <template #call_type-cell="{ row }"><span class="text-xs text-[var(--neutral-800)]">{{ callTypeLabel(row.original.call_type) }}</span></template>
          <template #request_id-cell="{ row }"><AppButton v-if="row.original.request_id" data-testid="request-detail-trigger" intent="ghost" size="xs" class="font-mono" @click.stop="openRequest(row.original)">{{ shortID(row.original.request_id, 20) }}</AppButton><span v-else>—</span></template>
          <template #session_id-cell="{ row }"><span v-if="row.original.session_id" class="font-mono text-xs text-[var(--neutral-800)]">{{ shortID(row.original.session_id) }}</span><span v-else>—</span></template>
          <template #endpoint-cell="{ row }"><span class="font-mono text-xs text-[var(--neutral-800)]">{{ row.original.endpoint }}</span></template>
        </UTable>
      </div>

      <div class="flex items-center justify-between border-t border-[var(--color-divider)] px-4 py-3">
        <span class="font-mono text-xs tabular-nums text-[var(--neutral-800)]">Rows {{ requests.length ? offset + 1 : 0 }}–{{ offset + requests.length }}</span>
        <div class="flex gap-2"><AppButton intent="secondary" size="sm" :disabled="offset === 0 || loading" @click="previousPage">Previous</AppButton><AppButton intent="secondary" size="sm" :disabled="!hasMore || loading" @click="nextPage">Next</AppButton></div>
      </div>
    </Frame>

    <USlideover v-model:open="detailOpen" side="right" title="Request Details" data-testid="request-detail-slideover" :ui="{ content: 'sm:max-w-[min(92vw,1400px)] rounded-none' }">
      <template #body>
        <div class="flex min-h-[76vh] min-w-0">
          <aside v-if="(detail || sessionRequests.length) && sessionSidebarOpen" data-testid="request-sidebar" class="w-72 shrink-0 border-r border-[var(--color-divider)] pr-4">
            <div class="mb-4 flex items-start justify-between gap-2">
              <div class="min-w-0">
                <p class="text-[length:var(--font-size-kicker)] font-semibold uppercase tracking-[.1em] text-[var(--neutral-700)]">SESSION / REQUEST</p>
                <p class="mt-1 text-sm font-semibold text-[var(--color-text)]">{{ sidebarTotalCount }} {{ sidebarTotalCount === 1 ? 'request' : 'requests' }}</p>
                <p class="mt-1 font-mono text-xs tabular-nums text-[var(--neutral-800)]">{{ formatDuration(sidebarDuration) }}</p>
                <p v-if="activeSessionID" class="mt-2 break-all font-mono text-[length:var(--font-size-kicker)] text-[var(--neutral-700)]" :title="activeSessionID">{{ activeSessionID }}</p>
              </div>
              <AppButton intent="ghost" size="xs" icon="i-lucide-chevron-right" aria-label="Collapse session sidebar" @click="sessionSidebarOpen = false" />
            </div>
            <USelectMenu v-if="activeSessionID && sessionRequests.length > 1" v-model="sessionSortMode" :items="sessionSortItems" label-key="label" value-key="value" class="mb-3 w-full" />
            <div v-if="sessionError" class="mb-3 flex items-start gap-2 border border-[var(--color-divider)] p-3 text-xs" data-testid="request-session-error"><StatusTag variant="failed">Session unavailable</StatusTag><p class="min-w-0 flex-1 leading-5 text-[var(--neutral-800)]">{{ sessionError }}</p></div>
            <div v-if="sessionTruncated" class="mb-3 flex items-start gap-2 border border-[var(--color-divider)] p-3 text-xs" data-testid="request-session-truncated"><StatusTag variant="pending">Session truncated</StatusTag><p class="min-w-0 flex-1 leading-5 text-[var(--neutral-800)]">Showing {{ sessionRequests.length }} of {{ sessionTotalCount }} retained requests.</p></div>
            <USkeleton v-if="activeSessionID && sessionLoading" class="h-32 w-full" />
            <UScrollArea v-else class="h-[calc(100vh-13rem)] pr-1">
              <div class="divide-y divide-[var(--color-divider)] border-y border-[var(--color-divider)]">
                <button
                  v-for="item in sidebarRequests"
                  :key="item.request_id || item.id"
                  type="button"
                  class="block w-full px-2 py-3 text-left transition-colors"
                  :class="detail?.request_id === item.request_id ? 'bg-[var(--accent-100)]' : 'hover:bg-[var(--neutral-100)]'"
                  @click="selectSessionRequest(item)"
                >
                  <div class="flex items-center gap-2">
                    <span class="min-w-0 flex-1 truncate text-xs font-medium">{{ item.call_type || 'request' }}</span>
                    <StatusTag :variant="isPending(item) ? 'pending' : item.result === 'success' ? 'ready' : 'failed'">{{ resultLabel(item) }}</StatusTag>
                  </div>
                  <p class="mt-1 truncate font-mono text-[length:var(--font-size-kicker)] text-[var(--neutral-700)]">{{ shortID(item.request_id, 26) }}</p>
                  <p class="mt-1 truncate text-[length:var(--font-size-kicker)] text-[var(--neutral-800)]">{{ requestModelName(item) }} · <span class="font-mono">{{ requestModelSlug(item) }}</span></p>
                  <p class="mt-1 font-mono text-[length:var(--font-size-kicker)] tabular-nums text-[var(--neutral-700)]">{{ formatDuration(item.duration_ms) }} · {{ item.total_tokens || 0 }} tok · {{ formatTime(item.started_at) }}</p>
                </button>
              </div>
            </UScrollArea>
          </aside>

          <div class="min-w-0 flex-1" :class="(detail || sessionRequests.length) && sessionSidebarOpen ? 'pl-6' : ''">
            <div v-if="(detail || sessionRequests.length) && !sessionSidebarOpen" class="mb-3"><AppButton intent="secondary" size="xs" icon="i-lucide-chevron-left" @click="sessionSidebarOpen = true">{{ activeSessionID ? 'Show session requests' : 'Show requests' }}</AppButton></div>
            <div class="space-y-0">
              <USkeleton v-if="detailLoading" class="h-40 w-full" />
              <div v-else-if="detailError" class="flex flex-wrap items-start gap-2 border-y border-[var(--color-divider)] px-4 py-3" data-testid="request-detail-error"><StatusTag variant="failed">Request details unavailable</StatusTag><p class="min-w-0 flex-1 text-xs leading-5 text-[var(--neutral-800)]">{{ detailError }}</p></div>
              <template v-else-if="detail">
                <div v-if="detail.result === 'error'" data-testid="request-failure-banner" class="flex flex-wrap items-start gap-2 border-y border-[var(--color-divider)] px-4 py-3">
                  <StatusTag variant="failed">Request Failed</StatusTag>
                  <p class="min-w-0 flex-1 text-xs leading-5 text-[var(--neutral-800)]">{{ detail.error || `HTTP ${detail.status_code || 'error'}` }}</p>
                </div>

                <section data-testid="request-detail-overview" class="border-b border-[var(--color-divider)] py-5">
                  <div class="mb-4 flex items-center justify-between gap-3">
                    <div><p class="text-[length:var(--font-size-kicker)] font-semibold uppercase tracking-[.1em] text-[var(--neutral-700)]">REQUEST</p><h3 class="mt-1 font-heading text-lg font-semibold text-[var(--color-text)]">Request Details</h3></div>
                    <StatusTag :variant="isPending(detail) ? 'pending' : detail.result === 'success' ? 'ready' : 'failed'">{{ resultLabel(detail) }}</StatusTag>
                  </div>
                  <p v-if="isPending(detail)" class="mb-3 text-xs text-[var(--neutral-800)]">The request is still in progress.</p>
                  <div data-testid="request-detail-overview-grid" class="grid gap-x-12 gap-y-4 lg:grid-cols-2">
                    <dl class="grid min-w-0 grid-cols-[max-content_minmax(0,1fr)] gap-x-3 gap-y-2.5 text-sm">
                      <dt class="text-[var(--neutral-700)]">Model</dt><dd class="min-w-0 break-words">{{ requestModelName(detail) }}</dd>
                      <dt class="text-[var(--neutral-700)]">Model slug</dt><dd class="min-w-0 break-all font-mono text-xs">{{ requestModelSlug(detail) }}</dd>
                      <dt class="text-[var(--neutral-700)]">Call Type</dt><dd>{{ detail.call_type || '—' }}</dd>
                      <dt class="text-[var(--neutral-700)]">Endpoint</dt><dd class="min-w-0 break-all font-mono text-xs">{{ detail.endpoint }}</dd>
                      <dt class="text-[var(--neutral-700)]">Streaming</dt><dd>{{ detail.streaming ? 'True' : 'False' }}</dd>
                      <dt class="text-[var(--neutral-700)]">Key Alias</dt><dd>{{ requestKeyAlias(detail) }}</dd>
                      <dt class="text-[var(--neutral-700)]">Request ID</dt><dd class="min-w-0 break-all font-mono text-xs">{{ detail.request_id }}</dd>
                    </dl>
                    <dl class="grid min-w-0 grid-cols-[max-content_minmax(0,1fr)] gap-x-3 gap-y-2.5 text-sm">
                      <dt class="text-[var(--neutral-700)]">Instance ID</dt><dd class="min-w-0 break-all font-mono text-xs"><NuxtLink v-if="currentInstanceTarget(detail)" :to="currentInstanceTarget(detail)" class="hover:underline">{{ detail.instance_id }}</NuxtLink><span v-else>{{ detail.instance_id || 'Unresolved' }}</span></dd>
                      <dt class="text-[var(--neutral-700)]">Model ID</dt><dd class="min-w-0 break-all font-mono text-xs">{{ detail.model_id || '—' }}</dd>
                      <dt class="text-[var(--neutral-700)]">Session ID</dt><dd class="min-w-0 break-all font-mono text-xs">{{ detail.session_id || '—' }}</dd>
                      <dt class="text-[var(--neutral-700)]">Trace ID</dt><dd class="min-w-0 break-all font-mono text-xs">{{ detail.trace_id || '—' }}</dd>
                    </dl>
                  </div>
                </section>

                <section data-testid="request-detail-metrics" class="border-b border-[var(--color-divider)] py-5">
                  <div class="mb-4"><p class="text-[length:var(--font-size-kicker)] font-semibold uppercase tracking-[.1em] text-[var(--neutral-700)]">PERFORMANCE</p><h3 class="mt-1 font-heading text-lg font-semibold text-[var(--color-text)]">Metrics</h3></div>
                  <div data-testid="request-detail-metrics-grid" class="grid gap-x-12 gap-y-4 lg:grid-cols-2">
                    <dl class="grid min-w-0 grid-cols-[max-content_minmax(0,1fr)] gap-x-3 gap-y-2.5 text-sm">
                      <dt class="text-[var(--neutral-700)]">Tokens</dt><dd><span class="font-mono tabular-nums">{{ detail.total_tokens }}</span> <span class="text-[var(--neutral-800)]">({{ detail.prompt_tokens }} prompt + {{ detail.generated_tokens }} completion)</span></dd>
                      <dt class="text-[var(--neutral-700)]">Duration</dt><dd class="font-mono tabular-nums">{{ formatDuration(detail.duration_ms) }}</dd>
                      <dt class="text-[var(--neutral-700)]">Prompt Processing</dt><dd class="font-mono tabular-nums">{{ formatRate(detail.prompt_tokens_per_second) }}</dd>
                      <dt class="text-[var(--neutral-700)]">Queue Time</dt><dd class="font-mono tabular-nums">{{ formatDuration(detail.queue_duration_ms) }}</dd>
                      <dt class="text-[var(--neutral-700)]">Start Time</dt><dd class="font-mono tabular-nums">{{ formatTime(detail.started_at) }}</dd>
                    </dl>
                    <dl class="grid min-w-0 grid-cols-[max-content_minmax(0,1fr)] gap-x-3 gap-y-2.5 text-sm">
                      <dt class="text-[var(--neutral-700)]">Time to First Token</dt><dd class="font-mono tabular-nums">{{ formatDuration(detail.ttft_ms) }}</dd>
                      <dt class="text-[var(--neutral-700)]">Generation Speed</dt><dd class="font-mono tabular-nums">{{ formatRate(detail.generation_tokens_per_second) }}</dd>
                      <dt class="text-[var(--neutral-700)]">Load Time</dt><dd class="font-mono tabular-nums">{{ formatDuration(detail.load_duration_ms) }}</dd>
                      <dt class="text-[var(--neutral-700)]">End Time</dt><dd class="font-mono tabular-nums">{{ formatTime(detail.finished_at) }}</dd>
                      <dt class="text-[var(--neutral-700)]">Autoloaded</dt><dd>{{ detail.autoloaded ? 'True' : 'False' }}</dd>
                    </dl>
                  </div>
                </section>

                <section data-testid="request-detail-content" class="border-b border-[var(--color-divider)] py-5">
                  <div class="mb-4 flex items-center justify-between gap-3">
                    <div><p class="text-[length:var(--font-size-kicker)] font-semibold uppercase tracking-[.1em] text-[var(--neutral-700)]">PAYLOAD</p><h3 class="mt-1 font-heading text-lg font-semibold text-[var(--color-text)]">Request &amp; Response</h3></div>
                    <div v-if="detail.request_body || detail.response_body" class="flex gap-1">
                      <AppButton size="xs" :intent="detailMode === 'pretty' ? 'primary' : 'secondary'" @click="detailMode = 'pretty'">Pretty</AppButton>
                      <AppButton size="xs" :intent="detailMode === 'json' ? 'primary' : 'secondary'" @click="detailMode = 'json'">JSON</AppButton>
                    </div>
                  </div>
                  <div v-if="!detail.request_body && !detail.response_body" class="border-l-2 border-[var(--color-divider)] pl-3 text-sm">
                    <p class="font-semibold">Content not recorded</p>
                    <p class="mt-1 text-[var(--neutral-800)]">This request used metadata-only logging, so request and response payloads were not retained.</p>
                  </div>
                  <template v-else-if="detailMode === 'pretty'">
                    <div v-if="requestMessages.length" class="space-y-3">
                      <div class="flex items-center gap-2"><UIcon name="i-lucide-message-square" class="text-[var(--neutral-700)]" /><p class="text-sm font-semibold">Input</p><span class="font-mono text-xs tabular-nums text-[var(--neutral-800)]">Tokens: {{ detail.prompt_tokens }}</span></div>
                      <div v-for="(message, index) in requestMessages" :key="index" class="border-t border-[var(--color-divider)] pt-3 first:border-t-0 first:pt-0">
                        <p class="text-[length:var(--font-size-kicker)] font-semibold uppercase tracking-wide text-[var(--neutral-700)]">{{ message.role || 'message' }}</p>
                        <pre class="mt-1 whitespace-pre-wrap font-mono text-xs text-[var(--neutral-800)]">{{ typeof message.content === 'string' ? message.content : JSON.stringify(message.content, null, 2) }}</pre>
                      </div>
                    </div>
                    <div v-if="requestTools.length" class="mt-4 border-t border-[var(--color-divider)] pt-4"><p class="text-xs font-semibold uppercase text-[var(--neutral-700)]">Tools</p><pre class="mt-2 whitespace-pre-wrap font-mono text-xs text-[var(--neutral-800)]">{{ JSON.stringify(requestTools, null, 2) }}</pre></div>
                    <div v-if="responseToolCalls.length" class="mt-4 border-t border-[var(--color-divider)] pt-4"><p class="text-xs font-semibold uppercase text-[var(--neutral-700)]">Response tool calls</p><pre class="mt-2 whitespace-pre-wrap font-mono text-xs text-[var(--neutral-800)]">{{ JSON.stringify(responseToolCalls, null, 2) }}</pre></div>
                    <div v-if="!requestMessages.length" class="space-y-2"><p class="text-sm font-semibold">Input <span class="ml-2 font-mono text-xs font-normal tabular-nums text-[var(--neutral-800)]">Tokens: {{ detail.prompt_tokens }}</span></p><pre class="whitespace-pre-wrap font-mono text-xs text-[var(--neutral-800)]">{{ prettyBody(detail.request_body) || '—' }}</pre></div>
                    <div class="mt-4 border-t border-[var(--color-divider)] pt-4"><p class="text-sm font-semibold">Output <span class="ml-2 font-mono text-xs font-normal tabular-nums text-[var(--neutral-800)]">Tokens: {{ detail.generated_tokens }}</span></p><pre class="mt-2 whitespace-pre-wrap font-mono text-xs text-[var(--neutral-800)]">{{ prettyBody(detail.response_body) || '—' }}</pre></div>
                  </template>
                  <template v-else>
                    <div><p class="text-xs font-semibold uppercase text-[var(--neutral-700)]">Request JSON</p><pre class="mt-2 whitespace-pre-wrap font-mono text-xs text-[var(--neutral-800)]">{{ prettyBody(detail.request_body) || '—' }}</pre></div>
                    <div class="mt-4 border-t border-[var(--color-divider)] pt-4"><p class="text-xs font-semibold uppercase text-[var(--neutral-700)]">Response JSON</p><pre class="mt-2 whitespace-pre-wrap font-mono text-xs text-[var(--neutral-800)]">{{ prettyBody(detail.response_body) || '—' }}</pre></div>
                  </template>
                </section>

                <section data-testid="request-detail-client-metadata" class="py-5">
                  <div class="mb-4"><p class="text-[length:var(--font-size-kicker)] font-semibold uppercase tracking-[.1em] text-[var(--neutral-700)]">CLIENT</p><h3 class="mt-1 font-heading text-lg font-semibold text-[var(--color-text)]">Client Metadata</h3></div>
                  <div class="grid gap-x-12 gap-y-4 lg:grid-cols-2">
                    <dl class="grid min-w-0 grid-cols-[max-content_minmax(0,1fr)] gap-x-3 gap-y-2.5 text-sm">
                      <dt class="text-[var(--neutral-700)]">Client IP</dt><dd class="font-mono text-xs">{{ detail.client_ip || '—' }}</dd>
                      <dt class="text-[var(--neutral-700)]">User-Agent</dt><dd class="min-w-0 break-words text-xs">{{ detail.user_agent || '—' }}</dd>
                    </dl>
                    <dl class="grid min-w-0 grid-cols-[max-content_minmax(0,1fr)] gap-x-3 gap-y-2.5 text-sm">
                      <dt class="text-[var(--neutral-700)]">Autoloaded</dt><dd>{{ detail.autoloaded ? 'True' : 'False' }}</dd>
                    </dl>
                  </div>
                </section>
              </template>
            </div>
          </div>
        </div>
      </template>
    </USlideover>
  </div>
</template>