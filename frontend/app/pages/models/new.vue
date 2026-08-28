<script setup lang="ts">
type AvailableGGUF = { path: string; name: string; total_bytes: number; quantization?: string; suggested_options?: Record<string, string> }
type CreateResponse = { model: { id: string }; instance?: { id: string }; start_error?: string }
type HFFile = { path: string; size: number; oid?: string }
type HFDependency = { kind: string; name: string; quantization?: string; total_bytes: number; files: HFFile[] }
type HFArtifact = { id: string; name: string; quantization?: string; model_bytes: number; total_bytes: number; shard_count: number; expected_shards: number; complete: boolean; files: HFFile[]; dependencies?: HFDependency[] }
type ModelInspection = HFArtifact & { architecture?: string; context_length?: number; gguf_version?: number; metadata_count?: number; warning?: string; suggested_options?: Record<string, string> }
type HFDetail = { id: string; revision: string; artifacts: HFArtifact[] }

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
const autoSuggestedOptions = ref<Record<string, string>>({})
const localInspection = ref<ModelInspection | null>(null)
const remoteDetail = ref<HFDetail | null>(null)
const remoteArtifact = ref<HFArtifact | null>(null)
const remoteRepo = computed(() => typeof route.query.repo === 'string' ? route.query.repo.trim() : '')
const remoteArtifactID = computed(() => typeof route.query.artifact === 'string' ? route.query.artifact.trim() : '')
const remoteMode = computed(() => Boolean(remoteRepo.value && remoteArtifactID.value))

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

const ggufItems = computed(() => availableGGUFs.value.map(file => {
  const details = [file.quantization, ...ggufCapabilities(file)].filter(Boolean)
  return {
    label: `${file.path}${details.length ? ` · ${details.join(' · ')}` : ''}`,
    value: file.path
  }
}))
const ggufPlaceholder = computed(() => scanning.value
  ? 'Scanning model folder…'
  : availableGGUFs.value.length ? 'Select GGUF' : 'No unregistered GGUF files found')
const selectedGGUF = computed(() => availableGGUFs.value.find(file => file.path === form.gguf_path) || null)
const detectedHelpers = computed(() => {
  const dependencies = remoteMode.value
    ? (remoteArtifact.value?.dependencies || [])
    : (localInspection.value?.dependencies || [])
  if (dependencies.length) {
    return dependencies.map(dependency => {
      const label = dependency.kind === 'mmproj' ? 'Vision projector' : dependency.kind === 'mtp' ? 'MTP draft model' : dependency.kind
      return `${label}: ${dependency.name}`
    })
  }
  const options = localInspection.value?.suggested_options || selectedGGUF.value?.suggested_options || {}
  const helpers: string[] = []
  if (options.mmproj) helpers.push(`Vision projector: ${filename(options.mmproj)}`)
  if (options['spec-draft-model']) helpers.push(`MTP draft model: ${filename(options['spec-draft-model'])}`)
  return helpers
})
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
function formatBytes(value: number) {
  if (!value) return 'Unknown size'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let amount = value
  let index = 0
  while (amount >= 1024 && index < units.length - 1) { amount /= 1024; index++ }
  return `${amount >= 10 || index === 0 ? amount.toFixed(0) : amount.toFixed(1)} ${units[index]}`
}
function remoteDefaultName() {
  const repoName = remoteRepo.value.split('/').pop() || remoteRepo.value
  return remoteArtifact.value?.quantization ? `${repoName} ${remoteArtifact.value.quantization}` : repoName
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
    <div class="flex items-start justify-between gap-6">
      <UPageHeader
        class="min-w-0 flex-1"
        headline="MODEL REGISTRY"
        :title="remoteMode ? 'Launch Hugging Face model' : 'Add model'"
        :description="remoteMode ? 'Configure the Model and its first Instance now. The Instance stays in Downloading state until the selected GGUF is ready.' : 'Register a GGUF model and optionally bootstrap its first addressable Instance.'"
      />
      <UButton :to="remoteMode ? '/discover' : '/models'" color="neutral" variant="soft">{{ remoteMode ? 'Back to Discover' : 'Back to models' }}</UButton>
    </div>

    <UCard class="max-w-4xl">
      <UAlert v-if="error" class="mb-5" color="error" variant="subtle" :description="error" />
      <UForm :state="form" class="space-y-6" @submit="createModel">
        <template v-if="remoteMode">
          <UFormField label="Hugging Face artifact">
            <div class="rounded-lg border border-default p-4">
              <div v-if="remoteLoading" class="space-y-2"><USkeleton class="h-5 w-2/3" /><USkeleton class="h-4 w-1/3" /></div>
              <div v-else-if="remoteArtifact" class="space-y-1">
                <div class="flex flex-wrap items-center gap-2"><span class="font-semibold">{{ remoteRepo }}</span><UBadge v-if="remoteArtifact.quantization" color="primary" variant="subtle">{{ remoteArtifact.quantization }}</UBadge></div>
                <p class="font-mono text-xs text-muted">{{ remoteArtifact.name }}</p>
                <p class="text-xs text-muted">{{ formatBytes(remoteArtifact.total_bytes) }} total<span v-if="remoteArtifact.dependencies?.length"> including detected helpers</span></p>
              </div>
            </div>
          </UFormField>
        </template>
        <UFormField v-else label="GGUF file" name="gguf_path" description="Already-registered GGUF files and detected helper GGUFs are hidden." required>
          <USelectMenu v-model="form.gguf_path" data-testid="gguf-select" class="w-full" :items="ggufItems" label-key="label" value-key="value" :placeholder="ggufPlaceholder" :disabled="scanning || !availableGGUFs.length" required />
        </UFormField>
        <UAlert
          v-if="detectedHelpers.length"
          data-testid="detected-gguf-helpers"
          color="success"
          variant="subtle"
          title="Detected llama.cpp helpers"
          :description="remoteMode ? `${detectedHelpers.join(' · ')}. These helpers will be downloaded and attached automatically.` : `${detectedHelpers.join(' · ')}. Their model-level llama.cpp options were filled automatically.`"
        />
        <UFormField label="Model name" name="name" required>
          <UInput v-model="form.name" data-testid="model-name" class="w-full" placeholder="Qwen Coder 32B" required />
        </UFormField>
        <UFormField label="Context capability" name="context_length" description="Maximum context supported by the artifact. GGUF metadata is used automatically when available; the value remains editable.">
          <UInputNumber v-model="form.context_length" data-testid="context-capability" class="w-full" :min="0" :step="1" @update:model-value="contextEdited = true" />
          <p v-if="inspectingMetadata" class="mt-1 text-xs text-muted">Reading GGUF metadata…</p>
          <p v-else-if="detectedArchitecture && form.context_length > 0 && !metadataWarning" class="mt-1 text-xs text-muted">Detected from {{ detectedArchitecture }} GGUF metadata.</p>
        </UFormField>
        <UAlert v-if="metadataWarning" data-testid="metadata-warning" color="warning" variant="subtle" title="GGUF metadata could not be detected" :description="metadataWarning" />

        <USeparator label="Model llama.cpp defaults" />
        <LlamaCppOptionsEditor v-model="form.options" scope="model" />

        <USeparator label="First Instance" />
        <UAlert v-if="remoteMode" color="primary" variant="subtle" title="Instance is created immediately" description="It is kept disabled internally while downloading, shown as Downloading in Instances, then enabled as soon as the GGUF is complete." />
        <UCheckbox v-else v-model="createFirstInstance" data-testid="create-first-instance" label="Create a first Instance" />
        <div v-if="createFirstInstance" class="space-y-4 rounded-lg border border-default p-4">
          <UFormField label="Instance name" name="first_instance.name" description="Defaults from the Model name while first-Instance creation is enabled." required>
            <UInput v-model="form.first_instance.name" data-testid="instance-name" class="w-full" placeholder="Qwen Coding 32B" required />
          </UFormField>
          <UFormField label="Instance slug" name="first_instance.slug" description="Exact OpenAI model ID. Defaults from the Instance name but can be customized." required>
            <UInput v-model="form.first_instance.slug" data-testid="instance-slug" class="w-full font-mono" required @update:model-value="firstInstanceSlugEdited = true" />
          </UFormField>
          <div class="space-y-3">
            <UCheckbox v-model="form.first_instance.always_on" data-testid="always-on" label="Always On" description="Keep this Instance running whenever resources permit." />
            <UCheckbox v-model="form.first_instance.autoload_enabled" data-testid="autoload-enabled" label="Autoload on request" />
            <UCheckbox v-model="form.first_instance.eviction_enabled" data-testid="eviction-enabled" label="Allow resource-pressure eviction" description="Allow the manager to stop this Instance when RAM/VRAM is needed for another Instance." />
          </div>
          <UCheckbox v-model="form.first_instance.start" data-testid="start-instance" :label="remoteMode ? 'Launch this Instance when the download completes' : 'Launch this Instance after creation'" />
          <p class="text-xs text-muted">Advanced Instance settings such as priority, GPU placement, tensor split, idle timeout and instance-level llama.cpp overrides are configured from Instances.</p>
        </div>

        <div class="flex flex-wrap justify-end gap-2 pt-2">
          <UButton v-if="!remoteMode" type="button" color="neutral" variant="soft" :loading="scanning" @click="scanGGUFs">Rescan</UButton>
          <UButton :to="remoteMode ? '/discover' : '/models'" color="neutral" variant="soft">Cancel</UButton>
          <UButton type="submit" :loading="busy" :disabled="submitDisabled">{{ remoteMode ? 'Create and download' : 'Create model' }}</UButton>
        </div>
      </UForm>
    </UCard>
  </div>
</template>