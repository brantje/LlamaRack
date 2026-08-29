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

const pageSize = 25
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
const filters = reactive({ window: '1h', instance_id: '', endpoint: '', api_key_id: '', result: '', status_code: '', streaming: '', search: '' })
let loadGeneration = 0

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
const instanceItems = computed(() => [{ label: 'All Instances', value: '' }, ...manager.instances.value.map(item => ({ label: `${item.name} (${item.id})`, value: item.id }))])
const columns: TableColumn<RequestRecord>[] = [
  { accessorKey: 'started_at', header: 'Time' }, { accessorKey: 'result', header: 'Status' },
  { accessorKey: 'call_type', header: 'Call Type' }, { accessorKey: 'request_id', header: 'Request ID' },
  { accessorKey: 'trace_id', header: 'Trace ID' }, { accessorKey: 'instance_id', header: 'Instance / Model' },
  { accessorKey: 'endpoint', header: 'Endpoint' }, { accessorKey: 'api_key', header: 'API Key' },
  { accessorKey: 'total_tokens', header: 'Tokens' }, { accessorKey: 'duration_ms', header: 'Duration' },
  { accessorKey: 'ttft_ms', header: 'TTFT' }
]
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
function requestKey(item: RequestRecord) { return item.api_key ? item.api_key.name || item.api_key.prefix || item.api_key.id || 'API key' : '—' }
function isPending(item: RequestRecord) { return item.finished_at === 0 || !item.result || item.result === 'pending' }
function resultLabel(item: RequestRecord) { return isPending(item) ? 'pending' : String(item.status_code || item.result) }
function formatDuration(value?: number) {
  if (value === undefined || !Number.isFinite(value)) return '—'
  return value < 1000 ? `${Math.round(value)} ms` : `${(value / 1000).toFixed(value >= 10_000 ? 1 : 2)} s`
}
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
async function applyFilters() { offset.value = 0; await loadRequests() }
async function previousPage() { offset.value = Math.max(0, offset.value - pageSize); await loadRequests() }
async function nextPage() { offset.value += pageSize; await loadRequests() }
async function clearTrace() {
  traceID.value = ''
  offset.value = 0
  await router.replace({ path: '/logs', query: {} })
  await loadRequests()
}
async function toggleLiveStreaming() {
  liveStreamingEnabled.value = !liveStreamingEnabled.value
  if (liveStreamingEnabled.value && routeReady.value && manager.user.value && offset.value === 0) await loadRequests()
}
async function selectRequest(requestID: string) {
  detailOpen.value = true
  detailLoading.value = true
  detailError.value = ''
  detail.value = null
  detailMode.value = 'pretty'
  try { detail.value = await manager.request<RequestDetail>(`/api/v1/observability/requests/${encodeURIComponent(requestID)}`) }
  catch (value: any) { detailError.value = value?.data?.error || value?.message || 'Unable to load request details' }
  finally { detailLoading.value = false }
}
async function initializePage() {
  if (routeReady.value || !manager.initialized.value || !manager.user.value) return
  traceID.value = String(route.query.trace_id || '').trim()
  await loadRequests()
  routeReady.value = true
  const requestID = String(route.query.request_id || '').trim()
  if (requestID) await selectRequest(requestID)
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
watch(detailOpen, async (open) => {
  if (open || !route.query.request_id) return
  const query = { ...route.query }; delete query.request_id
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
      <UPageHeader class="min-w-0 flex-1" headline="OBSERVABILITY" title="Request logs" description="Persistent OpenAI-compatible inference request history, correlation IDs, traces and performance metadata." />
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
      <template #header><p class="text-xs font-extrabold tracking-[0.18em] text-dimmed">FILTERS</p><p class="mt-1 text-xs text-muted">Filters are applied server-side to retained request history.</p></template>
      <div class="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
        <UFormField label="Time window"><USelectMenu v-model="filters.window" :items="windowItems" label-key="label" value-key="value" class="w-full" /></UFormField>
        <UFormField label="Instance"><USelectMenu v-model="filters.instance_id" :items="instanceItems" label-key="label" value-key="value" class="w-full" /></UFormField>
        <UFormField label="Endpoint"><USelectMenu v-model="filters.endpoint" :items="endpointItems" label-key="label" value-key="value" class="w-full" /></UFormField>
        <UFormField label="API key ID"><UInput v-model="filters.api_key_id" class="w-full" placeholder="Key ID" /></UFormField>
        <UFormField label="Result"><USelectMenu v-model="filters.result" :items="resultItems" label-key="label" value-key="value" class="w-full" /></UFormField>
        <UFormField label="HTTP status"><UInput v-model="filters.status_code" type="number" min="100" max="599" class="w-full" placeholder="Any status" /></UFormField>
        <UFormField label="Streaming"><USelectMenu v-model="filters.streaming" :items="streamingItems" label-key="label" value-key="value" class="w-full" /></UFormField>
        <UFormField label="Search"><UInput v-model="filters.search" class="w-full" icon="i-lucide-search" placeholder="Request ID, trace, model…" @keyup.enter="applyFilters" /></UFormField>
      </div>
      <div class="mt-4 flex justify-end"><UButton data-testid="apply-request-log-filters" :loading="loading" @click="applyFilters">Apply filters</UButton></div>
    </UCard>

    <UCard data-testid="request-log-table">
      <template #header><div class="flex items-center justify-between gap-3"><div><p class="text-xs font-extrabold tracking-[0.18em] text-dimmed">REQUEST HISTORY</p><p class="mt-1 text-xs text-muted">{{ traceID ? 'Oldest first for this trace.' : 'Newest first.' }} Full payloads load only when opening a request.</p></div><UBadge color="neutral" variant="soft">{{ requests.length }} shown</UBadge></div></template>
      <UEmpty v-if="!loading && !requests.length" variant="naked" title="No matching requests" description="Adjust the filters or send inference traffic through the gateway." />
      <div v-else class="overflow-x-auto">
        <UTable :data="requests" :columns="columns" class="min-w-[1200px]">
          <template #started_at-cell="{ row }"><span class="whitespace-nowrap font-mono text-xs">{{ formatTime(row.original.started_at) }}</span></template>
          <template #result-cell="{ row }"><UBadge :color="isPending(row.original) ? 'neutral' : row.original.result === 'success' ? 'success' : 'error'" variant="subtle" size="sm">{{ resultLabel(row.original) }}</UBadge></template>
          <template #call_type-cell="{ row }"><span class="text-xs">{{ callTypeLabel(row.original.call_type) }}</span></template>
          <template #request_id-cell="{ row }"><UButton v-if="row.original.request_id" data-testid="request-detail-trigger" color="neutral" variant="link" size="xs" class="font-mono" @click="selectRequest(row.original.request_id)">{{ row.original.request_id }}</UButton><span v-else>—</span></template>
          <template #trace_id-cell="{ row }"><NuxtLink v-if="row.original.trace_id" class="font-mono text-xs text-primary" :to="`/logs?trace_id=${encodeURIComponent(row.original.trace_id)}`">{{ row.original.trace_id }}</NuxtLink><span v-else>—</span></template>
          <template #instance_id-cell="{ row }"><span class="font-mono text-xs">{{ row.original.instance_id || '—' }}</span></template>
          <template #endpoint-cell="{ row }"><span class="font-mono text-xs text-muted">{{ row.original.endpoint }}</span></template>
          <template #api_key-cell="{ row }"><span class="text-xs">{{ requestKey(row.original) }}</span></template>
          <template #total_tokens-cell="{ row }"><span class="font-mono text-xs">{{ row.original.total_tokens || '—' }}</span></template>
          <template #duration_ms-cell="{ row }"><span class="font-mono text-xs">{{ formatDuration(row.original.duration_ms) }}</span></template>
          <template #ttft_ms-cell="{ row }"><span class="font-mono text-xs">{{ formatDuration(row.original.ttft_ms) }}</span></template>
        </UTable>
      </div>
      <template #footer><div class="flex items-center justify-between"><span class="text-xs text-muted">Rows {{ requests.length ? offset + 1 : 0 }}–{{ offset + requests.length }}</span><div class="flex gap-2"><UButton color="neutral" variant="soft" size="sm" :disabled="offset === 0 || loading" @click="previousPage">Previous</UButton><UButton color="neutral" variant="soft" size="sm" :disabled="!hasMore || loading" @click="nextPage">Next</UButton></div></div></template>
    </UCard>

    <USlideover v-model:open="detailOpen" side="right" :title="detail?.request_id || 'Request details'" description="Inference request identity, timings, content and retained metadata." data-testid="request-detail-slideover">
      <template #body>
        <div class="space-y-5">
          <USkeleton v-if="detailLoading" class="h-40 w-full" />
          <UAlert v-else-if="detailError" color="error" variant="subtle" title="Request details unavailable" :description="detailError" />
          <template v-else-if="detail">
            <UAlert
              :color="isPending(detail) ? 'neutral' : detail.result === 'error' ? 'error' : 'success'"
              variant="subtle"
              :title="isPending(detail) ? 'Request pending' : detail.status_code ? `HTTP ${detail.status_code}` : detail.result === 'error' ? 'Request failed' : 'Request completed'"
              :description="isPending(detail) ? 'The request is still in progress.' : detail.result === 'error' ? (detail.error || 'The request failed.') : 'The request completed successfully.'"
            />
            <section><h3 class="mb-2 text-sm font-semibold">Request identity</h3><dl class="grid grid-cols-[7rem_minmax(0,1fr)] gap-2 text-sm"><dt class="text-muted">Request ID</dt><dd class="break-all font-mono text-xs">{{ detail.request_id }}</dd><dt class="text-muted">Trace ID</dt><dd class="break-all font-mono text-xs">{{ detail.trace_id || '—' }}</dd><dt class="text-muted">Call type</dt><dd>{{ callTypeLabel(detail.call_type) }}</dd><dt class="text-muted">Instance</dt><dd class="font-mono text-xs">{{ detail.instance_id || 'Unresolved' }}</dd><dt class="text-muted">Endpoint</dt><dd class="font-mono text-xs">{{ detail.endpoint }}</dd></dl></section>
            <USeparator />
            <section><h3 class="mb-2 text-sm font-semibold">Timings & tokens</h3><div class="grid grid-cols-2 gap-3 text-sm"><div>Duration <strong>{{ formatDuration(detail.duration_ms) }}</strong></div><div>TTFT <strong>{{ formatDuration(detail.ttft_ms) }}</strong></div><div>Tokens <strong>{{ detail.total_tokens }}</strong></div><div>Generation <strong>{{ detail.generation_tokens_per_second ? `${detail.generation_tokens_per_second.toFixed(1)} tok/s` : '—' }}</strong></div></div></section>
            <USeparator />
            <section class="space-y-3">
              <div class="flex items-center justify-between"><h3 class="text-sm font-semibold">Request & response content</h3><div v-if="detail.request_body || detail.response_body" class="flex gap-1"><UButton size="xs" :variant="detailMode === 'pretty' ? 'solid' : 'soft'" @click="detailMode = 'pretty'">Pretty</UButton><UButton size="xs" :variant="detailMode === 'json' ? 'solid' : 'soft'" @click="detailMode = 'json'">JSON</UButton></div></div>
              <UAlert v-if="!detail.request_body && !detail.response_body" color="neutral" variant="subtle" title="Content not recorded" description="This request used metadata-only logging, so request and response payloads were not retained." />
              <template v-else-if="detailMode === 'pretty'">
                <div v-if="requestMessages.length"><p class="text-xs font-semibold uppercase text-dimmed">Messages</p><div v-for="(message, index) in requestMessages" :key="index" class="mt-2"><UBadge color="neutral" variant="soft" size="sm">{{ message.role || 'message' }}</UBadge><pre class="mt-1 whitespace-pre-wrap text-xs">{{ typeof message.content === 'string' ? message.content : JSON.stringify(message.content, null, 2) }}</pre></div></div>
                <div v-if="requestTools.length"><p class="text-xs font-semibold uppercase text-dimmed">Tools</p><pre class="whitespace-pre-wrap text-xs">{{ JSON.stringify(requestTools, null, 2) }}</pre></div>
                <div v-if="responseToolCalls.length"><p class="text-xs font-semibold uppercase text-dimmed">Response tool calls</p><pre class="whitespace-pre-wrap text-xs">{{ JSON.stringify(responseToolCalls, null, 2) }}</pre></div>
                <div v-if="!requestMessages.length"><p class="text-xs font-semibold uppercase text-dimmed">Request</p><pre class="whitespace-pre-wrap text-xs">{{ prettyBody(detail.request_body) || '—' }}</pre></div>
                <div><p class="text-xs font-semibold uppercase text-dimmed">Response</p><pre class="whitespace-pre-wrap text-xs">{{ prettyBody(detail.response_body) || '—' }}</pre></div>
              </template>
              <template v-else><div><p class="text-xs font-semibold uppercase text-dimmed">Request JSON</p><pre class="whitespace-pre-wrap text-xs">{{ prettyBody(detail.request_body) || '—' }}</pre></div><div><p class="text-xs font-semibold uppercase text-dimmed">Response JSON</p><pre class="whitespace-pre-wrap text-xs">{{ prettyBody(detail.response_body) || '—' }}</pre></div></template>
            </section>
            <USeparator />
            <section><h3 class="mb-2 text-sm font-semibold">Client metadata</h3><dl class="grid grid-cols-[7rem_minmax(0,1fr)] gap-2 text-sm"><dt class="text-muted">Client IP</dt><dd class="font-mono text-xs">{{ detail.client_ip || '—' }}</dd><dt class="text-muted">User-Agent</dt><dd class="break-words text-xs">{{ detail.user_agent || '—' }}</dd><dt class="text-muted">Started</dt><dd>{{ formatTime(detail.started_at) }}</dd><dt class="text-muted">Finished</dt><dd>{{ formatTime(detail.finished_at) }}</dd></dl></section>
          </template>
        </div>
      </template>
    </USlideover>
  </div>
</template>
