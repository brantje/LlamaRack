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
  <div>
    <header class="page-header">
      <div>
        <p class="eyebrow">MODEL REGISTRY</p>
        <h1>Add model</h1>
        <p class="muted">Select an unregistered GGUF file and configure its model in one step.</p>
      </div>
      <NuxtLink to="/models" class="ghost">Back to models</NuxtLink>
    </header>

    <section v-if="canOperate" class="panel" style="max-width: 720px">
      <p v-if="error" class="alert error">{{ error }}</p>
      <form @submit.prevent="createModel">
        <label>
          GGUF file
          <select v-model="form.gguf_path" required :disabled="scanning || !availableGGUFs.length">
            <option value="" disabled>{{ scanning ? 'Scanning model folder…' : availableGGUFs.length ? 'Select GGUF' : 'No unregistered GGUF files found' }}</option>
            <option v-for="file in availableGGUFs" :key="file.path" :value="file.path">
              {{ file.path }}{{ file.quantization ? ` · ${file.quantization}` : '' }}
            </option>
          </select>
          <small class="muted">The model folder is scanned recursively. Already-added GGUF files are hidden.</small>
        </label>

        <label>
          Model name
          <input v-model="form.name" placeholder="Qwen Coder" required>
        </label>

        <label>
          Public model ID
          <input v-model="form.model_id" placeholder="qwen-coder" required @input="markPublicIdEdited">
          <small class="muted">Auto-filled from the model name. You can override it.</small>
        </label>

        <div class="field-row">
          <label>
            Priority
            <select v-model="form.priority">
              <option value="low">Low</option>
              <option value="normal">Normal</option>
              <option value="high">High</option>
            </select>
          </label>
          <label>
            Routing
            <select v-model="form.routing_policy">
              <option value="least_active">Least active</option>
              <option value="round_robin">Round robin</option>
            </select>
          </label>
        </div>

        <label class="check"><input v-model="form.autoload_enabled" type="checkbox"> Autoload on request</label>
        <label class="check"><input v-model="form.always_on" type="checkbox"> Always on</label>

        <div class="row-actions">
          <button type="button" class="ghost" :disabled="scanning" @click="scanGGUFs">{{ scanning ? 'Scanning…' : 'Rescan' }}</button>
          <NuxtLink to="/models" class="ghost">Cancel</NuxtLink>
          <button class="primary" :disabled="busy || scanning || !form.gguf_path">{{ busy ? 'Creating…' : 'Create model' }}</button>
        </div>
      </form>
    </section>

    <section v-else class="panel">
      <p class="muted">Your role cannot create models.</p>
    </section>
  </div>
</template>
