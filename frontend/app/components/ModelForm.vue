<script setup lang="ts">
import { companionOptionKeys, type ModelInspection } from '~/utils/modelCompanions'

type AvailableGGUF = {
  path: string
  name: string
  total_bytes: number
  modified_at?: string
  quantization?: string
  suggested_options?: Record<string, string>
}

type HFFile = { path: string; size: number; oid?: string }
type HFDependency = {
  kind: string
  name: string
  quantization?: string
  total_bytes: number
  files: HFFile[]
  option_path?: string
}
type HFArtifact = {
  id: string
  name: string
  quantization?: string
  model_bytes: number
  total_bytes: number
  shard_count: number
  expected_shards: number
  complete: boolean
  files: HFFile[]
  dependencies?: HFDependency[]
}
type HFDetail = { id: string; revision: string; artifacts: HFArtifact[] }

type ModelFormFirstInstance = {
  name: string
  slug: string
  always_on: boolean
  autoload_enabled: boolean
  eviction_enabled: boolean
  start: boolean
}

type ModelFormState = {
  name: string
  context_length: number
  options: Record<string, string>
  gguf_path?: string
  first_instance?: ModelFormFirstInstance
}

const props = withDefaults(defineProps<{
  form: ModelFormState
  mode: 'create' | 'edit'
  title: string
  description: string
  submitLabel: string
  busy?: boolean
  error?: string
  loading?: boolean
  submitDisabled?: boolean
  dirty?: boolean
  modelId?: string
  inspection?: ModelInspection | null
  inspecting?: boolean
  remote?: boolean
  remoteRepo?: string
  remoteArtifactId?: string
  backTo?: string
  backLabel?: string
}>(), {
  busy: false,
  error: '',
  loading: false,
  submitDisabled: false,
  dirty: false,
  modelId: '',
  inspection: null,
  inspecting: false,
  remote: false,
  remoteRepo: '',
  remoteArtifactId: '',
  backTo: '/models',
  backLabel: 'Back to models'
})
const emit = defineEmits<{ submit: [] }>()
const createFirstInstance = defineModel<boolean>('createFirstInstance', { default: true })

const manager = useManager()
const scanning = ref(false)
const inspectingMetadata = ref(false)
const remoteLoading = ref(false)
const localError = ref('')
const metadataWarning = ref('')
const detectedArchitecture = ref('')
const contextEdited = ref(false)
const availableGGUFs = ref<AvailableGGUF[]>([])
const firstInstanceSlugEdited = ref(false)
const autoSuggestedName = ref('')
const autoSuggestedOptions = ref<Record<string, string>>({})
const localInspection = ref<ModelInspection | null>(null)
const remoteArtifact = ref<HFArtifact | null>(null)

const displayError = computed(() => props.error || localError.value)
const overrideCount = computed(() =>
  Object.keys(props.form.options).filter(key => !companionOptionKeys.includes(key)).length
)
const selectedGGUF = computed(() => availableGGUFs.value.find(file => file.path === props.form.gguf_path) || null)
const resolvedInspection = computed(() => props.inspection ?? localInspection.value)
const inspectingCompanions = computed(() => props.inspecting || inspectingMetadata.value)
const identityTestId = computed(() => props.mode === 'edit' ? 'model-edit-metadata' : 'model-form-identity')
const defaultsTestId = computed(() => props.mode === 'edit' ? 'model-edit-defaults' : 'model-form-defaults')
const companionsTestId = computed(() => props.mode === 'edit' ? 'model-edit-companions' : 'detected-gguf-helpers')
const submitHintTestId = computed(() => props.mode === 'edit' ? 'model-edit-submit-hint' : 'model-submit-requirements')
const errorTestId = computed(() => props.mode === 'edit' ? 'model-edit-error' : 'model-add-error')
const companionsDescription = computed(() => props.mode === 'edit'
  ? "Detected from this Model's GGUF. Disable to clear the extra flags; Enable restores inspect defaults."
  : 'Scanned alongside the selected GGUF · options filled automatically.'
)
const nameValid = computed(() => Boolean(props.form.name.trim()))
const firstInstanceValid = computed(() => {
  if (props.mode !== 'create' || !createFirstInstance.value) return true
  const first = props.form.first_instance
  return Boolean(first?.name.trim() && first.slug.trim())
})
const artifactValid = computed(() => {
  if (props.mode !== 'create') return true
  return props.remote ? Boolean(remoteArtifact.value) : Boolean(props.form.gguf_path)
})
const canSubmit = computed(() => nameValid.value && firstInstanceValid.value && artifactValid.value)
const submitBlocked = computed(() => {
  if (props.busy || scanning.value || remoteLoading.value) return true
  if (!canSubmit.value || props.submitDisabled) return true
  return false
})
const submitHint = computed(() => {
  if (props.mode === 'edit') {
    if (!nameValid.value) return 'Required: Model name.'
    return props.dirty ? 'Unsaved changes.' : 'No changes to save.'
  }
  return 'Required: a GGUF artifact and Model name. When First Instance is enabled, its name and slug are also required.'
})

function ggufCapabilities(file: AvailableGGUF) {
  const options = file.suggested_options || {}
  const capabilities: string[] = []
  if (options['spec-draft-model'] || options['spec-type'] === 'draft-mtp') capabilities.push('MTP')
  if (options.mmproj) capabilities.push('Vision')
  return capabilities
}

function slugify(value: string) {
  return value.toLowerCase().trim().replace(/[^\p{L}\p{N}]+/gu, '-').replace(/^-+|-+$/g, '')
}
function capitalizeFirst(value: string) {
  const characters = Array.from(value.trim())
  if (!characters.length) return ''
  characters[0] = characters[0]!.toLocaleUpperCase()
  return characters.join('')
}
function formatBytes(value: number) {
  if (!value) return 'Unknown size'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let amount = value
  let index = 0
  while (amount >= 1024 && index < units.length - 1) { amount /= 1024; index++ }
  return `${amount >= 10 || index === 0 ? amount.toFixed(0) : amount.toFixed(1)} ${units[index]}`
}
function formatModified(value?: string) {
  if (!value) return 'Unknown'
  const timestamp = Date.parse(value)
  if (!Number.isFinite(timestamp)) return 'Unknown'
  const delta = Math.max(0, Date.now() - timestamp)
  const minutes = Math.floor(delta / 60_000)
  if (minutes < 1) return 'just now'
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  const days = Math.floor(hours / 24)
  if (days < 30) return `${days}d ago`
  return new Date(timestamp).toLocaleDateString()
}
function remoteDefaultName() {
  const repoName = props.remoteRepo.split('/').pop() || props.remoteRepo
  return remoteArtifact.value?.quantization ? `${repoName} ${remoteArtifact.value.quantization}` : repoName
}
function applyAutoSuggestedName(name: string) {
  const suggested = capitalizeFirst(name)
  if (!suggested) return
  if (!props.form.name.trim() || props.form.name === autoSuggestedName.value) {
    props.form.name = suggested
    autoSuggestedName.value = suggested
  }
}
function applyAutoSuggestedOptions(suggested: Record<string, string>) {
  const next = { ...props.form.options }
  for (const [key, value] of Object.entries(autoSuggestedOptions.value)) {
    if (next[key] === value) delete next[key]
  }
  const applied: Record<string, string> = {}
  for (const [key, value] of Object.entries(suggested)) {
    if (!Object.prototype.hasOwnProperty.call(next, key)) {
      next[key] = value
      applied[key] = value
    }
  }
  props.form.options = next
  autoSuggestedOptions.value = applied
}

function messageFor(value: any, fallback: string) {
  return value?.data?.error || value?.message || fallback
}

watch(() => props.form.name, (name) => {
  if (props.mode === 'create' && createFirstInstance.value && props.form.first_instance) {
    props.form.first_instance.name = name
  }
})
watch(createFirstInstance, (enabled) => {
  if (props.mode === 'create' && enabled && props.form.first_instance) {
    props.form.first_instance.name = props.form.name
  }
})
watch(() => props.form.first_instance?.name, (name) => {
  if (props.mode !== 'create' || firstInstanceSlugEdited.value || !props.form.first_instance) return
  props.form.first_instance.slug = slugify(name || '')
})
watch(() => props.form.gguf_path, (path) => {
  if (props.mode !== 'create' || props.remote) return
  localInspection.value = null
  if (props.form.name === autoSuggestedName.value) props.form.name = ''
  autoSuggestedName.value = ''
  const suggested = availableGGUFs.value.find(file => file.path === path)?.suggested_options || {}
  applyAutoSuggestedOptions(suggested)
  void inspectSelectedGGUF(path || '')
})

async function inspectSelectedGGUF(path: string) {
  metadataWarning.value = ''
  detectedArchitecture.value = ''
  contextEdited.value = false
  if (props.remote || !path) return
  inspectingMetadata.value = true
  try {
    const result = await manager.request<ModelInspection>('/api/v1/models/inspect', {
      method: 'POST',
      body: { gguf_path: path }
    })
    if (props.form.gguf_path !== path) return
    localInspection.value = result
    applyAutoSuggestedName(result.model_name || '')
    applyAutoSuggestedOptions(result.suggested_options || {})
    metadataWarning.value = result.warning || ''
    detectedArchitecture.value = result.architecture || ''
    if (!contextEdited.value && Number(result.context_length) > 0) props.form.context_length = Number(result.context_length)
  } catch (e: any) {
    if (props.form.gguf_path === path) {
      localInspection.value = null
      metadataWarning.value = messageFor(e, 'Unable to inspect GGUF metadata automatically')
    }
  } finally {
    if (props.form.gguf_path === path) inspectingMetadata.value = false
  }
}

async function scanGGUFs() {
  if (props.remote) return
  scanning.value = true
  localError.value = ''
  try {
    availableGGUFs.value = await manager.request<AvailableGGUF[]>('/api/v1/models/available') || []
    if (!availableGGUFs.value.some(file => file.path === props.form.gguf_path)) props.form.gguf_path = ''
  } catch (e: any) {
    localError.value = messageFor(e, 'Unable to scan model folder')
    availableGGUFs.value = []
    props.form.gguf_path = ''
  } finally {
    scanning.value = false
  }
}

async function loadRemoteArtifact() {
  remoteLoading.value = true
  localError.value = ''
  try {
    const detail = await manager.request<HFDetail>(`/api/v1/huggingface/model?repo=${encodeURIComponent(props.remoteRepo)}`)
    remoteArtifact.value = detail?.artifacts?.find(item => item.id === props.remoteArtifactId) || null
    if (!remoteArtifact.value) throw new Error('Selected Hugging Face artifact is no longer available')
    if (!remoteArtifact.value.complete) throw new Error('Selected Hugging Face split GGUF is incomplete')
    createFirstInstance.value = true
    if (!props.form.name) props.form.name = remoteDefaultName()
  } catch (e: any) {
    remoteArtifact.value = null
    localError.value = messageFor(e, 'Unable to load Hugging Face artifact')
  } finally {
    remoteLoading.value = false
  }
}

onMounted(() => {
  if (props.mode !== 'create') return
  if (props.remote) void loadRemoteArtifact()
  else void scanGGUFs()
})
</script>

<template>
  <div class="space-y-5">
    <div class="flex flex-wrap items-start justify-between gap-4" :data-testid="mode === 'create' ? 'model-add-header' : undefined">
      <UPageHeader
        class="min-w-0 flex-1"
        headline="MODEL REGISTRY"
        :title="title"
        :description="description"
      />
      <AppButton :to="backTo" intent="secondary">{{ backLabel }}</AppButton>
    </div>

    <Frame v-if="displayError" class="p-3" :data-testid="errorTestId">
      <div class="flex flex-wrap items-start gap-2">
        <StatusTag variant="failed">{{ mode === 'edit' ? 'Model update failed' : 'Unable to complete model operation' }}</StatusTag>
        <p class="min-w-0 flex-1 text-xs leading-5 text-[var(--neutral-800)]">{{ displayError }}</p>
      </div>
    </Frame>

    <div v-if="loading" class="space-y-4" data-testid="model-edit-loading">
      <USkeleton class="h-44 w-full" />
      <USkeleton class="h-64 w-full" />
    </div>

    <UForm v-else :state="form" class="space-y-5" @submit="emit('submit')">
      <nav
        v-if="mode === 'create'"
        class="flex flex-wrap gap-2 border-y border-[var(--color-divider)] py-3"
        aria-label="Model form sections"
        data-testid="model-form-section-nav"
      >
        <AppButton to="#model-artifact" intent="ghost" size="xs">Artifact</AppButton>
        <AppButton to="#model-companions" intent="ghost" size="xs">Companions</AppButton>
        <AppButton to="#model-identity" intent="ghost" size="xs">Identity</AppButton>
        <AppButton to="#model-defaults" intent="ghost" size="xs">Defaults</AppButton>
        <AppButton to="#model-first-instance" intent="ghost" size="xs">First Instance</AppButton>
      </nav>

      <Frame v-if="mode === 'create'" id="model-artifact" class="p-5 scroll-mt-4" data-testid="model-form-gguf">
        <div class="mb-4 flex flex-wrap items-start justify-between gap-3">
          <div>
            <p class="text-[length:var(--font-size-kicker)] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">ARTIFACT</p>
            <h2 class="mt-1 text-base font-semibold">GGUF file</h2>
          </div>
          <AppButton v-if="!remote" type="button" intent="secondary" size="xs" :loading="scanning" @click="scanGGUFs">Rescan</AppButton>
        </div>

        <template v-if="remote">
          <div class="border border-[var(--color-divider)] p-4" data-testid="remote-artifact-summary">
            <div v-if="remoteLoading" class="space-y-2"><USkeleton class="h-5 w-2/3" /><USkeleton class="h-4 w-1/3" /></div>
            <div v-else-if="remoteArtifact" class="space-y-2">
              <div class="flex flex-wrap items-center gap-2">
                <span class="font-semibold">{{ remoteRepo }}</span>
                <StatusTag v-if="remoteArtifact.quantization" variant="neutral">{{ remoteArtifact.quantization }}</StatusTag>
              </div>
              <p class="break-all font-mono text-[length:var(--font-size-h6)]">{{ remoteArtifact.name }}</p>
              <p class="text-[length:var(--font-size-table-header)] text-[var(--neutral-700)]">{{ formatBytes(remoteArtifact.total_bytes) }} total<span v-if="remoteArtifact.dependencies?.length"> · including detected helpers</span></p>
            </div>
          </div>
        </template>

        <div v-else data-testid="gguf-select" class="border border-[var(--color-divider)]">
          <div v-if="scanning" class="px-4 py-6 text-sm text-[var(--neutral-700)]">Scanning model folder…</div>
          <div v-else-if="!availableGGUFs.length" class="px-4 py-6" data-testid="gguf-empty-state">
            <p class="text-sm font-semibold text-[var(--color-text)]">No unregistered GGUF files found</p>
            <p class="mt-1 max-w-2xl text-xs leading-5 text-[var(--neutral-800)]">Create model is unavailable until a GGUF artifact is selected. Rescan this manager or open Discover to find one.</p>
            <div class="mt-3"><AppButton to="/models/discover" intent="secondary" size="xs">Open Discover</AppButton></div>
          </div>
          <label
            v-for="file in availableGGUFs"
            v-else
            :key="file.path"
            class="flex cursor-pointer items-start gap-3 border-b border-[var(--color-divider)] px-4 py-3 last:border-b-0"
            :class="form.gguf_path === file.path ? 'bg-[var(--accent-100)]' : 'bg-transparent'"
            data-testid="gguf-option"
          >
            <input v-model="form.gguf_path" type="radio" name="gguf_path" :value="file.path" class="mt-1">
            <span class="min-w-0 flex-1">
              <span class="block break-all font-mono text-[length:var(--font-size-h6)]">{{ file.path }}</span>
              <span class="mt-1 block text-[length:var(--font-size-table-header)] text-[var(--neutral-700)]">{{ formatBytes(file.total_bytes) }} · modified {{ formatModified(file.modified_at) }}</span>
            </span>
            <span v-if="ggufCapabilities(file).length" class="flex shrink-0 flex-wrap justify-end gap-1">
              <span v-for="capability in ggufCapabilities(file)" :key="capability" class="border px-2 py-1 text-[length:var(--font-size-kicker)] font-semibold" :class="{
                'border-[var(--color-success)] bg-[var(--success-100)] text-[var(--success-700)]': capability === 'MTP',
                'border-[var(--color-accent)] bg-[var(--accent-100)] text-[var(--accent-800)]': capability === 'Vision',
                'border-[var(--color-divider)]': capability !== 'MTP' && capability !== 'Vision'
              }">{{ capability }}</span>
            </span>
          </label>
        </div>
        <p v-if="!remote" class="mt-2 text-xs text-[var(--neutral-700)]">Already-registered GGUF files and detected helper GGUFs are hidden.</p>
      </Frame>

      <ModelCompanionFiles
        v-model="form.options"
        :inspection="resolvedInspection"
        :fallback-suggested-options="selectedGGUF?.suggested_options || {}"
        :remote="remote"
        :remote-artifact="remoteArtifact"
        :inspecting="inspectingCompanions"
        :testid="companionsTestId"
        :description="companionsDescription"
      />

      <Frame id="model-identity" class="p-5 scroll-mt-4" :data-testid="identityTestId">
        <div class="mb-4">
          <p class="text-[length:var(--font-size-kicker)] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">IDENTITY</p>
          <h2 class="mt-1 text-base font-semibold">{{ mode === 'edit' ? 'Model metadata' : 'Model identity' }}</h2>
        </div>
        <div class="grid gap-5 md:grid-cols-2">
          <UFormField label="Model name" name="name" description="Model name is used to identify the model in the manager and in the API." required>
            <UInput v-model="form.name" data-testid="model-name" class="w-full" placeholder="Qwen Coder 32B" required />
          </UFormField>
          <UFormField
            label="Context capability"
            name="context_length"
            :description="mode === 'edit' ? 'Maximum context supported by this registered artifact/configuration. Use 0 when unknown.' : 'Maximum context supported by the artifact. The value remains editable.'"
          >
            <UInputNumber v-model="form.context_length" data-testid="context-capability" class="w-full font-mono tabular-nums" :min="0" :step="1" @update:model-value="contextEdited = true" />
            <p v-if="mode === 'create' && inspectingMetadata" class="mt-1 text-xs text-[var(--neutral-700)]">Reading GGUF metadata…</p>
            <p v-else-if="mode === 'create' && detectedArchitecture && form.context_length > 0 && !metadataWarning" class="mt-1 text-xs text-[var(--neutral-700)]">Detected from {{ detectedArchitecture }} GGUF metadata.</p>
          </UFormField>
        </div>
        <div v-if="mode === 'create' && metadataWarning" data-testid="metadata-warning" class="mt-4 flex flex-wrap items-start gap-3 border border-[var(--color-divider)] p-3">
          <StatusTag variant="pending">Metadata warning</StatusTag>
          <p class="min-w-0 flex-1 text-sm text-[var(--neutral-800)]">{{ metadataWarning }}</p>
        </div>
      </Frame>

      <Frame id="model-defaults" class="p-5 scroll-mt-4" :data-testid="defaultsTestId" collapsible subtitle="LLAMA.CPP" title="Model llama.cpp defaults" :description="`Reusable across every Instance of this Model. ${overrideCount} overrides configured, click to expand.`">
        <div class="mb-4">
          <p class="text-[length:var(--font-size-kicker)] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">{{ mode === 'edit' ? 'LLAMA.CPP DEFAULTS' : 'LLAMA.CPP' }}</p>
          <h2 class="mt-1 text-base font-semibold">Model llama.cpp defaults</h2>
          <p class="mt-1 text-xs text-[var(--neutral-700)]">{{ mode === 'edit' ? 'Reusable defaults inherited by every Instance of this Model unless that Instance overrides the flag.' : 'Reusable across every Instance of this Model. Only overrides are stored here; everything else inherits from global defaults.' }}</p>
        </div>
        <LlamaCppOptionsEditor v-model="form.options" scope="model" :model-id="modelId" :exclude-keys="companionOptionKeys" />
      </Frame>

      <Frame v-if="mode === 'create'" id="model-first-instance" class="p-5 scroll-mt-4" data-testid="model-form-first-instance">
        <div class="mb-4">
          <p class="text-[length:var(--font-size-kicker)] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">BOOTSTRAP</p>
          <h2 class="mt-1 text-base font-semibold">First Instance</h2>
        </div>
        <div v-if="remote" class="mb-4 border border-[var(--color-divider)] p-3 text-sm text-[var(--neutral-800)]">
          The Instance is created immediately and shown as Downloading until the GGUF completes.
        </div>
        <UCheckbox v-else v-model="createFirstInstance" data-testid="create-first-instance" label="Create a first Instance" />

        <div v-if="createFirstInstance && form.first_instance" class="mt-4 space-y-4 border border-[var(--color-divider)] p-4">
          <div class="grid gap-4 md:grid-cols-2">
            <UFormField label="Instance name" name="first_instance.name" description="Defaults from the Model name while first-Instance creation is enabled." required>
              <UInput v-model="form.first_instance.name" data-testid="instance-name" class="w-full" placeholder="Qwen Coding 32B" required />
            </UFormField>
            <UFormField label="Instance slug" name="first_instance.slug" description="Exact OpenAI model ID. Defaults from the Instance name but can be customized." required>
              <UInput v-model="form.first_instance.slug" data-testid="instance-slug" class="w-full font-mono" required @update:model-value="firstInstanceSlugEdited = true" />
            </UFormField>
          </div>
          <div class="grid gap-4 md:grid-cols-2">
            <UCheckbox v-model="form.first_instance.always_on" data-testid="always-on" label="Always On" description="Keep this Instance running whenever resources permit." />
            <UCheckbox v-model="form.first_instance.autoload_enabled" data-testid="autoload-enabled" label="Autoload on request" />
            <UCheckbox v-model="form.first_instance.eviction_enabled" data-testid="eviction-enabled" label="Allow resource-pressure eviction" description="Allow the manager to stop this Instance when RAM/VRAM is needed for another Instance." />
            <UCheckbox v-model="form.first_instance.start" data-testid="start-instance" :label="remote ? 'Launch this Instance when the download completes' : 'Launch this Instance after creation'" />
          </div>
          <p class="text-xs text-[var(--neutral-700)]">Advanced Instance settings such as priority, GPU placement, tensor split, idle timeout and instance-level llama.cpp overrides are configured from Instances.</p>
        </div>
      </Frame>

      <div class="flex flex-wrap items-center gap-x-4 gap-y-2 border-t border-[var(--color-divider)] pt-5">
        <p class="mr-auto max-w-2xl text-xs leading-5 text-[var(--neutral-700)]" :data-testid="submitHintTestId">{{ submitHint }}</p>
        <AppButton v-if="mode === 'create' && !remote" type="button" intent="ghost" :loading="scanning" @click="scanGGUFs">Rescan</AppButton>
        <AppButton :to="backTo" intent="secondary">Cancel</AppButton>
        <AppButton type="submit" intent="primary" :loading="busy" :disabled="submitBlocked">{{ submitLabel }}</AppButton>
      </div>
    </UForm>
  </div>
</template>
