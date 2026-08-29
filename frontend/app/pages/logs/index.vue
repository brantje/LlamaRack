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
  tokens_per_second?: number
  prompt_tokens_per_second?: number
  generation_tokens_per_second?: number
  queue_duration_ms: number
  load_duration_ms: number
  autoloaded: boolean
  error?: string
}
type RequestDetail = RequestRecord & { request_body?: string; response_body?: string }
type RequestPage = { items: RequestRecord[]; limit: number; offset: number; has_more: boolean }

const pageSize = 25
const requests = ref<RequestRecord[]>([])
const hasMore = ref(false)
const offset = ref(0)
const loading = ref(false)
const error = ref('')
const detailOpen = ref(false)
const detailLoading = ref(false)
const detailError = ref('')
const detail = ref<RequestDetail | null>(null)
const detailMode = ref<'pretty' | 'json'>('pretty')

const filters = reactive({
  window: '1h',
  instance_id: '',
  endpoint: '',
  api_key_id: '',
  result: '',
  status_code: '',
  streaming: '',
  search: ''
})

const windowItems = [
  { label: 'Last 15 minutes', value: '15m' },
  { label: 'Last hour', value: '1h' },
  { label: 'Last 24 hours', value: '24h' },
  { label: 'Last 7 days', value: '7d' },
  { label: 'All retained history', value: 'all' }
]
const endpointItems = [
  { label: 'All endpoints', value: '' },
  { label: 'Chat completions', value: '/v1/chat/completions' },
  { label: 'Completions', value: '/v1/completions' },
  { label: 'Responses', value: '/v1/responses' },
  { label: 'Embeddings', value: '/v1/embeddings' }
]
const resultItems = [
  { label: 'All results', value: '' },
  { label: 'Success', value: 'success' },
  { label: 'Error', value: 'error' }
]
const streamingItems = [
  { label: 'Streaming + non-streaming', value: '' },
  { label: 'Streaming', value: 'true' },
  { label: 'Non-streaming', value: 'false' }
]
const instanceItems = computed(() => [
  { label: 'All Instances', value: '' },
  ...manager.instances.value.map(instance => ({ label: `${instance.name} (${instance.id})`, value: instance.id }))
])
const traceID = computed(() => String(route.query.trace_id || '').trim())

const columns: TableColumn<RequestRecord>[] = [
  { accessorKey: 'started_at', header: 'Time' },
  { accessorKey: 'result', header: 'Status' },
  { accessorKey: 'call_type', header: 'Call Type' },
  { accessorKey: 'request_id', header: 'Request ID' },
  { accessorKey: 'trace_id', header: 'Trace ID' },
  { accessorKey: 'instance_id', header: 'Instance / Model' },
  { accessorKey: 'endpoint', header: 'Endpoint' },
  { accessorKey: 'api_key', header: 'API Key' },
  { accessorKey: 'total_tokens', header: 'Tokens' },
  { accessorKey: 'duration_ms', header: 'Duration' },
  { accessorKey: 'ttft_ms', header: 'TTFT' }
]

function callTypeLabel(value?: string) {
  return ({ chat_completion: 'Chat Completion', completion: 'Completion', response: 'Responses', embedding: 'Embedding' } as Record<string, string>)[value || ''] || '—'
}

function requestKey(record: RequestRecord) {
  if (!record.api_key) return '—'
  return record.api_key.name || record.api_key.prefix || record.api_key.id || 'API key'
}

function formatDuration(value?: number) {
  if (value === undefined || !Number.isFinite(value)) return '—'
  if (value < 1000) return `${Math.round(value)} ms`
  return `${(value / 1000).toFixed(value >= 10_000 ? 1 : 2)} s`
}

function formatTime(value: number) {
  if (!value) return '—'
  return new Date(value).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

function sinceForWindow() {
  const durations: Record<string, number> = { '15m': 15 * 60_000, '1h': 60 * 60_000, '24h': 24 * 60 * 60_000, '7d': 7 * 24 * 60 * 60_000 }
  const duration = durations[filters.window]
  return duration ? Date.now() - duration : 0
}

function listPath() {
  const query = new URLSearchParams()
  query.set('limit', String(pageSize))
  query.set('offset', String(offset.value))
  const since = sinceForWindow()
  if (since) query.set('since', String(since))
  if (traceID.value) query.set('trace_id', traceID.value)
  if (filters.instance_id) query.set('instance_id', filters.instance_id)
  if (filters.endpoint) query.set('endpoint', filters.endpoint)
  if (filters.api_key_id.trim()) query.set('api_key_id', filters.api_key_id.trim())
  if (filters.result) query.set('result', filters.result)
  if (filters.status_code.trim()) query.set('status_code', filters.status_code.trim())
  if (filters.streaming) query.set('streaming', filters.streaming)
  if (filters.search.trim()) query.set('search', filters.search.trim())
  return `/api/v1/observability/requests?${query.toString()}`
}

async function loadRequests() {
  loading.value = true
  error.value = ''
  try {
    const payload = await manager.request<RequestPage>(listPath())
    requests.value = payload.items || []
    hasMore.value = Boolean(payload.has_more)
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to load request logs'
    requests.value = []
    hasMore.value = false
  } finally {
    loading.value = false
  }
}

async function applyFilters() {
  offset.value = 0
  await loadRequests()
}

async function previousPage() {
  offset.value = Math.max(0, offset.value - pageSize)
  await loadRequests()
}

async function nextPage() {
  offset.value += pageSize
  await loadRequests()
}

async function clearTrace() {
  offset.value = 0
  await router.replace({ path: '/logs', query: {} })
}

async function selectRequest(requestID: string, updateRoute = true) {
  detailOpen.value = true
  detailLoading.value = true
  detailError.value = ''
  detail.value = null
  detailMode.value = 'pretty'
  if (updateRoute) await router.replace({ path: '/logs', query: { ...route.query, request_id: requestID } })
  try {
    detail.value = await manager.request<RequestDetail>(`/api/v1/observability/requests/${encodeURIComponent(requestID)}`)
  } catch (value: any) {
    detailError.value = value?.data?.error || value?.message || 'Unable to load request details'
  } finally {
    detailLoading.value = false
  }
}

function parseBody(raw?: string) {
  if (!raw) return null
  try { return JSON.parse(raw) } catch { return raw }
}

function prettyBody(raw?: string) {
  const parsed = parseBody(raw)
  if (parsed === null) return ''
  return typeof parsed === 'string' ? parsed : JSON.stringify(parsed, null, 2)
}

const requestObject = computed<any>(() => parseBody(detail.value?.request_body))
const responseObject = computed<any>(() => parseBody(detail.value?.response_body))
const requestMessages = computed<any[]>(() => Array.isArray(requestObject.value?.messages) ? requestObject.value.messages : [])
const requestTools = computed<any[]>(() => Array.isArray(requestObject.value?.tools) ? requestObject.value.tools : [])
const responseToolCalls = computed<any[]>(() => {
  if (!Array.isArray(responseObject.value?.choices)) return []
  return responseObject.value.choices.flatMap((choice: any) => Array.isArray(choice?.message?.tool_calls) ? choice.message.tool_calls : [])
})

watch(traceID, async () => {
  offset.value = 0
  await loadRequests()
})

watch(detailOpen, async (open) => {
  if (open || !route.query.request_id) return
  const query = { ...route.query }
  delete query.request_id
  await router.replace({ path: '/logs', query })
})

onMounted(async () => {
  await loadRequests()
  const requestID = String(route.query.request_id || '').trim()
  if (requestID) await selectRequest(requestID, false)
})
</script>

<template>
  <div class="space-y-5" data-testid="request-logs-page">
    <div class="flex flex-wrap items-start justify-between gap-4">
      <UPageHeader class="min-w-0 flex-1" headline="OBSERVABILITY" title="Request logs" description="Persistent OpenAI-compatible inference request history, correlation IDs, traces and performance metadata." />
      <UButton color="neutral" variant="soft" :loading="loading" icon="i-lucide-refresh-cw" @click="loadRequests">Refresh</UButton>
    </div>

    <UAlert
      v-if="traceID"
      data-testid="trace-filter"
      color="info"
      variant="subtle"
      title="Trace filter active"
      :description="`Showing requests in chronological order for ${traceID}.`"
    >
      <template #actions><UButton size="xs" color="neutral" variant="soft" @click="clearTrace">Clear trace</UButton></template>
    </UAlert>

    <UAlert v-if="error" color="error" variant="subtle" title="Request history unavailable" :description="error" />

    <UCard data-testid="request-log-filters">
      <template #header><div><p class="text-xs font-extrabold tracking-[0.18em] text-dimmed">FILTERS</p><p class="mt-1 text-xs text-muted">Filters are applied server-side to retained request history.</p></div></template>
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
      <template #header>
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div><p class="text-xs font-extrabold tracking-[0.18em] text-dimmed">REQUEST HISTORY</p><p class="mt-1 text-xs text-muted">{{ traceID ? 'Oldest first for this trace.' : 'Newest first.' }} Full payloads load only when opening a request.</p></div>
          <UBadge color="neutral" variant="soft">{{ requests.length }} shown</UBadge>
        </div>
      </template>
      <UEmpty v-if="!loading && !requests.length" variant="naked" title="No matching requests" description="Adjust the filters or send inference traffic through the gateway." />
      <div v-else class="overflow-x-auto">
        <UTable :data="requests" :columns="columns" class="min-w-[1200px]">
          <template #started_at-cell="{ row }"><span class="whitespace-nowrap font-mono text-xs">{{ formatTime(row.original.started_at) }}</span></template>
          <template #result-cell="{ row }"><UBadge :color="row.original.result === 'success' ? 'success' : row.original.result === 'pending' ? 'warning' : 'error'" variant="subtle" size="sm">{{ row.original.status_code || row.original.result }}</UBadge></template>
          <template #call_type-cell="{ row }"><span class="whitespace-nowrap text-xs">{{ callTypeLabel(row.original.call_type) }}</span></template>
          <template #request_id-cell="{ row }"><UButton data-testid="request-detail-trigger" color="neutral" variant="link" size="xs" class="font-mono" @click="selectRequest(row.original.request_id)">{{ row.original.request_id }}</UButton></template>
          <template #trace_id-cell="{ row }"><NuxtLink v-if="row.original.trace_id" class="font-mono text-xs text-primary" :to="`/logs?trace_id=${encodeURIComponent(row.original.trace_id)}`">{{ row.original.trace_id }}</NuxtLink><span v-else>—</span></template>
          <template #instance_id-cell="{ row }"><span class="font-mono text-xs">{{ row.original.instance_id || '—' }}</span></template>
          <template #endpoint-cell="{ row }"><span class="font-mono text-xs text-muted">{{ row.original.endpoint }}</span></template>
          <template #api_key-cell="{ row }"><span class="text-xs">{{ requestKey(row.original) }}</span></template>
          <template #total_tokens-cell="{ row }"><span class="font-mono text-xs">{{ row.original.total_tokens || '—' }}</span></template>
          <template #duration_ms-cell="{ row }"><span class="whitespace-nowrap font-mono text-xs">{{ formatDuration(row.original.duration_ms) }}</span></template>
          <template #ttft_ms-cell="{ row }"><span class="whitespace-nowrap font-mono text-xs">{{ formatDuration(row.original.ttft_ms) }}</span></template>
        </UTable>
      </div>
      <template #footer>
        <div class="flex items-center justify-between gap-3">
          <span class="text-xs text-muted">Rows {{ requests.length ? offset + 1 : 0 }}–{{ offset + requests.length }}</span>
          <div class="flex gap-2">
            <UButton color="neutral" variant="soft" size="sm" :disabled="offset === 0 || loading" @click="previousPage">Previous</UButton>
            <UButton color="neutral" variant="soft" size="sm" :disabled="!hasMore || loading" @click="nextPage">Next</UButton>
          </div>
        </div>
      </template>
    </UCard>

    <USlideover v-model:open="detailOpen" side="right" :title="detail?.request_id || 'Request details'" description="Inference request identity, timings, content and retained metadata." data-testid="request-detail-slideover">
      <template #body>
        <div class="space-y-6">
          <USkeleton v-if="detailLoading" class="h-40 w-full" />
          <UAlert v-else-if="detailError" color="error" variant="subtle" title="Request details unavailable" :description="detailError" />
          <template v-else-if="detail">
            <UAlert v-if="detail.result === 'error'" color="error" variant="subtle" :title="`HTTP ${detail.status_code || 'error'}`" :description="detail.error || 'The request failed.'" />
            <UAlert v-else color="success" variant="subtle" :title="`HTTP ${detail.status_code}`" description="The request completed successfully." />

            <section class="space-y-3">
              <h3 class="text-sm font-semibold text-highlighted">Request identity</h3>
              <dl class="grid grid-cols-[8rem_minmax(0,1fr)] gap-x-3 gap-y-2 text-sm">
                <dt class="text-muted">Request ID</dt><dd class="break-all font-mono text-xs">{{ detail.request_id }}</dd>
                <dt class="text-muted">Trace ID</dt><dd><NuxtLink v-if="detail.trace_id" class="break-all font-mono text-xs text-primary" :to="`/logs?trace_id=${encodeURIComponent(detail.trace_id)}`">{{ detail.trace_id }}</NuxtLink><span v-else>—</span></dd>
                <dt class="text-muted">Call type</dt><dd>{{ callTypeLabel(detail.call_type) }}</dd>
                <dt class="text-muted">Instance</dt><dd class="font-mono text-xs">{{ detail.instance_id || 'Unresolved' }}</dd>
                <dt class="text-muted">Endpoint</dt><dd class="font-mono text-xs">{{ detail.endpoint }}</dd>
                <dt class="text-muted">API key</dt><dd>{{ requestKey(detail) }}</dd>
                <dt class="text-muted">Streaming</dt><dd>{{ detail.streaming ? 'Yes' : 'No' }}</dd>
              </dl>
            </section>

            <USeparator />
            <section class="space-y-3">
              <h3 class="text-sm font-semibold text-highlighted">Timings & tokens</h3>
              <dl class="grid grid-cols-2 gap-3 text-sm sm:grid-cols-3">
                <div><dt class="text-xs text-muted">Duration</dt><dd class="mt-1 font-mono">{{ formatDuration(detail.duration_ms) }}</dd></div>
                <div><dt class="text-xs text-muted">TTFT</dt><dd class="mt-1 font-mono">{{ formatDuration(detail.ttft_ms) }}</dd></div>
                <div><dt class="text-xs text-muted">Queue</dt><dd class="mt-1 font-mono">{{ formatDuration(detail.queue_duration_ms) }}</dd></div>
                <div><dt class="text-xs text-muted">Prompt tokens</dt><dd class="mt-1 font-mono">{{ detail.prompt_tokens }}</dd></div>
                <div><dt class="text-xs text-muted">Generated tokens</dt><dd class="mt-1 font-mono">{{ detail.generated_tokens }}</dd></div>
                <div><dt class="text-xs text-muted">Total tokens</dt><dd class="mt-1 font-mono">{{ detail.total_tokens }}</dd></div>
                <div><dt class="text-xs text-muted">Prompt throughput</dt><dd class="mt-1 font-mono">{{ detail.prompt_tokens_per_second ? `${detail.prompt_tokens_per_second.toFixed(1)} tok/s` : '—' }}</dd></div>
                <div><dt class="text-xs text-muted">Generation throughput</dt><dd class="mt-1 font-mono">{{ detail.generation_tokens_per_second ? `${detail.generation_tokens_per_second.toFixed(1)} tok/s` : '—' }}</dd></div>
                <div><dt class="text-xs text-muted">Autoload</dt><dd class="mt-1">{{ detail.autoloaded ? `${formatDuration(detail.load_duration_ms)} load` : 'No' }}</dd></div>
              </dl>
            </section>

            <USeparator />
            <section class="space-y-3">
              <div class="flex flex-wrap items-center justify-between gap-3">
                <h3 class="text-sm font-semibold text-highlighted">Request & response content</h3>
                <UFieldGroup v-if="detail.request_body || detail.response_body">
                  <UButton size="xs" :variant="detailMode === 'pretty' ? 'solid' : 'soft'" @click="detailMode = 'pretty'">Pretty</UButton>
                  <UButton size="xs" :variant="detailMode === 'json' ? 'solid' : 'soft'" @click="detailMode = 'json'">JSON</UButton>
                </UFieldGroup>
              </div>
              <UAlert v-if="!detail.request_body && !detail.response_body" color="neutral" variant="subtle" title="Content not recorded" description="This request used metadata-only logging, so request and response payloads were not retained." />
              <template v-else-if="detailMode === 'pretty'">
                <div v-if="requestMessages.length" class="space-y-3">
                  <p class="text-xs font-semibold uppercase tracking-wide text-dimmed">Messages</p>
                  <div v-for="(message, index) in requestMessages" :key="index" class="border-b border-default pb-3 last:border-0">
                    <UBadge color="neutral" variant="soft" size="sm">{{ message.role || 'message' }}</UBadge>
                    <pre class="mt-2 whitespace-pre-wrap break-words text-xs">{{ typeof message.content === 'string' ? message.content : JSON.stringify(message.content, null, 2) }}</pre>
                    <pre v-if="message.tool_calls" class="mt-2 whitespace-pre-wrap break-words text-xs text-muted">{{ JSON.stringify(message.tool_calls, null, 2) }}</pre>
                  </div>
                </div>
                <div v-if="requestTools.length" class="space-y-2"><p class="text-xs font-semibold uppercase tracking-wide text-dimmed">Tools</p><pre class="whitespace-pre-wrap break-words text-xs">{{ JSON.stringify(requestTools, null, 2) }}</pre></div>
                <div v-if="responseToolCalls.length" class="space-y-2"><p class="text-xs font-semibold uppercase tracking-wide text-dimmed">Response tool calls</p><pre class="whitespace-pre-wrap break-words text-xs">{{ JSON.stringify(responseToolCalls, null, 2) }}</pre></div>
                <div v-if="!requestMessages.length" class="space-y-2"><p class="text-xs font-semibold uppercase tracking-wide text-dimmed">Request</p><pre class="max-h-80 overflow-auto whitespace-pre-wrap break-words text-xs">{{ prettyBody(detail.request_body) || '—' }}</pre></div>
                <div class="space-y-2"><p class="text-xs font-semibold uppercase tracking-wide text-dimmed">Response</p><pre class="max-h-80 overflow-auto whitespace-pre-wrap break-words text-xs">{{ prettyBody(detail.response_body) || '—' }}</pre></div>
              </template>
              <template v-else>
                <div class="space-y-2"><p class="text-xs font-semibold uppercase tracking-wide text-dimmed">Request JSON</p><pre class="max-h-80 overflow-auto whitespace-pre-wrap break-words text-xs">{{ prettyBody(detail.request_body) || '—' }}</pre></div>
                <div class="space-y-2"><p class="text-xs font-semibold uppercase tracking-wide text-dimmed">Response JSON</p><pre class="max-h-80 overflow-auto whitespace-pre-wrap break-words text-xs">{{ prettyBody(detail.response_body) || '—' }}</pre></div>
              </template>
            </section>

            <USeparator />
            <section class="space-y-3">
              <h3 class="text-sm font-semibold text-highlighted">Client metadata</h3>
              <dl class="grid grid-cols-[8rem_minmax(0,1fr)] gap-x-3 gap-y-2 text-sm">
                <dt class="text-muted">Client IP</dt><dd class="font-mono text-xs">{{ detail.client_ip || '—' }}</dd>
                <dt class="text-muted">User-Agent</dt><dd class="break-words text-xs">{{ detail.user_agent || '—' }}</dd>
                <dt class="text-muted">Started</dt><dd>{{ formatTime(detail.started_at) }}</dd>
                <dt class="text-muted">Finished</dt><dd>{{ formatTime(detail.finished_at) }}</dd>
              </dl>
            </section>
          </template>
        </div>
      </template>
    </USlideover>
  </div>
</template>
