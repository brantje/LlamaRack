<script setup lang="ts">
const manager = useManager()
const router = useRouter()
const { canOperate } = manager
const busy = ref(false)
const error = ref('')
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

watch(() => form.name, (name) => {
  if (!publicIdEdited.value) form.model_id = slugifyModelID(name)
})

function markPublicIdEdited() {
  publicIdEdited.value = true
}

async function createModel() {
  busy.value = true
  error.value = ''
  try {
    await manager.request('/api/v1/models', { method: 'POST', body: form })
    await manager.refresh()
    await router.push('/models')
  } catch (e: any) {
    error.value = e?.data?.error || e?.message || 'Unable to create model'
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
        <p class="muted">Register a local GGUF file and configure its model in one step.</p>
      </div>
      <NuxtLink to="/models" class="ghost">Back to models</NuxtLink>
    </header>

    <section v-if="canOperate" class="panel" style="max-width: 720px">
      <p v-if="error" class="alert error">{{ error }}</p>
      <form @submit.prevent="createModel">
        <label>
          GGUF path
          <input v-model="form.gguf_path" placeholder="/models/qwen.gguf" required>
          <small class="muted">The file must already exist inside the backend models directory.</small>
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
          <NuxtLink to="/models" class="ghost">Cancel</NuxtLink>
          <button class="primary" :disabled="busy">{{ busy ? 'Creating…' : 'Create model' }}</button>
        </div>
      </form>
    </section>

    <section v-else class="panel">
      <p class="muted">Your role cannot create models.</p>
    </section>
  </div>
</template>
