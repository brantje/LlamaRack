<script setup lang="ts">
import type { Instance, RuntimeTelemetry } from '~/composables/useManager'

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

const manager = useManager()
const route = useRoute()
const instanceID = computed(() => String(route.params.id || ''))
const loading = ref(true)
const error = ref('')
const instance = computed(() => manager.instances.value.find(item => item.id === instanceID.value))
const model = computed(() => instance.value ? manager.models.value.find(item => item.id === instance.value!.model_id) : undefined)
const runtime = computed(() => instance.value ? manager.runtimeForInstance(instance.value) : undefined)
const telemetry = computed(() => instance.value ? manager.telemetryForInstance(instance.value) as DetailTelemetry | undefined : undefined)
const llama = computed(() => telemetry.value?.llama_metrics)
const specPositions = computed(() => Object.entries(llama.value?.spec_accepted_tokens_per_position || {}).sort((a, b) => Number(a[0]) - Number(b[0])))
const hasProcessGPUUtilization = computed(() => Boolean(telemetry.value?.gpus?.some(gpu => gpu.utilization_pct !== undefined)))
const gpuUsageLabel = computed(() => telemetry.value?.gpu_utilization_pct !== undefined && !hasProcessGPUUtilization.value ? 'Global GPU usage' : 'Instance GPU usage')

function stateColor(state?: string) {
  if (state === 'READY') return 'success'
  if (state === 'FAILED') return 'error'
  if (state === 'STARTING' || state === 'LOADING') return 'primary'
  return 'neutral'
}

function formatNumber(value?: number, digits = 0) {
  if (value === undefined || !Number.isFinite(value)) return '—'
  return value.toLocaleString('en-US', { minimumFractionDigits: digits, maximumFractionDigits: digits })
}

function formatRate(value?: number) {
  return value === undefined || !Number.isFinite(value) ? '—' : `${formatNumber(value, 1)} tok/s`
}

function formatPercent(value?: number) {
  return value === undefined || !Number.isFinite(value) ? '—' : `${formatNumber(value, 1)}%`
}

function formatSeconds(value?: number) {
  return value === undefined || !Number.isFinite(value) ? '—' : `${formatNumber(value, 2)} s`
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

function contextHighWatermark() {
  if (llama.value?.context_tokens_max === undefined) return '—'
  const used = formatNumber(llama.value.context_tokens_max)
  return model.value?.context_length ? `${used} / ${formatNumber(model.value.context_length)}` : used
}

onMounted(async () => {
  try {
    if (!instance.value) await manager.refresh()
    if (!instance.value) error.value = `Instance “${instanceID.value}” was not found.`
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to load Instance details'
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <div class="space-y-5">
    <div class="flex items-start justify-between gap-6">
      <UPageHeader
        class="min-w-0 flex-1"
        headline="INSTANCE DETAIL"
        :title="instance?.name || instanceID"
        description="Live runtime resources and the complete llama.cpp metrics snapshot for this Instance."
      />
      <div class="flex flex-wrap justify-end gap-2">
        <UButton to="/instances" color="neutral" variant="soft">Back to Instances</UButton>
        <UButton v-if="instance" :to="`/instances/${encodeURIComponent(instance.id)}/edit`" color="neutral" variant="soft">Edit</UButton>
      </div>
    </div>

    <UAlert v-if="error" color="error" variant="subtle" :description="error" />
    <div v-if="loading" class="space-y-3"><USkeleton class="h-36 w-full" /><USkeleton class="h-52 w-full" /></div>

    <template v-else-if="instance">
      <UCard data-testid="instance-detail-runtime">
        <div class="space-y-4">
          <div class="flex flex-wrap items-center justify-between gap-3">
            <div>
              <p class="text-sm font-semibold text-highlighted">Runtime snapshot</p>
              <p class="mt-1 font-mono text-xs text-muted">{{ instance.id }}</p>
            </div>
            <UBadge :color="stateColor(runtime?.state)" variant="subtle">{{ runtime?.state || 'UNLOADED' }}</UBadge>
          </div>
          <dl class="grid gap-4 text-sm sm:grid-cols-2 lg:grid-cols-4">
            <div><dt class="text-xs text-dimmed">PID</dt><dd class="mt-1 font-semibold text-highlighted">{{ runtime?.pid || '—' }}</dd></div>
            <div><dt class="text-xs text-dimmed">Port</dt><dd class="mt-1 font-semibold text-highlighted">{{ runtime?.port || '—' }}</dd></div>
            <div><dt class="text-xs text-dimmed">Placed on</dt><dd class="mt-1 font-semibold text-highlighted">{{ telemetry?.gpu_devices?.length ? telemetry.gpu_devices.join(', ') : '—' }}</dd></div>
            <div><dt class="text-xs text-dimmed">{{ gpuUsageLabel }}</dt><dd class="mt-1 font-semibold text-highlighted">{{ formatPercent(telemetry?.gpu_utilization_pct) }}</dd></div>
            <div><dt class="text-xs text-dimmed">VRAM</dt><dd class="mt-1 font-semibold text-highlighted">{{ formatBytes(telemetry?.vram_used_bytes) }}</dd></div>
            <div><dt class="text-xs text-dimmed">CPU</dt><dd class="mt-1 font-semibold text-highlighted">{{ formatPercent(telemetry?.cpu_percent) }}</dd></div>
            <div><dt class="text-xs text-dimmed">RAM</dt><dd class="mt-1 font-semibold text-highlighted">{{ formatBytes(telemetry?.memory_used_bytes) }}</dd></div>
            <div><dt class="text-xs text-dimmed">Snapshot time</dt><dd class="mt-1 font-semibold text-highlighted">{{ telemetry?.collected_at ? new Date(telemetry.collected_at).toLocaleTimeString() : '—' }}</dd></div>
          </dl>
        </div>
      </UCard>

      <UAlert
        v-if="!llama"
        color="neutral"
        variant="subtle"
        :title="runtime?.state === 'READY' ? 'Collecting llama.cpp metrics' : 'llama.cpp metrics unavailable while stopped'"
        :description="runtime?.state === 'READY' ? 'The detail page updates automatically when the next metrics snapshot arrives.' : 'Start the Instance to populate throughput, request, decode and speculative-decoding metrics.'"
      />

      <template v-else>
        <UCard data-testid="instance-detail-throughput">
          <div class="space-y-4">
            <div><p class="text-sm font-semibold text-highlighted">Throughput & load</p><p class="mt-1 text-xs text-muted">Live llama.cpp gauges from the managed worker.</p></div>
            <dl class="grid gap-4 text-sm sm:grid-cols-2 lg:grid-cols-3">
              <div><dt class="text-xs text-dimmed">Generation throughput</dt><dd class="mt-1 text-lg font-semibold text-highlighted">{{ formatRate(llama.predicted_tokens_per_second) }}</dd></div>
              <div><dt class="text-xs text-dimmed">Prompt throughput</dt><dd class="mt-1 text-lg font-semibold text-highlighted">{{ formatRate(llama.prompt_tokens_per_second) }}</dd></div>
              <div><dt class="text-xs text-dimmed">Active requests</dt><dd class="mt-1 text-lg font-semibold text-highlighted">{{ formatNumber(llama.requests_processing) }}</dd></div>
              <div><dt class="text-xs text-dimmed">Queued requests</dt><dd class="mt-1 text-lg font-semibold text-highlighted">{{ formatNumber(llama.requests_deferred) }}</dd></div>
              <div><dt class="text-xs text-dimmed">Context high-watermark</dt><dd class="mt-1 text-lg font-semibold text-highlighted">{{ contextHighWatermark() }}</dd></div>
              <div><dt class="text-xs text-dimmed">Busy slots / decode</dt><dd class="mt-1 text-lg font-semibold text-highlighted">{{ formatNumber(llama.busy_slots_per_decode, 2) }}</dd></div>
            </dl>
          </div>
        </UCard>

        <UCard data-testid="instance-detail-counters">
          <div class="space-y-4">
            <div><p class="text-sm font-semibold text-highlighted">Cumulative counters</p><p class="mt-1 text-xs text-muted">Counters reset when the llama-server process restarts.</p></div>
            <dl class="grid gap-4 text-sm sm:grid-cols-2 lg:grid-cols-3">
              <div><dt class="text-xs text-dimmed">Prompt tokens</dt><dd class="mt-1 font-semibold text-highlighted">{{ formatNumber(llama.prompt_tokens_total) }}</dd></div>
              <div><dt class="text-xs text-dimmed">Prompt processing time</dt><dd class="mt-1 font-semibold text-highlighted">{{ formatSeconds(llama.prompt_seconds_total) }}</dd></div>
              <div><dt class="text-xs text-dimmed">Generated tokens</dt><dd class="mt-1 font-semibold text-highlighted">{{ formatNumber(llama.predicted_tokens_total) }}</dd></div>
              <div><dt class="text-xs text-dimmed">Generation time</dt><dd class="mt-1 font-semibold text-highlighted">{{ formatSeconds(llama.predicted_seconds_total) }}</dd></div>
              <div><dt class="text-xs text-dimmed">llama_decode() calls</dt><dd class="mt-1 font-semibold text-highlighted">{{ formatNumber(llama.decode_total) }}</dd></div>
            </dl>
          </div>
        </UCard>

        <UCard data-testid="instance-detail-speculative">
          <div class="space-y-4">
            <div><p class="text-sm font-semibold text-highlighted">Speculative decoding</p><p class="mt-1 text-xs text-muted">Counters are zero when speculative decoding is disabled.</p></div>
            <dl class="grid gap-4 text-sm sm:grid-cols-2 lg:grid-cols-4">
              <div><dt class="text-xs text-dimmed">Draft tokens</dt><dd class="mt-1 font-semibold text-highlighted">{{ formatNumber(llama.spec_draft_tokens_total) }}</dd></div>
              <div><dt class="text-xs text-dimmed">Accepted draft tokens</dt><dd class="mt-1 font-semibold text-highlighted">{{ formatNumber(llama.spec_accepted_tokens_total) }}</dd></div>
              <div><dt class="text-xs text-dimmed">Verification steps</dt><dd class="mt-1 font-semibold text-highlighted">{{ formatNumber(llama.spec_drafts_total) }}</dd></div>
              <div><dt class="text-xs text-dimmed">Acceptance rate</dt><dd class="mt-1 font-semibold text-highlighted">{{ formatPercent(llama.spec_acceptance_rate_pct) }}</dd></div>
            </dl>
            <div v-if="specPositions.length" class="space-y-2">
              <p class="text-xs font-semibold text-muted">Accepted tokens per draft position</p>
              <div class="flex flex-wrap gap-2" data-testid="instance-detail-spec-positions">
                <UBadge v-for="[position, value] in specPositions" :key="position" color="neutral" variant="soft">Position {{ position }}: {{ formatNumber(value) }}</UBadge>
              </div>
            </div>
          </div>
        </UCard>
      </template>
    </template>
  </div>
</template>
