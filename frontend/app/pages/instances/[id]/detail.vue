<script setup lang="ts">
import type { Instance, RuntimeTelemetry } from '~/composables/useManager'
import { startupBackoffMessage } from '~/utils/startupBackoff'

type LlamaMetrics = {
  prompt_tokens_total?: number
  prompt_seconds_total?: number
  prompt_tokens_per_second?: number
  predicted_tokens_total?: number
  predicted_seconds_total?: number
  predicted_tokens_per_second?: number
  requests_processing?: number
  requests_deferred?: number
  context_tokens_max?: number
  decode_total?: number
  busy_slots_per_decode?: number
  spec_draft_tokens_total?: number
  spec_accepted_tokens_total?: number
  spec_drafts_total?: number
  spec_accepted_tokens_per_position?: Record<string, number>
  spec_acceptance_rate_pct?: number
}
type DetailTelemetry = RuntimeTelemetry & { llama_metrics?: LlamaMetrics }
type SeriesPoint = { timestamp: number; value: number }
type SeriesResponse = { metric: string; bucket_seconds: number; items: SeriesPoint[] }
type SettingValue<T> = { value: T; source?: string; editable?: boolean }
type GeneralSettings = { observability_retention_days?: SettingValue<number> }
type RangeOption = { label: string; value: number; disabled?: boolean }
type ChartPoint = { timestamp: number; value: number | null }
type Companion = { kind: 'Vision projector' | 'MTP draft model'; path: string; flag: string; size?: number }
type EffectiveConfig = { effective?: { values?: Record<string, string>; sources?: Record<string, string> } }
type InspectionDependency = { kind: string; total_bytes?: number }
type ModelInspection = { dependencies?: InspectionDependency[] }
type VRAMSegment = { label: string; bytes: number; percent: number; token: string }
type RuntimeWithTime = ReturnType<ReturnType<typeof useManager>['runtimeForInstance']> & { started_at?: string; ready_at?: string }

const manager = useManager()
const route = useRoute()
const instanceSlug = computed(() => String(route.params.id || ''))
const loading = ref(true)
const historyLoading = ref(false)
const historyError = ref('')
const pending = ref('')
const error = ref('')
const settings = ref<GeneralSettings | null>(null)
const selectedWindow = ref(900)
const companions = ref<Companion[]>([])
const confirmation = ref<{ request: (options: Record<string, string>) => Promise<boolean> } | null>(null)
let historySequence = 0

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
const activeRuntimeStates = new Set(['STARTING', 'LOADING', 'READY', 'DRAINING', 'STOPPING'])
const requestSeries = ref<SeriesPoint[]>([])
const promptSeries = ref<SeriesPoint[]>([])
const generatedSeries = ref<SeriesPoint[]>([])
const latencyAverageSeries = ref<SeriesPoint[]>([])
const latencyP50Series = ref<SeriesPoint[]>([])
const latencyP95Series = ref<SeriesPoint[]>([])
const contextSeries = ref<SeriesPoint[]>([])

const instance = computed(() => manager.instances.value.find(item => item.slug === instanceSlug.value))
const instanceID = computed(() => instance.value?.id || '')
const model = computed(() => instance.value ? manager.models.value.find(item => item.id === instance.value!.model_id) : undefined)
const runtime = computed(() => (instance.value ? manager.runtimeForInstance(instance.value) : undefined) as RuntimeWithTime | undefined)
const telemetry = computed(() => instance.value ? manager.telemetryForInstance(instance.value) as DetailTelemetry | undefined : undefined)
const llama = computed(() => telemetry.value?.llama_metrics)
const backoffMessage = computed(() => startupBackoffMessage(runtime.value))
const hardware = computed(() => manager.observabilityLive.value?.hardware)
const fleetTelemetry = computed(() => {
  const live = manager.observabilityLive.value?.telemetry || []
  return live.length ? live : Object.values(manager.runtimeTelemetry.value)
})
const specPositions = computed(() => Object.entries(llama.value?.spec_accepted_tokens_per_position || {}).sort((a, b) => Number(a[0]) - Number(b[0])))
const selectedRange = computed(() => rangeOptions.find(option => option.value === selectedWindow.value) ?? rangeOptions[2])
const selectedRangeLabel = computed(() => selectedRange.value.label)
const retentionDays = computed(() => {
  const value = Number(settings.value?.observability_retention_days?.value ?? 30)
  return Number.isFinite(value) && value > 0 ? value : 30
})
const retentionSeconds = computed(() => retentionDays.value * 86400)
const selectableRanges = computed<RangeOption[]>(() => rangeOptions.map(option => ({ ...option, disabled: option.value > retentionSeconds.value })))
const bucketSeconds = computed(() => {
  if (selectedWindow.value <= 1800) return 60
  if (selectedWindow.value <= 3600) return 120
  if (selectedWindow.value <= 21600) return 300
  if (selectedWindow.value <= 43200) return 600
  if (selectedWindow.value <= 86400) return 900
  return 3600
})

const completedRequests = computed(() => requestSeries.value.reduce((total, point) => total + point.value, 0))
const tokenTotal = computed(() => promptSeries.value.reduce((total, point) => total + point.value, 0) + generatedSeries.value.reduce((total, point) => total + point.value, 0))
const averageLatency = computed(() => {
  const counts = new Map(requestSeries.value.map(point => [point.timestamp, point.value]))
  let weighted = 0
  let count = 0
  for (const point of latencyAverageSeries.value) {
    const requests = counts.get(point.timestamp) || 0
    weighted += point.value * requests
    count += requests
  }
  return count > 0 ? weighted / count : undefined
})
const contextUsed = computed(() => llama.value?.context_tokens_max)
const contextPercent = computed(() => contextUsed.value !== undefined && model.value?.context_length
  ? clampPercent(contextUsed.value / model.value.context_length * 100)
  : undefined)
const runtimeStartedAt = computed(() => runtime.value?.started_at ? new Date(runtime.value.started_at) : undefined)
const runtimeUptime = computed(() => runtimeStartedAt.value && Number.isFinite(runtimeStartedAt.value.getTime()) && activeRuntimeStates.has(runtime.value?.state || '')
  ? Math.max(0, Date.now() - runtimeStartedAt.value.getTime())
  : undefined)
const isRunning = computed(() => activeRuntimeStates.has(runtime.value?.state || 'UNLOADED'))

const placementIDs = computed(() => {
  if (telemetry.value?.gpu_devices?.length) return telemetry.value.gpu_devices
  const attributed = telemetry.value?.gpus?.map(gpu => gpu.device_id).filter(Boolean) || []
  if (attributed.length) return attributed
  return instance.value?.gpu_devices || []
})
const relevantGPUs = computed(() => {
  const ids = new Set(placementIDs.value)
  if (!ids.size) return []
  return (hardware.value?.gpus || []).filter(gpu => ids.has(gpu.id))
})
const allocationGPUs = computed(() => hardware.value?.gpus || [])
const relevantVRAMTotal = computed(() => relevantGPUs.value.reduce((total, gpu) => total + Math.max(0, gpu.total_bytes), 0))
const currentVRAM = computed(() => {
  const values = telemetry.value?.gpus
    ?.map(gpu => gpu.vram_used_bytes)
    .filter((value): value is number => value !== undefined && Number.isFinite(value) && value >= 0) || []
  return values.length ? values.reduce((sum, value) => sum + value, 0) : undefined
})
const currentVRAMPercent = computed(() => currentVRAM.value !== undefined && relevantVRAMTotal.value > 0
  ? clampPercent(currentVRAM.value / relevantVRAMTotal.value * 100)
  : undefined)
const processGPUValues = computed(() => telemetry.value?.gpus
  ?.map(gpu => gpu.utilization_pct)
  .filter((value): value is number => value !== undefined && Number.isFinite(value) && value >= 0) || [])
const currentGPUUsage = computed(() => {
  if (processGPUValues.value.length) return processGPUValues.value.reduce((sum, value) => sum + value, 0) / processGPUValues.value.length
  const fallback = telemetry.value?.gpu_utilization_pct
  return fallback !== undefined && Number.isFinite(fallback) && fallback >= 0 ? fallback : undefined
})
const gpuUsageLabel = computed(() => processGPUValues.value.length ? 'Instance GPU usage' : 'Global GPU usage')

const timeline = computed(() => {
  const bucketMS = bucketSeconds.value * 1000
  const end = Math.floor(Date.now() / bucketMS) * bucketMS
  const buckets = Math.ceil(selectedWindow.value / bucketSeconds.value)
  return Array.from({ length: buckets + 1 }, (_, index) => end - (buckets - index) * bucketMS)
})
function zeroFilled(items: SeriesPoint[], normalizePerMinute = false): ChartPoint[] {
  const values = new Map(items.map(point => [point.timestamp, point.value]))
  const divisor = normalizePerMinute ? Math.max(1, bucketSeconds.value / 60) : 1
  return timeline.value.map(timestamp => ({ timestamp, value: (values.get(timestamp) || 0) / divisor }))
}
function gapFilled(items: SeriesPoint[], transform: (value: number) => number | null = value => value): ChartPoint[] {
  const values = new Map(items.map(point => [point.timestamp, point.value]))
  return timeline.value.map(timestamp => ({ timestamp, value: values.has(timestamp) ? transform(values.get(timestamp)!) : null }))
}
const requestChart = computed(() => zeroFilled(requestSeries.value, true))
const promptChart = computed(() => zeroFilled(promptSeries.value, true))
const generatedChart = computed(() => zeroFilled(generatedSeries.value, true))
const latencyP50Chart = computed(() => gapFilled(latencyP50Series.value))
const latencyP95Chart = computed(() => gapFilled(latencyP95Series.value))
const contextChart = computed(() => gapFilled(contextSeries.value, value => model.value?.context_length
  ? clampPercent(value / model.value.context_length * 100)
  : null))

const instanceColorTokens = ['var(--accent-500)', 'var(--accent-600)', 'var(--accent-700)', 'var(--accent-400)', 'var(--accent-800)']
const instanceColorByID = computed(() => {
  const ids = [...new Set(manager.instances.value.map(item => item.id))].sort()
  return new Map(ids.map((id, index) => [id, instanceColorTokens[index % instanceColorTokens.length]!]))
})
function gpuAssignments(gpuID: string) {
  return fleetTelemetry.value
    .map(sample => {
      const direct = sample.gpus?.find(gpu => gpu.device_id === gpuID)?.vram_used_bytes
      if (direct !== undefined && Number.isFinite(direct) && direct > 0) return { id: sample.instance_id, bytes: direct }
      if (sample.gpu_devices?.length === 1 && sample.gpu_devices[0] === gpuID && sample.vram_used_bytes !== undefined && Number.isFinite(sample.vram_used_bytes) && sample.vram_used_bytes > 0) {
        return { id: sample.instance_id, bytes: sample.vram_used_bytes }
      }
      return undefined
    })
    .filter((item): item is { id: string; bytes: number } => Boolean(item))
    .sort((a, b) => a.id === instanceID.value ? -1 : b.id === instanceID.value ? 1 : a.id.localeCompare(b.id))
}
function vramSegments(gpu: NonNullable<typeof hardware.value>['gpus'][number]): VRAMSegment[] {
  const total = Math.max(0, gpu.total_bytes)
  const assignments = gpuAssignments(gpu.id)
  const segments: VRAMSegment[] = []
  let remaining = total
  for (const assignment of assignments) {
    const bytes = Math.min(Math.max(0, assignment.bytes), remaining)
    if (!bytes) continue
    segments.push({
      label: assignment.id === instanceID.value ? `${assignment.id} (this Instance)` : assignment.id,
      bytes,
      percent: total ? bytes / total * 100 : 0,
      token: assignment.id === instanceID.value ? 'var(--color-accent)' : (instanceColorByID.value.get(assignment.id) || 'var(--accent-600)')
    })
    remaining = Math.max(0, remaining - bytes)
  }
  const attributed = segments.reduce((sum, segment) => sum + segment.bytes, 0)
  const deviceUsed = Math.min(total, Math.max(0, gpu.used_bytes))
  const unattributed = Math.min(Math.max(0, deviceUsed - attributed), remaining)
  if (unattributed > 0) {
    segments.push({ label: 'Unattributed process memory', bytes: unattributed, percent: total ? unattributed / total * 100 : 0, token: 'var(--neutral-700)' })
    remaining = Math.max(0, remaining - unattributed)
  }
  segments.push({ label: 'Free', bytes: remaining, percent: total ? remaining / total * 100 : 0, token: 'var(--neutral-500)' })
  return segments
}

function statusVariant(state?: string): 'ready' | 'pending' | 'neutral' | 'failed' {
  if (state === 'READY') return 'ready'
  if (state === 'FAILED' || state === 'CANCELLED') return 'failed'
  if (state === 'STARTING' || state === 'LOADING' || state === 'DRAINING' || state === 'STOPPING') return 'pending'
  return 'neutral'
}
function clampPercent(value: number) { return Math.max(0, Math.min(100, value)) }
function formatNumber(value?: number, digits = 0) {
  if (value === undefined || !Number.isFinite(value)) return '—'
  return value.toLocaleString(undefined, { minimumFractionDigits: digits, maximumFractionDigits: digits })
}
function formatRate(value?: number) { return value === undefined || !Number.isFinite(value) ? '—' : `${formatNumber(value, 1)} tok/s` }
function formatPercent(value?: number) { return value === undefined || !Number.isFinite(value) ? '—' : `${formatNumber(value, 1)}%` }
function formatSeconds(value?: number) { return value === undefined || !Number.isFinite(value) ? '—' : `${formatNumber(value, 2)} s` }
function formatDuration(value?: number) {
  if (value === undefined || !Number.isFinite(value)) return '—'
  if (value < 1000) return `${Math.round(value)} ms`
  return `${(value / 1000).toFixed(value >= 10_000 ? 1 : 2)} s`
}
function formatUptime(milliseconds?: number) {
  if (milliseconds === undefined || !Number.isFinite(milliseconds)) return '—'
  const seconds = Math.floor(milliseconds / 1000)
  const hours = Math.floor(seconds / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  if (hours) return `${hours}h ${minutes}m`
  if (minutes) return `${minutes}m ${seconds % 60}s`
  return `${seconds}s`
}
function formatBytes(value?: number) {
  if (value === undefined || !Number.isFinite(value) || value < 0) return '—'
  if (value === 0) return '0 B'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let amount = value
  let unit = 0
  while (amount >= 1024 && unit < units.length - 1) { amount /= 1024; unit++ }
  return `${amount >= 10 || unit === 0 ? amount.toFixed(0) : amount.toFixed(1)} ${units[unit]}`
}
function formatLocal(value?: string) {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isFinite(date.getTime()) ? date.toLocaleString() : '—'
}
function contextHighWatermark() {
  if (llama.value?.context_tokens_max === undefined) return '—'
  const used = formatNumber(llama.value.context_tokens_max)
  return model.value?.context_length ? `${used} / ${formatNumber(model.value.context_length)}` : used
}

function historyPath(metric: string) {
  const query = new URLSearchParams({ metric, window_seconds: String(selectedWindow.value), bucket_seconds: String(bucketSeconds.value), instance_id: instanceID.value })
  return `/api/v1/observability/timeseries?${query.toString()}`
}
async function loadHistory() {
  if (!instance.value) return
  const sequence = ++historySequence
  historyLoading.value = true
  historyError.value = ''
  try {
    const metrics = ['requests', 'prompt_tokens', 'generated_tokens', 'latency', 'latency_p50', 'latency_p95', 'instance_context_tokens_max']
    const results = await Promise.all(metrics.map(metric => manager.request<SeriesResponse>(historyPath(metric))))
    if (sequence !== historySequence) return
    ;[requestSeries.value, promptSeries.value, generatedSeries.value, latencyAverageSeries.value, latencyP50Series.value, latencyP95Series.value, contextSeries.value] = results.map(result => result.items || [])
  } catch (value: any) {
    if (sequence === historySequence) historyError.value = value?.data?.error || value?.message || 'Unable to load Instance history'
  } finally {
    if (sequence === historySequence) historyLoading.value = false
  }
}
async function loadCompanions() {
  companions.value = []
  if (!model.value) return
  try {
    const config = await manager.request<EffectiveConfig>(`/api/v1/llamacpp/config?model_id=${encodeURIComponent(model.value.id)}`)
    const values = config?.effective?.values || {}
    const sources = config?.effective?.sources || {}
    const helpers: Array<Companion & { dependencyKind: string }> = []
    const modelOwned = (key: string) => sources[key] === 'model' || sources[key] === 'detected'
    if (values.mmproj && modelOwned('mmproj')) helpers.push({ kind: 'Vision projector', path: values.mmproj, flag: '--mmproj', dependencyKind: 'mmproj' })
    if (values['spec-draft-model'] && modelOwned('spec-draft-model')) helpers.push({ kind: 'MTP draft model', path: values['spec-draft-model'], flag: '--spec-draft-model', dependencyKind: 'mtp' })
    if (!helpers.length) return
    let inspection: ModelInspection | undefined
    try {
      inspection = await manager.request<ModelInspection>('/api/v1/models/inspect', { method: 'POST', body: { gguf_path: model.value.gguf_path } })
    } catch {
      // A configured helper is still meaningful when optional GGUF inspection fails.
    }
    companions.value = helpers.map(helper => ({
      kind: helper.kind,
      path: helper.path,
      flag: helper.flag,
      size: inspection?.dependencies?.find(dependency => dependency.kind === helper.dependencyKind)?.total_bytes
    }))
  } catch {
    companions.value = []
  }
}

async function runtimeAction(operation: 'start' | 'stop' | 'kill') {
  if (!instance.value) return
  if (operation === 'start' && instance.value.eviction_enabled) {
    const confirmed = await confirmation.value?.request({ title: 'Launch Instance', description: 'Launching this Instance may stop other idle Instances if resource-pressure eviction is required.', confirmLabel: 'Launch Instance', confirmTone: 'default' })
    if (!confirmed) return
  }
  if (operation === 'kill') {
    const confirmed = await confirmation.value?.request({ title: 'Kill Instance', description: 'Kill this Instance immediately? Active requests may fail.', confirmLabel: 'Kill Instance', confirmTone: 'destructive' })
    if (!confirmed) return
  }
  pending.value = operation
  error.value = ''
  try {
    await manager.request(`/api/v1/instances/${encodeURIComponent(instance.value.slug)}/${operation}`, { method: 'POST' })
    await manager.refresh()
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || `Unable to ${operation} Instance`
  } finally {
    pending.value = ''
  }
}
async function removeInstance() {
  if (!instance.value) return
  const confirmed = await confirmation.value?.request({ title: 'Delete Instance', description: `Delete Instance “${instance.value.name}”? The registered Model and GGUF file are kept.`, confirmLabel: 'Delete Instance', confirmTone: 'destructive' })
  if (!confirmed) return
  pending.value = 'delete'
  error.value = ''
  try {
    await manager.request(`/api/v1/instances/${encodeURIComponent(instance.value.slug)}`, { method: 'DELETE' })
    await manager.refresh()
    await navigateTo('/instances')
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to delete Instance'
  } finally {
    pending.value = ''
  }
}
async function loadPage() {
  try {
    if (!instance.value) await manager.refresh()
    if (!instance.value) {
      error.value = `Instance “${instanceSlug.value}” was not found.`
      return
    }
    if (error.value.includes('was not found')) error.value = ''
    try { settings.value = await manager.request<GeneralSettings>('/api/v1/settings/general') } catch { settings.value = null }
    if (selectedWindow.value > retentionSeconds.value) selectedWindow.value = [...rangeOptions].reverse().find(option => option.value <= retentionSeconds.value)?.value ?? 900
    await Promise.all([loadHistory(), loadCompanions()])
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to load Instance details'
  } finally {
    loading.value = false
  }
}
function setSelectedWindow(value: number) { selectedWindow.value = value }
watch(selectedWindow, (next, previous) => {
  if (next === previous || loading.value || !instance.value) return
  if (next > retentionSeconds.value) {
    selectedWindow.value = [...rangeOptions].reverse().find(option => option.value <= retentionSeconds.value)?.value ?? 900
    return
  }
  void loadHistory()
})
watch([instanceSlug, () => instance.value?.id], () => {
  loading.value = true
  if (error.value.includes('was not found')) error.value = ''
  void loadPage()
}, { immediate: true })
defineExpose({ setSelectedWindow })
</script>

<template>
  <div class="space-y-8">
    <div class="flex flex-wrap items-start justify-between gap-5">
      <div class="min-w-0 flex-1">
        <p class="text-[length:var(--font-size-kicker)] font-semibold uppercase tracking-[.18em] text-[var(--accent-700)]">INSTANCE DETAIL</p>
        <div class="mt-2 flex flex-wrap items-center gap-3">
          <h1 class="text-2xl font-semibold text-[var(--color-text)]">{{ instance?.name || instanceSlug }}</h1>
          <StatusTag v-if="instance && !error.includes('was not found')" :variant="statusVariant(runtime?.state)">{{ runtime?.state || 'UNLOADED' }}</StatusTag>
        </div>
        <p class="mt-2 max-w-3xl text-sm text-[var(--neutral-800)]">Live runtime resources and llama.cpp performance for this Instance.</p>
      </div>
      <div v-if="instance" class="flex flex-wrap items-center justify-end gap-2">
        <AppButton to="/instances" intent="secondary">Back to Instances</AppButton>
        <AppButton :to="`/instances/${encodeURIComponent(instance.slug)}/edit`" intent="secondary">Edit</AppButton>
        <AppButton intent="secondary" tone="destructive" :loading="pending === 'kill'" @click="runtimeAction('kill')">Kill</AppButton>
        <AppButton intent="secondary" tone="destructive" :loading="pending === 'delete'" @click="removeInstance">Delete</AppButton>
        <AppButton v-if="!isRunning" intent="primary" :loading="pending === 'start'" @click="runtimeAction('start')">Launch</AppButton>
        <AppButton v-else intent="secondary" :loading="pending === 'stop'" @click="runtimeAction('stop')">Stop</AppButton>
      </div>
    </div>

    <Frame v-if="error" class="p-3" data-testid="instance-detail-error">
      <div class="flex flex-wrap items-start gap-2">
        <StatusTag variant="failed">Instance detail unavailable</StatusTag>
        <p class="min-w-0 flex-1 text-xs text-muted">{{ error }}</p>
      </div>
    </Frame>
    <div v-if="loading" class="grid gap-4 md:grid-cols-2 xl:grid-cols-4"><USkeleton v-for="n in 4" :key="n" class="h-36 w-full" /></div>

    <template v-else-if="instance">
      <section class="grid gap-4 md:grid-cols-2 xl:grid-cols-4" data-testid="instance-detail-summary">
        <Frame class="p-4">
          <p class="text-[length:var(--font-size-kicker)] uppercase tracking-[.12em] text-[var(--neutral-700)]">Status</p>
          <p class="mt-3 font-mono text-xl font-semibold tabular-nums">{{ runtime?.state || 'UNLOADED' }}</p>
          <div class="mt-3 space-y-1 text-[length:var(--font-size-table-header)] text-[var(--neutral-800)]"><p>Uptime <span class="float-right font-mono tabular-nums text-[var(--color-text)]">{{ formatUptime(runtimeUptime) }}</span></p><p>Started <span class="float-right font-mono tabular-nums text-[var(--color-text)]">{{ runtime?.started_at ? formatLocal(runtime.started_at) : '—' }}</span></p></div>
        </Frame>
        <Frame class="p-4">
          <p class="text-[length:var(--font-size-kicker)] uppercase tracking-[.12em] text-[var(--neutral-700)]">Requests · {{ selectedRangeLabel }}</p>
          <p class="mt-3 font-mono text-xl font-semibold tabular-nums">{{ formatNumber(completedRequests) }}</p>
          <div class="mt-3 space-y-1 text-[length:var(--font-size-table-header)] text-[var(--neutral-800)]"><p>Tokens <span class="float-right font-mono tabular-nums text-[var(--color-text)]">{{ formatNumber(tokenTotal) }}</span></p><p>Average latency <span class="float-right font-mono tabular-nums text-[var(--color-text)]">{{ formatDuration(averageLatency) }}</span></p></div>
        </Frame>
        <Frame class="p-4">
          <p class="text-[length:var(--font-size-kicker)] uppercase tracking-[.12em] text-[var(--neutral-700)]">Context</p>
          <p class="mt-3 font-mono text-xl font-semibold tabular-nums">{{ contextHighWatermark() }}</p>
          <div class="mt-3 space-y-1 text-[length:var(--font-size-table-header)] text-[var(--neutral-800)]"><p>Utilization <span class="float-right font-mono tabular-nums text-[var(--color-text)]">{{ formatPercent(contextPercent) }}</span></p><p>Maximum context <span class="float-right font-mono tabular-nums text-[var(--color-text)]">{{ model?.context_length ? formatNumber(model.context_length) : '—' }}</span></p></div>
        </Frame>
        <Frame class="p-4">
          <p class="text-[length:var(--font-size-kicker)] uppercase tracking-[.12em] text-[var(--neutral-700)]">VRAM</p>
          <p class="mt-3 font-mono text-xl font-semibold tabular-nums">{{ formatBytes(currentVRAM) }} / {{ relevantVRAMTotal ? formatBytes(relevantVRAMTotal) : '—' }}</p>
          <div class="mt-3 space-y-1 text-[length:var(--font-size-table-header)] text-[var(--neutral-800)]"><p>Utilization <span class="float-right font-mono tabular-nums text-[var(--color-text)]">{{ formatPercent(currentVRAMPercent) }}</span></p><p>Placement <span class="float-right font-mono text-[var(--color-text)]">{{ placementIDs.length ? placementIDs.join(', ') : '—' }}</span></p></div>
        </Frame>
      </section>

      <div class="flex flex-wrap items-center justify-between gap-3">
        <div><h2 class="text-base font-semibold">Performance history</h2><p class="mt-1 text-xs text-[var(--neutral-700)]">Server-bucketed history for this Instance only.</p></div>
        <div class="flex items-center gap-2"><span v-if="historyLoading" class="text-[length:var(--font-size-kicker)] uppercase tracking-[.12em] text-[var(--neutral-700)]">Refreshing</span><USelect v-model="selectedWindow" data-testid="instance-detail-history-range" aria-label="Instance history range" :items="selectableRanges" value-key="value" label-key="label" size="sm" class="min-w-28" /></div>
      </div>

      <Frame v-if="historyError" class="p-3" data-testid="instance-detail-history-error">
        <div class="flex flex-wrap items-center gap-2">
          <StatusTag variant="failed">Performance history unavailable</StatusTag>
          <p class="min-w-0 flex-1 text-xs text-muted">{{ historyError }}</p>
          <AppButton intent="secondary" size="xs" :loading="historyLoading" @click="loadHistory">Retry history</AppButton>
        </div>
      </Frame>

      <section class="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
        <Frame class="p-4" data-testid="instance-detail-chart-requests">
          <div class="mb-3"><h3 class="text-sm font-semibold">Requests per minute</h3><p class="mt-1 text-[length:var(--font-size-kicker)] text-[var(--neutral-700)]">Completed gateway requests, including errors.</p></div>
          <InstanceHistoryChart :series="[{ label: 'Requests', points: requestChart, token: 'accent' }]" value-format="number" />
        </Frame>
        <Frame class="p-4" data-testid="instance-detail-chart-tokens">
          <div class="mb-3"><h3 class="text-sm font-semibold">Tokens per minute</h3><p class="mt-1 text-[length:var(--font-size-kicker)] text-[var(--neutral-700)]">Prompt/input and generated/output traffic.</p></div>
          <InstanceHistoryChart :series="[{ label: 'Prompt / input', points: promptChart, token: 'accent' }, { label: 'Generated / output', points: generatedChart, token: 'accent-strong' }]" value-format="tokens" />
        </Frame>
        <Frame class="p-4" data-testid="instance-detail-chart-latency">
          <div class="mb-3"><h3 class="text-sm font-semibold">Request latency</h3><p class="mt-1 text-[length:var(--font-size-kicker)] text-[var(--neutral-700)]">End-to-end p50 / p95 for completed requests.</p></div>
          <InstanceHistoryChart :series="[{ label: 'p50', points: latencyP50Chart, token: 'accent' }, { label: 'p95', points: latencyP95Chart, token: 'accent-strong' }]" value-format="duration" />
        </Frame>
        <Frame class="p-4" data-testid="instance-detail-chart-context">
          <div class="mb-3"><h3 class="text-sm font-semibold">Context utilization</h3><p class="mt-1 text-[length:var(--font-size-kicker)] text-[var(--neutral-700)]">Historical llama.cpp context high-watermark.</p></div>
          <InstanceHistoryChart :series="[{ label: 'Context', points: contextChart, token: 'accent' }]" value-format="percent" :min="0" :max="100" />
        </Frame>
      </section>

      <section data-testid="instance-detail-vram-allocation" class="space-y-3">
        <div><h2 class="text-base font-semibold">VRAM allocation</h2><p class="mt-1 text-xs text-[var(--neutral-700)]">Current device allocation, including attributed and unattributed process memory.</p></div>
        <div v-if="allocationGPUs.length" class="grid gap-4 lg:grid-cols-2">
          <Frame v-for="gpu in allocationGPUs" :key="gpu.id" class="p-4">
            <div class="flex flex-wrap items-baseline justify-between gap-2"><h3 class="text-sm font-semibold">{{ gpu.id }} · {{ gpu.name }}</h3><span class="font-mono text-[length:var(--font-size-table-header)] tabular-nums text-[var(--neutral-800)]">{{ formatBytes(gpu.used_bytes) }} / {{ formatBytes(gpu.total_bytes) }} · {{ formatPercent(gpu.utilization_pct) }} util</span></div>
            <div class="mt-4 flex h-3 w-full overflow-hidden bg-[var(--neutral-300)]">
              <span v-for="segment in vramSegments(gpu)" :key="segment.label" class="h-full" :style="{ width: `${segment.percent}%`, backgroundColor: segment.token }" />
            </div>
            <div class="mt-4 divide-y divide-[var(--color-divider)] border-t border-[var(--color-divider)]">
              <div v-for="segment in vramSegments(gpu)" :key="segment.label" class="flex items-center justify-between gap-3 py-2 text-[length:var(--font-size-table-header)]"><span class="flex min-w-0 items-center gap-2"><i class="block h-2 w-2 shrink-0" :style="{ backgroundColor: segment.token }" /><span class="truncate">{{ segment.label }}</span></span><span class="shrink-0 font-mono tabular-nums">{{ formatBytes(segment.bytes) }}</span></div>
            </div>
          </Frame>
        </div>
        <Frame v-else class="p-4 text-sm text-[var(--neutral-700)]">No GPU allocation is available for this Instance.</Frame>
      </section>

      <Frame class="p-4" data-testid="instance-detail-runtime">
        <div class="flex flex-wrap items-start justify-between gap-3"><div><h2 class="text-sm font-semibold">Runtime snapshot</h2><p class="mt-1 font-mono text-[length:var(--font-size-table-header)] text-[var(--neutral-700)]">{{ instance.id }}</p></div><StatusTag :variant="statusVariant(runtime?.state)">{{ runtime?.state || 'UNLOADED' }}</StatusTag></div>
        <p v-if="backoffMessage || runtime?.last_error" class="mt-3 font-mono text-[length:var(--font-size-table-header)] leading-snug text-[var(--accent-800)]" data-testid="instance-detail-startup-backoff">{{ backoffMessage || runtime?.last_error }}</p>
        <dl class="mt-4 grid gap-0 border-t border-l border-[var(--color-divider)] sm:grid-cols-2 lg:grid-cols-4">
          <div v-for="item in [
            ['PID', runtime?.pid || '—'], ['Port', runtime?.port || '—'], ['Placed on', placementIDs.length ? placementIDs.join(', ') : '—'],
            [gpuUsageLabel, formatPercent(currentGPUUsage)], ['VRAM', formatBytes(currentVRAM)], ['CPU', formatPercent(telemetry?.cpu_percent)],
            ['RAM', formatBytes(telemetry?.memory_used_bytes)], ['Snapshot time', telemetry?.collected_at ? formatLocal(telemetry.collected_at) : '—']
          ]" :key="String(item[0])" class="border-r border-b border-[var(--color-divider)] p-3"><dt class="text-[length:var(--font-size-kicker)] uppercase tracking-[.12em] text-[var(--neutral-700)]">{{ item[0] }}</dt><dd class="mt-1 font-mono text-[length:var(--font-size-body)] tabular-nums">{{ item[1] }}</dd></div>
        </dl>
      </Frame>

      <Frame v-if="companions.length" class="p-4" data-testid="instance-detail-companions">
        <h2 class="text-sm font-semibold">Companion files</h2>
        <div class="mt-3 divide-y divide-[var(--color-divider)] border-t border-[var(--color-divider)]">
          <div v-for="helper in companions" :key="`${helper.kind}-${helper.path}`" class="flex flex-wrap items-center justify-between gap-3 py-3"><div class="min-w-0"><p class="text-xs font-semibold">{{ helper.kind }}</p><p class="mt-1 break-all font-mono text-[length:var(--font-size-table-header)]">{{ helper.path }}</p><p class="mt-1 text-[length:var(--font-size-kicker)] text-[var(--neutral-700)]">{{ helper.flag }} · {{ formatBytes(helper.size) }}</p></div><StatusTag variant="ready">Enabled</StatusTag></div>
        </div>
      </Frame>

      <Frame v-if="!llama" class="p-4" data-testid="instance-detail-metrics-note">
        <h2 class="text-sm font-semibold">{{ runtime?.state === 'READY' ? 'Collecting llama.cpp metrics' : 'llama.cpp metrics unavailable while stopped' }}</h2>
        <p class="mt-1 text-xs text-[var(--neutral-700)]">{{ runtime?.state === 'READY' ? 'The detail page updates automatically when the next metrics snapshot arrives.' : 'Launch the Instance to populate throughput, request, decode and speculative-decoding metrics.' }}</p>
      </Frame>

      <template v-else>
        <Frame class="p-4" data-testid="instance-detail-throughput">
          <div><h2 class="text-sm font-semibold">Throughput & load</h2><p class="mt-1 text-[length:var(--font-size-kicker)] text-[var(--neutral-700)]">Live llama.cpp gauges from the managed worker.</p></div>
          <dl class="mt-4 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            <div v-for="item in [['Generation throughput', formatRate(llama.predicted_tokens_per_second)], ['Prompt throughput', formatRate(llama.prompt_tokens_per_second)], ['Active requests', formatNumber(llama.requests_processing)], ['Queued requests', formatNumber(llama.requests_deferred)], ['Context high-watermark', contextHighWatermark()], ['Busy slots / decode', formatNumber(llama.busy_slots_per_decode, 2)]]" :key="String(item[0])"><dt class="text-[length:var(--font-size-kicker)] text-[var(--neutral-700)]">{{ item[0] }}</dt><dd class="mt-1 font-mono text-[length:var(--font-size-h4)] font-semibold tabular-nums">{{ item[1] }}</dd></div>
          </dl>
        </Frame>
        <Frame class="p-4" data-testid="instance-detail-counters">
          <div><h2 class="text-sm font-semibold">Cumulative counters</h2><p class="mt-1 text-[length:var(--font-size-kicker)] text-[var(--neutral-700)]">Counters reset when the llama-server process restarts.</p></div>
          <dl class="mt-4 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            <div v-for="item in [['Prompt tokens', formatNumber(llama.prompt_tokens_total)], ['Prompt processing time', formatSeconds(llama.prompt_seconds_total)], ['Generated tokens', formatNumber(llama.predicted_tokens_total)], ['Generation time', formatSeconds(llama.predicted_seconds_total)], ['llama_decode() calls', formatNumber(llama.decode_total)]]" :key="String(item[0])"><dt class="text-[length:var(--font-size-kicker)] text-[var(--neutral-700)]">{{ item[0] }}</dt><dd class="mt-1 font-mono text-[length:var(--font-size-body)] tabular-nums">{{ item[1] }}</dd></div>
          </dl>
        </Frame>
        <Frame class="p-4" data-testid="instance-detail-speculative">
          <div><h2 class="text-sm font-semibold">Speculative decoding</h2><p class="mt-1 text-[length:var(--font-size-kicker)] text-[var(--neutral-700)]">Counters are zero when speculative decoding is disabled.</p></div>
          <dl class="mt-4 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <div v-for="item in [['Draft tokens', formatNumber(llama.spec_draft_tokens_total)], ['Accepted draft tokens', formatNumber(llama.spec_accepted_tokens_total)], ['Verification steps', formatNumber(llama.spec_drafts_total)], ['Acceptance rate', formatPercent(llama.spec_acceptance_rate_pct)]]" :key="String(item[0])"><dt class="text-[length:var(--font-size-kicker)] text-[var(--neutral-700)]">{{ item[0] }}</dt><dd class="mt-1 font-mono text-[length:var(--font-size-body)] tabular-nums">{{ item[1] }}</dd></div>
          </dl>
          <div v-if="specPositions.length" class="mt-4 border-t border-[var(--color-divider)] pt-3"><p class="text-[length:var(--font-size-kicker)] font-semibold text-[var(--neutral-700)]">Accepted tokens per draft position</p><div class="mt-2 flex flex-wrap gap-2" data-testid="instance-detail-spec-positions"><span v-for="[position, value] in specPositions" :key="position" class="bg-[var(--neutral-300)] px-2 py-1 font-mono text-[length:var(--font-size-kicker)]">Position {{ position }}: {{ formatNumber(value) }}</span></div></div>
        </Frame>
      </template>

      <section id="logs" class="space-y-3"><div><h2 class="text-base font-semibold">Instance logs</h2><p class="mt-1 text-xs text-[var(--neutral-700)]">Current-session llama-server output.</p></div><InstanceLogViewer :instance-id="instance.id" /></section>
    </template>
    <AppConfirmationModal ref="confirmation" />
  </div>
</template>