<script setup lang="ts">
type AvailableGGUF = { path: string; name: string; total_bytes: number; quantization?: string; suggested_options?: Record<string, string> }
type CreateResponse = { model: { id: string }; instance?: { id: string }; start_error?: string }

const manager = useManager()
const router = useRouter()
const busy = ref(false)
const scanning = ref(false)
const error = ref('')
const availableGGUFs = ref<AvailableGGUF[]>([])
const createFirstInstance = ref(true)
const firstInstanceSlugEdited = ref(false)
const autoSuggestedOptions = ref<Record<string, string>>({})
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

const ggufItems = computed(() => availableGGUFs.value.map(file => ({
  label: `${file.path}${file.quantization ? ` · ${file.quantization}` : ''}`,
  value: file.path
})))
const ggufPlaceholder = computed(() => scanning.value
  ? 'Scanning model folder…'
  : availableGGUFs.value.length ? 'Select GGUF' : 'No unregistered GGUF files found')
const selectedGGUF = computed(() => availableGGUFs.value.find(file => file.path === form.gguf_path) || null)
const detectedHelpers = computed(() => {
  const options = selectedGGUF.value?.suggested_options || {}
  const helpers: string[] = []
  if (options.mmproj) helpers.push(`Vision projector: ${filename(options.mmproj)}`)
  if (options['spec-draft-model']) helpers.push(`MTP draft model: ${filename(options['spec-draft-model'])}`)
  return helpers
})

function slugify(value: string) {
  return value.toLowerCase().trim().replace(/[^\p{L}\p{N}]+/gu, '-').replace(/^-+|-+$/g, '')
}
function filename(value: string) {
  return value.split(/[\\/]/).pop() || value
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
  const next = { ...form.options }
  for (const [key, value] of Object.entries(autoSuggestedOptions.value)) {
    if (next[key] === value) delete next[key]
  }

  const suggested = availableGGUFs.value.find(file => file.path === path)?.suggested_options || {}
  const applied: Record<string, string> = {}
  for (const [key, value] of Object.entries(suggested)) {
    if (!Object.prototype.hasOwnProperty.call(next, key)) {
      next[key] = value
      applied[key] = value
    }
  }
  form.options = next
  autoSuggestedOptions.value = applied
})

function messageFor(value: any, fallback: string) {
  return value?.data?.error || value?.message || fallback
}

async function scanGGUFs() {
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

onMounted(() => void scanGGUFs())

async function createModel() {
  busy.value = true
  error.value = ''
  try {
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
    error.value = messageFor(e, 'Unable to create model')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="space-y-5">
    <div class="flex items-start justify-between gap-6">
      <UPageHeader class="min-w-0 flex-1" headline="MODEL REGISTRY" title="Add model" description="Register a GGUF model and optionally bootstrap its first addressable Instance." />
      <UButton to="/models" color="neutral" variant="soft">Back to models</UButton>
    </div>

    <UCard class="max-w-4xl">
      <UAlert v-if="error" class="mb-5" color="error" variant="subtle" :description="error" />
      <UForm :state="form" class="space-y-6" @submit="createModel">
        <UFormField label="GGUF file" name="gguf_path" description="Already-registered GGUF files and detected helper GGUFs are hidden." required>
          <USelectMenu v-model="form.gguf_path" data-testid="gguf-select" class="w-full" :items="ggufItems" label-key="label" value-key="value" :placeholder="ggufPlaceholder" :disabled="scanning || !availableGGUFs.length" required />
        </UFormField>
        <UAlert
          v-if="detectedHelpers.length"
          data-testid="detected-gguf-helpers"
          color="success"
          variant="subtle"
          title="Detected llama.cpp helpers"
          :description="`${detectedHelpers.join(' · ')}. Their model-level llama.cpp options were filled automatically.`"
        />
        <UFormField label="Model name" name="name" required>
          <UInput v-model="form.name" data-testid="model-name" class="w-full" placeholder="Qwen Coder 32B" required />
        </UFormField>
        <UFormField label="Context capability" name="context_length" description="Maximum context supported by the artifact/configuration. Use 0 when unknown.">
          <UInputNumber v-model="form.context_length" class="w-full" :min="0" :step="1" />
        </UFormField>

        <USeparator label="Model llama.cpp defaults" />
        <LlamaCppOptionsEditor v-model="form.options" scope="model" />

        <USeparator label="First Instance" />
        <UCheckbox v-model="createFirstInstance" data-testid="create-first-instance" label="Create a first Instance" />
        <div v-if="createFirstInstance" class="space-y-4 rounded-lg border border-default p-4">
          <UFormField label="Instance name" name="first_instance.name" description="Defaults from the Model name while first-Instance creation is enabled." required>
            <UInput v-model="form.first_instance.name" data-testid="instance-name" class="w-full" placeholder="Qwen Coding 32B" required />
          </UFormField>
          <UFormField label="Instance slug" name="first_instance.slug" description="Exact OpenAI model ID. Defaults from the Instance name but can be customized." required>
            <UInput v-model="form.first_instance.slug" data-testid="instance-slug" class="w-full font-mono" required @update:model-value="firstInstanceSlugEdited = true" />
          </UFormField>
          <div class="space-y-3">
            <UCheckbox v-model="form.first_instance.always_on" data-testid="always-on" label="Always on" />
            <UCheckbox v-model="form.first_instance.autoload_enabled" data-testid="autoload-enabled" label="Autoload on request" />
            <UCheckbox v-model="form.first_instance.eviction_enabled" data-testid="eviction-enabled" label="Allow resource-pressure eviction" />
          </div>
          <UCheckbox v-model="form.first_instance.start" data-testid="start-instance" label="Launch this Instance after creation" />
          <p class="text-xs text-muted">Advanced Instance settings such as priority, GPU placement, tensor split, idle timeout and instance-level llama.cpp overrides are configured from Instances.</p>
        </div>

        <div class="flex flex-wrap justify-end gap-2 pt-2">
          <UButton type="button" color="neutral" variant="soft" :loading="scanning" @click="scanGGUFs">Rescan</UButton>
          <UButton to="/models" color="neutral" variant="soft">Cancel</UButton>
          <UButton type="submit" :loading="busy" :disabled="scanning || !form.gguf_path || (createFirstInstance && (!form.first_instance.name || !form.first_instance.slug))">Create model</UButton>
        </div>
      </UForm>
    </UCard>
  </div>
</template>
