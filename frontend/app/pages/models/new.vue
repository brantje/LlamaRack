<script setup lang="ts">
type AvailableGGUF = {
  path: string
  name: string
  total_bytes: number
  quantization?: string
}

const manager = useManager()
const router = useRouter()
const { canOperate } = manager
const busy = ref(false)
const scanning = ref(false)
const error = ref('')
const availableGGUFs = ref<AvailableGGUF[]>([])
const publicIdEdited = ref(false)
const form = reactive({
  gguf_path: '',
  name: '',
  model_id: '',
  priority: 'normal',
  routing_policy: 'least_active',
  autoload_enabled: true,
  always_on: false
})

const ggufItems = computed(() => availableGGUFs.value.map(file => ({
  label: `${file.path}${file.quantization ? ` · ${file.quantization}` : ''}`,
  value: file.path
})))
const priorityItems = [
  { label: 'Low', value: 'low' },
  { label: 'Normal', value: 'normal' },
  { label: 'High', value: 'high' }
]
const routingItems = [
  { label: 'Least active', value: 'least_active' },
  { label: 'Round robin', value: 'round_robin' }
]
const ggufPlaceholder = computed(() => scanning.value
  ? 'Scanning model folder…'
  : availableGGUFs.value.length
    ? 'Select GGUF'
    : 'No unregistered GGUF files found')

function slugifyModelID(value: string) {
  return value
    .normalize('NFKD')
    .replace(/[\u0300-\u036f]/g, '')
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
}

function messageFor(errorValue: any, fallback: string) {
  return errorValue?.data?.error || errorValue?.message || fallback
}

watch(() => form.name, (name) => {
  if (!publicIdEdited.value) form.model_id = slugifyModelID(name)
})

function markPublicIdEdited() {
  publicIdEdited.value = true
}

async function scanGGUFs() {
  if (!canOperate.value) return
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

onMounted(scanGGUFs)

async function createModel() {
  busy.value = true
  error.value = ''
  try {
    await manager.request('/api/v1/models', { method: 'POST', body: form })
    await manager.refresh()
    await router.push('/models')
  } catch (e: any) {
    error.value = messageFor(e, 'Unable to create model')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="grid gap-6">
    <UPageHeader headline="MODEL REGISTRY" title="Add model" description="Select an unregistered GGUF file and configure its model in one step.">
      <template #links>
        <UButton label="Back to models" to="/models" color="neutral" variant="outline" />
      </template>
    </UPageHeader>

    <UCard v-if="canOperate" class="max-w-3xl">
      <UAlert v-if="error" class="mb-5" color="error" variant="subtle" :description="error" />

      <UForm :state="form" class="grid gap-5" @submit="createModel">
        <UFormField
          label="GGUF file"
          name="gguf_path"
          description="The model folder is scanned recursively. Already-added GGUF files are hidden."
          required
        >
          <USelect
            v-model="form.gguf_path"
            :items="ggufItems"
            label-key="label"
            value-key="value"
            :placeholder="ggufPlaceholder"
            :disabled="scanning || !availableGGUFs.length"
            required
            class="w-full"
            data-testid="gguf-select"
          />
        </UFormField>

        <UFormField label="Model name" name="name" required>
          <UInput v-model="form.name" placeholder="Qwen Coder" required class="w-full" data-testid="model-name" />
        </UFormField>

        <UFormField
          label="Public model ID"
          name="model_id"
          description="Auto-filled from the model name. You can override it."
          required
        >
          <UInput
            v-model="form.model_id"
            placeholder="qwen-coder"
            required
            class="w-full"
            data-testid="model-id"
            @input="markPublicIdEdited"
          />
        </UFormField>

        <div class="grid gap-4 sm:grid-cols-2">
          <UFormField label="Priority" name="priority">
            <USelect
              v-model="form.priority"
              :items="priorityItems"
              label-key="label"
              value-key="value"
              class="w-full"
              data-testid="priority-select"
            />
          </UFormField>
          <UFormField label="Routing" name="routing_policy">
            <USelect
              v-model="form.routing_policy"
              :items="routingItems"
              label-key="label"
              value-key="value"
              class="w-full"
              data-testid="routing-select"
            />
          </UFormField>
        </div>

        <div class="grid gap-3 sm:grid-cols-2">
          <UCheckbox v-model="form.autoload_enabled" label="Autoload on request" data-testid="autoload-checkbox" />
          <UCheckbox v-model="form.always_on" label="Always on" data-testid="always-on-checkbox" />
        </div>

        <div class="flex flex-wrap justify-end gap-2 pt-2">
          <UButton
            type="button"
            :label="scanning ? 'Scanning…' : 'Rescan'"
            color="neutral"
            variant="outline"
            :loading="scanning"
            @click="scanGGUFs"
          />
          <UButton label="Cancel" to="/models" color="neutral" variant="ghost" />
          <UButton
            type="submit"
            :label="busy ? 'Creating…' : 'Create model'"
            :loading="busy"
            :disabled="busy || scanning || !form.gguf_path"
          />
        </div>
      </UForm>
    </UCard>

    <UAlert v-else color="warning" variant="subtle" description="Your role cannot create models." />
  </div>
</template>
