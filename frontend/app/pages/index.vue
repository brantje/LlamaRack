<script setup lang="ts">
import type { TableColumn } from '@nuxt/ui'
import type { HardwareGPU, HardwareSnapshot, RuntimeTelemetry } from '~/composables/useManager'
import { startupBackoffMessage } from '~/utils/startupBackoff'

const manager = useManager()
const { instances, runtimes, observabilityLive } = manager

type APIKeyRef = { id: string; name: string; prefix: string }
type RequestRecord = {
  id: number
  request_id?: string
  accepted_at?: number
  started_at: number
  finished_at: number
  instance_id: string
  model_slug?: string
  endpoint: string
  api_key?: APIKeyRef
  streaming: boolean
  status_code: number
  result: string
  duration_ms: number
  ttft_ms?: number
  prompt_tokens: number
  generated_tokens: number
  total_tokens: number
  tokens_per_second?: number
  queue_duration_ms: number
  load_duration_ms: number
  autoloaded: boolean
  error?: string
}
type HardwareOverview = { hardware: HardwareSnapshot; telemetry: RuntimeTelemetry[] }
type LifecycleSummary = { autoloads: number; failed_starts: number; load_duration_ms_total: number }
type GatewaySummary = {
  since: number
  requests: number
  successes: number
  errors: number
  active: number
  queued: number
  active_api_keys: number
  prompt_tokens: number
  generated_tokens: number
  total_tokens: number
}
type ManagementSummary = GatewaySummary & { lifecycle: LifecycleSummary; hardware: HardwareOverview }
type SettingValue<T> = { value: T; source: string; editable: boolean }
type GeneralSettings = {
  idle_unload_seconds: SettingValue<number>
  observability_retention_days?: SettingValue<number>
}
type AttentionItem = { key: string; title: string; detail: string; to?: string }
type RangeOption = { label: string; value: number; disabled?: boolean }
type VRAMSegment = {
  label: string
  bytes: number
  percent: number
  color: string
  colorKey: string
}

const rangeOptions = [
  { label: '5 min', value: 300 },
  { label: '10 min', value: 600 },
  { label: '15 min', value: 900 },
  { label: '30 min', value: 1800 },
  { label: '1 hour', value: 3600 },
  { label: '6 hours', value: 21600 },
  { label: '12 hours', value: 43200 },
  { label: '24 hours', value: 86400 },
  { label: '1 week', value: 604800 }
] as const

const summary = ref<ManagementSummary | null>(null)
const recentRequests = ref<RequestRecord[]>([])
const settings = ref<GeneralSettings | null>(null)
const selectedWindow = ref<number>(900)
const loading = ref(false)
const refreshing = ref(false)
const lastUpdatedAt = ref<number | null>(null)
const dashboardError = ref('')
let windowRequestSequence = 0

const runtimeList = computed(() => Object.values(runtimes.value).flat())
const readyCount = computed(() => runtimeList.value.filter(runtime => runtime.state === 'READY').length)
const startingCount = computed(() => runtimeList.value.filter(runtime => runtime.state === 'STARTING' || runtime.state === 'LOADING').length)
const failedCount = computed(() => runtimeList.value.filter(runtime => runtime.state === 'FAILED').length)
const selectedRange = computed(() => rangeOptions.find(option => option.value === selectedWindow.value) ?? rangeOptions[2])
const selectedRangeLabel = computed(() => selectedRange.value.label)
const retentionDays = computed(() => {
  const value = Number(settings.value?.observability_retention_days?.value ?? 30)
  return Number.isFinite(value) && value > 0 ? value : 30
})
const retentionSeconds = computed(() => retentionDays.value * 24 * 3600)
const selectableRanges = computed<RangeOption[]>(() => rangeOptions.map(option => ({
  label: option.label,
  value: option.value,
  disabled: option.value > retentionSeconds.value
})))
const hasRestrictedRanges = computed(() => rangeOptions.some(option => option.value > retentionSeconds.value))
const gatewaySummary = computed<GatewaySummary | null>(() => summary.value)
const currentActivity = computed(() => observabilityLive.value?.gateway ?? summary.value)
const liveRequests = computed<RequestRecord[]>(() => (observabilityLive.value?.requests as RequestRecord[] | undefined) ?? [])
const streamingCount = computed(() => liveRequests.value.filter(request => request.streaming && request.result === 'pending').length)
const isUpdating = computed(() => loading.value || refreshing.value)
const lastUpdatedLabel = computed(() => {
  if (isUpdating.value) return 'Updating…'
  return lastUpdatedAt.value ? `Last updated ${formatTime(lastUpdatedAt.value)}` : 'Not updated yet'
})

const emptyHardware = (): HardwareSnapshot => ({
  ram_total_bytes: 0,
  ram_available_bytes: 0,
  gpus: [],
  processes: [],
  collected_at: ''
})
const hardware = computed(() => observabilityLive.value?.hardware || summary.value?.hardware?.hardware || emptyHardware())
const telemetry = computed(() => observabilityLive.value?.telemetry || summary.value?.hardware?.telemetry || [])
const totalVRAM = computed(() => hardware.value.gpus.reduce((total, gpu) => total + Math.max(0, gpu.total_bytes), 0))
const committedVRAM = computed(() => hardware.value.gpus.reduce((total, gpu) => total + gpuCommittedBytes(gpu), 0))
const deviceUsedVRAM = computed(() => hardware.value.gpus.reduce((total, gpu) => total + Math.min(Math.max(0, gpu.used_bytes), Math.max(0, gpu.total_bytes)), 0))
const unattributedVRAM = computed(() => Math.max(0, deviceUsedVRAM.value - committedVRAM.value))
const vramPercent = computed(() => totalVRAM.value > 0 ? Math.min(100, (committedVRAM.value / totalVRAM.value) * 100) : 0)
const ramUsed = computed(() => Math.max(0, hardware.value.ram_total_bytes - hardware.value.ram_available_bytes))
const ramPercent = computed(() => hardware.value.ram_total_bytes > 0 ? Math.min(100, (ramUsed.value / hardware.value.ram_total_bytes) * 100) : 0)
const idleSeconds = computed(() => Number(settings.value?.idle_unload_seconds?.value || 0))
const idleOverrides = computed(() => instances.value.filter(instance => instance.idle_unload_seconds > 0).length)
const tokenTotalK = computed(() => formatThousands(gatewaySummary.value?.total_tokens || 0))
const successRate = computed<number | null>(() => {
  const requests = gatewaySummary.value?.requests || 0
  return requests > 0 ? Math.round(((gatewaySummary.value?.successes || 0) / requests) * 1000) / 10 : null
})
const meanAutoloadDuration = computed(() => {
  const lifecycle = summary.value?.lifecycle
  return lifecycle?.autoloads ? lifecycle.load_duration_ms_total / lifecycle.autoloads : undefined
})

const gatewayColumns: TableColumn<RequestRecord>[] = [
  { accessorKey: 'started_at', header: 'Time' },
  { accessorKey: 'instance_id', header: 'Instance' },
  { accessorKey: 'endpoint', header: 'Endpoint' },
  { accessorKey: 'api_key', header: 'Key' },
  { accessorKey: 'total_tokens', header: 'Tokens' },
  { accessorKey: 'ttft_ms', header: 'TTFT' },
  { accessorKey: 'duration_ms', header: 'Latency' },
  { accessorKey: 'tokens_per_second', header: 'tok/s' },
  { accessorKey: 'result', header: 'Result' }
]

const instanceColorTokens = [
  { color: 'var(--accent-500)', key: 'accent-500' },
  { color: 'var(--accent-600)', key: 'accent-600' },
  { color: 'var(--accent-700)', key: 'accent-700' },
  { color: 'var(--accent-400)', key: 'accent-400' },
  { color: 'var(--accent-800)', key: 'accent-800' }
] as const
const instanceColorByID = computed(() => {
  const ids = Array.from(new Set(telemetry.value.map(sample => sample.instance_id))).sort()
  return new Map(ids.map((id, index) => [id, instanceColorTokens[index % instanceColorTokens.length]!]))
})

function instanceByID(id?: string) {
  return id ? instances.value.find(instance => instance.id === id) : undefined
}
function instancePublicLabel(id?: string) {
  const instance = instanceByID(id)
  return instance?.slug || id || '—'
}
function instanceDetailTarget(id?: string) {
  const instance = instanceByID(id)
  return instance ? `/instances/${encodeURIComponent(instance.slug)}/detail` : '/instances'
}
function requestInstanceLabel(record: RequestRecord) {
  return record.model_slug || instancePublicLabel(record.instance_id)
}

function formatBytes(value: number) {
  if (!Number.isFinite(value) || value < 0) return '—'
  if (value === 0) return '0 B'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let amount = value
  let index = 0
  while (amount >= 1024 && index < units.length - 1) {
    amount /= 1024
    index++
  }
  const digits = amount >= 10 || index === 0 ? 0 : 1
  return `${amount.toFixed(digits)} ${units[index]}`
}

function formatDuration(milliseconds?: number) {
  if (milliseconds === undefined || !Number.isFinite(milliseconds)) return '—'
  if (milliseconds < 1000) return `${Math.round(milliseconds)} ms`
  return `${(milliseconds / 1000).toFixed(milliseconds >= 10_000 ? 1 : 2)} s`
}

function formatRate(value?: number) {
  if (value === undefined || !Number.isFinite(value)) return '—'
  return value >= 100 ? Math.round(value).toString() : value.toFixed(1).replace(/\.0$/, '')
}

function formatThousands(value: number) {
  if (!Number.isFinite(value) || value <= 0) return '0'
  const amount = value / 1000
  return amount >= 10 ? amount.toFixed(0) : amount.toFixed(1).replace(/\.0$/, '')
}

function formatTime(timestamp: number) {
  if (!timestamp) return '—'
  return new Date(timestamp).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

function formatIdle(seconds: number) {
  if (seconds <= 0) return 'Disabled'
  if (seconds % 3600 === 0) return `${seconds / 3600} h`
  if (seconds % 60 === 0) return `${seconds / 60} min`
  return `${seconds} sec`
}

function requestKey(record: RequestRecord) {
  return record.api_key?.name || record.api_key?.prefix || '—'
}

function requestDetailTarget(record: RequestRecord) {
  return record.request_id ? `/logs?request_id=${encodeURIComponent(record.request_id)}` : '/logs'
}

function requestStatusVariant(record: RequestRecord): 'ready' | 'pending' | 'neutral' | 'failed' {
  if (record.result === 'pending') return 'pending'
  if (record.result === 'success' || (record.status_code >= 200 && record.status_code < 300)) return 'ready'
  if (record.result === 'error' || record.status_code >= 400) return 'failed'
  return 'neutral'
}

function gpuPercent(gpu: HardwareGPU) {
  return gpu.total_bytes > 0 ? Math.min(100, Math.max(0, gpu.used_bytes / gpu.total_bytes * 100)) : 0
}

function gpuAssignments(gpuID: string) {
  return telemetry.value.flatMap((sample) => {
    const observed = sample.gpus?.find(gpu => gpu.device_id === gpuID)
    if (!observed && !sample.gpu_devices?.includes(gpuID)) return []
    const used = observed?.vram_used_bytes ?? (sample.gpu_devices?.length === 1 ? sample.vram_used_bytes : undefined)
    return used !== undefined && used > 0 ? [{ instanceID: sample.instance_id, used }] : []
  })
}

function gpuCommittedBytes(gpu: HardwareGPU) {
  const attributed = gpuAssignments(gpu.id).reduce((total, assignment) => total + assignment.used, 0)
  return Math.min(Math.max(0, gpu.total_bytes), attributed)
}

function gpuSegments(gpu: HardwareGPU): VRAMSegment[] {
  const total = Math.max(0, gpu.total_bytes)
  if (total === 0) {
    return [{ label: 'Free', bytes: 0, percent: 100, color: 'var(--neutral-500)', colorKey: 'neutral-500' }]
  }

  let remaining = total
  const segments: VRAMSegment[] = []
  for (const assignment of gpuAssignments(gpu.id)) {
    if (remaining <= 0) break
    const bytes = Math.min(remaining, assignment.used)
    if (bytes <= 0) continue
    const token = instanceColorByID.value.get(assignment.instanceID) ?? instanceColorTokens[0]
    segments.push({ label: instancePublicLabel(assignment.instanceID), bytes, percent: bytes / total * 100, color: token.color, colorKey: token.key })
    remaining -= bytes
  }

  const attributed = total - remaining
  const deviceUsed = Math.min(total, Math.max(0, gpu.used_bytes))
  const unattributed = Math.min(remaining, Math.max(0, deviceUsed - attributed))
  if (unattributed > 0) {
    segments.push({
      label: 'Unattributed process memory',
      bytes: unattributed,
      percent: unattributed / total * 100,
      color: 'var(--neutral-700)',
      colorKey: 'neutral-700'
    })
    remaining -= unattributed
  }
  if (remaining > 0) {
    segments.push({ label: 'Free', bytes: remaining, percent: remaining / total * 100, color: 'var(--neutral-500)', colorKey: 'neutral-500' })
  }
  return segments
}

const attention = computed<AttentionItem[]>(() => {
  const items: AttentionItem[] = []
  for (const runtime of runtimeList.value.filter(runtime => runtime.state === 'FAILED')) {
    const backoff = startupBackoffMessage(runtime)
    const current = instanceByID(runtime.instance_id)
    items.push({
      key: `failed-${runtime.instance_id}`,
      title: `${current?.slug || runtime.instance_id} failed to start`,
      detail: backoff || runtime.last_error || 'The managed llama-server process is in FAILED state.',
      to: current ? `/instances/${encodeURIComponent(current.slug)}/detail` : '/instances'
    })
  }
  for (const request of recentRequests.value.filter(request => request.result !== 'success' && request.result !== 'pending' && request.status_code !== 500).slice(0, 2)) {
    items.push({
      key: `request-${request.request_id || request.id}`,
      title: `${requestInstanceLabel(request)} returned ${request.status_code || 'an error'}`,
      detail: request.error || `${request.endpoint} failed during the last ${selectedRangeLabel.value}.`,
      to: requestDetailTarget(request)
    })
  }
  for (const instance of instances.value) {
    if (!instance.always_on || manager.instanceState(instance) !== 'UNLOADED') continue
    const backoff = startupBackoffMessage(manager.runtimeForInstance(instance))
    items.push({
      key: `always-${instance.id}`,
      title: `${instance.slug} is Always-On but unloaded`,
      detail: backoff || 'The Instance may have been stopped manually or could be waiting for resources.',
      to: `/instances/${encodeURIComponent(instance.slug)}/detail`
    })
  }
  for (const gpu of hardware.value.gpus) {
    if (gpuPercent(gpu) < 90) continue
    items.push({
      key: `gpu-${gpu.id}`,
      title: `${gpu.id} is at ${Math.round(gpuPercent(gpu))}% VRAM`,
      detail: 'New loads may require resource-pressure eviction.',
      to: '/instances'
    })
  }
  if (ramPercent.value >= 90) {
    items.push({
      key: 'ram-pressure',
      title: `Host RAM is at ${Math.round(ramPercent.value)}%`,
      detail: 'Host memory pressure may affect model loading and inference.',
      to: '/instances'
    })
  }
  return items.slice(0, 6)
})

async function loadWindowData() {
  if (!manager.initialized.value || !manager.user.value) return
  const sequence = ++windowRequestSequence
  const windowSeconds = selectedWindow.value
  const since = Date.now() - windowSeconds * 1000
  loading.value = true
  dashboardError.value = ''
  try {
    const [summaryValue, requestsValue] = await Promise.all([
      manager.request<ManagementSummary>(`/api/v1/observability/summary?window_seconds=${windowSeconds}`),
      manager.request<{ items?: RequestRecord[] }>(`/api/v1/observability/requests?since=${since}&limit=50`)
    ])
    if (sequence !== windowRequestSequence) return
    summary.value = summaryValue
    recentRequests.value = requestsValue.items || []
    lastUpdatedAt.value = Date.now()
  } catch (error: any) {
    if (sequence !== windowRequestSequence) return
    dashboardError.value = error?.data?.error || error?.message || 'Unable to load Dashboard observability data'
  } finally {
    if (sequence === windowRequestSequence) loading.value = false
  }
}

async function loadDashboard() {
  if (!manager.initialized.value || !manager.user.value) return
  try {
    settings.value = await manager.request<GeneralSettings>('/api/v1/settings/general')
  } catch (error: any) {
    dashboardError.value = error?.data?.error || error?.message || 'Unable to load Dashboard settings'
  }
  await loadWindowData()
}

async function refreshDashboard() {
  if (!manager.initialized.value || !manager.user.value || refreshing.value) return
  refreshing.value = true
  try {
    await Promise.allSettled([manager.refresh(), loadDashboard()])
  } finally {
    refreshing.value = false
  }
}

function setSelectedWindow(value: number) {
  selectedWindow.value = value
}

defineExpose({ setSelectedWindow })

watch(
  [() => manager.initialized.value, () => manager.user.value],
  ([initialized, user]) => {
    if (!initialized || !user) return
    void loadDashboard()
  },
  { immediate: true }
)

watch(selectedWindow, (next, previous) => {
  if (next === previous || !manager.initialized.value || !manager.user.value) return
  if (next > retentionSeconds.value) {
    const fallback = [...rangeOptions].reverse().find(option => option.value <= retentionSeconds.value)?.value ?? 900
    if (fallback !== next) selectedWindow.value = fallback
    return
  }
  void loadWindowData()
})
</script>

<template>
  <div class="space-y-8" data-testid="observability-dashboard">
    <div class="flex flex-col gap-4 md:flex-row md:items-start md:justify-between" data-testid="dashboard-header">
      <UPageHeader
        class="w-full min-w-0 md:flex-1"
        headline="CONTROL PLANE"
        title="Dashboard"
        description="Live inference traffic, runtime health and accelerator allocation."
      />
      <div class="flex w-full flex-col gap-2 md:w-auto md:items-end" data-testid="dashboard-actions">
        <div class="flex flex-wrap items-center gap-2 md:justify-end">
          <div class="min-w-28">
            <USelect
              v-model="selectedWindow"
              data-testid="dashboard-range"
              aria-label="Observability time range"
              :items="selectableRanges"
              value-key="value"
              label-key="label"
              size="sm"
            />
          </div>
          <AppButton to="/admin/system-logs" intent="secondary" data-testid="dashboard-system-logs">Logs</AppButton>
          <AppButton intent="secondary" :loading="isUpdating" data-testid="dashboard-refresh" @click="refreshDashboard">Refresh</AppButton>
          <AppButton to="/playground" intent="primary">Playground</AppButton>
        </div>
        <div class="flex w-full flex-wrap items-center justify-between gap-x-3 gap-y-1 md:justify-end">
          <AppButton
            v-if="attention.length"
            to="#needs-attention"
            intent="secondary"
            size="xs"
            icon="i-lucide-triangle-alert"
            :aria-label="`${attention.length} ${attention.length === 1 ? 'item needs' : 'items need'} attention`"
            data-testid="dashboard-attention-link"
          >
            Needs attention <span class="font-mono tabular-nums">· {{ attention.length }}</span>
          </AppButton>
          <span class="font-mono text-xs text-muted" data-testid="dashboard-last-updated" aria-live="polite">{{ lastUpdatedLabel }}</span>
        </div>
      </div>
    </div>

    <p v-if="hasRestrictedRanges" class="text-xs text-muted" data-testid="dashboard-retention-note">
      History is retained for {{ retentionDays }} day{{ retentionDays === 1 ? '' : 's' }}; longer ranges are unavailable.
    </p>

    <Frame v-if="dashboardError" class="p-3" data-testid="dashboard-error">
      <div class="flex flex-wrap items-start gap-2">
        <StatusTag variant="failed">Observability data unavailable</StatusTag>
        <p class="min-w-0 flex-1 text-xs text-muted">{{ dashboardError }}</p>
      </div>
    </Frame>

    <div class="grid grid-cols-2 gap-3 xl:grid-cols-4">
      <Frame data-testid="dashboard-running" class="p-4">
        <p class="text-[length:var(--font-size-table-header)] font-medium uppercase tracking-[.1em] text-muted">Ready</p>
        <div class="mt-2 flex items-baseline gap-1.5">
          <strong class="font-[var(--font-heading)] text-[length:var(--font-size-h3)] font-semibold">{{ readyCount }}</strong>{{ ' ' }}<span class="text-sm text-muted">/ {{ instances.length }} Instances</span>
        </div>
        <p class="mt-1 text-xs text-muted">{{ startingCount }} loading · {{ failedCount }} failed</p>
      </Frame>

      <Frame data-testid="dashboard-vram" class="p-4">
        <p class="text-[length:var(--font-size-table-header)] font-medium uppercase tracking-[.1em] text-muted" title="VRAM committed to manager-attributed llama.cpp Instances; other device processes are excluded.">VRAM committed</p>
        <div class="mt-2 flex items-baseline gap-1.5">
          <strong class="font-[var(--font-heading)] text-[length:var(--font-size-h3)] font-semibold">{{ formatBytes(committedVRAM) }}</strong>
          <span class="text-sm text-muted">/ {{ formatBytes(totalVRAM) }}</span>
        </div>
        <p class="mt-1 text-xs text-muted">
          {{ Math.round(vramPercent) }}% of total device capacity attributed to managed Instances<span v-if="unattributedVRAM > 0"> · {{ formatBytes(unattributedVRAM) }} device use unattributed</span>
        </p>
      </Frame>

      <Frame data-testid="dashboard-gateway" class="p-4">
        <p class="text-[length:var(--font-size-table-header)] font-medium uppercase tracking-[.1em] text-muted">Gateway · {{ selectedRangeLabel }}</p>
        <div class="mt-2 flex items-baseline gap-1.5">
          <strong class="font-[var(--font-heading)] text-[length:var(--font-size-h3)] font-semibold">{{ gatewaySummary?.requests || 0 }}</strong>
          <span class="text-sm text-muted">req</span>
        </div>
        <p class="mt-1 text-xs text-muted">{{ gatewaySummary?.active_api_keys || 0 }} key{{ gatewaySummary?.active_api_keys === 1 ? '' : 's' }} active</p>
      </Frame>

      <Frame data-testid="dashboard-idle" class="p-4">
        <p class="text-[length:var(--font-size-table-header)] font-medium uppercase tracking-[.1em] text-muted" title="How long an idle Instance may remain loaded before automatic unload.">Idle unload</p>
        <div class="mt-2 flex items-baseline gap-1.5">
          <strong class="font-[var(--font-heading)] text-[length:var(--font-size-h3)] font-semibold">{{ formatIdle(idleSeconds) }}</strong>
          <span class="text-sm text-muted">global</span>
        </div>
        <p class="mt-1 text-xs text-muted">{{ idleOverrides }} Instance override{{ idleOverrides === 1 ? '' : 's' }}</p>
      </Frame>
    </div>

    <div class="grid grid-cols-2 gap-3 xl:grid-cols-4" data-testid="dashboard-observability-kpis">
      <Frame class="p-4">
        <p class="text-[length:var(--font-size-table-header)] font-medium uppercase tracking-[.1em] text-muted">Tokens · {{ selectedRangeLabel }}</p>
        <div class="mt-2 flex items-baseline gap-1.5">
          <strong class="font-[var(--font-heading)] text-[length:var(--font-size-h3)] font-semibold">{{ tokenTotalK }}</strong>
          <span class="text-sm text-muted">k total</span>
        </div>
        <p class="mt-1 text-xs text-muted">{{ gatewaySummary?.prompt_tokens || 0 }} prompt · {{ gatewaySummary?.generated_tokens || 0 }} generated</p>
      </Frame>

      <Frame class="p-4">
        <p class="text-[length:var(--font-size-table-header)] font-medium uppercase tracking-[.1em] text-muted" title="Requests currently running or waiting at the inference gateway.">In flight</p>
        <div class="mt-2 flex items-baseline gap-1.5">
          <strong class="font-[var(--font-heading)] text-[length:var(--font-size-h3)] font-semibold">{{ currentActivity?.active || 0 }}</strong>{{ ' ' }}<span class="text-sm text-muted">active</span>
        </div>
        <p class="mt-1 text-xs text-muted">{{ currentActivity?.queued || 0 }} queued · {{ streamingCount }} streaming</p>
      </Frame>

      <Frame class="p-4">
        <p class="text-[length:var(--font-size-table-header)] font-medium uppercase tracking-[.1em] text-muted" title="Requests that started an unloaded Instance in the selected range. Load, unload, then load again counts as two cold starts.">Autoloads · {{ selectedRangeLabel }}</p>
        <div class="mt-2 flex items-baseline gap-1.5">
          <strong class="font-[var(--font-heading)] text-[length:var(--font-size-h3)] font-semibold">{{ summary?.lifecycle?.autoloads || 0 }}</strong>
          <span class="text-sm text-muted">cold starts</span>
        </div>
        <p class="mt-1 text-xs text-muted">{{ summary?.lifecycle?.failed_starts || 0 }} failed start · {{ formatDuration(meanAutoloadDuration) }} mean load</p>
      </Frame>

      <Frame class="p-4" data-testid="dashboard-success-rate">
        <p class="text-[length:var(--font-size-table-header)] font-medium uppercase tracking-[.1em] text-muted">Success rate · {{ selectedRangeLabel }}</p>
        <div class="mt-2 flex items-baseline gap-1.5">
          <strong class="font-[var(--font-heading)] text-[length:var(--font-size-h3)] font-semibold">{{ successRate === null ? '—' : successRate }}</strong>
          <span v-if="successRate !== null" class="text-sm text-muted">%</span>
        </div>
        <p v-if="(gatewaySummary?.requests || 0) > 0" class="mt-1 text-xs text-muted">{{ gatewaySummary?.successes || 0 }} ok · {{ gatewaySummary?.errors || 0 }} errors</p>
        <p v-else class="mt-1 text-xs text-muted">No requests in {{ selectedRangeLabel }}</p>
      </Frame>
    </div>

    <section class="space-y-3" data-testid="dashboard-vram-allocation">
      <div>
        <p class="text-[length:var(--font-size-table-header)] font-medium uppercase tracking-[.1em] text-muted">VRAM ALLOCATION</p>
        <h2 class="mt-1 text-[length:var(--font-size-screen-title)]">VRAM allocation</h2>
        <p class="mt-1 text-xs text-muted">Manager-attributed Instance VRAM; Free is device capacity not attributed to managed Instances.</p>
      </div>

      <UEmpty
        v-if="!hardware.gpus.length"
        variant="naked"
        title="No GPU telemetry available"
        description="GPU allocation will appear when CUDA or ROCm devices are detected."
      />

      <div v-else class="grid gap-3 lg:grid-cols-2">
        <Frame v-for="gpu in hardware.gpus" :key="gpu.id" class="flex h-full flex-col p-4" :data-testid="`gpu-card-${gpu.id}`">
          <div class="flex flex-wrap items-start justify-between gap-3 text-xs">
            <span class="font-mono text-muted">{{ gpu.id }} · {{ gpu.name }}</span>
            <span class="ml-auto font-mono text-highlighted">{{ formatBytes(Math.min(Math.max(gpu.used_bytes, 0), Math.max(gpu.total_bytes, 0))) }} / {{ formatBytes(gpu.total_bytes) }} · {{ Math.round(gpu.utilization_pct) }}% GPU util</span>
          </div>

          <div :data-testid="`gpu-progress-${gpu.id}`" class="mt-4">
            <div class="flex h-[6px] w-full overflow-hidden bg-[var(--neutral-400)]" :aria-label="`${gpu.id} VRAM allocation`">
              <div
                v-for="segment in gpuSegments(gpu)"
                :key="segment.label"
                :style="{ width: `${segment.percent}%`, background: segment.color }"
                :title="`${segment.label}: ${formatBytes(segment.bytes)}`"
              />
            </div>
            <div class="mt-3 space-y-1.5">
              <div
                v-for="segment in gpuSegments(gpu)"
                :key="`legend-${segment.label}`"
                class="flex items-center justify-between gap-4 font-mono text-xs"
                :data-vram-label="segment.label"
                :data-vram-color="segment.colorKey"
              >
                <span class="flex min-w-0 items-center gap-2 text-muted">
                  <span class="size-2 shrink-0" :style="{ background: segment.color }" />
                  <span class="truncate">{{ segment.label }}</span>
                </span>
                <span class="shrink-0 text-highlighted">{{ formatBytes(segment.bytes) }}</span>
              </div>
            </div>
          </div>
        </Frame>
      </div>

      <Frame v-if="hardware.ram_total_bytes > 0" class="p-4" data-testid="dashboard-host-ram">
        <div class="grid gap-3 lg:grid-cols-[minmax(0,340px)_auto] lg:items-end">
          <div>
            <p class="text-[length:var(--font-size-table-header)] font-medium uppercase tracking-[.1em] text-muted">Host RAM</p>
            <div class="mt-2 h-2 w-full bg-[var(--neutral-400)]">
              <div class="h-full bg-[var(--color-accent)]" :style="{ width: `${ramPercent}%` }" />
            </div>
            <p class="mt-2 font-mono text-xs text-highlighted">{{ formatBytes(ramUsed) }} / {{ formatBytes(hardware.ram_total_bytes) }}</p>
          </div>
          <p class="text-xs text-muted lg:text-right">Sampled with GPU telemetry; loads are refused above the host memory headroom.</p>
        </div>
      </Frame>
    </section>

    <div class="grid gap-3 xl:grid-cols-[2fr_1fr]">
      <Frame data-testid="dashboard-gateway-traffic" class="min-w-0 overflow-hidden" :class="attention.length ? 'order-2 xl:order-1' : 'order-1'">
        <div class="flex items-center justify-between gap-3 border-b border-[var(--color-divider)] px-4 py-3">
          <div>
            <p class="text-[length:var(--font-size-table-header)] font-medium uppercase tracking-[.1em] text-muted">GATEWAY TRAFFIC</p>
            <h3 class="mt-1 text-xl">Gateway traffic · last {{ selectedRangeLabel }}</h3>
          </div>
          <div class="flex items-center gap-2">
            <span class="font-mono text-xs text-muted">{{ recentRequests.length }} shown</span>
            <AppButton to="/logs" intent="ghost" size="xs" data-testid="open-request-logs">Request logs</AppButton>
          </div>
        </div>

        <div v-if="!recentRequests.length" class="flex flex-col items-center gap-3 px-4 py-8 text-center">
          <UEmpty variant="naked" title="No recent gateway traffic" description="Requests in the selected history window will appear here." />
          <AppButton to="/playground" intent="secondary" size="xs">Send a test request</AppButton>
        </div>
        <div v-else class="overflow-x-auto" role="region" tabindex="0" aria-label="Gateway traffic. Scroll horizontally for request metrics and result.">
          <p class="border-b border-[var(--color-divider)] px-4 py-2 text-xs text-muted md:hidden">Scroll horizontally for request metrics and result.</p>
          <UTable :data="recentRequests" :columns="gatewayColumns" class="min-w-[860px] w-full">
          <template #started_at-cell="{ row }">
            <NuxtLink :to="requestDetailTarget(row.original)" class="font-mono text-xs hover:text-[var(--color-accent)] hover:underline">{{ formatTime(row.original.started_at) }}</NuxtLink>
          </template>
          <template #instance_id-cell="{ row }">
            <div>
              <NuxtLink :to="instanceDetailTarget(row.original.instance_id)" class="font-mono text-xs text-[var(--color-accent)] hover:underline">{{ requestInstanceLabel(row.original) }}</NuxtLink>
              <div class="mt-0.5 font-mono text-xs text-muted">{{ row.original.streaming ? 'stream' : 'unary' }}</div>
            </div>
          </template>
          <template #endpoint-cell="{ row }"><span class="font-mono text-xs text-muted">{{ row.original.endpoint }}</span></template>
          <template #api_key-cell="{ row }"><span class="text-xs">{{ requestKey(row.original) }}</span></template>
          <template #total_tokens-cell="{ row }"><span class="font-mono text-xs">{{ row.original.total_tokens ?? '—' }}</span></template>
          <template #ttft_ms-cell="{ row }"><span class="font-mono text-xs">{{ formatDuration(row.original.ttft_ms) }}</span></template>
          <template #duration_ms-cell="{ row }"><span class="font-mono text-xs">{{ formatDuration(row.original.duration_ms) }}</span></template>
          <template #tokens_per_second-cell="{ row }"><span class="font-mono text-xs">{{ formatRate(row.original.tokens_per_second) }}</span></template>
          <template #result-cell="{ row }">
            <div class="flex flex-wrap gap-1">
              <StatusTag :variant="requestStatusVariant(row.original)">{{ row.original.status_code || row.original.result || '—' }}</StatusTag>
              <StatusTag v-if="row.original.autoloaded" variant="pending">autoload</StatusTag>
            </div>
          </template>
          </UTable>
        </div>
      </Frame>

      <Frame id="needs-attention" data-testid="dashboard-attention" class="p-4" :class="attention.length ? 'order-1 xl:order-2' : 'order-2'">
        <div class="mb-4">
          <p class="text-[length:var(--font-size-table-header)] font-medium uppercase tracking-[.1em] text-muted">OPERATIONS</p>
          <h3 class="mt-1 text-xl">Needs attention</h3>
        </div>
        <p v-if="!attention.length" class="py-3 text-sm text-muted" data-testid="dashboard-attention-empty">Nothing needs attention.</p>
        <div v-else class="space-y-3">
          <div v-for="item in attention" :key="item.key" class="border-l-2 border-[var(--color-accent)] pl-3">
            <div class="flex items-start justify-between gap-3">
              <div class="min-w-0">
                <p class="text-[length:var(--font-size-h6)] font-medium text-highlighted">{{ item.title }}</p>
                <p class="mt-1 text-xs text-muted">{{ item.detail }}</p>
              </div>
              <AppButton v-if="item.to" :to="item.to" intent="ghost" size="xs">Review</AppButton>
            </div>
          </div>
        </div>
      </Frame>
    </div>
  </div>
</template>