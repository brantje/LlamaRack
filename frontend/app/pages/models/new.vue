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
  eviction_enabled: true,
  idle_unload_seconds: 0,
  routing_policy: 'least_active',
  autoload_enabled: true,
  always_on: false
})

const ggufItems = computed(() => availableGGUFs.value.map(file => ({
  label: `${file.path}${file.quantization ? ` · ${file.quantization}` : ''}`,
  value: file.path
})))
const routingItems = [
  { label: 'Least active', value: 'least_active' },
  { label: 'Round robin', value: 'round_robin' }
]
const ggufPlaceholder = computed(() => scanning.value
  ? 'Scanning model folder…'
  : availableGGUFs.value.length
    ? 'Select GGUF'
    : 'No unregistered GGUF files found')

const priorityItems = [
  { label: 'Low', value: 'low' },
  { label: 'Normal', value: 'normal' },
  { label: 'High', value: 'high' }
]

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
  <div class="space-y-5">
    <div class="flex items-start justify-between gap-6">
      <UPageHeader
        class="min-w-0 flex-1"
        headline="MODEL REGISTRY"
        title="Add model"
        description="Select an unregistered GGUF file and configure its model in one step."
      />
      <UButton to="/models" color="neutral" variant="soft">Back to models</UButton>
    </div>

    <UCard v-if="canOperate" class="max-w-3xl">
      <UAlert v-if="error" class="mb-5" color="error" variant="subtle" :description="error" />

      <UForm :state="form" class="space-y-6" @submit="createModel">
        <UFormField label="GGUF file" name="gguf_path" description="The model folder is scanned recursively. Already-added GGUF files are hidden." required>
          <USelect
            v-model="form.gguf_path"
            data-testid="gguf-select"
            class="w-full"
            :items="ggufItems"
            label-key="label"
            value-key="value"
            :placeholder="ggufPlaceholder"
            :disabled="scanning || !availableGGUFs.length"
            required
          />
        </UFormField>

        <UFormField label="Model name" name="name" required>
          <UInput v-model="form.name" data-testid="model-name" class="w-full" placeholder="Qwen Coder" required />
        </UFormField>

        <UFormField label="Public model ID" name="model_id" description="Auto-filled from the model name. You can override it." required>
          <UInput
            v-model="form.model_id"
            data-testid="model-id"
            class="w-full"
            placeholder="qwen-coder"
            required
            @input="markPublicIdEdited"
          />
        </UFormField>

        <UFormField label="Routing" name="routing_policy">
          <USelect v-model="form.routing_policy" data-testid="routing-select" class="w-full" :items="routingItems" label-key="label" value-key="value" />
        </UFormField>

        <USeparator label="Eviction" />
        <p class="-mt-3 text-sm leading-6 text-muted">Controls idle unloading now and resource-pressure eviction when Phase 7 hardware scheduling is enabled.</p>

        <div class="grid gap-4 md:grid-cols-2">
          <UFormField label="Priority" name="priority" description="Lower-priority models are preferred eviction candidates.">
            <USelect v-model="form.priority" data-testid="priority-select" class="w-full" :items="priorityItems" label-key="label" value-key="value" />
          </UFormField>
          <UFormField label="Idle unload timeout (seconds)" name="idle_unload_seconds" description="0 inherits the global LCM_IDLE_UNLOAD_SECONDS setting.">
            <UInputNumber
              v-model="form.idle_unload_seconds"
              data-testid="idle-timeout"
              class="w-full"
              :min="0"
              :step="1"
            />
          </UFormField>
        </div>

        <div class="space-y-3">
          <UCheckbox v-model="form.eviction_enabled" data-testid="eviction-enabled" label="Allow resource-pressure eviction" />
          <p class="pl-6 text-xs text-muted">Always-On models remain protected from normal eviction even when this is enabled.</p>
        </div>

        <USeparator label="Lifecycle" />
        <div class="space-y-3">
          <UCheckbox v-model="form.autoload_enabled" data-testid="autoload-enabled" label="Autoload on request" />
          <UCheckbox v-model="form.always_on" data-testid="always-on" label="Always on" />
        </div>

        <div class="flex flex-wrap justify-end gap-2 pt-2">
          <UButton type="button" color="neutral" variant="soft" :loading="scanning" @click="scanGGUFs">Rescan</UButton>
          <UButton to="/models" color="neutral" variant="soft">Cancel</UButton>
          <UButton type="submit" :loading="busy" :disabled="scanning || !form.gguf_path">Create model</UButton>
        </div>
      </UForm>
    </UCard>

    <UAlert v-else color="neutral" variant="subtle" description="Your role cannot create models." />
  </div>
</template>
