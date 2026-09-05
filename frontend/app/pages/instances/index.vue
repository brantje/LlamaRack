<script setup lang="ts">
import type { Instance, RuntimeTelemetry } from '~/composables/useManager'
import { startupBackoffMessage } from '~/utils/startupBackoff'

type ImportStatus = {
  id: string
  job_id: string
  model_id: string
  instance_id?: string
  state: string
  error?: string
  start_when_ready: boolean
}

type GeneralSettings = { idle_unload_seconds?: { value?: number } }
type ViewMode = 'table' | 'cards'
type StateFilter = 'all' | 'ready' | 'stopped' | 'problems'

const manager = useManager()
const { instances, models } = manager
const pending = ref('')
const error = ref('')
const refreshing = ref(false)
const logsOpen = ref(false)
const logInstanceId = ref('')
const logTitle = ref('')
const importStates = ref<Record<string, ImportStatus>>({})
const defaultIdleSeconds = ref(300)
const viewMode = ref<ViewMode>('table')
const stateFilter = ref<StateFilter>('all')
const confirmation = ref<{ request: (options: Record<string, string>) => Promise<boolean> } | null>(null)
const viewStorageKey = 'llamarack.instances.view'
const activeRuntimeStates = new Set(['STARTING', 'LOADING', 'READY', 'DRAINING', 'STOPPING'])
const filterOptions: Array<{ value: StateFilter; label: string }> = [
  { value: 'all', label: 'All' },
  { value: 'ready', label: 'Ready' },
  { value: 'stopped', label: 'Stopped' },
  { value: 'problems', label: 'Problems' }
]
let importTimer: ReturnType<typeof setTimeout> | undefined
let disposed = false

const filteredInstances = computed(() => instances.value.filter((instance) => {
  const state = instanceState(instance)
  if (stateFilter.value === 'ready') return state === 'READY'
  if (stateFilter.value === 'stopped') return state === 'UNLOADED'
  if (stateFilter.value === 'problems') return state === 'FAILED' || state === 'CANCELLED'
  return true
}))

const telemetrySnapshot = computed(() => {
  if (manager.observabilityLive.value?.collected_at) return manager.observabilityLive.value.collected_at
  const timestamps = Object.values(manager.runtimeTelemetry.value).map(sample => sample.collected_at).filter(Boolean).sort()
  return timestamps.length ? timestamps[timestamps.length - 1]! : ''
})

function modelName(id: string) {
  return models.value.find(model => model.id === id)?.name || id
}

function importFor(instance: Instance) {
  return importStates.value[instance.id]
}

function runtimeFor(instance: Instance) {
  return manager.runtimeForInstance(instance)
}

function hasRuntimeRecord(instance: Instance) {
  return Boolean((manager.runtimes.value[instance.model_id] || []).some(item => item.instance_id === instance.id))
}

function instanceState(instance: Instance) {
  const imported = importFor(instance)
  if (imported && imported.state !== 'COMPLETED') return imported.state
  return manager.instanceState(instance)
}

function statusVariant(state: string): 'ready' | 'pending' | 'neutral' | 'failed' {
  if (state === 'READY') return 'ready'
  if (state === 'FAILED' || state === 'CANCELLED') return 'failed'
  if (['STARTING', 'LOADING', 'DRAINING', 'STOPPING', 'DOWNLOADING'].includes(state)) return 'pending'
  return 'neutral'
}

function importBlocked(instance: Instance) {
  const imported = importFor(instance)
  return Boolean(imported && imported.state !== 'COMPLETED')
}

function isRunning(instance: Instance) {
  return activeRuntimeStates.has(instanceState(instance))
}

function telemetryFor(instance: Instance) {
  return manager.telemetryForInstance(instance)
}

function hasProcessGPUUtilization(sample?: RuntimeTelemetry) {
  return Boolean(sample?.gpus?.some(gpu => gpu.utilization_pct !== undefined))
}

function hasProcessVRAM(sample?: RuntimeTelemetry) {
  return Boolean(sample?.gpus?.some(gpu => gpu.vram_used_bytes !== undefined))
}

function globalGPUFallback(sample?: RuntimeTelemetry) {
  return Boolean(sample?.gpu_utilization_pct !== undefined && !hasProcessGPUUtilization(sample))
}

function globalVRAMFallback(sample?: RuntimeTelemetry) {
  return Boolean(sample?.vram_used_bytes !== undefined && !hasProcessVRAM(sample))
}

function gpuPercent(sample?: RuntimeTelemetry) {
  const values = sample?.gpus?.map(gpu => gpu.utilization_pct).filter((value): value is number => value !== undefined && Number.isFinite(value)) || []
  if (values.length) return values.reduce((sum, value) => sum + value, 0) / values.length
  if (sample?.gpu_utilization_pct !== undefined && Number.isFinite(sample.gpu_utilization_pct)) return sample.gpu_utilization_pct
  return undefined
}

function vramBytes(sample?: RuntimeTelemetry) {
  const values = sample?.gpus?.map(gpu => gpu.vram_used_bytes).filter((value): value is number => value !== undefined && Number.isFinite(value)) || []
  if (values.length) return values.reduce((sum, value) => sum + value, 0)
  if (sample?.vram_used_bytes !== undefined && Number.isFinite(sample.vram_used_bytes)) return sample.vram_used_bytes
  return undefined
}

function gpuCapacity(sample?: RuntimeTelemetry) {
  if (!sample?.gpu_devices?.length) return 0
  const hardware = manager.observabilityLive.value?.hardware.gpus || []
  return sample.gpu_devices.reduce((sum, id) => sum + (hardware.find(gpu => gpu.id === id)?.total_bytes || 0), 0)
}

function hostRAMTotal() {
  return manager.observabilityLive.value?.hardware.ram_total_bytes || 0
}

function clampPercent(value?: number) {
  if (value === undefined || !Number.isFinite(value) || value < 0) return 0
  return Math.max(0, Math.min(100, value))
}

function fractionPercent(value?: number, total = 0) {
  if (value === undefined || !Number.isFinite(value) || value < 0 || !total) return 0
  return clampPercent((value / total) * 100)
}

function formatBytes(value?: number) {
  if (value === undefined || !Number.isFinite(value) || value < 0) return '—'
  if (value === 0) return '0 B'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let amount = value
  let unit = 0
  while (amount >= 1024 && unit < units.length - 1) {
    amount /= 1024
    unit++
  }
  return `${amount >= 10 || unit === 0 ? amount.toFixed(0) : amount.toFixed(1)} ${units[unit]}`
}

function formatPercent(value?: number) {
  if (value === undefined || !Number.isFinite(value) || value < 0) return '—'
  return `${value >= 10 ? value.toFixed(0) : value.toFixed(1)}%`
}

function formatIdleMinutes(seconds: number) {
  const minutes = seconds / 60
  return Number.isInteger(minutes) ? String(minutes) : minutes.toFixed(1).replace(/\.0$/, '')
}

function lifecycle(instance: Instance) {
  const base = instance.always_on ? 'Always On' : 'On demand'
  return instance.idle_unload_seconds > 0 ? `${base} · idle ${formatIdleMinutes(instance.idle_unload_seconds)} min` : base
}

function placement(instance: Instance) {
  if (!isRunning(instance)) return '—'
  const sample = telemetryFor(instance)
  const devices = sample?.gpu_devices?.length ? sample.gpu_devices : (instance.gpu_devices || [])
  return devices.length ? devices.join(', ') : '—'
}

function effectiveIdleSeconds(instance: Instance) {
  return instance.idle_unload_seconds > 0 ? instance.idle_unload_seconds : defaultIdleSeconds.value
}

function nonRunningMessage(instance: Instance) {
  const imported = importFor(instance)
  if (imported?.state === 'DOWNLOADING') {
    return imported.start_when_ready
      ? 'Model is downloading. This Instance will launch automatically when the verified GGUF download completes.'
      : 'Model is downloading. The Instance will become launchable when the verified GGUF download completes.'
  }
  if (imported && (imported.state === 'FAILED' || imported.state === 'CANCELLED')) return imported.error || 'Open Downloads to retry or inspect this import.'
  const runtime = runtimeFor(instance)
  const backoff = startupBackoffMessage(runtime)
  if (backoff) return backoff
  if (runtime.state === 'FAILED') return runtime.last_error || 'llama-server exited unexpectedly.'
  if (runtime.state === 'UNLOADED' && hasRuntimeRecord(instance) && effectiveIdleSeconds(instance) > 0) return `Unloaded after ${effectiveIdleSeconds(instance)} s without inference activity.`
  if (runtime.state === 'UNLOADED') {
    return instance.autoload_enabled
      ? 'Never launched since the last manager restart. Autoload will start it on the next gateway request.'
      : 'Never launched since the last manager restart. Launch it manually when needed.'
  }
  return `${instanceState(instance)} — runtime telemetry is not available.`
}

function snapshotLabel(value: string) {
  if (!value) return 'Telemetry snapshot · awaiting data'
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return `Telemetry snapshot · ${value}`
  return `Telemetry snapshot · ${parsed.toLocaleString()}`
}

function scheduleImportRefresh(active: boolean) {
  if (importTimer) {
    clearTimeout(importTimer)
    importTimer = undefined
  }
  if (!active || disposed) return
  importTimer = setTimeout(() => {
    importTimer = undefined
    void refreshImportStates()
  }, 1000)
}

async function refreshImportStates() {
  try {
    const previousActive = Object.values(importStates.value).some(item => item.state === 'DOWNLOADING')
    const items = await manager.request<ImportStatus[]>('/api/v1/imports') || []
    importStates.value = Object.fromEntries(items.filter(item => item.instance_id).map(item => [item.instance_id!, item]))
    const active = items.some(item => item.instance_id && item.state === 'DOWNLOADING')
    if (previousActive && !active) await manager.refresh()
    scheduleImportRefresh(active)
  } catch {
    scheduleImportRefresh(false)
  }
}

async function refreshGeneralSettings() {
  try {
    const settings = await manager.request<GeneralSettings>('/api/v1/settings/general')
    const value = Number(settings?.idle_unload_seconds?.value)
    if (Number.isFinite(value) && value >= 0) defaultIdleSeconds.value = value
  } catch {
    // Instance controls and telemetry remain useful if settings cannot be read.
  }
}

async function refreshPage() {
  refreshing.value = true
  error.value = ''
  try {
    await Promise.allSettled([manager.refresh(), refreshImportStates(), refreshGeneralSettings()])
  } finally {
    refreshing.value = false
  }
}

function cardOverflowItems(instance: Instance) {
  return [[
    { label: 'Restart', onSelect: () => { void action(instance, 'restart') } },
    { label: 'Kill', onSelect: () => { void action(instance, 'kill') } },
    { label: 'Duplicate', onSelect: () => { void action(instance, 'duplicate') } },
    { type: 'separator' as const },
    { label: 'Delete', onSelect: () => { void remove(instance) } }
  ]]
}

async function action(instance: Instance, operation: 'start' | 'stop' | 'restart' | 'kill' | 'duplicate') {
  if (importBlocked(instance)) return
  if (operation === 'start' && instance.eviction_enabled) {
    const confirmed = await confirmation.value?.request({
      title: 'Launch Instance',
      description: 'Launching this Instance may stop other idle Instances if resource-pressure eviction is required.',
      confirmLabel: 'Launch Instance',
      confirmTone: 'default'
    })
    if (!confirmed) return
  }
  if (operation === 'kill') {
    const confirmed = await confirmation.value?.request({
      title: 'Kill Instance',
      description: 'Kill this Instance immediately? Active requests may fail.',
      confirmLabel: 'Kill Instance',
      confirmTone: 'destructive'
    })
    if (!confirmed) return
  }
  pending.value = `${instance.id}:${operation}`
  error.value = ''
  try {
    await manager.request(`/api/v1/instances/${encodeURIComponent(instance.slug)}/${operation}`, { method: 'POST' })
    await manager.refresh()
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || `Unable to ${operation} Instance`
  } finally {
    pending.value = ''
  }
}

async function remove(instance: Instance) {
  const confirmed = await confirmation.value?.request({
    title: 'Delete Instance',
    description: `Delete Instance “${instance.name}”? The registered Model and GGUF file are kept.`,
    confirmLabel: 'Delete Instance',
    confirmTone: 'destructive'
  })
  if (!confirmed) return
  pending.value = `${instance.id}:delete`
  error.value = ''
  try {
    await manager.request(`/api/v1/instances/${encodeURIComponent(instance.slug)}`, { method: 'DELETE' })
    await manager.refresh()
    await refreshImportStates()
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to delete Instance'
  } finally {
    pending.value = ''
  }
}

function showLogs(instance: Instance) {
  error.value = ''
  logInstanceId.value = instance.id
  logTitle.value = `${instance.name} logs`
  logsOpen.value = true
}

onMounted(() => {
  try {
    const stored = sessionStorage.getItem(viewStorageKey)
    if (stored === 'table' || stored === 'cards') viewMode.value = stored
  } catch {
    // Session persistence is a convenience; Table remains the fallback.
  }
  void refreshImportStates()
  void refreshGeneralSettings()
})

watch(viewMode, (mode) => {
  if (!import.meta.client) return
  try { sessionStorage.setItem(viewStorageKey, mode) } catch {}
})

onBeforeUnmount(() => {
  disposed = true
  if (importTimer) clearTimeout(importTimer)
})
</script>

<template>
  <div class="space-y-6" data-testid="instances-page">
    <div class="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between" data-testid="instances-header">
      <UPageHeader
        class="w-full min-w-0 sm:flex-1"
        headline="CONTROL PLANE"
        title="Instances"
        description="Durable llama-server definitions. Instance slugs are the model IDs used by OpenAI-compatible clients."
      />
      <div class="flex w-full flex-wrap items-center justify-start gap-2 sm:w-auto sm:shrink-0 sm:justify-end">
        <div class="flex border border-[var(--color-divider)]" data-testid="instances-view-toggle" aria-label="Instance view">
          <UButton type="button" :color="viewMode === 'table' ? 'primary' : 'neutral'" :variant="viewMode === 'table' ? 'soft' : 'ghost'" size="sm" data-testid="instances-view-table" :aria-pressed="viewMode === 'table'" @click="viewMode = 'table'">Table</UButton>
          <UButton type="button" :color="viewMode === 'cards' ? 'primary' : 'neutral'" :variant="viewMode === 'cards' ? 'soft' : 'ghost'" size="sm" class="border-l border-[var(--color-divider)]" data-testid="instances-view-cards" :aria-pressed="viewMode === 'cards'" @click="viewMode = 'cards'">Cards</UButton>
        </div>
        <AppButton intent="secondary" :loading="refreshing" @click="refreshPage">Refresh</AppButton>
        <AppButton to="/instances/new" intent="primary">New Instance</AppButton>
      </div>
    </div>

    <div class="flex flex-wrap items-center justify-between gap-3 border-y border-[var(--color-divider)] py-3">
      <div class="flex flex-wrap gap-2" data-testid="instances-filters">
        <UButton
          v-for="filter in filterOptions"
          :key="filter.value"
          type="button"
          :color="stateFilter === filter.value ? 'primary' : 'neutral'"
          :variant="stateFilter === filter.value ? 'soft' : 'ghost'"
          size="sm"
          :aria-pressed="stateFilter === filter.value"
          :data-testid="`instances-filter-${filter.value}`"
          @click="stateFilter = filter.value"
        >{{ filter.label }}</UButton>
      </div>
      <span class="font-mono text-[length:var(--font-size-table-header)] text-[var(--neutral-700)]" data-testid="instances-telemetry-snapshot">{{ snapshotLabel(telemetrySnapshot) }}</span>
    </div>

    <Frame v-if="error" class="p-3" data-testid="instances-error">
      <div class="flex flex-wrap items-start gap-2">
        <StatusTag variant="failed">Instance operation failed</StatusTag>
        <p class="min-w-0 flex-1 text-xs text-muted">{{ error }}</p>
      </div>
    </Frame>

    <UEmpty v-if="!instances.length" title="No Instances configured" description="Create an Instance for a registered Model. Stopped Instances remain here and can be launched later.">
      <template #actions><AppButton to="/instances/new" intent="primary" size="sm">New Instance</AppButton></template>
    </UEmpty>
    <UEmpty v-else-if="!filteredInstances.length" title="No Instances match this filter" description="Choose another state filter to see the rest of the fleet." />

    <Frame v-else-if="viewMode === 'table'" class="overflow-x-auto" data-testid="instances-table-view" role="region" tabindex="0" aria-label="Instances table. Scroll horizontally for telemetry, lifecycle and actions.">
      <p class="border-b border-[var(--color-divider)] px-3 py-2 text-xs text-[var(--neutral-700)] md:hidden">Scroll horizontally for telemetry, lifecycle and actions.</p>
      <table class="min-w-[1180px] w-full border-collapse text-left text-xs">
        <thead class="bg-[var(--neutral-200)] text-[length:var(--font-size-table-header)] uppercase tracking-[.08em] text-[var(--neutral-700)]">
          <tr>
            <th class="px-3 py-3 font-semibold">Instance</th><th class="px-3 py-3 font-semibold">Model</th><th class="px-3 py-3 font-semibold">State</th><th class="px-3 py-3 font-semibold">Placement</th><th class="px-3 py-3 font-semibold">GPU</th><th class="px-3 py-3 font-semibold">VRAM</th><th class="px-3 py-3 font-semibold">CPU</th><th class="px-3 py-3 font-semibold">RAM</th><th class="px-3 py-3 font-semibold">PID</th><th class="px-3 py-3 font-semibold">Port</th><th class="px-3 py-3 font-semibold">Lifecycle</th><th class="px-3 py-3 font-semibold">Actions</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="instance in filteredInstances" :key="instance.id" class="border-t border-[var(--color-divider)] align-top" :data-instance-state="instanceState(instance)">
            <td class="px-3 py-3">
              <NuxtLink :to="`/instances/${encodeURIComponent(instance.slug)}/detail`" class="block text-[length:var(--font-size-table-body)] font-semibold text-[var(--color-text)] hover:text-[var(--accent-700)]">{{ instance.name }}</NuxtLink>
              <div class="mt-1 flex items-center gap-1">
                <code class="break-all font-mono text-[length:var(--font-size-table-header)] text-[var(--neutral-700)]" data-testid="instance-id">{{ instance.slug }}</code>
                <AppCopyButton :text="instance.slug" icon-only color="neutral" variant="ghost" size="xs" error-message="Unable to copy Instance slug. Select the slug and copy it manually." data-testid="copy-instance-id" @copied="error = ''" @error="message => error = message" />
              </div>
            </td>
            <td class="px-3 py-3 text-[var(--neutral-800)]">{{ modelName(instance.model_id) }}</td>
            <td class="px-3 py-3">
              <StatusTag :variant="statusVariant(instanceState(instance))">{{ instanceState(instance) }}</StatusTag>
              <p v-if="importFor(instance)?.state === 'DOWNLOADING'" class="mt-1 max-w-[180px] text-[length:var(--font-size-kicker)] leading-snug text-[var(--neutral-700)]">Model is downloading. {{ importFor(instance)?.start_when_ready ? 'This Instance will launch automatically when ready.' : 'Open Downloads to monitor progress.' }}</p>
              <p v-else-if="importFor(instance)?.state === 'FAILED' || importFor(instance)?.state === 'CANCELLED'" class="mt-1 max-w-[180px] font-mono text-[length:var(--font-size-kicker)] leading-snug text-[var(--accent-800)]">{{ importFor(instance)?.error || 'Open Downloads to retry or inspect this import.' }}</p>
              <template v-else>
                <div v-if="importFor(instance)?.state === 'COMPLETED' && importFor(instance)?.error" data-testid="import-metadata-warning" class="mt-1 flex max-w-[220px] items-start gap-2 border border-[var(--color-divider)] px-2 py-1">
                  <StatusTag variant="pending">Import warning</StatusTag>
                  <p class="min-w-0 text-[length:var(--font-size-kicker)] leading-snug text-[var(--neutral-800)]">{{ importFor(instance)?.error }}</p>
                </div>
                <p v-if="startupBackoffMessage(runtimeFor(instance))" class="mt-1 max-w-[180px] font-mono text-[length:var(--font-size-kicker)] leading-snug text-[var(--accent-800)]" data-testid="instance-startup-backoff">{{ startupBackoffMessage(runtimeFor(instance)) }}</p>
                <p v-else-if="instanceState(instance) === 'FAILED'" class="mt-1 max-w-[180px] font-mono text-[length:var(--font-size-kicker)] leading-snug text-[var(--accent-800)]">{{ runtimeFor(instance).last_error || 'llama-server exited unexpectedly.' }}</p>
              </template>
            </td>
            <td class="px-3 py-3 font-mono text-[length:var(--font-size-table-header)] text-[var(--neutral-800)]">{{ placement(instance) }}</td>
            <td class="px-3 py-3">
              <template v-if="isRunning(instance)">
                <div class="font-mono text-[length:var(--font-size-table-header)] text-[var(--color-text)]">{{ formatPercent(gpuPercent(telemetryFor(instance))) }}</div>
                <div class="mt-1 h-[3px] w-[62px] bg-[var(--neutral-300)]"><div class="h-full bg-[var(--color-accent)]" :style="{ width: `${clampPercent(gpuPercent(telemetryFor(instance)))}%` }" /></div>
                <div v-if="globalGPUFallback(telemetryFor(instance))" class="mt-1 text-[length:var(--font-size-kicker)] text-[var(--accent-700)]">global</div>
              </template><span v-else class="font-mono text-[length:var(--font-size-table-header)] text-[var(--neutral-700)]">—</span>
            </td>
            <td class="px-3 py-3">
              <template v-if="isRunning(instance)">
                <div class="font-mono text-[length:var(--font-size-table-header)] text-[var(--color-text)]">{{ formatBytes(vramBytes(telemetryFor(instance))) }}</div>
                <div class="mt-1 h-[3px] w-[62px] bg-[var(--neutral-300)]"><div class="h-full bg-[var(--color-accent)]" :style="{ width: `${fractionPercent(vramBytes(telemetryFor(instance)), gpuCapacity(telemetryFor(instance)))}%` }" /></div>
                <div v-if="globalVRAMFallback(telemetryFor(instance))" class="mt-1 text-[length:var(--font-size-kicker)] text-[var(--accent-700)]">global</div>
              </template><span v-else class="font-mono text-[length:var(--font-size-table-header)] text-[var(--neutral-700)]">—</span>
            </td>
            <td class="px-3 py-3 font-mono text-[length:var(--font-size-table-header)] text-[var(--color-text)]">{{ isRunning(instance) ? formatPercent(telemetryFor(instance)?.cpu_percent) : '—' }}</td>
            <td class="px-3 py-3 font-mono text-[length:var(--font-size-table-header)] text-[var(--color-text)]">{{ isRunning(instance) ? formatBytes(telemetryFor(instance)?.memory_used_bytes) : '—' }}</td>
            <td class="px-3 py-3 font-mono text-[length:var(--font-size-table-header)] text-[var(--neutral-800)]">{{ isRunning(instance) ? (runtimeFor(instance).pid ?? '—') : '—' }}</td>
            <td class="px-3 py-3 font-mono text-[length:var(--font-size-table-header)] text-[var(--neutral-800)]">{{ isRunning(instance) ? (runtimeFor(instance).port ?? '—') : '—' }}</td>
            <td class="px-3 py-3 text-[length:var(--font-size-table-header)] text-[var(--neutral-800)]">{{ lifecycle(instance) }}</td>
            <td class="px-3 py-3"><div class="flex flex-wrap items-center gap-1">
              <AppButton v-if="importBlocked(instance)" to="/downloads" intent="ghost" size="xs">View download</AppButton>
              <template v-else>
                <AppButton v-if="['UNLOADED', 'FAILED'].includes(instanceState(instance))" intent="ghost" size="xs" :loading="pending === `${instance.id}:start`" @click="action(instance, 'start')">Launch</AppButton>
                <AppButton v-else intent="ghost" size="xs" :loading="pending === `${instance.id}:stop`" @click="action(instance, 'stop')">Stop</AppButton>
                <AppButton intent="ghost" size="xs" @click="showLogs(instance)">Logs</AppButton>
                <UDropdownMenu :items="cardOverflowItems(instance)">
                  <AppButton intent="ghost" size="xs" icon="i-lucide-ellipsis" data-testid="instance-table-more" aria-label="More instance actions" />
                </UDropdownMenu>
              </template>
              <AppButton v-if="importBlocked(instance)" intent="ghost" size="xs" :loading="pending === `${instance.id}:delete`" @click="remove(instance)">Delete</AppButton>
            </div></td>
          </tr>
        </tbody>
      </table>
    </Frame>

    <div v-else class="grid gap-5 md:grid-cols-2 2xl:grid-cols-3" data-testid="instances-card-view">
      <Frame v-for="instance in filteredInstances" :key="instance.id" class="p-5 transition-opacity" :class="isRunning(instance) ? '' : 'opacity-80'" data-testid="instance-card">
        <div class="flex h-full flex-col gap-4">
          <div class="flex items-start justify-between gap-4">
            <div class="min-w-0">
              <NuxtLink :to="`/instances/${encodeURIComponent(instance.slug)}/detail`" class="block truncate text-[length:var(--font-size-body)] font-semibold text-[var(--color-text)] hover:text-[var(--accent-700)]">{{ instance.name }}</NuxtLink>
              <div class="mt-1 flex items-center gap-1">
                <code class="break-all font-mono text-[length:var(--font-size-table-header)] text-[var(--neutral-700)]" data-testid="instance-id">{{ instance.slug }}</code>
                <AppCopyButton :text="instance.slug" icon-only color="neutral" variant="ghost" size="xs" error-message="Unable to copy Instance slug. Select the slug and copy it manually." data-testid="copy-instance-id" @copied="error = ''" @error="message => error = message" />
              </div>
              <p class="mt-1 text-[length:var(--font-size-table-header)] text-[var(--neutral-700)]">{{ modelName(instance.model_id) }}</p>
            </div>
            <StatusTag :variant="statusVariant(instanceState(instance))">{{ instanceState(instance) }}</StatusTag>
          </div>

          <div v-if="importFor(instance)?.state === 'COMPLETED' && importFor(instance)?.error" data-testid="import-metadata-warning" class="flex items-start gap-2 border border-[var(--color-divider)] px-3 py-2">
            <StatusTag variant="pending">Import warning</StatusTag>
            <p class="min-w-0 text-[length:var(--font-size-table-header)] text-[var(--neutral-800)]">{{ importFor(instance)?.error }}</p>
          </div>

          <div class="grid grid-cols-2 gap-x-4 gap-y-2 text-[length:var(--font-size-table-header)] text-[var(--neutral-700)]">
            <span>Priority: {{ instance.priority }}</span><span>GPU: {{ instance.gpu_mode }}</span><span>{{ instance.always_on ? 'Always On' : 'Not Always On' }}</span><span>{{ instance.autoload_enabled ? 'Autoload' : 'Manual load' }}</span><span class="col-span-2">{{ instance.eviction_enabled ? 'Resource-pressure eviction allowed' : 'Protected from resource-pressure eviction' }}</span>
          </div>

          <div v-if="isRunning(instance)" class="grid grid-cols-2 gap-x-5 gap-y-4 border-t border-[var(--color-divider)] pt-4" data-testid="instance-telemetry">
            <div><div class="text-[length:var(--font-size-kicker)] text-[var(--neutral-700)]">{{ globalGPUFallback(telemetryFor(instance)) ? 'Global GPU usage' : 'Instance GPU usage' }}</div><div class="mt-1 font-mono text-[length:var(--font-size-h5)] text-[var(--color-text)]" data-testid="instance-gpu-usage">{{ formatPercent(gpuPercent(telemetryFor(instance))) }}</div><div class="mt-1 h-[3px] w-full bg-[var(--neutral-300)]"><div class="h-full bg-[var(--color-accent)]" :style="{ width: `${clampPercent(gpuPercent(telemetryFor(instance)))}%` }" /></div></div>
            <div><div class="text-[length:var(--font-size-kicker)] text-[var(--neutral-700)]">VRAM</div><div class="mt-1 font-mono text-[length:var(--font-size-h5)] text-[var(--color-text)]" data-testid="instance-vram">{{ formatBytes(vramBytes(telemetryFor(instance))) }}</div><div class="mt-1 h-[3px] w-full bg-[var(--neutral-300)]"><div class="h-full bg-[var(--color-accent)]" :style="{ width: `${fractionPercent(vramBytes(telemetryFor(instance)), gpuCapacity(telemetryFor(instance)))}%` }" /></div></div>
            <div><div class="text-[length:var(--font-size-kicker)] text-[var(--neutral-700)]">CPU</div><div class="mt-1 font-mono text-[length:var(--font-size-h5)] text-[var(--color-text)]" data-testid="instance-cpu">{{ formatPercent(telemetryFor(instance)?.cpu_percent) }}</div><div class="mt-1 h-[3px] w-full bg-[var(--neutral-300)]"><div class="h-full bg-[var(--color-accent)]" :style="{ width: `${clampPercent(telemetryFor(instance)?.cpu_percent)}%` }" /></div></div>
            <div><div class="text-[length:var(--font-size-kicker)] text-[var(--neutral-700)]">RAM</div><div class="mt-1 font-mono text-[length:var(--font-size-h5)] text-[var(--color-text)]" data-testid="instance-memory">{{ formatBytes(telemetryFor(instance)?.memory_used_bytes) }}</div><div class="mt-1 h-[3px] w-full bg-[var(--neutral-300)]"><div class="h-full bg-[var(--color-accent)]" :style="{ width: `${fractionPercent(telemetryFor(instance)?.memory_used_bytes, hostRAMTotal())}%` }" /></div></div>
            <div class="col-span-2 text-[length:var(--font-size-kicker)] text-[var(--neutral-700)]" data-testid="instance-gpu-placement">{{ placement(instance) }}</div>
          </div>

          <div v-else class="border-t border-[var(--color-divider)] pt-4">
            <div v-if="instanceState(instance) === 'FAILED' || instanceState(instance) === 'CANCELLED'" class="bg-[var(--neutral-200)] px-3 py-2 font-mono text-[length:var(--font-size-table-header)] leading-relaxed text-[var(--neutral-800)]">{{ nonRunningMessage(instance) }}</div>
            <p v-else class="text-[length:var(--font-size-table-header)] leading-relaxed text-[var(--neutral-700)]">{{ nonRunningMessage(instance) }}</p>
            <AppButton v-if="importBlocked(instance)" to="/downloads" intent="ghost" size="xs" class="mt-2">View download</AppButton>
          </div>

          <div class="mt-auto flex flex-wrap gap-1 border-t border-[var(--color-divider)] pt-3">
            <template v-if="!importBlocked(instance)">
              <AppButton v-if="['UNLOADED', 'FAILED'].includes(instanceState(instance))" intent="ghost" size="xs" :loading="pending === `${instance.id}:start`" @click="action(instance, 'start')">Launch</AppButton>
              <AppButton v-else intent="ghost" size="xs" :loading="pending === `${instance.id}:stop`" @click="action(instance, 'stop')">Stop</AppButton>
            </template>
            <AppButton :to="`/instances/${encodeURIComponent(instance.slug)}/detail`" intent="ghost" size="xs">Details</AppButton>
            <AppButton v-if="!importBlocked(instance)" intent="ghost" size="xs" @click="showLogs(instance)">Logs</AppButton>
            <UDropdownMenu v-if="!importBlocked(instance)" :items="cardOverflowItems(instance)">
              <AppButton intent="ghost" size="xs" data-testid="instance-card-more" aria-label="More instance actions">More</AppButton>
            </UDropdownMenu>
            <AppButton v-else intent="ghost" size="xs" :loading="pending === `${instance.id}:delete`" @click="remove(instance)">Delete</AppButton>
          </div>
        </div>
      </Frame>
    </div>

    <p v-if="instances.length" class="border-t border-[var(--color-divider)] pt-4 text-[length:var(--font-size-table-header)] leading-relaxed text-[var(--neutral-700)]">GPU telemetry degrades per metric: when process-level utilization cannot be attributed, the assigned device's utilization is shown and labelled Global GPU usage. CPU and RAM stay process-scoped.</p>

    <UModal v-model:open="logsOpen" :title="logTitle" :ui="{ content: 'w-[calc(100vw-2rem)] max-w-none sm:max-w-6xl' }"><template #body><InstanceLogViewer v-if="logsOpen && logInstanceId" :instance-id="logInstanceId" embedded /></template></UModal>
    <AppConfirmationModal ref="confirmation" />
  </div>
</template>