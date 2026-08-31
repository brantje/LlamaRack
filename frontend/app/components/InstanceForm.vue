<script setup lang="ts">
import type { HardwareGPU, Model } from '~/composables/useManager'

type InstanceFormState = {
  model_id: string
  name: string
  slug: string
  enabled: boolean
  always_on: boolean
  autoload_enabled: boolean
  priority: string
  eviction_enabled: boolean
  idle_unload_seconds: number
  gpu_mode: string
  gpu_devices: string[]
  tensor_split: string
  request_log_mode: string
  options: Record<string, string>
}
type SettingValue<T> = { value?: T }
type GeneralSettings = { idle_unload_seconds?: SettingValue<number> }
type EffectiveConfig = { effective?: { values?: Record<string, string>; sources?: Record<string, string> } }
type InspectionDependency = { kind: string; total_bytes?: number }
type ModelInspection = { dependencies?: InspectionDependency[] }
type CompanionDefinition = { key: 'mmproj' | 'spec-draft-model'; label: string; flag: string; dependencyKind: string }
type DetectedCompanion = { path: string; size?: number }

const props = withDefaults(defineProps<{
  form: InstanceFormState
  title: string
  submitLabel: string
  busy?: boolean
  error?: string
  loading?: boolean
  showLaunchAfterCreate?: boolean
  launchAfterCreate?: boolean
  submitDisabled?: boolean
  submitDisabledReason?: string
  dirty?: boolean
  instanceId?: string
}>(), {
  busy: false,
  error: '',
  loading: false,
  showLaunchAfterCreate: false,
  launchAfterCreate: false,
  submitDisabled: false,
  submitDisabledReason: '',
  dirty: false,
  instanceId: ''
})
const emit = defineEmits<{
  submit: []
  'update:launchAfterCreate': [value: boolean]
}>()

const manager = useManager()
const globalIdleSeconds = ref(300)
const hardwareGPUs = ref<HardwareGPU[]>([])
const companions = ref<Partial<Record<CompanionDefinition['key'], DetectedCompanion>>>({})
const companionLoading = ref(false)
let slugEdited = false
let companionSequence = 0

const companionDefinitions: CompanionDefinition[] = [
  { key: 'mmproj', label: 'Vision projector', flag: '--mmproj', dependencyKind: 'mmproj' },
  { key: 'spec-draft-model', label: 'MTP draft model', flag: '--spec-draft-model', dependencyKind: 'mtp' }
]
const selectedModel = computed(() => manager.models.value.find(model => model.id === props.form.model_id))
const detectedCompanionKeys = computed(() => companionDefinitions.filter(item => companions.value[item.key]).map(item => item.key))
const canSubmit = computed(() => Boolean(props.form.model_id && props.form.name.trim() && props.form.slug.trim()))

function slugify(value: string) {
  return value.toLowerCase().trim().replace(/[^\p{L}\p{N}]+/gu, '-').replace(/^-+|-+$/g, '')
}

function formatBytes(value?: number) {
  if (value === undefined || !Number.isFinite(value) || value < 0) return 'size unavailable'
  if (value === 0) return '0 B'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let amount = value
  let unit = 0
  while (amount >= 1024 && unit < units.length - 1) { amount /= 1024; unit++ }
  return `${amount >= 10 || unit === 0 ? amount.toFixed(0) : amount.toFixed(1)} ${units[unit]}`
}

function modelMeta(model: Model) {
  return `${model.quantization || '—'} · ${formatBytes(model.total_bytes)} · ctx ${model.context_length.toLocaleString()}`
}

function selectModel(model: Model) {
  const nextOptions = { ...props.form.options }
  delete nextOptions.mmproj
  delete nextOptions['spec-draft-model']
  props.form.options = nextOptions
  props.form.model_id = model.id
  props.form.name = model.name
  slugEdited = false
  props.form.slug = slugify(model.name)
}

function updateName(value: unknown) {
  props.form.name = String(value || '')
  if (!slugEdited) props.form.slug = slugify(props.form.name)
}

function updateSlug(value: unknown) {
  slugEdited = true
  props.form.slug = String(value || '')
}

function companionState(definition: CompanionDefinition) {
  const detected = companions.value[definition.key]
  if (!detected) return 'none'
  if (Object.prototype.hasOwnProperty.call(props.form.options, definition.key) && props.form.options[definition.key] === '') return 'disabled'
  return 'detected'
}

function companionValue(definition: CompanionDefinition) {
  return props.form.options[definition.key] ?? companions.value[definition.key]?.path ?? ''
}

function setCompanionValue(definition: CompanionDefinition, value: unknown) {
  props.form.options = { ...props.form.options, [definition.key]: String(value || '') }
}

function disableCompanion(definition: CompanionDefinition) {
  props.form.options = { ...props.form.options, [definition.key]: '' }
}

function enableCompanion(definition: CompanionDefinition) {
  const detected = companions.value[definition.key]
  if (!detected) return
  props.form.options = { ...props.form.options, [definition.key]: detected.path }
}

async function loadCompanions() {
  const model = selectedModel.value
  const sequence = ++companionSequence
  companions.value = {}
  if (!model) return
  companionLoading.value = true
  try {
    const config = await manager.request<EffectiveConfig>(`/api/v1/llamacpp/config?model_id=${encodeURIComponent(model.id)}`)
    if (sequence !== companionSequence || model.id !== props.form.model_id) return
    const values = config?.effective?.values || {}
    const sources = config?.effective?.sources || {}
    const detected: Partial<Record<CompanionDefinition['key'], DetectedCompanion>> = {}
    for (const definition of companionDefinitions) {
      const source = sources[definition.key]
      const path = values[definition.key]
      if (path && (source === 'model' || source === 'detected')) detected[definition.key] = { path }
    }
    if (Object.keys(detected).length) {
      try {
        const inspection = await manager.request<ModelInspection>('/api/v1/models/inspect', { method: 'POST', body: { gguf_path: model.gguf_path } })
        for (const definition of companionDefinitions) {
          if (!detected[definition.key]) continue
          detected[definition.key]!.size = inspection?.dependencies?.find(item => item.kind === definition.dependencyKind)?.total_bytes
        }
      } catch {
        // Paths remain actionable if optional GGUF inspection cannot provide sizes.
      }
    }
    if (sequence === companionSequence && model.id === props.form.model_id) companions.value = detected
  } catch {
    if (sequence === companionSequence) companions.value = {}
  } finally {
    if (sequence === companionSequence) companionLoading.value = false
  }
}

async function loadSupportingData() {
  const liveGPUs = manager.observabilityLive.value?.hardware.gpus || []
  if (liveGPUs.length) hardwareGPUs.value = liveGPUs

  try {
    const settings = await manager.request<GeneralSettings>('/api/v1/settings/general')
    const idle = Number(settings?.idle_unload_seconds?.value)
    if (Number.isFinite(idle) && idle >= 0) globalIdleSeconds.value = idle
  } catch {
    // Global lifecycle defaults are optional context; the form remains usable without them.
  }

  if (liveGPUs.length) return
  try {
    const hardware = await manager.request<{ gpus?: HardwareGPU[] }>('/api/v1/hardware')
    if (hardware?.gpus) hardwareGPUs.value = hardware.gpus
  } catch {
    // Hardware discovery is optional; manual placement can remain empty if unavailable.
  }
}

function setContextSize(value: string) {
  props.form.options = { ...props.form.options, 'ctx-size': value }
}

function setPlacementMode(mode: 'auto' | 'manual') {
  props.form.gpu_mode = mode
  if (mode === 'auto') {
    props.form.gpu_devices = []
    props.form.tensor_split = ''
  }
}

function toggleGPU(id: string) {
  const selected = new Set(props.form.gpu_devices)
  if (selected.has(id)) selected.delete(id)
  else selected.add(id)
  props.form.gpu_devices = [...selected]
}

function gpuSelected(id: string) {
  return props.form.gpu_devices.includes(id)
}

watch(() => props.form.model_id, loadCompanions)
onMounted(() => {
  slugEdited = Boolean(props.form.slug && slugify(props.form.name) !== slugify(props.form.slug))
  void Promise.all([loadSupportingData(), loadCompanions()])
})
</script>

<template>
  <div class="space-y-6" data-testid="instance-form">
    <div class="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between" data-testid="instance-form-header">
      <UPageHeader
        class="w-full min-w-0 sm:flex-1"
        headline="CONTROL PLANE"
        :title="title"
        description="Configure one durable llama-server process. The slug is the exact OpenAI model ID and defaults from the Instance name."
      />
      <div class="w-full sm:w-auto sm:shrink-0"><AppButton to="/instances" intent="secondary">Back to Instances</AppButton></div>
    </div>

    <Frame v-if="error" class="p-3" data-testid="instance-form-error">
      <div class="flex flex-wrap items-start gap-2">
        <StatusTag variant="failed">Unable to save Instance</StatusTag>
        <p class="min-w-0 flex-1 text-xs text-muted">{{ error }}</p>
      </div>
    </Frame>
    <div v-if="loading" class="space-y-3"><USkeleton class="h-12 w-full" /><USkeleton class="h-56 w-full" /></div>

    <UForm v-else :state="form" class="space-y-5" @submit="emit('submit')">
      <nav class="flex flex-wrap gap-2 border-y border-[var(--color-divider)] py-3" aria-label="Instance form sections" data-testid="instance-form-section-nav">
        <AppButton to="#instance-identity" intent="ghost" size="xs">Identity</AppButton>
        <AppButton to="#instance-companions" intent="ghost" size="xs">Companions</AppButton>
        <AppButton to="#instance-lifecycle" intent="ghost" size="xs">Lifecycle</AppButton>
        <AppButton to="#instance-placement" intent="ghost" size="xs">Placement</AppButton>
        <AppButton to="#instance-overrides" intent="ghost" size="xs">Overrides</AppButton>
        <AppButton to="#instance-observability" intent="ghost" size="xs">Observability</AppButton>
      </nav>

      <Frame id="instance-identity" class="p-5 scroll-mt-4" data-testid="instance-form-identity">
        <div class="mb-4"><h2 class="text-base font-semibold">Model + identity</h2><p class="mt-1 text-xs text-[var(--neutral-700)]">Choose the registered GGUF and define the OpenAI-facing Instance identity.</p></div>
        <div class="grid gap-5 lg:grid-cols-2">
          <fieldset>
            <legend class="mb-2 text-xs font-semibold">Registered Model</legend>
            <div class="divide-y divide-[var(--color-divider)] border border-[var(--color-divider)]" data-testid="instance-model-list">
              <label
                v-for="model in manager.models.value"
                :key="model.id"
                class="flex cursor-pointer items-start gap-3 p-3 transition-colors"
                :class="form.model_id === model.id ? 'bg-[var(--accent-100)]' : 'bg-transparent hover:bg-[var(--neutral-200)]'"
              >
                <input type="radio" name="registered-model" :value="model.id" :checked="form.model_id === model.id" class="mt-0.5" @change="selectModel(model)">
                <span class="min-w-0"><span class="block text-sm font-semibold">{{ model.name }}</span><span class="mt-1 block font-mono text-[length:var(--font-size-table-header)] text-[var(--neutral-700)]">{{ modelMeta(model) }}</span></span>
              </label>
            </div>
          </fieldset>
          <div class="space-y-4">
            <UFormField label="Instance name" name="name" required><UInput :model-value="form.name" data-testid="instance-name" class="w-full" required @update:model-value="updateName" /></UFormField>
            <UFormField label="Instance slug — exact OpenAI model ID" name="slug" required><UInput :model-value="form.slug" data-testid="instance-slug" class="w-full font-mono" required @update:model-value="updateSlug" /></UFormField>
          </div>
        </div>
      </Frame>

      <Frame id="instance-companions" class="p-5 scroll-mt-4" data-testid="instance-form-companions">
        <div class="mb-4"><div class="flex flex-wrap items-center gap-2"><h2 class="text-base font-semibold">Companion files</h2><span v-if="companionLoading" class="text-[length:var(--font-size-kicker)] uppercase tracking-[.12em] text-[var(--neutral-700)]">Resolving</span></div><p class="mt-1 text-xs text-[var(--neutral-700)]">Detected next to the Model's GGUF and inherited from its llama.cpp defaults. Disable to run this Instance without them.</p></div>
        <div class="grid gap-3 lg:grid-cols-2">
          <div
            v-for="definition in companionDefinitions"
            :key="definition.key"
            class="border p-4"
            :class="companionState(definition) === 'detected' ? 'border-[var(--color-accent)]' : 'border-[var(--color-divider)]'"
            :data-testid="`companion-${definition.key}`"
          >
            <div class="flex flex-wrap items-start justify-between gap-3">
              <div><p class="text-sm font-semibold">{{ definition.label }}</p><p class="mt-1 font-mono text-[length:var(--font-size-table-header)] text-[var(--neutral-700)]">{{ definition.flag }}</p></div>
              <div class="flex items-center gap-2">
                <StatusTag v-if="companionState(definition) === 'detected'" variant="ready">Auto-detected</StatusTag>
                <StatusTag v-else-if="companionState(definition) === 'disabled'" variant="neutral">Ignored</StatusTag>
                <StatusTag v-else variant="neutral">None found</StatusTag>
                <AppButton v-if="companionState(definition) === 'detected'" type="button" intent="ghost" size="xs" @click="disableCompanion(definition)">Disable</AppButton>
                <AppButton v-else-if="companionState(definition) === 'disabled'" type="button" intent="ghost" size="xs" @click="enableCompanion(definition)">Enable</AppButton>
              </div>
            </div>
            <template v-if="companionState(definition) !== 'none'">
              <UFormField :label="definition.flag" class="mt-4"><UInput :model-value="companionValue(definition)" class="w-full font-mono" placeholder="not set" @update:model-value="setCompanionValue(definition, $event)" /></UFormField>
              <p v-if="companionState(definition) === 'detected'" class="mt-2 font-mono text-[length:var(--font-size-kicker)] text-[var(--neutral-700)]">{{ formatBytes(companions[definition.key]?.size) }} · inherited from the Model defaults</p>
              <p v-else class="mt-2 text-[length:var(--font-size-kicker)] text-[var(--neutral-700)]">value cleared — the flag is not passed</p>
            </template>
            <p v-else class="mt-4 text-xs text-[var(--neutral-700)]">No matching file was detected alongside this Model's GGUF.</p>
          </div>
        </div>
      </Frame>

      <Frame id="instance-lifecycle" class="p-5 scroll-mt-4" data-testid="instance-form-lifecycle">
        <div class="mb-4"><h2 class="text-base font-semibold">Lifecycle & scheduling</h2></div>
        <div class="grid gap-5 lg:grid-cols-2">
          <div>
            <p class="mb-2 text-xs font-semibold">Priority</p>
            <div class="inline-flex border border-[var(--color-divider)]" data-testid="instance-priority">
              <UButton
                v-for="priority in ['low', 'normal', 'high']"
                :key="priority"
                type="button"
                size="sm"
                :color="form.priority === priority ? 'primary' : 'neutral'"
                :variant="form.priority === priority ? 'solid' : 'ghost'"
                class="border-r border-[var(--color-divider)] last:border-r-0"
                :aria-pressed="form.priority === priority"
                :data-testid="`priority-${priority}`"
                @click="form.priority = priority"
              >{{ priority.charAt(0).toUpperCase() + priority.slice(1) }}</UButton>
            </div>
          </div>
          <UFormField :label="`Idle unload timeout (seconds · 0 inherits the global ${globalIdleSeconds} s)`" name="idle_unload_seconds"><UInputNumber v-model="form.idle_unload_seconds" class="w-full" :min="0" /></UFormField>
        </div>
        <div class="mt-5 grid gap-4 md:grid-cols-2">
          <UCheckbox v-model="form.enabled" label="Enabled" />
          <UCheckbox v-model="form.always_on" label="Always On" description="Keep this Instance running whenever resources permit." />
          <UCheckbox v-model="form.autoload_enabled" label="Autoload on request" />
          <UCheckbox v-model="form.eviction_enabled" label="Allow resource-pressure eviction" description="Allow the manager to stop this Instance when RAM/VRAM is needed for another Instance." />
        </div>
      </Frame>

      <Frame id="instance-placement" class="p-5 scroll-mt-4" data-testid="instance-form-placement">
        <div class="mb-4"><h2 class="text-base font-semibold">Placement</h2></div>
        <div class="mb-4 inline-flex border border-[var(--color-divider)]" data-testid="placement-mode">
          <UButton type="button" size="sm" :color="form.gpu_mode === 'auto' ? 'primary' : 'neutral'" :variant="form.gpu_mode === 'auto' ? 'solid' : 'ghost'" class="border-r border-[var(--color-divider)]" :aria-pressed="form.gpu_mode === 'auto'" data-testid="placement-mode-auto" @click="setPlacementMode('auto')">Auto</UButton>
          <UButton type="button" size="sm" :color="form.gpu_mode === 'manual' ? 'primary' : 'neutral'" :variant="form.gpu_mode === 'manual' ? 'solid' : 'ghost'" :aria-pressed="form.gpu_mode === 'manual'" data-testid="placement-mode-manual" @click="setPlacementMode('manual')">Manual</UButton>
        </div>
        <p v-if="form.gpu_mode === 'auto'" class="mb-4 text-xs text-[var(--neutral-700)]">The scheduler picks devices from fresh VRAM state at launch time.</p>
        <div v-else class="mb-5 space-y-4" data-testid="manual-placement-controls">
          <div class="grid gap-2 md:grid-cols-2 xl:grid-cols-3">
            <label v-for="gpu in hardwareGPUs" :key="gpu.id" class="flex cursor-pointer items-center gap-3 border p-3" :class="gpuSelected(gpu.id) ? 'border-[var(--color-accent)]' : 'border-[var(--color-divider)]'">
              <input type="checkbox" :checked="gpuSelected(gpu.id)" @change="toggleGPU(gpu.id)">
              <span><span class="block font-mono text-xs font-semibold">{{ gpu.id }}</span><span class="mt-1 block text-[length:var(--font-size-kicker)] text-[var(--neutral-700)]">{{ formatBytes(gpu.free_bytes) }} free</span></span>
            </label>
            <p v-if="!hardwareGPUs.length" class="text-xs text-[var(--neutral-700)]">No GPU devices were detected.</p>
          </div>
          <UFormField label="Tensor split" name="tensor_split" class="max-w-[320px]"><UInput v-model="form.tensor_split" class="w-full font-mono" placeholder="60,40" /></UFormField>
        </div>
        <div class="hardware-placement-core">
          <HardwarePlacementEditor
            :model-id="form.model_id"
            :llama-options="form.options"
            v-model:gpu-mode="form.gpu_mode"
            v-model:gpu-devices="form.gpu_devices"
            v-model:tensor-split="form.tensor_split"
            @update:context-size="setContextSize"
          />
        </div>
      </Frame>

      <Frame id="instance-overrides" class="p-5 scroll-mt-4" data-testid="instance-form-overrides">
        <div class="mb-4">
          <h2 class="text-base font-semibold">Instance llama.cpp overrides</h2>
          <p class="mt-1 text-xs text-[var(--neutral-700)]">Applied over the Model defaults, which are applied over the global defaults. Only overrides are stored at this layer.</p>
        </div>
        <LlamaCppOptionsEditor
          v-model="form.options"
          scope="instance"
          :model-id="form.model_id"
          :instance-id="instanceId"
          :exclude-keys="detectedCompanionKeys"
        />
      </Frame>

      <Frame id="instance-observability" class="p-5 scroll-mt-4" data-testid="instance-form-observability">
        <div class="mb-4"><h2 class="text-base font-semibold">Observability & privacy</h2><p class="mt-1 text-xs text-[var(--neutral-700)]">Preserves per-Instance inference request logging controls.</p></div>
        <UFormField label="Inference request logging" name="request_log_mode" description="Metadata-only is the privacy-preserving default and still records timing, tokens, result, endpoint, streaming state and safe API-key attribution.">
          <select v-model="form.request_log_mode" class="w-full max-w-xl border border-[var(--color-divider)] bg-[var(--color-surface)] px-3 py-2 text-sm">
            <option value="metadata">Metadata only</option>
            <option value="full">Full request and response content</option>
          </select>
        </UFormField>
        <div v-if="form.request_log_mode === 'full'" class="mt-3 border-l-2 border-[var(--color-accent)] pl-3" data-testid="instance-full-log-warning">
          <p class="text-sm font-semibold text-[var(--color-text)]">Full content logging enabled</p>
          <p class="mt-1 text-xs leading-5 text-[var(--neutral-800)]">Prompts, messages, generated content, embeddings payloads and tool arguments for this Instance may be retained until the configured observability retention period expires.</p>
        </div>
      </Frame>

      <div class="flex flex-wrap items-center justify-between gap-4 border-t border-[var(--color-divider)] pt-5">
        <UCheckbox v-if="showLaunchAfterCreate" :model-value="launchAfterCreate" label="Launch after creation" @update:model-value="emit('update:launchAfterCreate', Boolean($event))" />
        <span v-else />
        <div class="flex min-w-0 flex-1 flex-wrap items-center gap-3">
          <StatusTag v-if="dirty" data-testid="instance-dirty-state" variant="pending">Unsaved changes</StatusTag>
          <p v-if="!canSubmit" class="max-w-xl text-xs leading-5 text-[var(--neutral-700)]" data-testid="instance-submit-requirements">Select a registered Model and enter an Instance name and slug to enable {{ submitLabel }}.</p>
          <p v-else-if="submitDisabled && submitDisabledReason" class="max-w-xl text-xs leading-5 text-[var(--neutral-700)]" data-testid="instance-submit-requirements">{{ submitDisabledReason }}</p>
        </div>
        <div class="flex items-center gap-2"><AppButton to="/instances" intent="secondary">Cancel</AppButton><AppButton type="submit" intent="primary" :loading="busy" :disabled="!canSubmit || submitDisabled">{{ submitLabel }}</AppButton></div>
      </div>
    </UForm>
  </div>
</template>

<style scoped>
.hardware-placement-core :deep(.space-y-4 > .grid.gap-4.md\:grid-cols-2) { display: none; }
.hardware-placement-core :deep(.space-y-4 > .grid.gap-4.md\:grid-cols-2 + [role="alert"]) { display: none; }
</style>