<script setup lang="ts">
type AvailableGGUF = { path: string; name: string; total_bytes: number; quantization?: string }

type CreateResponse = { model: { id: string }; instance?: { id: string }; start_error?: string }

const manager = useManager()
const router = useRouter()
const busy = ref(false)
const scanning = ref(false)
const error = ref('')
const availableGGUFs = ref<AvailableGGUF[]>([])
const createFirstInstance = ref(true)
const form = reactive({
  gguf_path: '',
  name: '',
  context_length: 0,
  first_instance: {
    name: '',
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

watch(() => form.name, (name) => {
  if (!form.first_instance.name) form.first_instance.name = name
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
      <UPageHeader
        class="min-w-0 flex-1"
        headline="MODEL REGISTRY"
        title="Add model"
        description="Register a GGUF model and optionally bootstrap its first addressable Instance."
      />
      <UButton to="/models" color="neutral" variant="soft">Back to models</UButton>
    </div>

    <UCard class="max-w-3xl">
      <UAlert v-if="error" class="mb-5" color="error" variant="subtle" :description="error" />
      <UForm :state="form" class="space-y-6" @submit="createModel">
        <UFormField label="GGUF file" name="gguf_path" description="Already-registered GGUF files are hidden." required>
          <USelect v-model="form.gguf_path" data-testid="gguf-select" class="w-full" :items="ggufItems" label-key="label" value-key="value" :placeholder="ggufPlaceholder" :disabled="scanning || !availableGGUFs.length" required />
        </UFormField>

        <UFormField label="Model name" name="name" required>
          <UInput v-model="form.name" data-testid="model-name" class="w-full" placeholder="Qwen Coder 32B" required />
        </UFormField>

        <UFormField label="Context capability" name="context_length" description="Maximum context supported by the artifact/configuration. Use 0 when unknown.">
          <UInputNumber v-model="form.context_length" class="w-full" :min="0" :step="1" />
        </UFormField>

        <USeparator label="First Instance" />
        <UCheckbox v-model="createFirstInstance" data-testid="create-first-instance" label="Create a first Instance" />

        <div v-if="createFirstInstance" class="space-y-4 rounded-lg border border-default p-4">
          <UFormField label="Instance name" name="first_instance.name" description="The name is slugified into the OpenAI model ID." required>
            <UInput v-model="form.first_instance.name" data-testid="instance-name" class="w-full" placeholder="Qwen Coding 32B" required />
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
          <UButton type="submit" :loading="busy" :disabled="scanning || !form.gguf_path || (createFirstInstance && !form.first_instance.name)">Create model</UButton>
        </div>
      </UForm>
    </UCard>
  </div>
</template>
