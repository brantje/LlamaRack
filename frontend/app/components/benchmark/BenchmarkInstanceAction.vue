<script setup lang="ts">
import type { Instance, Model, HardwareSnapshot } from '~/composables/useManager'
import type {
  BenchmarkCapabilities,
  BenchmarkRuntimeOption,
  BenchmarkRuntimeOverrides,
  BenchmarkRun,
  BenchmarkWorkloadProfile,
  EffectiveLlamaConfig
} from '~/composables/useBenchmarks'

const props = defineProps<{
  instance: Instance
  model?: Model
  referenceRun?: BenchmarkRun
  triggerLabel?: string
}>()
const emit = defineEmits<{ created: [run: BenchmarkRun] }>()

const benchmarks = useBenchmarks()
const open = ref(false)
const loading = ref(false)
const submitting = ref(false)
const error = ref('')
const capabilities = ref<BenchmarkCapabilities | null>(null)
const effective = ref<EffectiveLlamaConfig | null>(null)
const hardware = ref<HardwareSnapshot | null>(null)
const selectedProfileID = ref('')
const listValues = reactive<Record<string, string>>({})
const scalarValues = reactive<Record<string, number>>({})
const booleanValues = reactive<Record<string, boolean>>({})
const runtimeDraft = reactive<Record<string, string | number | boolean | string[]>>({})

const effectiveValues = computed(() => effective.value?.effective?.values || {})
const runtimeOptions = computed(() => capabilities.value?.runtime_options || [])
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
const presetProfiles = computed(() => {
  const caps = capabilities.value
  if (!caps) return []
  return caps.workload.presets?.length ? caps.workload.presets : [caps.workload.default]
})
const selectedPreset = computed(() => presetProfiles.value.find(profile => profile.id === selectedProfileID.value))
const isCustom = computed(() => selectedProfileID.value === 'custom-v1')
const profileItems = computed(() => [
  ...presetProfiles.value.map(profile => ({ label: profile.name || profile.id || 'Workload', value: profile.id || '' })),
  { label: 'Custom / advanced', value: 'custom-v1' }
])
const canRun = computed(() => Boolean(capabilities.value?.available) && !loading.value && !submitting.value)

function formatCombinedCases(value: unknown) {
  if (!Array.isArray(value)) return ''
  return value.map((item: any) => `${item?.prompt_tokens ?? ''}:${item?.generation_tokens ?? ''}`).filter(value => !value.startsWith(':') && !value.endsWith(':')).join(', ')
}

function applyWorkload(caps: BenchmarkCapabilities, workload: BenchmarkWorkloadProfile) {
  for (const key of Object.keys(listValues)) delete listValues[key]
  for (const key of Object.keys(scalarValues)) delete scalarValues[key]
  for (const key of Object.keys(booleanValues)) delete booleanValues[key]
  for (const field of caps.workload.fields || []) {
    const value = (workload as unknown as Record<string, unknown>)[field.key]
    if (field.kind === 'integer-list') listValues[field.key] = Array.isArray(value) ? value.join(', ') : ''
    else if (field.kind === 'token-pair-list') listValues[field.key] = formatCombinedCases(value)
    else if (field.kind === 'integer') scalarValues[field.key] = Number(value || 0)
    else if (field.kind === 'boolean') booleanValues[field.key] = Boolean(value)
  }
}

function comparableWorkload(workload: BenchmarkWorkloadProfile) {
  return JSON.stringify({
    prompt_tokens: workload.prompt_tokens || [],
    generation_tokens: workload.generation_tokens || [],
    combined_cases: workload.combined_cases || [],
    context_depths: workload.context_depths || [],
    repetitions: workload.repetitions,
    warmup: workload.warmup
  })
}

function resetDraft(caps: BenchmarkCapabilities) {
  const target = props.referenceRun?.workload_profile || caps.workload.default
  const matching = presetProfiles.value.find(profile => comparableWorkload(profile) === comparableWorkload(target))
  selectedProfileID.value = matching?.id || (props.referenceRun ? 'custom-v1' : target.id || 'custom-v1')
  applyWorkload(caps, target)
}

function parseIntegerList(value: string, label: string) {
  const parts = value.split(',').map(part => part.trim()).filter(Boolean)
  if (!parts.length) return []
  const values = parts.map(part => Number(part))
  if (values.some(item => !Number.isInteger(item))) throw new Error(`${label} must be a comma-separated list of whole numbers.`)
  return values
}

function parseTokenPairList(value: string, label: string) {
  const parts = value.split(',').map(part => part.trim()).filter(Boolean)
  if (!parts.length) return []
  return parts.map(part => {
    const values = part.split(':').map(value => value.trim())
    if (values.length !== 2) throw new Error(`${label} must use prompt:generation pairs separated by commas.`)
    const prompt = Number(values[0])
    const generation = Number(values[1])
    if (!Number.isInteger(prompt) || prompt < 1 || !Number.isInteger(generation) || generation < 1) {
      throw new Error(`${label} must use positive whole-number prompt:generation pairs.`)
    }
    return { prompt_tokens: prompt, generation_tokens: generation }
  })
}

function resolvedWorkload(): BenchmarkWorkloadProfile {
  const caps = capabilities.value
  if (!caps) throw new Error('Benchmark capabilities are unavailable.')
  const preset = selectedPreset.value
  if (!isCustom.value && preset) {
    return {
      ...preset,
      prompt_tokens: [...(preset.prompt_tokens || [])],
      generation_tokens: [...(preset.generation_tokens || [])],
      combined_cases: preset.combined_cases?.map(item => ({ ...item })),
      context_depths: preset.context_depths ? [...preset.context_depths] : undefined,
      tuning_hints: preset.tuning_hints ? [...preset.tuning_hints] : undefined
    }
  }
  const workload: BenchmarkWorkloadProfile = {
    id: 'custom-v1',
    version: caps.workload.version,
    prompt_tokens: [],
    generation_tokens: [],
    repetitions: 0,
    warmup: true
  }
  for (const field of caps.workload.fields || []) {
    if (field.kind === 'integer-list') (workload as unknown as Record<string, unknown>)[field.key] = parseIntegerList(listValues[field.key] || '', field.label)
    else if (field.kind === 'token-pair-list') (workload as unknown as Record<string, unknown>)[field.key] = parseTokenPairList(listValues[field.key] || '', field.label)
    else if (field.kind === 'integer') (workload as unknown as Record<string, unknown>)[field.key] = Number(scalarValues[field.key])
    else if (field.kind === 'boolean') (workload as unknown as Record<string, unknown>)[field.key] = Boolean(booleanValues[field.key])
  }
  return workload
}

function parseBoolean(value: unknown) {
  return ['true', '1', 'yes', 'on'].includes(String(value ?? '').trim().toLowerCase())
}

function instanceRuntimeValue(field: BenchmarkRuntimeOption): string | number | boolean | string[] {
  if (field.instance_field === 'gpu_devices') return [...(props.instance.gpu_devices || [])]
  if (field.instance_field === 'tensor_split') return props.instance.tensor_split || ''
  const raw = field.instance_option ? effectiveValues.value[field.instance_option] : undefined
  if (field.kind === 'boolean') return raw === undefined ? parseBoolean(field.default_value) : parseBoolean(raw)
  if (field.kind === 'integer') {
    const value = Number(raw ?? field.default_value)
    return Number.isFinite(value) ? value : ''
  }
  return raw ?? field.default_value ?? ''
}

function comparableRuntimeValue(field: BenchmarkRuntimeOption, value: unknown) {
  if (field.kind === 'integer') {
    const numeric = Number(value)
    return Number.isFinite(numeric) ? JSON.stringify(numeric) : JSON.stringify('')
  }
  if (field.kind === 'boolean') return JSON.stringify(Boolean(value))
  if (field.kind === 'device-list') return JSON.stringify(Array.isArray(value) ? value : [])
  return JSON.stringify(String(value ?? '').trim())
}

function runtimeOverridden(field: BenchmarkRuntimeOption) {
  return comparableRuntimeValue(field, runtimeDraft[field.key]) !== comparableRuntimeValue(field, instanceRuntimeValue(field))
}

function resetRuntime(field: BenchmarkRuntimeOption) {
  runtimeDraft[field.key] = structuredClone(instanceRuntimeValue(field)) as string | number | boolean | string[]
}

function initializeRuntime() {
  for (const key of Object.keys(runtimeDraft)) delete runtimeDraft[key]
  for (const field of runtimeOptions.value) resetRuntime(field)
}

function runtimeOverrides(): BenchmarkRuntimeOverrides {
  const result: Record<string, unknown> = {}
  for (const field of runtimeOptions.value) {
    if (!runtimeOverridden(field)) continue
    const value = runtimeDraft[field.key]
    if (field.kind === 'integer') {
      const parsed = Number(value)
      if (!Number.isInteger(parsed)) throw new Error(`${field.label} must be a whole number.`)
      if (field.minimum !== undefined && parsed < field.minimum) throw new Error(`${field.label} must be at least ${field.minimum}.`)
      if (field.maximum !== undefined && parsed > field.maximum) throw new Error(`${field.label} must be at most ${field.maximum}.`)
      result[field.key] = parsed
    } else if (field.kind === 'device-list') {
      result[field.key] = Array.isArray(value) ? [...value] : []
    } else if (field.kind === 'boolean') {
      result[field.key] = Boolean(value)
    } else {
      result[field.key] = String(value ?? '').trim()
    }
  }
  return result as BenchmarkRuntimeOverrides
}

function runtimeDisplay(value: unknown) {
  if (Array.isArray(value)) return value.length ? value.join(', ') : 'Automatic'
  if (value === undefined || value === null || value === '') return 'Automatic'
  if (typeof value === 'boolean') return value ? 'On' : 'Off'
  return String(value)
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
    initializeRuntime()
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
    const run = await benchmarks.create(props.instance, resolvedWorkload(), runtimeOverrides())
    emit('created', run)
    open.value = false
    const suffix = props.referenceRun ? `?baseline=${encodeURIComponent(props.referenceRun.id)}` : ''
    await navigateTo(`/benchmarks/${encodeURIComponent(run.id)}${suffix}`)
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to start benchmark.'
  } finally {
    submitting.value = false
  }
}

watch(selectedProfileID, value => {
  const caps = capabilities.value
  if (!caps || value === 'custom-v1') return
  const preset = presetProfiles.value.find(profile => profile.id === value)
  if (preset) applyWorkload(caps, preset)
})
watch(open, value => { if (value) void load() })
</script>

<template>
  <AppButton intent="secondary" data-testid="instance-benchmark-action" @click="open = true">{{ triggerLabel || (referenceRun ? 'Run another benchmark' : 'Benchmark') }}</AppButton>

  <UModal v-model:open="open" :title="referenceRun ? 'Run another benchmark' : `Benchmark ${instance.name}`" :dismissible="!submitting" :ui="{ content: 'w-[calc(100vw-2rem)] max-w-none sm:max-w-4xl' }">
    <template #body>
      <div class="space-y-6" data-testid="benchmark-modal">
        <p class="text-sm leading-6 text-muted">Run <code class="font-mono">llama-bench</code> directly from an immutable snapshot of this saved Instance. Runtime changes are scoped to this benchmark and never modify the saved Instance. This is compute intensive and may affect other workloads sharing the hardware.</p>

        <Frame v-if="error" class="p-3">
          <div class="flex items-start gap-2">
            <StatusTag variant="failed">Benchmark blocked</StatusTag>
            <p class="min-w-0 flex-1 text-xs leading-5 text-muted">{{ error }}</p>
          </div>
        </Frame>

        <div v-if="loading" class="space-y-3"><USkeleton class="h-24 w-full" /><USkeleton class="h-36 w-full" /></div>
        <template v-else>
          <section class="space-y-3" data-testid="benchmark-runtime-controls">
            <div>
              <p class="text-[length:var(--font-size-kicker)] font-semibold uppercase tracking-[.14em] text-[var(--neutral-700)]">Runtime configuration</p>
              <p class="mt-1 text-xs text-muted">Values start from the current effective Instance settings. Only options advertised by this <code class="font-mono">llama-bench</code> build and safely mapped by LlamaRack are editable.</p>
            </div>
            <dl class="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-2">
              <div><dt class="text-xs text-muted">Model</dt><dd class="mt-0.5 font-medium">{{ model?.name || instance.model_id }}</dd></div>
              <div><dt class="text-xs text-muted">GPU mode</dt><dd class="mt-0.5 font-mono">{{ instance.gpu_mode || 'auto' }}</dd></div>
            </dl>

            <div v-if="runtimeOptions.length" class="divide-y divide-[var(--color-divider)] border-y border-[var(--color-divider)]">
              <div v-for="field in runtimeOptions" :key="field.key" class="grid gap-3 py-4 lg:grid-cols-[minmax(0,1fr)_minmax(16rem,1fr)] lg:items-start">
                <div>
                  <div class="flex flex-wrap items-center gap-2">
                    <p class="text-sm font-medium">{{ field.label }}</p>
                    <StatusTag v-if="runtimeOverridden(field)" variant="pending">Override</StatusTag>
                    <StatusTag variant="neutral">{{ field.instance_editable ? 'Instance setting' : 'Benchmark only' }}</StatusTag>
                  </div>
                  <p class="mt-1 text-xs text-muted">Instance: <span class="font-mono">{{ runtimeDisplay(instanceRuntimeValue(field)) }}</span></p>
                  <p v-if="field.description" class="mt-1 text-xs leading-5 text-muted">{{ field.description }}</p>
                </div>
                <div class="space-y-2">
                  <USelectMenu v-if="field.kind === 'device-list'" v-model="runtimeDraft[field.key]" :items="field.choices || []" multiple class="w-full" :aria-label="`${field.label} benchmark value`" />
                  <USelect v-else-if="field.kind === 'enum'" v-model="runtimeDraft[field.key]" :items="field.choices || []" class="w-full" :aria-label="`${field.label} benchmark value`" />
                  <UCheckbox v-else-if="field.kind === 'boolean'" v-model="runtimeDraft[field.key]" :label="`Benchmark value: ${runtimeDisplay(runtimeDraft[field.key])}`" :aria-label="`${field.label} benchmark value`" />
                  <UInput v-else-if="field.kind === 'integer'" v-model.number="runtimeDraft[field.key]" type="number" class="w-full" :min="field.minimum" :max="field.maximum" :aria-label="`${field.label} benchmark value`" />
                  <UInput v-else v-model="runtimeDraft[field.key]" class="w-full" :placeholder="field.kind === 'tensor-split' ? 'Example: 1,1' : undefined" :aria-label="`${field.label} benchmark value`" />
                  <div class="flex items-center justify-between gap-2">
                    <p class="text-xs text-muted">Benchmark: <span class="font-mono">{{ runtimeDisplay(runtimeDraft[field.key]) }}</span></p>
                    <UButton v-if="runtimeOverridden(field)" color="neutral" variant="ghost" size="xs" @click="resetRuntime(field)">Reset to Instance</UButton>
                  </div>
                </div>
              </div>
            </div>
            <div v-else>
              <p class="text-xs text-muted">Instance configuration · read only for this run because this <code class="font-mono">llama-bench</code> capability response does not advertise any runtime setting that LlamaRack can safely override.</p>
              <div v-if="importantOptions.length" class="mt-3 overflow-x-auto border-t border-[var(--color-divider)] pt-3">
                <table class="w-full text-left text-xs">
                  <thead class="text-muted"><tr><th class="pb-2 pr-4 font-medium">llama.cpp option</th><th class="pb-2 pr-4 font-medium">Value</th><th class="pb-2 font-medium">Source</th></tr></thead>
                  <tbody class="divide-y divide-[var(--color-divider)]">
                    <tr v-for="option in importantOptions" :key="option.key"><td class="py-2 pr-4 font-mono">--{{ option.key }}</td><td class="py-2 pr-4 font-mono">{{ option.value }}</td><td class="py-2 text-muted">{{ option.source || 'resolved' }}</td></tr>
                  </tbody>
                </table>
              </div>
            </div>
          </section>

          <section class="space-y-4 border-t border-[var(--color-divider)] pt-5" data-testid="benchmark-workload-controls">
            <div>
              <p class="text-[length:var(--font-size-kicker)] font-semibold uppercase tracking-[.14em] text-[var(--neutral-700)]">Benchmark workload</p>
              <p class="mt-1 text-xs text-muted">Choose what this Instance is meant to do. Reruns keep the reference workload by default so runtime deltas stay comparable.</p>
            </div>
            <template v-if="capabilities?.available">
              <UFormField label="Workload profile" description="Profiles change only controlled llama-bench cases, context depths, repetitions and warm-up behavior.">
                <USelect v-model="selectedProfileID" :items="profileItems" value-key="value" label-key="label" class="w-full" data-testid="benchmark-workload-profile" />
              </UFormField>

              <div v-if="selectedPreset" class="space-y-3 border-t border-[var(--color-divider)] pt-3">
                <div>
                  <p class="text-sm font-medium">{{ selectedPreset.name || selectedPreset.id }}</p>
                  <p v-if="selectedPreset.description" class="mt-1 text-xs leading-5 text-muted">{{ selectedPreset.description }}</p>
                  <p v-if="selectedPreset.focus" class="mt-1 text-xs leading-5"><span class="font-medium">Optimization focus:</span> {{ selectedPreset.focus }}</p>
                </div>
                <dl class="grid gap-x-6 gap-y-2 text-xs sm:grid-cols-3">
                  <div><dt class="text-muted">Prompt cases</dt><dd class="mt-0.5 font-mono">{{ selectedPreset.prompt_tokens?.join(', ') || 'None' }}</dd></div>
                  <div><dt class="text-muted">Generation cases</dt><dd class="mt-0.5 font-mono">{{ selectedPreset.generation_tokens?.join(', ') || 'None' }}</dd></div>
                  <div><dt class="text-muted">Combined turns</dt><dd class="mt-0.5 font-mono">{{ selectedPreset.combined_cases?.length ? selectedPreset.combined_cases.map(item => `${item.prompt_tokens}:${item.generation_tokens}`).join(', ') : 'None' }}</dd></div>
                  <div><dt class="text-muted">Context depths</dt><dd class="mt-0.5 font-mono">{{ selectedPreset.context_depths?.length ? selectedPreset.context_depths.join(', ') : '0 / default' }}</dd></div>
                  <div><dt class="text-muted">Repetitions</dt><dd class="mt-0.5 font-mono">{{ selectedPreset.repetitions }}</dd></div>
                </dl>
                <div v-if="selectedPreset.tuning_hints?.length" class="border-t border-[var(--color-divider)] pt-3">
                  <p class="text-xs font-medium">Settings this workload is useful for testing</p>
                  <div class="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted">
                    <code v-for="hint in selectedPreset.tuning_hints" :key="hint.key" class="font-mono">--{{ hint.key }}</code>
                  </div>
                </div>
              </div>

              <div v-if="isCustom" class="grid gap-4 border-t border-[var(--color-divider)] pt-3 sm:grid-cols-2" data-testid="benchmark-custom-workload">
                <div class="sm:col-span-2"><p class="text-xs font-medium">Advanced workload fields</p><p class="mt-1 text-xs text-muted">Use this only when the supplied workload intents do not represent what you need to measure.</p></div>
                <UFormField v-for="field in capabilities.workload.fields" :key="field.key" :label="field.label" :description="field.description" :class="field.kind === 'integer-list' || field.kind === 'token-pair-list' ? 'sm:col-span-2' : ''">
                  <UInput v-if="field.kind === 'integer-list' || field.kind === 'token-pair-list'" v-model="listValues[field.key]" class="w-full" :placeholder="field.kind === 'token-pair-list' ? '512:128, 1024:128' : '512, 2048'" />
                  <UInput v-else-if="field.kind === 'integer'" v-model.number="scalarValues[field.key]" class="w-full" type="number" :min="field.minimum" :max="field.maximum" />
                  <UCheckbox v-else-if="field.kind === 'boolean'" v-model="booleanValues[field.key]" :label="field.label" />
                </UFormField>
              </div>
            </template>
            <div v-else class="flex items-start gap-2"><StatusTag variant="failed">Unavailable</StatusTag><p class="text-xs leading-5 text-muted">{{ capabilities?.reason || 'The bundled llama-bench interface is unavailable.' }}</p></div>
          </section>

          <section class="space-y-3 border-t border-[var(--color-divider)] pt-5">
            <div>
              <p class="text-[length:var(--font-size-kicker)] font-semibold uppercase tracking-[.14em] text-[var(--neutral-700)]">Hardware / admission</p>
              <p class="mt-1 text-xs text-muted">LlamaRack takes a fresh snapshot and performs non-preemptive scheduler admission from the effective benchmark configuration when you start the run. Existing Instances are not evicted for a benchmark.</p>
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
