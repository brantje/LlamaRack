<script setup lang="ts">
import {
  currentZoneIndex,
  formatCompactContext,
  formatContextRange,
  gpuAutoRole,
  gpuAutoRoleLabel,
  gpuDisplayName,
  isPlacementRanges,
  minimumContext,
  nearbyTransitionCopy,
  noFitSliderMaximum,
  primaryPlacementResult,
  snapContext,
  contextToSliderPosition,
  contextStep,
  sliderPositionToContext,
  sliderZones,
  useCompactRangeLayout,
  usingGpuSummary,
  whyPlacement,
  zoneShortLabel,
  type PlacementRanges,
  type PlacementZone
} from '~/utils/placementPresentation'

type GPU = {
  id: string
  backend: string
  index: number
  uuid?: string
  name: string
  total_bytes: number
  used_bytes: number
  free_bytes: number
  utilization_pct: number
}
type HardwareSnapshot = {
  ram_total_bytes?: number
  ram_available_bytes?: number
  gpus?: GPU[]
  collected_at?: string
}
type Recommendation = {
  context_length: number
  context_capability?: number
  context_assumed: boolean
  confidence: 'low' | 'medium' | 'high' | string
  metadata?: { context_length?: number }
  metadata_warning?: string
  hardware_warning?: string
  quantization: { name?: string; summary: string; tradeoff: string }
  memory: {
    weights_bytes: number
    kv_cache_bytes: number
    runtime_overhead_bytes: number
    cpu_only_ram_bytes: number
    full_offload_vram_bytes: number
  }
  current_fit: boolean
  total_hardware_fit: boolean
  cpu_fit: boolean
  offload: { mode: string; gpu_layers?: number; n_cpu_moe?: number; devices?: string[]; tensor_split?: string; kv_on_gpu?: boolean; reason: string }
  placement_ranges?: PlacementRanges
}
type ConfigResponse = {
  effective?: { values?: Record<string, string> }
}

const props = defineProps<{
  gpuMode: string
  gpuDevices: string[]
  tensorSplit: string
  modelId?: string
  llamaOptions?: Record<string, string>
  hidePlacementControls?: boolean
}>()
const emit = defineEmits<{
  'update:gpuMode': [value: string]
  'update:gpuDevices': [value: string[]]
  'update:tensorSplit': [value: string]
  'update:contextSize': [value: string]
}>()

const defaultContext = 4096
const manager = useManager()
const snapshot = ref<HardwareSnapshot | null>(null)
const recommendation = ref<Recommendation | null>(null)
const contextSize = ref(defaultContext)
const knownCapability = ref(0)
const contextSource = ref('estimate default')
const loading = ref(false)
const recommendationLoading = ref(false)
const error = ref('')
const recommendationError = ref('')
let recommendationTimer: ReturnType<typeof setTimeout> | undefined

const modeItems = [{ label: 'Automatic', value: 'auto' }, { label: 'Manual', value: 'manual' }]
const gpus = computed(() => snapshot.value?.gpus || [])
const deviceItems = computed(() => gpus.value.map(gpu => ({
  label: `${gpuDisplayName([gpu], gpu.id)} · ${gpu.id} · ${formatBytes(gpu.free_bytes)} free`, value: gpu.id
})))
const contextCapability = computed(() => Math.max(0, knownCapability.value, recommendation.value?.context_capability || recommendation.value?.metadata?.context_length || 0))
const placementRanges = computed(() => recommendation.value && isPlacementRanges(recommendation.value.placement_ranges) ? recommendation.value.placement_ranges : undefined)
const placementZones = computed(() => placementRanges.value?.available ? placementRanges.value.zones || [] : [])
const mappingZones = computed(() => sliderZones(placementZones.value, placementRanges.value?.context_step || contextStep))
const modelContextLimit = computed(() => {
  const fromRanges = placementRanges.value?.maximum_context || 0
  return Math.max(fromRanges, contextCapability.value, 0)
})
const sliderContextMaximum = computed(() => {
  const capped = noFitSliderMaximum(placementZones.value, placementRanges.value?.context_step || contextStep)
  if (capped) return Math.max(minimumContext, capped)
  if (modelContextLimit.value > 0) return modelContextLimit.value
  return Math.max(contextSize.value, contextCapability.value || 32768, defaultContext)
})
const contextMaximum = computed(() => sliderContextMaximum.value)
const compactRanges = computed(() => useCompactRangeLayout(placementZones.value.length))
const currentRangeIndex = computed(() => currentZoneIndex(placementZones.value, contextSize.value))
const currentZone = computed(() => currentRangeIndex.value >= 0 ? placementZones.value[currentRangeIndex.value] : undefined)
const nearby = computed(() => nearbyTransitionCopy(placementRanges.value, contextSize.value))
const useZoneSlider = computed(() => mappingZones.value.length > 0)
const sliderMin = computed(() => useZoneSlider.value ? 0 : minimumContext)
const sliderMax = computed(() => useZoneSlider.value ? mappingZones.value.length : contextMaximum.value)
const sliderStep = computed(() => useZoneSlider.value ? 0.001 : 1)
const sliderValue = computed(() => useZoneSlider.value ? contextToSliderPosition(mappingZones.value, contextSize.value) : contextSize.value)
const primaryResult = computed(() => {
  const item = recommendation.value
  if (!item) return null
  return primaryPlacementResult(item.offload, item.current_fit, gpus.value, item.cpu_fit)
})
const whyCopy = computed(() => {
  const item = recommendation.value
  if (!item) return null
  return whyPlacement(item.offload, item.current_fit)
})
const selectedDevices = computed(() => currentZone.value?.devices || recommendation.value?.offload.devices || [])
const gpuSummary = computed(() => usingGpuSummary(selectedDevices.value.length, gpus.value.length))
const fitsAfterFreeing = computed(() => Boolean(recommendation.value && !recommendation.value.current_fit && recommendation.value.total_hardware_fit))
const autoPlacementCopy = computed(() => {
  if (recommendation.value?.offload.mode === 'moe') {
    return {
      summary: 'LlamaRack uses every currently free GPU for this MoE placement and keeps routed experts in system RAM.',
      detail: 'Attention and the configured GPU layers stay on GPU; system RAM is reserved for the expert blocks that do not fit in VRAM.'
    }
  }
  return {
    summary: 'LlamaRack chooses the smallest GPU set that safely fits the model.',
    detail: 'It keeps the model on one GPU when possible and adds GPUs only when needed.'
  }
})

function isRecommendation(value: unknown): value is Recommendation {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false
  const item = value as Partial<Recommendation>
  return typeof item.context_length === 'number'
    && typeof item.confidence === 'string'
    && typeof item.current_fit === 'boolean'
    && Boolean(item.quantization && typeof item.quantization.summary === 'string' && typeof item.quantization.tradeoff === 'string')
    && Boolean(item.memory && typeof item.memory.full_offload_vram_bytes === 'number' && typeof item.memory.cpu_only_ram_bytes === 'number' && typeof item.memory.kv_cache_bytes === 'number')
    && Boolean(item.offload && typeof item.offload.mode === 'string' && typeof item.offload.reason === 'string')
}

function parseContext(value: unknown) {
  const parsed = Number(value)
  return Number.isFinite(parsed) && parsed > 0 ? Math.floor(parsed) : null
}

function commitContext(value: number) {
  const max = modelContextLimit.value > 0 ? modelContextLimit.value : sliderContextMaximum.value
  return snapContext(value, minimumContext, max)
}

async function refreshHardware() {
  loading.value = true
  error.value = ''
  try {
    const result = await manager.request<HardwareSnapshot>('/api/v1/hardware')
    snapshot.value = result && typeof result === 'object' && !Array.isArray(result) ? result : null
  } catch (value: any) {
    snapshot.value = null
    error.value = value?.data?.error || value?.message || 'Unable to read hardware state'
  } finally {
    loading.value = false
  }
}

async function loadContext() {
  const local = parseContext(props.llamaOptions?.['ctx-size'])
  if (local) {
    contextSize.value = local
    contextSource.value = 'Instance override'
    return
  }
  contextSize.value = defaultContext
  contextSource.value = 'estimate default'
  if (!props.modelId) return

  try {
    const params = new URLSearchParams({ model_id: props.modelId })
    const result = await manager.request<ConfigResponse>(`/api/v1/llamacpp/config?${params.toString()}`)
    const inherited = parseContext(result?.effective?.values?.['ctx-size'])
    if (inherited) {
      contextSize.value = inherited
      contextSource.value = 'inherited llama.cpp config'
    }
  } catch {
    // Recommendation remains useful with the documented 4K estimate default.
  }
}

async function refreshRecommendation() {
  recommendationError.value = ''
  if (!props.modelId) {
    recommendation.value = null
    return
  }
  recommendationLoading.value = true
  try {
    const query = new URLSearchParams({ context_length: String(commitContext(contextSize.value)) })
    const result = await manager.request<Recommendation>(`/api/v1/models/${encodeURIComponent(props.modelId)}/recommendation?${query.toString()}`)
    if (!isRecommendation(result)) {
      recommendation.value = null
      return
    }
    recommendation.value = result
    const capability = result.context_capability || result.metadata?.context_length || 0
    if (capability > 0) knownCapability.value = capability
    const limit = result.placement_ranges && isPlacementRanges(result.placement_ranges)
      ? result.placement_ranges.maximum_context || capability
      : capability
    if (limit > 0 && contextSize.value > limit) {
      const next = snapContext(limit, minimumContext, limit)
      contextSize.value = next
      emit('update:contextSize', String(next))
    }
  } catch (value: any) {
    recommendation.value = null
    recommendationError.value = value?.data?.error || value?.message || 'Unable to estimate model resources'
  } finally {
    recommendationLoading.value = false
  }
}

async function refresh() {
  await Promise.all([refreshHardware(), loadContext()])
  await refreshRecommendation()
}

function scheduleRecommendation() {
  if (recommendationTimer) clearTimeout(recommendationTimer)
  recommendationTimer = setTimeout(() => {
    const snapped = commitContext(contextSize.value)
    if (snapped !== contextSize.value) {
      contextSize.value = snapped
      emit('update:contextSize', String(snapped))
    }
    void refreshRecommendation()
  }, 180)
}

function updateSlider(value: unknown) {
  const raw = Array.isArray(value) ? value[0] : value
  const parsed = Number(raw)
  if (!Number.isFinite(parsed)) return
  if (useZoneSlider.value) {
    updateContext(sliderPositionToContext(mappingZones.value, parsed), true)
    return
  }
  updateContext(parsed, true)
}

function updateContext(value: unknown, live = false) {
  const raw = Array.isArray(value) ? value[0] : value
  const parsed = parseContext(raw)
  if (!parsed) return
  const clamped = Math.min(Math.max(parsed, minimumContext), sliderContextMaximum.value)
  const next = live ? clamped : commitContext(clamped)
  contextSize.value = next
  contextSource.value = 'Instance override'
  emit('update:contextSize', String(commitContext(next)))
  scheduleRecommendation()
}

function formatBytes(value: number) {
  if (!Number.isFinite(value) || value <= 0) return '0 B'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let amount = value
  let unit = 0
  while (amount >= 1024 && unit < units.length - 1) {
    amount /= 1024
    unit++
  }
  return `${amount >= 10 || unit === 0 ? amount.toFixed(0) : amount.toFixed(1)} ${units[unit]}`
}

function updateDevices(value: unknown) {
  emit('update:gpuDevices', Array.isArray(value) ? value.map(String) : [])
}

function selectGpu(gpuID: string) {
  emit('update:gpuMode', 'manual')
  emit('update:gpuDevices', [gpuID])
  emit('update:tensorSplit', '')
}

function isSelected(gpuID: string) {
  return props.gpuMode === 'manual' && props.gpuDevices.length === 1 && props.gpuDevices[0] === gpuID
}

function zoneCurrent(zone: PlacementZone, index: number) {
  return currentRangeIndex.value === index
}

watch(() => props.gpuMode, (mode) => {
  if (mode === 'auto') {
    emit('update:gpuDevices', [])
    emit('update:tensorSplit', '')
  }
})
watch(() => props.modelId, async () => {
  recommendation.value = null
  knownCapability.value = 0
  await loadContext()
  await refreshRecommendation()
})
watch(() => props.llamaOptions?.['ctx-size'], async (value, oldValue) => {
  if (value === oldValue) return
  const incoming = parseContext(value)
  if (incoming && commitContext(incoming) === commitContext(contextSize.value)) return
  await loadContext()
  scheduleRecommendation()
})
onMounted(() => void refresh())
onBeforeUnmount(() => {
  if (recommendationTimer) clearTimeout(recommendationTimer)
})
</script>

<template>
  <div class="space-y-4">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div>
        <p class="font-semibold">{{ gpuMode === 'manual' ? 'Manual' : 'Automatic' }}</p>
        <p class="text-xs text-muted">{{ gpuMode === 'manual' ? 'Select the GPUs this Instance should use.' : autoPlacementCopy.summary }}</p>
        <p v-if="gpuMode !== 'manual'" class="mt-1 text-xs text-muted">{{ autoPlacementCopy.detail }}</p>
      </div>
      <UButton size="xs" color="neutral" variant="soft" :loading="loading || recommendationLoading" @click="refresh">Refresh hardware</UButton>
    </div>

    <div v-if="error" class="border border-[var(--color-divider)] p-3">
      <div class="flex items-start gap-2"><StatusTag variant="failed">Error</StatusTag><p class="text-xs leading-5 text-[var(--neutral-800)]">{{ error }}</p></div>
    </div>
    <div v-if="recommendationError" class="border border-[var(--color-divider)] p-3">
      <div class="flex items-start gap-2"><StatusTag variant="failed">Error</StatusTag><p class="text-xs leading-5 text-[var(--neutral-800)]">{{ recommendationError }}</p></div>
    </div>

    <UFormField
      v-if="modelId"
      label="Context size"
      name="ctx-size"
      description="How much text the model can keep in memory at once."
    >
      <div class="mb-2 flex flex-wrap justify-between gap-2 text-xs text-muted">
        <span>Selected: <strong class="font-mono tabular-nums">{{ contextSize.toLocaleString() }}</strong> tokens · {{ contextSource }}</span>
        <span v-if="contextCapability">Model capability: <strong class="font-mono tabular-nums">{{ contextCapability.toLocaleString() }}</strong> tokens</span>
      </div>
      <USlider
        data-testid="context-slider"
        aria-label="Context size in tokens"
        color="primary"
        size="xl"
        :model-value="sliderValue"
        :min="sliderMin"
        :max="sliderMax"
        :step="sliderStep"
        :ui="{
          root: 'cursor-pointer py-3 border border-[var(--color-primary)] py-4',
          track: 'h-4 overflow-visible rounded-none border border-[var(--neutral-600)] bg-[var(--neutral-500)]',
          range: 'rounded-none bg-[var(--color-accent)]',
          thumb: 'size-7 rounded-none border-2 border-[var(--color-accent)] bg-[var(--color-text)] cursor-pointer'
        }"
        @update:model-value="updateSlider"
      />
      <div class="mt-1 flex justify-between font-mono text-xs tabular-nums text-muted">
        <span>{{ formatCompactContext(minimumContext) }}</span>
        <span>{{ formatCompactContext(contextMaximum) }}</span>
      </div>
      
    </UFormField>

    <div v-if="modelId && placementRanges && !placementRanges.available" data-testid="placement-ranges-unavailable" class="border border-[var(--color-divider)] p-3">
      <div class="flex items-start gap-2">
        <StatusTag variant="neutral">Placement ranges unavailable</StatusTag>
        <p class="text-xs leading-5 text-[var(--neutral-800)]">{{ placementRanges.unavailable_reason || 'LlamaRack could not determine reliable context boundaries for this Model.' }}</p>
      </div>
    </div>

    <div v-if="placementZones.length" data-testid="placement-ranges" class="space-y-3">
      <div v-if="!compactRanges" class="hidden md:grid gap-px bg-[var(--color-divider)]" :style="{ gridTemplateColumns: `repeat(${placementZones.length}, minmax(0, 1fr))` }" data-testid="placement-range-bar">
        <button
          v-for="(zone, index) in placementZones"
          :key="`${zone.kind}-${zone.start_context}`"
          type="button"
          class="bg-[var(--color-surface)] px-2 py-3 text-left"
          :class="zoneCurrent(zone, index) ? 'outline outline-2 outline-[var(--color-accent)] outline-offset-[-2px]' : ''"
          :aria-pressed="zoneCurrent(zone, index)"
          :data-testid="`placement-zone-${index}`"
          @click="updateContext(zone.start_context)"
        >
          <span class="block text-xs font-semibold">{{ zoneShortLabel(zone) }}</span>
          <span class="mt-1 block font-mono text-[length:var(--font-size-table-header)] tabular-nums text-muted">{{ formatContextRange(zone.start_context, zone.end_context) }}</span>
          <span v-if="zoneCurrent(zone, index)" class="mt-2 block text-xs">Current</span>
        </button>
      </div>
      <div v-if="compactRanges" class="space-y-2 text-xs" data-testid="placement-range-compact">
        <p v-if="nearby.current"><span class="font-semibold">{{ zoneShortLabel(nearby.current) }}</span> at <span class="font-mono tabular-nums">{{ formatCompactContext(contextSize) }}</span></p>
        <p>Current context <span class="font-mono tabular-nums">{{ contextSize.toLocaleString() }}</span></p>
        <p v-if="nearby.previousLabel">Previous transition <span class="font-mono tabular-nums">{{ nearby.previousLabel }}</span></p>
        <p v-if="nearby.nextLabel">Next transition <span class="font-mono tabular-nums">{{ nearby.nextLabel }}</span></p>
        <p v-if="nearby.gpuOnlyMax">GPU-only limit <span class="font-mono tabular-nums">{{ nearby.gpuOnlyMax.toLocaleString() }}</span></p>
      </div>
      <div class="text-xs" :class="compactRanges ? '' : 'md:hidden'" data-testid="placement-range-list">
        <p v-for="(zone, index) in placementZones" :key="`list-${zone.kind}-${zone.start_context}`" class="flex flex-wrap justify-between gap-2 py-1">
          <span>{{ zoneShortLabel(zone) }}</span>
          <span class="font-mono tabular-nums">{{ formatContextRange(zone.start_context, zone.end_context) }}<span v-if="zoneCurrent(zone, index)"> ← current</span></span>
        </p>
        <p class="mt-2 text-muted">Current: {{ nearby.current ? zoneShortLabel(nearby.current) : 'Unknown' }} at <span class="font-mono tabular-nums">{{ formatCompactContext(contextSize) }}</span></p>
      </div>
      <UCollapsible v-if="compactRanges" data-testid="placement-range-all">
        <UButton color="neutral" variant="ghost" size="xs" trailing-icon="i-lucide-chevron-down" class="cursor-pointer px-0">All placement ranges</UButton>
        <template #content>
          <div class="mt-2 space-y-1 text-xs">
            <p v-for="(zone, index) in placementZones" :key="`all-${zone.kind}-${zone.start_context}`" class="flex flex-wrap justify-between gap-2">
              <span>{{ zoneShortLabel(zone) }}</span>
              <span class="font-mono tabular-nums">{{ formatContextRange(zone.start_context, zone.end_context) }}<span v-if="zoneCurrent(zone, index)"> ← current</span></span>
            </p>
          </div>
        </template>
      </UCollapsible>
    </div>

    <div v-if="recommendation" data-testid="hardware-recommendation" class="space-y-3">
      <div v-if="primaryResult" data-testid="execution-fit" class="border border-[var(--color-divider)] p-3">
        <div class="flex items-start gap-2">
          <StatusTag :variant="primaryResult.variant">{{ primaryResult.title }}</StatusTag>
          <div class="space-y-2 text-xs leading-5 text-[var(--neutral-800)]">
            <p>{{ primaryResult.description }}</p>
            <p>Model: {{ primaryResult.locations.model }} · Context cache: {{ primaryResult.locations.cache }}</p>
            <p v-if="gpuSummary && gpuMode !== 'manual'">{{ gpuSummary }}</p>
            <p v-if="fitsAfterFreeing">Fits after freeing GPU memory. Currently running workloads are using memory required by this placement.</p>
          </div>
        </div>
      </div>

      <div v-if="nearby.headline || nearby.previousLabel || nearby.nextLabel" data-testid="placement-transition" class="border border-[var(--color-divider)] p-3">
        <p v-if="nearby.headline" class="text-sm font-semibold">{{ nearby.headline }}</p>
        <p v-if="nearby.body" class="mt-1 text-xs leading-5 text-[var(--neutral-800)]">{{ nearby.body }}</p>
        <p v-if="nearby.previousLabel" class="mt-2 text-xs text-muted">Previous transition {{ nearby.previousLabel }}</p>
        <p v-if="nearby.nextLabel" class="mt-1 text-xs text-muted">Next transition {{ nearby.nextLabel }}</p>
        <UButton
          v-if="nearby.actionContext"
          data-testid="use-boundary-context"
          size="xs"
          color="primary"
          class="mt-3 cursor-pointer"
          @click="updateContext(nearby.actionContext)"
        >
          Use {{ nearby.actionContext.toLocaleString() }}
        </UButton>
      </div>

      <UCollapsible v-if="whyCopy" data-testid="placement-why">
        <UButton color="neutral" variant="ghost" size="xs" trailing-icon="i-lucide-chevron-down" class="cursor-pointer px-0">{{ whyCopy.title }}</UButton>
        <template #content>
          <p class="mt-2 text-xs leading-5 text-[var(--neutral-800)]">{{ whyCopy.body }}</p>
        </template>
      </UCollapsible>

      <UCollapsible data-testid="placement-technical">
        <UButton color="neutral" variant="ghost" size="xs" trailing-icon="i-lucide-chevron-down" class="cursor-pointer px-0">Technical details</UButton>
        <template #content>
          <div class="mt-3 space-y-3">
            <div class="flex flex-wrap items-center gap-2">
              <StatusTag :variant="recommendation.current_fit ? 'ready' : recommendation.total_hardware_fit ? 'pending' : 'neutral'">{{ recommendation.confidence }} confidence</StatusTag>
              <StatusTag v-if="recommendation.quantization.name" variant="neutral">{{ recommendation.quantization.name }}</StatusTag>
              <StatusTag variant="pending">{{ recommendation.offload.mode.replaceAll('_', ' ') }}</StatusTag>
              <StatusTag :variant="recommendation.current_fit ? 'ready' : 'neutral'">{{ recommendation.current_fit ? 'Fits current resources' : recommendation.total_hardware_fit ? 'Fits installed hardware after freeing resources' : recommendation.cpu_fit ? 'CPU fallback fits current RAM' : 'Resource pressure expected' }}</StatusTag>
            </div>
            <p class="text-xs text-muted">{{ recommendation.offload.reason }}</p>
            <dl class="grid gap-3 text-xs sm:grid-cols-2 lg:grid-cols-4">
              <div><dt class="text-dimmed">Weights estimate</dt><dd class="font-mono font-semibold tabular-nums">{{ formatBytes(recommendation.memory.weights_bytes) }}</dd></div>
              <div><dt class="text-dimmed">KV cache estimate</dt><dd class="font-mono font-semibold tabular-nums">{{ formatBytes(recommendation.memory.kv_cache_bytes) }}</dd></div>
              <div><dt class="text-dimmed">Runtime overhead</dt><dd class="font-mono font-semibold tabular-nums">{{ formatBytes(recommendation.memory.runtime_overhead_bytes) }}</dd></div>
              <div><dt class="text-dimmed">Full-offload VRAM</dt><dd class="font-mono font-semibold tabular-nums">{{ formatBytes(recommendation.memory.full_offload_vram_bytes) }}</dd></div>
              <div><dt class="text-dimmed">CPU-only RAM</dt><dd class="font-mono font-semibold tabular-nums">{{ formatBytes(recommendation.memory.cpu_only_ram_bytes) }}</dd></div>
              <div><dt class="text-dimmed">Selected context</dt><dd class="font-mono font-semibold tabular-nums">{{ recommendation.context_length.toLocaleString() }} tokens</dd></div>
              <div v-if="contextCapability"><dt class="text-dimmed">Context capability</dt><dd class="font-mono font-semibold tabular-nums">{{ contextCapability.toLocaleString() }} tokens</dd></div>
              <div v-if="recommendation.offload.gpu_layers"><dt class="text-dimmed">GPU layers</dt><dd class="font-mono font-semibold tabular-nums">{{ recommendation.offload.gpu_layers }}</dd></div>
              <div v-if="recommendation.offload.n_cpu_moe"><dt class="text-dimmed">CPU expert blocks</dt><dd class="font-mono font-semibold tabular-nums">{{ recommendation.offload.n_cpu_moe }}</dd></div>
            </dl>
            <div class="flex flex-wrap gap-2 text-xs">
              <StatusTag v-for="device in recommendation.offload.devices" :key="device" variant="neutral"><span class="font-mono">{{ device }}</span></StatusTag>
              <span v-if="recommendation.offload.tensor_split" class="text-muted">Tensor split: <strong class="font-mono">{{ recommendation.offload.tensor_split }}</strong></span>
              <span v-if="recommendation.offload.mode !== 'cpu'" class="text-muted">KV cache: <strong>{{ recommendation.offload.kv_on_gpu === false ? 'system RAM' : 'GPU' }}</strong></span>
            </div>
            <p class="text-xs text-muted"><strong>{{ recommendation.quantization.summary }}</strong> {{ recommendation.quantization.tradeoff }}</p>
            <div v-if="recommendation.metadata_warning" class="border border-[var(--color-divider)] p-3">
              <div class="flex items-start gap-2"><StatusTag variant="neutral">Metadata estimate fallback</StatusTag><p class="text-xs leading-5 text-[var(--neutral-800)]">{{ recommendation.metadata_warning }}</p></div>
            </div>
            <div v-if="recommendation.hardware_warning" class="border border-[var(--color-divider)] p-3">
              <div class="flex items-start gap-2"><StatusTag variant="pending">Hardware probe warning</StatusTag><p class="text-xs leading-5 text-[var(--neutral-800)]">{{ recommendation.hardware_warning }}</p></div>
            </div>
          </div>
        </template>
      </UCollapsible>
    </div>

    <p v-if="gpuSummary && gpus.length && gpuMode !== 'manual'" class="text-xs text-muted">{{ gpuSummary }}</p>
    <div v-if="snapshot" class="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
      <component
        :is="gpuMode === 'manual' ? 'button' : 'div'"
        v-for="gpu in gpus"
        :key="gpu.id"
        :type="gpuMode === 'manual' ? 'button' : undefined"
        :data-testid="`gpu-card-${gpu.id}`"
        :aria-pressed="gpuMode === 'manual' ? isSelected(gpu.id) : undefined"
        class="border border-[var(--color-divider)] bg-[var(--color-surface)] p-4 text-left"
        :class="{
          'cursor-pointer transition-colors hover:bg-[var(--neutral-100)] focus-visible:outline-none': gpuMode === 'manual',
          'border-[var(--color-accent)] bg-[var(--accent-100)]': isSelected(gpu.id)
        }"
        :aria-label="gpuMode === 'manual' ? `Select ${gpu.name || gpu.id}` : `${gpu.name || gpu.id}`"
        @click="gpuMode === 'manual' ? selectGpu(gpu.id) : undefined"
        @keydown.enter.prevent="gpuMode === 'manual' ? selectGpu(gpu.id) : undefined"
        @keydown.space.prevent="gpuMode === 'manual' ? selectGpu(gpu.id) : undefined"
      >
        <div class="flex items-start justify-between gap-2">
          <div class="min-w-0">
            <p class="text-sm font-semibold">{{ gpu.name || gpu.id }}</p>
            <p class="truncate font-mono text-xs text-muted">{{ gpu.id }}</p>
            <p class="mt-1 font-mono text-xs tabular-nums text-muted">{{ formatBytes(gpu.total_bytes) }}</p>
          </div>
          <div class="flex flex-col items-end gap-1">
            <StatusTag v-if="isSelected(gpu.id)" variant="ready">Selected</StatusTag>
            <StatusTag v-else-if="gpuMode !== 'manual'" :variant="gpuAutoRole(gpu.id, selectedDevices) === 'unused' ? 'neutral' : 'ready'">{{ gpuAutoRoleLabel(gpuAutoRole(gpu.id, selectedDevices), selectedDevices.length) }}</StatusTag>
            <StatusTag variant="neutral">{{ gpu.backend.toUpperCase() }}</StatusTag>
          </div>
        </div>
        <dl class="mt-3 grid grid-cols-2 gap-2 text-xs">
          <div><dt class="text-dimmed">VRAM free</dt><dd class="font-mono font-semibold tabular-nums">{{ formatBytes(gpu.free_bytes) }}</dd></div>
          <div><dt class="text-dimmed">VRAM total</dt><dd class="font-mono font-semibold tabular-nums">{{ formatBytes(gpu.total_bytes) }}</dd></div>
          <div><dt class="text-dimmed">Utilization</dt><dd class="font-mono font-semibold tabular-nums">{{ gpu.utilization_pct.toFixed(0) }}%</dd></div>
          <div><dt class="text-dimmed">VRAM used</dt><dd class="font-mono font-semibold tabular-nums">{{ formatBytes(gpu.used_bytes) }}</dd></div>
        </dl>
      </component>
      <div v-if="!gpus.length" class="border border-[var(--color-divider)] p-3 sm:col-span-2">
        <div class="flex items-start gap-2"><StatusTag variant="neutral">No GPU</StatusTag><p class="text-xs leading-5 text-[var(--neutral-800)]">No NVIDIA or ROCm GPUs were detected. Automatic mode will leave device placement to CPU/other available llama.cpp backends.</p></div>
      </div>
    </div>

    <template v-if="!hidePlacementControls">
      <div class="grid gap-4 md:grid-cols-2">
        <UFormField label="Placement mode" name="gpu_mode">
          <USelectMenu :model-value="gpuMode" class="w-full" :items="modeItems" label-key="label" value-key="value" @update:model-value="emit('update:gpuMode', String($event || 'auto'))" />
        </UFormField>
        <UFormField v-if="gpuMode === 'manual'" label="GPU devices" name="gpu_devices" description="Choose the exact llama.cpp device set. Manual selection is never expanded automatically.">
          <USelectMenu :model-value="gpuDevices" class="w-full" :items="deviceItems" label-key="label" value-key="value" multiple @update:model-value="updateDevices" />
        </UFormField>
        <UFormField v-if="gpuMode === 'manual' && gpuDevices.length > 1" label="Tensor split" name="tensor_split" description="Optional comma-separated proportions. Leave empty to let llama.cpp choose across the selected devices.">
          <UInput :model-value="tensorSplit" class="w-full font-mono" placeholder="3,1" @update:model-value="emit('update:tensorSplit', String($event || ''))" />
        </UFormField>
      </div>
      <div v-if="gpuMode === 'auto'" class="border border-[var(--color-divider)] p-3">
        <div class="flex items-start gap-2"><StatusTag variant="pending">Automatic</StatusTag><p class="text-xs leading-5 text-[var(--neutral-800)]">{{ autoPlacementCopy.summary }} {{ autoPlacementCopy.detail }}</p></div>
      </div>
    </template>
  </div>
</template>