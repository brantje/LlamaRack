<script setup lang="ts">
import type { ProgressGroupItem, TableColumn } from '@nuxt/ui'
import type { HardwareGPU, HardwareSnapshot, RuntimeTelemetry } from '~/composables/useManager'

const manager = useManager()
const { instances, runtimes, observabilityLive } = manager

type APIKeyRef = { id: string; name: string; prefix: string }
type RequestRecord = {
  id: number
  started_at: number
  finished_at: number
  instance_id: string
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
type ManagementSummary = GatewaySummary & {
  lifecycle: LifecycleSummary
  hardware: HardwareOverview
}
type SettingValue<T> = { value: T; source: string; editable: boolean }
type GeneralSettings = { idle_unload_seconds: SettingValue<number> }
type AttentionItem = { key: string; title: string; detail: string; to?: string }

const summary = ref<ManagementSummary | null>(null)
const recentRequests = ref<RequestRecord[]>([])
const settings = ref<GeneralSettings | null>(null)
const loading = ref(false)
const dashboardError = ref('')

const runtimeList = computed(() => Object.values(runtimes.value).flat())
const readyCount = computed(() => runtimeList.value.filter(runtime => runtime.state === 'READY').length)
const startingCount = computed(() => runtimeList.value.filter(runtime => runtime.state === 'STARTING' || runtime.state === 'LOADING').length)
const failedCount = computed(() => runtimeList.value.filter(runtime => runtime.state === 'FAILED').length)
const gatewaySummary = computed<GatewaySummary | null>(() => observabilityLive.value?.gateway || summary.value)
const displayedRequests = computed<RequestRecord[]>(() => (observabilityLive.value?.requests as RequestRecord[] | undefined) ?? recentRequests.value)

const emptyHardware = (): HardwareSnapshot => ({
  ram_total_bytes: 0,
  ram_available_bytes: 0,
  gpus: [],
  processes: [],
  collected_at: ''
})
const hardware = computed(() => observabilityLive.value?.hardware || summary.value?.hardware?.hardware || emptyHardware())
const telemetry = computed(() => observabilityLive.value?.telemetry || summary.value?.hardware?.telemetry || [])
const totalVRAM = computed(() => hardware.value.gpus.reduce((total, gpu) => total + gpu.total_bytes, 0))
const committedVRAM = computed(() => hardware.value.gpus.reduce((total, gpu) => total + gpuCommittedBytes(gpu), 0))
const vramPercent = computed(() => totalVRAM.value > 0 ? Math.min(100, (committedVRAM.value / totalVRAM.value) * 100) : 0)
const ramUsed = computed(() => Math.max(0, hardware.value.ram_total_bytes - hardware.value.ram_available_bytes))
const ramPercent = computed(() => hardware.value.ram_total_bytes > 0 ? Math.min(100, (ramUsed.value / hardware.value.ram_total_bytes) * 100) : 0)
const idleSeconds = computed(() => Number(settings.value?.idle_unload_seconds?.value || 0))
const idleOverrides = computed(() => instances.value.filter(instance => instance.idle_unload_seconds > 0).length)
const logsTarget = computed(() => {
  const failed = runtimeList.value.find(runtime => runtime.state === 'FAILED')
  const id = failed?.instance_id || instances.value[0]?.id
  return id ? `/instances/${encodeURIComponent(id)}/detail` : '/instances'
})

const gatewayColumns: TableColumn<RequestRecord>[] = [
  { accessorKey: 'started_at', header: 'Time' },
  { accessorKey: 'instance_id', header: 'Model' },
  { accessorKey: 'endpoint', header: 'Endpoint' },
  { accessorKey: 'api_key', header: 'Key' },
  { accessorKey: 'total_tokens', header: 'Tokens' },
  { accessorKey: 'duration_ms', header: 'Latency' },
  { accessorKey: 'result', header: 'Result' }
]

// `primary` and `success` both resolve to mint in app.config.ts, so using both
// would make separate models visually indistinguishable. Reserve `info` for Free.
const gpuProgressColors: NonNullable<ProgressGroupItem['color']>[] = ['primary', 'secondary', 'warning', 'error', 'neutral']
const gpuProgressColorByInstance = computed<Map<string, NonNullable<ProgressGroupItem['color']>>>(() => {
  const colors = new Map<string, NonNullable<ProgressGroupItem['color']>>()
  const instanceIDs = Array.from(new Set(telemetry.value.map(sample => sample.instance_id))).sort()
  instanceIDs.forEach((instanceID, index) => {
    colors.set(instanceID, gpuProgressColors[index % gpuProgressColors.length]!)
  })
  return colors
})

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
  if (!record.api_key) return '—'
  return record.api_key.name || record.api_key.prefix || 'API key'
}

function gpuPercent(gpu: HardwareGPU) {
  return gpu.total_bytes > 0 ? Math.min(100, Math.max(0, gpu.used_bytes / gpu.total_bytes * 100)) : 0
}

function gpuAssignments(gpuID: string) {
  return telemetry.value.flatMap((sample) => {
    const observed = sample.gpus?.find(gpu => gpu.device_id === gpuID)
    if (!observed && !sample.gpu_devices?.includes(gpuID)) return []
    const used = observed?.vram_used_bytes ?? (sample.gpu_devices?.length === 1 ? sample.vram_used_bytes : undefined)
    return [{ instanceID: sample.instance_id, used }]
  })
}

function gpuCommittedBytes(gpu: HardwareGPU) {
  const attributed = gpuAssignments(gpu.id).reduce((total, assignment) => {
    return total + (assignment.used !== undefined && assignment.used > 0 ? assignment.used : 0)
  }, 0)
  return Math.min(gpu.total_bytes, attributed)
}

function gpuProgressItems(gpu: HardwareGPU): ProgressGroupItem[] {
  const items: ProgressGroupItem[] = gpuAssignments(gpu.id)
    .filter(assignment => assignment.used !== undefined && assignment.used > 0)
    .map(assignment => ({
      label: assignment.instanceID,
      value: assignment.used,
      color: gpuProgressColorByInstance.value.get(assignment.instanceID) || 'primary'
    }))
  const committed = Math.min(gpu.total_bytes, items.reduce((total, item) => total + (item.value || 0), 0))
  const free = Math.max(0, gpu.total_bytes - committed)
  if (free > 0) {
    items.push({ label: 'Free', value: free, color: 'info' })
  }
  return items
}

const attention = computed<AttentionItem[]>(() => {
  const items: AttentionItem[] = []
  for (const runtime of runtimeList.value.filter(runtime => runtime.state === 'FAILED')) {
    items.push({
      key: `failed-${runtime.instance_id}`,
      title: `${runtime.instance_id} failed to start`,
      detail: runtime.last_error || 'The managed llama-server process is in FAILED state.',
      to: `/instances/${encodeURIComponent(runtime.instance_id)}/detail`
    })
  }
  for (const request of displayedRequests.value.filter(request => request.result !== 'success').slice(0, 2)) {
    items.push({
      key: `request-${request.id}`,
      title: `${request.instance_id} returned ${request.status_code || 'an error'}`,
      detail: request.error || `${request.endpoint} failed during the last 15 minutes.`,
      to: `/instances/${encodeURIComponent(request.instance_id)}/detail`
    })
  }
  for (const instance of instances.value) {
    if (!instance.always_on || manager.instanceState(instance) !== 'UNLOADED') continue
    items.push({
      key: `always-${instance.id}`,
      title: `${instance.id} is Always-On but unloaded`,
      detail: 'The Instance may have been stopped manually or could be waiting for resources.',
      to: `/instances/${encodeURIComponent(instance.id)}/detail`
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
    items.push({ key: 'ram-pressure', title: `Host RAM is at ${Math.round(ramPercent.value)}%`, detail: 'Host memory pressure may affect model loading and inference.', to: '/instances' })
  }
  return items.slice(0, 6)
})

async function loadDashboard() {
  loading.value = true
  dashboardError.value = ''
  const since = Date.now() - 15 * 60 * 1000
  try {
    const [summaryValue, requestsValue, settingsValue] = await Promise.all([
      manager.request<ManagementSummary>('/api/v1/observability/summary?window_seconds=900'),
      manager.request<{ items: RequestRecord[] }>(`/api/v1/observability/requests?since=${since}&limit=50`),
      manager.request<GeneralSettings>('/api/v1/settings/general')
    ])
    summary.value = summaryValue
    recentRequests.value = requestsValue.items || []
    settings.value = settingsValue
  } catch (error: any) {
    dashboardError.value = error?.data?.error || error?.message || 'Unable to load Dashboard observability data'
  } finally {
    loading.value = false
  }
}

async function refreshDashboard() {
  await Promise.allSettled([manager.refresh(), loadDashboard()])
}

watch(
  [() => manager.initialized.value, () => manager.user.value],
  ([initialized, user]) => {
    if (!initialized || !user) return
    void loadDashboard()
  },
  { immediate: true }
)
</script>

<template>
  <div class="space-y-5" data-testid="observability-dashboard">
    <div class="flex flex-wrap items-start justify-between gap-4">
      <UPageHeader class="min-w-0 flex-1" headline="OVERVIEW" title="Dashboard" description="Live inference traffic, runtime health and accelerator allocation." />
      <div class="flex flex-wrap gap-2">
        <UButton :to="logsTarget" color="neutral" variant="soft">Open logs</UButton>
        <UButton color="neutral" variant="soft" :loading="loading" @click="refreshDashboard">Refresh</UButton>
      </div>
    </div>

    <UAlert v-if="dashboardError" color="error" variant="subtle" title="Observability data unavailable" :description="dashboardError" />

    <div class="grid grid-cols-2 gap-3 xl:grid-cols-4">
      <UCard data-testid="dashboard-running">
        <p class="text-xs font-semibold uppercase tracking-[0.16em] text-dimmed">Running</p>
        <div class="mt-2"><strong class="text-3xl">{{ `${readyCount} / ${instances.length} Instances` }}</strong></div>
        <p class="mt-1 text-xs text-muted">{{ startingCount }} starting · {{ failedCount }} error{{ failedCount === 1 ? '' : 's' }}</p>
      </UCard>
      <UCard data-testid="dashboard-vram">
        <p class="text-xs font-semibold uppercase tracking-[0.16em] text-dimmed">VRAM committed</p>
        <div class="mt-2 flex items-baseline gap-1.5"><strong class="text-3xl">{{ formatBytes(committedVRAM) }}</strong><span class="text-sm text-muted">/ {{ formatBytes(totalVRAM) }}</span></div>
        <p class="mt-1 text-xs text-muted">{{ Math.round(vramPercent) }}% attributed to managed Instances</p>
      </UCard>
      <UCard data-testid="dashboard-gateway">
        <p class="text-xs font-semibold uppercase tracking-[0.16em] text-dimmed">Gateway · 15 min</p>
        <div class="mt-2 flex items-baseline gap-1.5"><strong class="text-3xl">{{ gatewaySummary?.requests || 0 }}</strong><span class="text-sm text-muted">requests</span></div>
        <p class="mt-1 text-xs text-muted">{{ gatewaySummary?.active_api_keys || 0 }} active API key{{ gatewaySummary?.active_api_keys === 1 ? '' : 's' }}</p>
      </UCard>
      <UCard data-testid="dashboard-idle">
        <p class="text-xs font-semibold uppercase tracking-[0.16em] text-dimmed">Idle unload</p>
        <div class="mt-2 flex items-baseline gap-1.5"><strong class="text-3xl">{{ formatIdle(idleSeconds) }}</strong><span class="text-sm text-muted">global</span></div>
        <p class="mt-1 text-xs text-muted">{{ idleOverrides }} Instance override{{ idleOverrides === 1 ? '' : 's' }}</p>
      </UCard>
    </div>

    <UCard data-testid="dashboard-vram-allocation">
      <template #header>
        <div>
          <p class="text-xs font-extrabold tracking-[0.18em] text-dimmed">VRAM ALLOCATION</p>
          <p class="mt-1 text-xs text-muted">Manager-attributed Instance VRAM; Free is device capacity not attributed to managed Instances.</p>
        </div>
      </template>
      <UEmpty v-if="!hardware.gpus.length" variant="naked" title="No GPU telemetry available" description="GPU allocation will appear when CUDA or ROCm devices are detected." />
      <div v-else class="grid gap-5 lg:grid-cols-2">
        <div v-for="gpu in hardware.gpus" :key="gpu.id" class="space-y-2">
          <div class="flex items-center justify-between gap-3 text-xs">
            <span class="font-mono text-muted">{{ gpu.id }} · {{ gpu.name }}</span>
            <span class="font-semibold text-highlighted">{{ formatBytes(gpuCommittedBytes(gpu)) }} / {{ formatBytes(gpu.total_bytes) }} · {{ Math.round(gpu.utilization_pct) }}% util</span>
          </div>
          <UProgressGroup
            :data-testid="`gpu-progress-${gpu.id}`"
            :items="gpuProgressItems(gpu)"
            :max="gpu.total_bytes"
            size="sm"
          >
            <template #item-label="{ item }">
              <span class="font-mono text-xs" :data-vram-label="item.label" :data-vram-color="item.color">{{ item.label }}</span>
            </template>
            <template #item-trailing="{ item }">
              <span class="text-xs text-muted">{{ formatBytes(item.value || 0) }}</span>
            </template>
          </UProgressGroup>
        </div>
      </div>
    </UCard>

    <div class="grid gap-5 xl:grid-cols-[minmax(0,2fr)_minmax(20rem,1fr)]">
      <UCard data-testid="dashboard-gateway-traffic">
        <template #header>
          <div class="flex items-center justify-between gap-3">
            <div><p class="text-xs font-extrabold tracking-[0.18em] text-dimmed">GATEWAY TRAFFIC · LAST 15 MIN</p><p class="mt-1 text-xs text-muted">Individual OpenAI-compatible requests with safe API-key attribution.</p></div>
            <UBadge color="neutral" variant="soft">{{ displayedRequests.length }} shown</UBadge>
          </div>
        </template>
        <UEmpty v-if="!displayedRequests.length" variant="naked" title="No recent gateway traffic" description="Requests will appear here after inference traffic reaches an addressable Instance." />
        <UTable v-else :data="displayedRequests" :columns="gatewayColumns">
          <template #started_at-cell="{ row }"><span class="font-mono text-xs">{{ formatTime(row.original.started_at) }}</span></template>
          <template #instance_id-cell="{ row }"><NuxtLink class="font-mono text-xs font-semibold text-primary" :to="`/instances/${encodeURIComponent(row.original.instance_id)}/detail`">{{ row.original.instance_id }}</NuxtLink></template>
          <template #endpoint-cell="{ row }"><span class="font-mono text-xs text-muted">{{ row.original.endpoint }}</span></template>
          <template #api_key-cell="{ row }"><span class="text-xs">{{ requestKey(row.original) }}</span></template>
          <template #total_tokens-cell="{ row }"><span class="font-mono text-xs">{{ row.original.total_tokens || '—' }}</span></template>
          <template #duration_ms-cell="{ row }"><span class="font-mono text-xs">{{ formatDuration(row.original.duration_ms) }}</span></template>
          <template #result-cell="{ row }"><UBadge :color="row.original.result === 'success' ? 'success' : 'error'" variant="subtle" size="sm">{{ row.original.status_code || row.original.result }}</UBadge></template>
        </UTable>
      </UCard>

      <UCard data-testid="dashboard-attention">
        <template #header><div><p class="text-xs font-extrabold tracking-[0.18em] text-dimmed">NEEDS ATTENTION</p><p class="mt-1 text-xs text-muted">Current operational conditions worth reviewing.</p></div></template>
        <UEmpty v-if="!attention.length" variant="naked" title="Nothing needs attention" description="No failed runtimes, recent request errors or high resource pressure detected." />
        <div v-else class="divide-y divide-default">
          <div v-for="item in attention" :key="item.key" class="py-3 first:pt-0 last:pb-0">
            <p class="text-sm font-semibold text-highlighted">{{ item.title }}</p>
            <p class="mt-1 text-xs leading-5 text-muted">{{ item.detail }}</p>
            <UButton v-if="item.to" :to="item.to" class="mt-2" size="xs" color="neutral" variant="link" trailing-icon="i-lucide-arrow-right">Review</UButton>
          </div>
        </div>
      </UCard>
    </div>
  </div>
</template>
