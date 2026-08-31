<script setup lang="ts">
type AvailableGGUF = {
  path: string
  name: string
  total_bytes: number
  modified_at?: string
  quantization?: string
  suggested_options?: Record<string, string>
}
type CreateResponse = { model: { id: string }; instance?: { id: string }; start_error?: string }
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
type ModelInspection = HFArtifact & {
  model_name?: string
  architecture?: string
  context_length?: number
  gguf_version?: number
  metadata_count?: number
  warning?: string
  suggested_options?: Record<string, string>
  dependency_candidates?: HFDependency[]
}
type HFDetail = { id: string; revision: string; artifacts: HFArtifact[] }
type CompanionDefinition = { key: 'mmproj' | 'spec-draft-model'; kind: 'mmproj' | 'mtp'; title: string; flag: string }

const manager = useManager()
const router = useRouter()
const route = useRoute()
const busy = ref(false)
const scanning = ref(false)
const inspectingMetadata = ref(false)
const remoteLoading = ref(false)
const error = ref('')
const metadataWarning = ref('')
const detectedArchitecture = ref('')
const contextEdited = ref(false)
const availableGGUFs = ref<AvailableGGUF[]>([])
const createFirstInstance = ref(true)
const firstInstanceSlugEdited = ref(false)
const autoSuggestedName = ref('')
const autoSuggestedOptions = ref<Record<string, string>>({})
const localInspection = ref<ModelInspection | null>(null)
const remoteDetail = ref<HFDetail | null>(null)
const remoteArtifact = ref<HFArtifact | null>(null)
const remoteRepo = computed(() => typeof route.query.repo === 'string' ? route.query.repo.trim() : '')
const remoteArtifactID = computed(() => typeof route.query.artifact === 'string' ? route.query.artifact.trim() : '')
const remoteMode = computed(() => Boolean(remoteRepo.value && remoteArtifactID.value))

const companionDefinitions: CompanionDefinition[] = [
  { key: 'mmproj', kind: 'mmproj', title: 'Vision projector', flag: '--mmproj' },
  { key: 'spec-draft-model', kind: 'mtp', title: 'MTP draft model', flag: '--spec-draft-model' }
]
const companionOptionKeys = ['mmproj', 'spec-draft-model', 'spec-type']

const form = reactive({
  gguf_path: '',
  name: '',
  context_length: 0,
  options: {} as Record<string, string>,
  first_instance: {
    name: '',
    slug: '',
    always_on: false,
    autoload_enabled: true,
    eviction_enabled: true,
    start: false
  }
})

function ggufCapabilities(file: AvailableGGUF) {
  const options = file.suggested_options || {}
  const capabilities: string[] = []
  if (options['spec-draft-model'] || options['spec-type'] === 'draft-mtp') capabilities.push('MTP')
  if (options.mmproj) capabilities.push('Vision')
  return capabilities
}

const selectedGGUF = computed(() => availableGGUFs.value.find(file => file.path === form.gguf_path) || null)
const submitDisabled = computed(() => {
  if (busy.value || scanning.value || remoteLoading.value) return true
  if (remoteMode.value ? !remoteArtifact.value : !form.gguf_path) return true
  if (!form.name.trim()) return true
  return createFirstInstance.value && (!form.first_instance.name.trim() || !form.first_instance.slug.trim())
})

function slugify(value: string) {
  return value.toLowerCase().trim().replace(/[^\p{L}\p{N}]+/gu, '-').replace(/^-+|-+$/g, '')
}
function filename(value: string) {
  return value.split(/[\\/]/).pop() || value
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
  const repoName = remoteRepo.value.split('/').pop() || remoteRepo.value
  return remoteArtifact.value?.quantization ? `${repoName} ${remoteArtifact.value.quantization}` : repoName
}
function applyAutoSuggestedName(name: string) {
  const suggested = capitalizeFirst(name)
  if (!suggested) return
  if (!form.name.trim() || form.name === autoSuggestedName.value) {
    form.name = suggested
    autoSuggestedName.value = suggested
  }
}
function applyAutoSuggestedOptions(suggested: Record<string, string>) {
  const next = { ...form.options }
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
  form.options = next
  autoSuggestedOptions.value = applied
}

function dependencyFor(definition: CompanionDefinition) {
  const dependencies = remoteMode.value ? (remoteArtifact.value?.dependencies || []) : (localInspection.value?.dependencies || [])
  return dependencies.find(dependency => dependency.kind === definition.kind) || null
}

function candidateList(definition: CompanionDefinition) {
  if (remoteMode.value) return []
  const candidates = (localInspection.value?.dependency_candidates || []).filter(candidate => candidate.kind === definition.kind)
  if (candidates.length) return candidates
  const selected = dependencyFor(definition)
  const optionPath = localInspection.value?.suggested_options?.[definition.key] || selectedGGUF.value?.suggested_options?.[definition.key]
  if (!selected || !optionPath) return []
  return [{ ...selected, option_path: optionPath }]
}

function detectedCompanionPath(definition: CompanionDefinition) {
  if (remoteMode.value) return ''
  return localInspection.value?.suggested_options?.[definition.key]
    || selectedGGUF.value?.suggested_options?.[definition.key]
    || dependencyFor(definition)?.option_path
    || ''
}

function companionAvailable(definition: CompanionDefinition) {
  if (remoteMode.value) return Boolean(dependencyFor(definition))
  return Boolean(detectedCompanionPath(definition) || candidateList(definition).length)
}

function companionState(definition: CompanionDefinition): 'detected' | 'disabled' | 'none' {
  if (!companionAvailable(definition)) return 'none'
  if (Object.prototype.hasOwnProperty.call(form.options, definition.key) && form.options[definition.key] === '') return 'disabled'
  return 'detected'
}

function companionValue(definition: CompanionDefinition) {
  if (Object.prototype.hasOwnProperty.call(form.options, definition.key)) return form.options[definition.key] || ''
  if (remoteMode.value) return dependencyFor(definition)?.files?.[0]?.path || dependencyFor(definition)?.name || ''
  return detectedCompanionPath(definition)
}

function activeCandidate(definition: CompanionDefinition) {
  const value = companionValue(definition)
  return candidateList(definition).find(candidate => candidate.option_path === value) || null
}

function companionDisplayName(definition: CompanionDefinition) {
  return activeCandidate(definition)?.name
    || dependencyFor(definition)?.name
    || filename(companionValue(definition))
}

function companionSize(definition: CompanionDefinition) {
  return activeCandidate(definition)?.total_bytes || dependencyFor(definition)?.total_bytes || 0
}

function disableCompanion(definition: CompanionDefinition) {
  const next = { ...form.options, [definition.key]: '' }
  if (definition.kind === 'mtp') next['spec-type'] = ''
  form.options = next
}

function enableCompanion(definition: CompanionDefinition) {
  const next = { ...form.options }
  if (remoteMode.value) {
    delete next[definition.key]
    if (definition.kind === 'mtp') delete next['spec-type']
  } else {
    const selected = dependencyFor(definition)
    const candidate = candidateList(definition).find(item => item.name === selected?.name) || candidateList(definition)[0]
    const path = candidate?.option_path || detectedCompanionPath(definition)
    if (path) next[definition.key] = path
    else delete next[definition.key]
    if (definition.kind === 'mtp') next['spec-type'] = 'draft-mtp'
  }
  form.options = next
}

function chooseCompanionCandidate(definition: CompanionDefinition, candidate: HFDependency) {
  if (!candidate.option_path) return
  const next = { ...form.options, [definition.key]: candidate.option_path }
  if (definition.kind === 'mtp') next['spec-type'] = 'draft-mtp'
  form.options = next
}

function setCompanionValue(definition: CompanionDefinition, value: unknown) {
  if (remoteMode.value) return
  const next = { ...form.options, [definition.key]: String(value || '') }
  if (definition.kind === 'mtp' && next[definition.key]) next['spec-type'] = 'draft-mtp'
  form.options = next
}

watch(() => form.name, (name) => {
  if (createFirstInstance.value) form.first_instance.name = name
})
watch(createFirstInstance, (enabled) => {
  if (enabled) form.first_instance.name = form.name
})
watch(() => form.first_instance.name, (name) => {
  if (!firstInstanceSlugEdited.value) form.first_instance.slug = slugify(name)
})
watch(() => form.gguf_path, (path) => {
  if (remoteMode.value) return
  localInspection.value = null
  if (form.name === autoSuggestedName.value) form.name = ''
  autoSuggestedName.value = ''
  const suggested = availableGGUFs.value.find(file => file.path === path)?.suggested_options || {}
  applyAutoSuggestedOptions(suggested)
  void inspectSelectedGGUF(path)
})

function messageFor(value: any, fallback: string) {
  return value?.data?.error || value?.message || fallback
}

async function inspectSelectedGGUF(path: string) {
  metadataWarning.value = ''
  detectedArchitecture.value = ''
  contextEdited.value = false
  if (remoteMode.value || !path) return
  inspectingMetadata.value = true
  try {
    const result = await manager.request<ModelInspection>('/api/v1/models/inspect', {
      method: 'POST',
      body: { gguf_path: path }
    })
    if (form.gguf_path !== path) return
    localInspection.value = result
    applyAutoSuggestedName(result.model_name || '')
    applyAutoSuggestedOptions(result.suggested_options || {})
    metadataWarning.value = result.warning || ''
    detectedArchitecture.value = result.architecture || ''
    if (!contextEdited.value && Number(result.context_length) > 0) form.context_length = Number(result.context_length)
  } catch (e: any) {
    if (form.gguf_path === path) {
      localInspection.value = null
      metadataWarning.value = messageFor(e, 'Unable to inspect GGUF metadata automatically')
    }
  } finally {
    if (form.gguf_path === path) inspectingMetadata.value = false
  }
}

async function scanGGUFs() {
  if (remoteMode.value) return
  scanning.value = true
  error.value = ''
  try {
    availableGGUFs.value = await manager.request<AvailableGGUF[]>('/api/v1/models/available') || []
    if (!availableGGUFs.value.some(file => file.path === form.gguf_path)) form.gguf_path = ''
  } catch (e: any) {
    error.value = messageFor(e, 'Unable to scan model folder')
    availableGGUFs.value = []
    form.gguf_path = ''
  } finally {
    scanning.value = false
  }
}

async function loadRemoteArtifact() {
  remoteLoading.value = true
  error.value = ''
  try {
    remoteDetail.value = await manager.request<HFDetail>(`/api/v1/huggingface/model?repo=${encodeURIComponent(remoteRepo.value)}`)
    remoteArtifact.value = remoteDetail.value?.artifacts?.find(item => item.id === remoteArtifactID.value) || null
    if (!remoteArtifact.value) throw new Error('Selected Hugging Face artifact is no longer available')
    if (!remoteArtifact.value.complete) throw new Error('Selected Hugging Face split GGUF is incomplete')
    createFirstInstance.value = true
    if (!form.name) form.name = remoteDefaultName()
  } catch (e: any) {
    remoteArtifact.value = null
    error.value = messageFor(e, 'Unable to load Hugging Face artifact')
  } finally {
    remoteLoading.value = false
  }
}

onMounted(() => {
  if (remoteMode.value) void loadRemoteArtifact()
  else void scanGGUFs()
})

async function createModel() {
  busy.value = true
  error.value = ''
  try {
    if (remoteMode.value) {
      const result = await manager.request<CreateResponse>('/api/v1/huggingface/import', {
        method: 'POST',
        body: {
          repo_id: remoteRepo.value,
          artifact_id: remoteArtifactID.value,
          name: form.name,
          context_length: form.context_length,
          options: form.options,
          first_instance: form.first_instance
        }
      })
      await manager.refresh()
      await router.push('/instances')
      return result
    }

    const body = {
      gguf_path: form.gguf_path,
      name: form.name,
      context_length: form.context_length,
      options: form.options,
      first_instance: createFirstInstance.value ? form.first_instance : undefined
    }
    const result = await manager.request<CreateResponse>('/api/v1/models', { method: 'POST', body })
    await manager.refresh()
    if (result.start_error) {
      error.value = `Model and Instance were created, but llama-server failed to start: ${result.start_error}`
      return
    }
    await router.push(createFirstInstance.value ? '/instances' : '/models')
  } catch (e: any) {
    error.value = messageFor(e, remoteMode.value ? 'Unable to create downloading Instance' : 'Unable to create model')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="space-y-5">
    <div class="flex flex-wrap items-start justify-between gap-4" data-testid="model-add-header">
      <UPageHeader
        class="min-w-0 flex-1"
        headline="MODEL REGISTRY"
        :title="remoteMode ? 'Launch Hugging Face model' : 'Add model'"
        :description="remoteMode ? 'Configure the Model and its first Instance now. The Instance stays in Downloading state until the selected GGUF is ready.' : 'Register a GGUF model and optionally bootstrap its first addressable Instance.'"
      />
      <AppButton :to="remoteMode ? '/models/discover' : '/models'" intent="secondary">{{ remoteMode ? 'Back to Discover' : 'Back to models' }}</AppButton>
    </div>

    <Frame v-if="error" class="border-[var(--accent-800)] p-3" data-testid="model-add-error">
      <p class="text-sm font-semibold text-[var(--accent-900)]">Unable to complete model operation</p>
      <p class="mt-1 text-xs text-[var(--neutral-800)]">{{ error }}</p>
    </Frame>

    <UForm :state="form" class="space-y-5" @submit="createModel">
      <nav class="flex flex-wrap gap-2 border-y border-[var(--color-divider)] py-3" aria-label="Model form sections" data-testid="model-form-section-nav">
        <AppButton to="#model-artifact" intent="ghost" size="xs">Artifact</AppButton>
        <AppButton to="#model-companions" intent="ghost" size="xs">Companions</AppButton>
        <AppButton to="#model-identity" intent="ghost" size="xs">Identity</AppButton>
        <AppButton to="#model-defaults" intent="ghost" size="xs">Defaults</AppButton>
        <AppButton to="#model-first-instance" intent="ghost" size="xs">First Instance</AppButton>
      </nav>

      <Frame id="model-artifact" class="p-5 scroll-mt-4" data-testid="model-form-gguf">
        <div class="mb-4 flex flex-wrap items-start justify-between gap-3">
          <div>
            <p class="text-[9.5px] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">ARTIFACT</p>
            <h2 class="mt-1 text-base font-semibold">GGUF file</h2>
          </div>
          <AppButton v-if="!remoteMode" type="button" intent="secondary" size="xs" :loading="scanning" @click="scanGGUFs">Rescan</AppButton>
        </div>

        <template v-if="remoteMode">
          <div class="border border-[var(--color-divider)] p-4" data-testid="remote-artifact-summary">
            <div v-if="remoteLoading" class="space-y-2"><USkeleton class="h-5 w-2/3" /><USkeleton class="h-4 w-1/3" /></div>
            <div v-else-if="remoteArtifact" class="space-y-2">
              <div class="flex flex-wrap items-center gap-2">
                <span class="font-semibold">{{ remoteRepo }}</span>
                <StatusTag v-if="remoteArtifact.quantization" variant="neutral">{{ remoteArtifact.quantization }}</StatusTag>
              </div>
              <p class="break-all font-mono text-[12.5px]">{{ remoteArtifact.name }}</p>
              <p class="text-[10.5px] text-[var(--neutral-700)]">{{ formatBytes(remoteArtifact.total_bytes) }} total<span v-if="remoteArtifact.dependencies?.length"> · including detected helpers</span></p>
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
              <span class="block break-all font-mono text-[12.5px]">{{ file.path }}</span>
              <span class="mt-1 block text-[10.5px] text-[var(--neutral-700)]">{{ formatBytes(file.total_bytes) }} · modified {{ formatModified(file.modified_at) }}</span>
            </span>
            <span v-if="ggufCapabilities(file).length" class="flex shrink-0 flex-wrap justify-end gap-1">
              <span v-for="capability in ggufCapabilities(file)" :key="capability" class="border border-[var(--color-divider)] px-2 py-1 text-[9.5px] font-semibold">{{ capability }}</span>
            </span>
          </label>
        </div>
        <p v-if="!remoteMode" class="mt-2 text-xs text-[var(--neutral-700)]">Already-registered GGUF files and detected helper GGUFs are hidden.</p>
      </Frame>

      <Frame id="model-companions" class="p-5 scroll-mt-4" data-testid="detected-gguf-helpers">
        <div class="mb-4">
          <p class="text-[9.5px] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">COMPANIONS</p>
          <h2 class="mt-1 text-base font-semibold">Companion files</h2>
          <p class="mt-1 text-xs text-[var(--neutral-700)]">Scanned alongside the selected GGUF · options filled automatically.</p>
        </div>
        <div class="grid gap-4 lg:grid-cols-2">
          <div
            v-for="definition in companionDefinitions"
            :key="definition.key"
            class="border p-4"
            :class="companionState(definition) === 'detected' ? 'border-[var(--color-accent)]' : 'border-[var(--color-divider)]'"
            :data-testid="`companion-${definition.kind}`"
          >
            <div class="flex flex-wrap items-start justify-between gap-3">
              <div>
                <p class="font-semibold">{{ definition.title }}</p>
                <p class="mt-1 font-mono text-[11px] text-[var(--neutral-700)]">{{ definition.flag }}</p>
              </div>
              <StatusTag v-if="companionState(definition) === 'detected'" variant="ready">Auto-detected</StatusTag>
              <StatusTag v-else-if="companionState(definition) === 'disabled'" variant="neutral">Ignored</StatusTag>
              <StatusTag v-else variant="neutral">None found</StatusTag>
            </div>

            <template v-if="companionState(definition) === 'detected'">
              <div class="mt-4 flex items-center gap-2">
                <UInput
                  :model-value="companionValue(definition)"
                  class="min-w-0 flex-1 font-mono"
                  :readonly="remoteMode"
                  :aria-label="`${definition.title} path`"
                  @update:model-value="setCompanionValue(definition, $event)"
                />
                <AppButton type="button" intent="ghost" size="xs" @click="disableCompanion(definition)">Disable</AppButton>
              </div>
              <p class="mt-2 text-[10.5px] text-[var(--neutral-700)]">{{ definition.title }}: {{ companionDisplayName(definition) }}<span v-if="companionSize(definition)"> · {{ formatBytes(companionSize(definition)) }}</span></p>
            </template>

            <template v-else-if="companionState(definition) === 'disabled'">
              <div class="mt-4 flex items-center gap-2">
                <UInput model-value="" class="min-w-0 flex-1 font-mono" placeholder="value cleared" readonly />
                <AppButton type="button" intent="ghost" size="xs" @click="enableCompanion(definition)">Enable</AppButton>
              </div>
              <p class="mt-2 text-[10.5px] text-[var(--neutral-800)]" :data-testid="`companion-disabled-${definition.kind}`">value cleared — the flag is not passed</p>
            </template>

            <p v-else class="mt-4 text-xs text-[var(--neutral-800)]" :data-testid="`companion-empty-${definition.kind}`">No compatible {{ definition.title.toLowerCase() }} was detected in this artifact scope.</p>

            <div v-if="candidateList(definition).length > 1" class="mt-4 border-t border-[var(--color-divider)] pt-3">
              <p class="mb-2 text-[9.5px] font-semibold uppercase tracking-[0.12em] text-[var(--neutral-700)]">Alternate candidates</p>
              <div class="flex flex-wrap gap-2">
                <UButton
                  v-for="candidate in candidateList(definition)"
                  :key="candidate.option_path || candidate.name"
                  type="button"
                  size="xs"
                  :color="activeCandidate(definition)?.option_path === candidate.option_path ? 'primary' : 'neutral'"
                  :variant="activeCandidate(definition)?.option_path === candidate.option_path ? 'soft' : 'ghost'"
                  class="font-mono"
                  :aria-pressed="activeCandidate(definition)?.option_path === candidate.option_path"
                  :data-testid="`companion-candidate-${definition.kind}`"
                  @click="chooseCompanionCandidate(definition, candidate)"
                >{{ candidate.quantization || candidate.name }}</UButton>
              </div>
            </div>
          </div>
        </div>
      </Frame>

      <Frame id="model-identity" class="p-5 scroll-mt-4" data-testid="model-form-identity">
        <div class="mb-4">
          <p class="text-[9.5px] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">IDENTITY</p>
          <h2 class="mt-1 text-base font-semibold">Model identity</h2>
        </div>
        <div class="grid gap-5 md:grid-cols-2">
          <UFormField label="Model name" name="name" required>
            <UInput v-model="form.name" data-testid="model-name" class="w-full" placeholder="Qwen Coder 32B" required />
          </UFormField>
          <UFormField label="Context capability" name="context_length" description="Maximum context supported by the artifact. The value remains editable.">
            <UInputNumber v-model="form.context_length" data-testid="context-capability" class="w-full" :min="0" :step="1" @update:model-value="contextEdited = true" />
            <p v-if="inspectingMetadata" class="mt-1 text-xs text-[var(--neutral-700)]">Reading GGUF metadata…</p>
            <p v-else-if="detectedArchitecture && form.context_length > 0 && !metadataWarning" class="mt-1 text-xs text-[var(--neutral-700)]">Detected from {{ detectedArchitecture }} GGUF metadata.</p>
          </UFormField>
        </div>
        <div v-if="metadataWarning" data-testid="metadata-warning" class="mt-4 flex flex-wrap items-start gap-3 border border-[var(--color-divider)] p-3">
          <StatusTag variant="pending">Metadata warning</StatusTag>
          <p class="min-w-0 flex-1 text-sm text-[var(--neutral-800)]">{{ metadataWarning }}</p>
        </div>
      </Frame>

      <Frame id="model-defaults" class="p-5 scroll-mt-4" data-testid="model-form-defaults">
        <div class="mb-4">
          <p class="text-[9.5px] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">LLAMA.CPP</p>
          <h2 class="mt-1 text-base font-semibold">Model llama.cpp defaults</h2>
          <p class="mt-1 text-xs text-[var(--neutral-700)]">Reusable across every Instance of this Model. Only overrides are stored here; everything else inherits from global defaults.</p>
        </div>
        <LlamaCppOptionsEditor v-model="form.options" scope="model" :exclude-keys="companionOptionKeys" />
      </Frame>

      <Frame id="model-first-instance" class="p-5 scroll-mt-4" data-testid="model-form-first-instance">
        <div class="mb-4">
          <p class="text-[9.5px] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">BOOTSTRAP</p>
          <h2 class="mt-1 text-base font-semibold">First Instance</h2>
        </div>
        <div v-if="remoteMode" class="mb-4 border border-[var(--color-divider)] p-3 text-sm text-[var(--neutral-800)]">
          The Instance is created immediately and shown as Downloading until the GGUF completes.
        </div>
        <UCheckbox v-else v-model="createFirstInstance" data-testid="create-first-instance" label="Create a first Instance" />

        <div v-if="createFirstInstance" class="mt-4 space-y-4 border border-[var(--color-divider)] p-4">
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
            <UCheckbox v-model="form.first_instance.start" data-testid="start-instance" :label="remoteMode ? 'Launch this Instance when the download completes' : 'Launch this Instance after creation'" />
          </div>
          <p class="text-xs text-[var(--neutral-700)]">Advanced Instance settings such as priority, GPU placement, tensor split, idle timeout and instance-level llama.cpp overrides are configured from Instances.</p>
        </div>
      </Frame>

      <div class="flex flex-wrap items-center gap-x-4 gap-y-2 border-t border-[var(--color-divider)] pt-5">
        <p class="mr-auto max-w-2xl text-xs leading-5 text-[var(--neutral-700)]" data-testid="model-submit-requirements">Required: a GGUF artifact and Model name. When First Instance is enabled, its name and slug are also required.</p>
        <AppButton v-if="!remoteMode" type="button" intent="ghost" :loading="scanning" @click="scanGGUFs">Rescan</AppButton>
        <AppButton :to="remoteMode ? '/models/discover' : '/models'" intent="secondary">Cancel</AppButton>
        <AppButton type="submit" intent="primary" :loading="busy" :disabled="submitDisabled">{{ remoteMode ? 'Create and download' : 'Create model' }}</AppButton>
      </div>
    </UForm>
  </div>
</template>