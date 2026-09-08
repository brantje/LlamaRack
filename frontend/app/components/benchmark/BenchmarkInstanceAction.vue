<script setup lang="ts">
import type { Instance, Model, HardwareSnapshot } from '~/composables/useManager'
import type { BenchmarkCapabilities, BenchmarkRun, BenchmarkWorkloadProfile, EffectiveLlamaConfig } from '~/composables/useBenchmarks'

const props = defineProps<{ instance: Instance; model?: Model }>()
const emit = defineEmits<{ created: [run: BenchmarkRun] }>()

const benchmarks = useBenchmarks()
const open = ref(false)
const loading = ref(false)
const submitting = ref(false)
const error = ref('')
const capabilities = ref<BenchmarkCapabilities | null>(null)
const effective = ref<EffectiveLlamaConfig | null>(null)
const hardware = ref<HardwareSnapshot | null>(null)
const listValues = reactive<Record<string, string>>({})
const scalarValues = reactive<Record<string, number>>({})
const booleanValues = reactive<Record<string, boolean>>({})

const effectiveValues = computed(() => effective.value?.effective?.values || {})
const importantOptions = computed(() => {
  const priority = ['ctx-size', 'n-gpu-layers', 'batch-size', 'ubatch-size', 'threads', 'cache-type-k', 'cache-type-v', 'flash-attn', 'kv-offload', 'n-cpu-moe', 'spec-draft-model', 'mmproj']
  return priority
    .filter(key => effectiveValues.value[key] !== undefined)
    .map(key => ({ key, value: effectiveValues.value[key]!, source: effective.value?.effective?.sources?.[key] || '' }))
})
const currentGPUs = computed(() => {
  const selected = new Set(props.instance.gpu_devices || [])
  const gpus = hardware.value?.gpus || []
  return selected.size ? gpus.filter(gpu => selected.has(gpu.id)) : gpus
})
const canRun = computed(() => Boolean(capabilities.value?.available) && !loading.value && !submitting.value)

function resetDraft(caps: BenchmarkCapabilities) {
  const defaults = caps.workload.default
  for (const key of Object.keys(listValues)) delete listValues[key]
  for (const key of Object.keys(scalarValues)) delete scalarValues[key]
  for (const key of Object.keys(booleanValues)) delete booleanValues[key]
  for (const field of caps.workload.fields || []) {
    const value = (defaults as unknown as Record<string, unknown>)[field.key]
    if (field.kind === 'integer-list') listValues[field.key] = Array.isArray(value) ? value.join(', ') : ''
    else if (field.kind === 'integer') scalarValues[field.key] = Number(value || 0)
    else if (field.kind === 'boolean') booleanValues[field.key] = Boolean(value)
  }
}

function parseIntegerList(value: string, label: string) {
  const parts = value.split(',').map(part => part.trim()).filter(Boolean)
  if (!parts.length) return []
  const values = parts.map(part => Number(part))
  if (values.some(item => !Number.isInteger(item))) throw new Error(`${label} must be a comma-separated list of whole numbers.`)
  return values
}

function resolvedWorkload(): BenchmarkWorkloadProfile {
  const caps = capabilities.value
  if (!caps) throw new Error('Benchmark capabilities are unavailable.')
  const defaults = caps.workload.default
  const workload: BenchmarkWorkloadProfile = {
    ...defaults,
    prompt_tokens: [...(defaults.prompt_tokens || [])],
    generation_tokens: [...(defaults.generation_tokens || [])]
  }
  for (const field of caps.workload.fields || []) {
    if (field.kind === 'integer-list') (workload as unknown as Record<string, unknown>)[field.key] = parseIntegerList(listValues[field.key] || '', field.label)
    else if (field.kind === 'integer') (workload as unknown as Record<string, unknown>)[field.key] = Number(scalarValues[field.key])
    else if (field.kind === 'boolean') (workload as unknown as Record<string, unknown>)[field.key] = Boolean(booleanValues[field.key])
  }
  return workload
}

function bytes(value?: number) {
  if (value === undefined || !Number.isFinite(value)) return '—'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let amount = Math.max(0, value)
  let index = 0
  while (amount >= 1024 && index < units.length - 1) { amount /= 1024; index++ }
  return `${amount >= 10 || index === 0 ? amount.toFixed(0) : amount.toFixed(1)} ${units[index]}`
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const [caps, config, snapshot] = await Promise.all([
      benchmarks.loadCapabilities(true),
      benchmarks.effectiveConfig(props.instance),
      benchmarks.hardware()
    ])
    capabilities.value = caps
    effective.value = config
    hardware.value = snapshot
    resetDraft(caps)
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to prepare this benchmark.'
  } finally {
    loading.value = false
  }
}

async function runBenchmark() {
  submitting.value = true
  error.value = ''
  try {
    const run = await benchmarks.create(props.instance, resolvedWorkload())
    emit('created', run)
    open.value = false
    await navigateTo(`/benchmarks/${encodeURIComponent(run.id)}`)
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to start benchmark.'
  } finally {
    submitting.value = false
  }
}

watch(open, value => { if (value) void load() })
</script>

<template>
  <AppButton intent="secondary" data-testid="instance-benchmark-action" @click="open = true">Benchmark</AppButton>

  <UModal v-model:open="open" :title="`Benchmark ${instance.name}`" :dismissible="!submitting" :ui="{ content: 'w-[calc(100vw-2rem)] max-w-none sm:max-w-3xl' }">
    <template #body>
      <div class="space-y-6" data-testid="benchmark-modal">
        <p class="text-sm leading-6 text-muted">Run <code class="font-mono">llama-bench</code> directly from an immutable snapshot of this saved Instance. This is compute intensive and may affect other workloads sharing the hardware.</p>

        <Frame v-if="error" class="p-3">
          <div class="flex items-start gap-2">
            <StatusTag variant="failed">Benchmark blocked</StatusTag>
            <p class="min-w-0 flex-1 text-xs leading-5 text-muted">{{ error }}</p>
          </div>
        </Frame>

        <div v-if="loading" class="space-y-3"><USkeleton class="h-24 w-full" /><USkeleton class="h-36 w-full" /></div>
        <template v-else>
          <section class="space-y-3">
            <div>
              <p class="text-[length:var(--font-size-kicker)] font-semibold uppercase tracking-[.14em] text-[var(--neutral-700)]">Instance configuration · read only</p>
              <p class="mt-1 text-xs text-muted">Runtime settings are inherited from the saved Instance and cannot be overridden here.</p>
            </div>
            <dl class="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-2">
              <div><dt class="text-xs text-muted">Model</dt><dd class="mt-0.5 font-medium">{{ model?.name || instance.model_id }}</dd></div>
              <div><dt class="text-xs text-muted">GPU mode</dt><dd class="mt-0.5 font-mono">{{ instance.gpu_mode || 'auto' }}</dd></div>
              <div><dt class="text-xs text-muted">Devices</dt><dd class="mt-0.5 font-mono">{{ instance.gpu_devices?.length ? instance.gpu_devices.join(', ') : 'Automatic' }}</dd></div>
              <div><dt class="text-xs text-muted">Tensor split</dt><dd class="mt-0.5 font-mono">{{ instance.tensor_split || 'Automatic' }}</dd></div>
            </dl>
            <div v-if="importantOptions.length" class="overflow-x-auto border-t border-[var(--color-divider)] pt-3">
              <table class="w-full text-left text-xs">
                <thead class="text-muted"><tr><th class="pb-2 pr-4 font-medium">llama.cpp option</th><th class="pb-2 pr-4 font-medium">Value</th><th class="pb-2 font-medium">Source</th></tr></thead>
                <tbody class="divide-y divide-[var(--color-divider)]">
                  <tr v-for="option in importantOptions" :key="option.key"><td class="py-2 pr-4 font-mono">--{{ option.key }}</td><td class="py-2 pr-4 font-mono">{{ option.value }}</td><td class="py-2 text-muted">{{ option.source || 'resolved' }}</td></tr>
                </tbody>
              </table>
            </div>
          </section>

          <section class="space-y-3 border-t border-[var(--color-divider)] pt-5">
            <div>
              <p class="text-[length:var(--font-size-kicker)] font-semibold uppercase tracking-[.14em] text-[var(--neutral-700)]">Benchmark workload</p>
              <p class="mt-1 text-xs text-muted">Only controlled workload dimensions are editable. Defaults are owned by the backend and versioned with the run.</p>
            </div>
            <div v-if="capabilities?.available" class="grid gap-4 sm:grid-cols-2">
              <UFormField v-for="field in capabilities.workload.fields" :key="field.key" :label="field.label" :description="field.description" :class="field.kind === 'integer-list' ? 'sm:col-span-2' : ''">
                <UInput v-if="field.kind === 'integer-list'" v-model="listValues[field.key]" class="w-full" :placeholder="'512, 2048'" />
                <UInput v-else-if="field.kind === 'integer'" v-model.number="scalarValues[field.key]" class="w-full" type="number" :min="field.minimum" :max="field.maximum" />
                <UCheckbox v-else-if="field.kind === 'boolean'" v-model="booleanValues[field.key]" :label="field.label" />
              </UFormField>
            </div>
            <div v-else class="flex items-start gap-2"><StatusTag variant="failed">Unavailable</StatusTag><p class="text-xs leading-5 text-muted">{{ capabilities?.reason || 'The bundled llama-bench interface is unavailable.' }}</p></div>
          </section>

          <section class="space-y-3 border-t border-[var(--color-divider)] pt-5">
            <div>
              <p class="text-[length:var(--font-size-kicker)] font-semibold uppercase tracking-[.14em] text-[var(--neutral-700)]">Hardware / admission</p>
              <p class="mt-1 text-xs text-muted">LlamaRack takes a fresh snapshot and performs non-preemptive scheduler admission when you start the run. Existing Instances are not evicted for a benchmark.</p>
            </div>
            <div v-if="currentGPUs.length" class="space-y-2">
              <div v-for="gpu in currentGPUs" :key="gpu.id" class="flex flex-wrap justify-between gap-3 text-sm">
                <span><span class="font-medium">{{ gpu.name || gpu.id }}</span> <span class="ml-1 font-mono text-xs text-muted">{{ gpu.id }}</span></span>
                <span class="font-mono text-xs">{{ bytes(gpu.free_bytes) }} free / {{ bytes(gpu.total_bytes) }}</span>
              </div>
            </div>
            <p v-else class="text-sm text-muted">No accelerator is selected or detected; CPU/host RAM admission still applies.</p>
          </section>
        </template>
      </div>
    </template>
    <template #footer>
      <div class="flex w-full justify-end gap-2">
        <UButton color="neutral" variant="outline" :disabled="submitting" @click="open = false">Cancel</UButton>
        <UButton color="primary" :loading="submitting" :disabled="!canRun" data-testid="run-benchmark" @click="runBenchmark">Run benchmark</UButton>
      </div>
    </template>
  </UModal>
</template>
