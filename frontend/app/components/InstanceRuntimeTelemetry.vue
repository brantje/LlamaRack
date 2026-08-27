<script setup lang="ts">
import type { RuntimeTelemetry } from '~/composables/useManager'

const props = defineProps<{
  state: string
  telemetry?: RuntimeTelemetry
}>()

const active = computed(() => ['STARTING', 'LOADING', 'READY', 'STOPPING'].includes(props.state))
const gpuLabel = computed(() => props.telemetry?.gpu_devices?.length ? props.telemetry.gpu_devices.join(', ') : 'No GPU allocation detected')
const hasProcessGPUUtilization = computed(() => Boolean(props.telemetry?.gpus?.some(gpu => gpu.utilization_pct !== undefined)))
const hasProcessVRAM = computed(() => Boolean(props.telemetry?.gpus?.some(gpu => gpu.vram_used_bytes !== undefined)))
const globalGPUFallback = computed(() => Boolean(props.telemetry?.gpu_utilization_pct !== undefined && !hasProcessGPUUtilization.value))
const globalVRAMFallback = computed(() => Boolean(props.telemetry?.vram_used_bytes !== undefined && !hasProcessVRAM.value))
const hasGlobalFallback = computed(() => globalGPUFallback.value || globalVRAMFallback.value)
const gpuUsageLabel = computed(() => globalGPUFallback.value ? 'GPU usage (global fallback)' : 'Instance GPU usage')
const vramLabel = computed(() => globalVRAMFallback.value ? 'VRAM (global fallback)' : 'VRAM')

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

function gpuDetail(device: RuntimeTelemetry['gpus'][number]) {
  const parts = [device.device_id]
  if (device.vram_used_bytes !== undefined) parts.push(formatBytes(device.vram_used_bytes))
  if (device.utilization_pct !== undefined) parts.push(formatPercent(device.utilization_pct))
  return parts.join(' · ')
}
</script>

<template>
  <div v-if="active" data-testid="instance-telemetry" class="space-y-3">
    <USeparator />
    <div class="flex items-center justify-between gap-3">
      <p class="text-xs font-semibold text-highlighted">Live Instance resources</p>
      <span v-if="telemetry" class="font-mono text-[11px] text-dimmed">PID {{ telemetry.pid }}</span>
      <span v-else class="text-[11px] text-dimmed">Collecting…</span>
    </div>
    <p v-if="hasGlobalFallback" data-testid="instance-global-fallback" class="text-[11px] leading-4 text-warning">
      One or more process-level GPU metrics are unavailable. Values marked global fallback are device-wide and may include other processes.
    </p>
    <dl class="grid grid-cols-2 gap-x-4 gap-y-2 text-xs">
      <div>
        <dt class="text-dimmed">Placed on</dt>
        <dd class="mt-0.5 font-semibold text-highlighted" data-testid="instance-gpu-placement">{{ telemetry ? gpuLabel : '—' }}</dd>
      </div>
      <div>
        <dt class="text-dimmed">{{ gpuUsageLabel }}</dt>
        <dd class="mt-0.5 font-semibold text-highlighted" data-testid="instance-gpu-usage">{{ formatPercent(telemetry?.gpu_utilization_pct) }}</dd>
      </div>
      <div>
        <dt class="text-dimmed">{{ vramLabel }}</dt>
        <dd class="mt-0.5 font-semibold text-highlighted" data-testid="instance-vram">{{ formatBytes(telemetry?.vram_used_bytes) }}</dd>
      </div>
      <div>
        <dt class="text-dimmed">CPU</dt>
        <dd class="mt-0.5 font-semibold text-highlighted" data-testid="instance-cpu">{{ formatPercent(telemetry?.cpu_percent) }}</dd>
      </div>
      <div>
        <dt class="text-dimmed">RAM</dt>
        <dd class="mt-0.5 font-semibold text-highlighted" data-testid="instance-memory">{{ formatBytes(telemetry?.memory_used_bytes) }}</dd>
      </div>
    </dl>
    <div v-if="telemetry?.gpus?.length" class="flex flex-wrap gap-1.5" data-testid="instance-gpu-details">
      <UBadge v-for="gpu in telemetry.gpus" :key="gpu.device_id" color="neutral" variant="soft" size="sm">
        {{ gpuDetail(gpu) }}
      </UBadge>
    </div>
  </div>
</template>
