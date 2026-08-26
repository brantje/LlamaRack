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
  ram_total_bytes: number
  ram_available_bytes: number
  gpus: GPU[]
  collected_at: string
}

const props = defineProps<{
  gpuMode: string
  gpuDevices: string[]
  tensorSplit: string
}>()
const emit = defineEmits<{
  'update:gpuMode': [value: string]
  'update:gpuDevices': [value: string[]]
  'update:tensorSplit': [value: string]
}>()

const manager = useManager()
const snapshot = ref<HardwareSnapshot | null>(null)
const loading = ref(false)
const error = ref('')
const modeItems = [{ label: 'Automatic · single GPU first', value: 'auto' }, { label: 'Manual', value: 'manual' }]
const deviceItems = computed(() => (snapshot.value?.gpus || []).map(gpu => ({
  label: `${gpu.id} · ${gpu.name} · ${formatBytes(gpu.free_bytes)} free`, value: gpu.id
})))

async function refresh() {
  loading.value = true
  error.value = ''
  try {
    snapshot.value = await manager.request<HardwareSnapshot>('/api/v1/hardware')
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to read hardware state'
  } finally {
    loading.value = false
  }
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

watch(() => props.gpuMode, (mode) => {
  if (mode === 'auto') {
    emit('update:gpuDevices', [])
    emit('update:tensorSplit', '')
  }
})
onMounted(() => void refresh())
</script>

<template>
  <div class="space-y-4">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div>
        <p class="font-semibold">GPU placement</p>
        <p class="text-xs text-muted">Automatic placement binds one GPU when the Instance safely fits and expands to multiple GPUs only when required.</p>
      </div>
      <UButton size="xs" color="neutral" variant="soft" :loading="loading" @click="refresh">Refresh hardware</UButton>
    </div>

    <UAlert v-if="error" color="warning" variant="subtle" :description="error" />
    <div v-if="snapshot" class="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
      <UCard v-for="gpu in snapshot.gpus" :key="gpu.id" :ui="{ body: 'p-4 sm:p-4' }">
        <div class="flex items-start justify-between gap-2">
          <div class="min-w-0"><p class="font-mono text-sm font-bold">{{ gpu.id }}</p><p class="truncate text-xs text-muted">{{ gpu.name }}</p></div>
          <UBadge size="sm" color="neutral" variant="subtle">{{ gpu.backend.toUpperCase() }}</UBadge>
        </div>
        <dl class="mt-3 grid grid-cols-2 gap-2 text-xs">
          <div><dt class="text-dimmed">VRAM free</dt><dd class="font-semibold">{{ formatBytes(gpu.free_bytes) }}</dd></div>
          <div><dt class="text-dimmed">VRAM total</dt><dd class="font-semibold">{{ formatBytes(gpu.total_bytes) }}</dd></div>
          <div><dt class="text-dimmed">Utilization</dt><dd class="font-semibold">{{ gpu.utilization_pct.toFixed(0) }}%</dd></div>
          <div><dt class="text-dimmed">VRAM used</dt><dd class="font-semibold">{{ formatBytes(gpu.used_bytes) }}</dd></div>
        </dl>
      </UCard>
      <UAlert v-if="!snapshot.gpus.length" class="sm:col-span-2" color="neutral" variant="subtle" description="No NVIDIA or ROCm GPUs were detected. Automatic mode will leave device placement to CPU/other available llama.cpp backends." />
    </div>

    <div class="grid gap-4 md:grid-cols-2">
      <UFormField label="Placement mode" name="gpu_mode">
        <USelectMenu :model-value="gpuMode" class="w-full" :items="modeItems" label-key="label" value-key="value" @update:model-value="emit('update:gpuMode', String($event || 'auto'))" />
      </UFormField>
      <UFormField v-if="gpuMode === 'manual'" label="GPU devices" name="gpu_devices" description="Choose the exact llama.cpp device set. Manual selection is never expanded automatically.">
        <USelectMenu :model-value="gpuDevices" class="w-full" :items="deviceItems" label-key="label" value-key="value" multiple @update:model-value="emit('update:gpuDevices', ($event || []) as string[])" />
      </UFormField>
      <UFormField v-if="gpuMode === 'manual' && gpuDevices.length > 1" label="Tensor split" name="tensor_split" description="Optional comma-separated proportions. Leave empty to let llama.cpp choose across the selected devices.">
        <UInput :model-value="tensorSplit" class="w-full font-mono" placeholder="3,1" @update:model-value="emit('update:tensorSplit', String($event || ''))" />
      </UFormField>
    </div>
    <UAlert v-if="gpuMode === 'auto'" color="primary" variant="subtle" title="Single-GPU first" description="At launch the manager reads fresh VRAM state, binds one adequate GPU when possible, and only then considers multi-GPU placement and eligible resource-pressure eviction." />
  </div>
</template>
