<script setup lang="ts">
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
  context_assumed: boolean
  confidence: 'low' | 'medium' | 'high' | string
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
  offload: { mode: string; gpu_layers?: number; devices?: string[]; tensor_split?: string; reason: string }
}

const props = defineProps<{
  gpuMode: string
  gpuDevices: string[]
  tensorSplit: string
  modelId?: string
}>()
const emit = defineEmits<{
  'update:gpuMode': [value: string]
  'update:gpuDevices': [value: string[]]
  'update:tensorSplit': [value: string]
}>()

const manager = useManager()
const snapshot = ref<HardwareSnapshot | null>(null)
const recommendation = ref<Recommendation | null>(null)
const loading = ref(false)
const recommendationLoading = ref(false)
const error = ref('')
const recommendationError = ref('')
const modeItems = [{ label: 'Automatic · single GPU first', value: 'auto' }, { label: 'Manual', value: 'manual' }]
const gpus = computed(() => snapshot.value?.gpus || [])
const deviceItems = computed(() => gpus.value.map(gpu => ({
  label: `${gpu.id} · ${gpu.name} · ${formatBytes(gpu.free_bytes)} free`, value: gpu.id
})))
const recommendationTone = computed<'success' | 'warning' | 'neutral'>(() => recommendation.value?.current_fit ? 'success' : recommendation.value?.total_hardware_fit ? 'warning' : 'neutral')
const recommendationTitle = computed(() => {
  const item = recommendation.value
  if (!item) return ''
  if (item.current_fit) return 'Fits current resources'
  if (item.total_hardware_fit) return 'Fits installed hardware after freeing resources'
  if (item.cpu_fit) return 'CPU fallback fits current RAM'
  return 'Resource pressure expected'
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

async function refreshRecommendation() {
  recommendation.value = null
  recommendationError.value = ''
  if (!props.modelId) return
  recommendationLoading.value = true
  try {
    const result = await manager.request<Recommendation>(`/api/v1/models/${encodeURIComponent(props.modelId)}/recommendation`)
    recommendation.value = isRecommendation(result) ? result : null
  } catch (value: any) {
    recommendationError.value = value?.data?.error || value?.message || 'Unable to estimate model resources'
  } finally {
    recommendationLoading.value = false
  }
}

async function refresh() {
  await Promise.all([refreshHardware(), refreshRecommendation()])
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

watch(() => props.gpuMode, (mode) => {
  if (mode === 'auto') {
    emit('update:gpuDevices', [])
    emit('update:tensorSplit', '')
  }
})
watch(() => props.modelId, () => void refreshRecommendation())
onMounted(() => void refresh())
</script>

<template>
  <div class="space-y-4">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div>
        <p class="font-semibold">GPU placement</p>
        <p class="text-xs text-muted">Automatic placement binds one GPU when the Instance safely fits and expands to multiple GPUs only when required.</p>
      </div>
      <UButton size="xs" color="neutral" variant="soft" :loading="loading || recommendationLoading" @click="refresh">Refresh hardware</UButton>
    </div>

    <UAlert v-if="error" color="warning" variant="subtle" :description="error" />
    <UAlert v-if="recommendationError" color="warning" variant="subtle" :description="recommendationError" />
    <div v-if="recommendation" data-testid="hardware-recommendation" class="space-y-3 rounded-lg border border-default p-4">
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div>
          <div class="flex flex-wrap items-center gap-2">
            <p class="font-semibold">{{ recommendationTitle }}</p>
            <UBadge :color="recommendationTone" variant="subtle">{{ recommendation.confidence }} confidence</UBadge>
            <UBadge v-if="recommendation.quantization.name" color="neutral" variant="soft">{{ recommendation.quantization.name }}</UBadge>
          </div>
          <p class="mt-1 text-xs text-muted">{{ recommendation.offload.reason }}</p>
        </div>
        <UBadge color="primary" variant="subtle">{{ recommendation.offload.mode.replace('_', ' ') }}</UBadge>
      </div>
      <dl class="grid gap-3 text-xs sm:grid-cols-2 lg:grid-cols-4">
        <div><dt class="text-dimmed">Full-offload VRAM</dt><dd class="font-semibold">{{ formatBytes(recommendation.memory.full_offload_vram_bytes) }}</dd></div>
        <div><dt class="text-dimmed">CPU-only RAM</dt><dd class="font-semibold">{{ formatBytes(recommendation.memory.cpu_only_ram_bytes) }}</dd></div>
        <div><dt class="text-dimmed">KV cache estimate</dt><dd class="font-semibold">{{ formatBytes(recommendation.memory.kv_cache_bytes) }}</dd></div>
        <div><dt class="text-dimmed">Context assumption</dt><dd class="font-semibold">{{ recommendation.context_length.toLocaleString() }} tokens<span v-if="recommendation.context_assumed"> assumed</span></dd></div>
      </dl>
      <div v-if="recommendation.offload.devices?.length || recommendation.offload.gpu_layers" class="flex flex-wrap gap-2 text-xs">
        <UBadge v-for="device in recommendation.offload.devices" :key="device" color="neutral" variant="soft">{{ device }}</UBadge>
        <span v-if="recommendation.offload.gpu_layers" class="text-muted">Recommended GPU layers: <strong>{{ recommendation.offload.gpu_layers }}</strong></span>
        <span v-if="recommendation.offload.tensor_split" class="text-muted">Tensor split: <strong class="font-mono">{{ recommendation.offload.tensor_split }}</strong></span>
      </div>
      <p class="text-xs text-muted"><strong>{{ recommendation.quantization.summary }}</strong> {{ recommendation.quantization.tradeoff }}</p>
      <UAlert v-if="recommendation.metadata_warning" color="neutral" variant="subtle" title="Metadata estimate fallback" :description="recommendation.metadata_warning" />
      <UAlert v-if="recommendation.hardware_warning" color="warning" variant="subtle" title="Hardware probe warning" :description="recommendation.hardware_warning" />
    </div>

    <div v-if="snapshot" class="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
      <UCard
        v-for="gpu in gpus"
        :key="gpu.id"
        :data-testid="`gpu-card-${gpu.id}`"
        :aria-pressed="isSelected(gpu.id)"
        role="button"
        tabindex="0"
        class="cursor-pointer transition-shadow hover:ring-1 hover:ring-primary/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary"
        :class="isSelected(gpu.id) ? 'ring-2 ring-primary' : ''"
        :ui="{ body: 'p-4 sm:p-4' }"
        @click="selectGpu(gpu.id)"
        @keydown.enter.prevent="selectGpu(gpu.id)"
        @keydown.space.prevent="selectGpu(gpu.id)"
      >
        <div class="flex items-start justify-between gap-2">
          <div class="min-w-0"><p class="font-mono text-sm font-bold">{{ gpu.id }}</p><p class="truncate text-xs text-muted">{{ gpu.name }}</p></div>
          <div class="flex items-center gap-1">
            <UBadge v-if="isSelected(gpu.id)" size="sm" color="primary" variant="subtle">Selected</UBadge>
            <UBadge size="sm" color="neutral" variant="subtle">{{ gpu.backend.toUpperCase() }}</UBadge>
          </div>
        </div>
        <dl class="mt-3 grid grid-cols-2 gap-2 text-xs">
          <div><dt class="text-dimmed">VRAM free</dt><dd class="font-semibold">{{ formatBytes(gpu.free_bytes) }}</dd></div>
          <div><dt class="text-dimmed">VRAM total</dt><dd class="font-semibold">{{ formatBytes(gpu.total_bytes) }}</dd></div>
          <div><dt class="text-dimmed">Utilization</dt><dd class="font-semibold">{{ gpu.utilization_pct.toFixed(0) }}%</dd></div>
          <div><dt class="text-dimmed">VRAM used</dt><dd class="font-semibold">{{ formatBytes(gpu.used_bytes) }}</dd></div>
        </dl>
      </UCard>
      <UAlert v-if="!gpus.length" class="sm:col-span-2" color="neutral" variant="subtle" description="No NVIDIA or ROCm GPUs were detected. Automatic mode will leave device placement to CPU/other available llama.cpp backends." />
    </div>

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
    <UAlert v-if="gpuMode === 'auto'" color="primary" variant="subtle" title="Single-GPU first" description="At launch the manager reads fresh VRAM state, binds one adequate GPU when possible, and only then considers multi-GPU placement and eligible resource-pressure eviction." />
  </div>
</template>
