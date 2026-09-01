<script setup lang="ts">
import { type ModelInspection } from '~/utils/modelCompanions'

const manager = useManager()
const route = useRoute()
const router = useRouter()
const id = computed(() => String(route.params.id || ''))
const busy = ref(false)
const loading = ref(true)
const loaded = ref(false)
const inspecting = ref(false)
const error = ref('')
const inspection = ref<ModelInspection | null>(null)
const form = reactive({ name: '', context_length: 0, options: {} as Record<string, string> })
const baselineFingerprint = ref('')

function formFingerprint() {
  return JSON.stringify({
    name: form.name,
    context_length: form.context_length,
    options: Object.entries(form.options).sort(([left], [right]) => left.localeCompare(right))
  })
}

const valid = computed(() => Boolean(form.name.trim()))
const dirty = computed(() => !loading.value && Boolean(baselineFingerprint.value) && formFingerprint() !== baselineFingerprint.value)
const canSubmit = computed(() => valid.value && dirty.value)

onMounted(async () => {
  try {
    const [model, options] = await Promise.all([
      manager.request<{ name?: string; context_length?: number; gguf_path?: string }>(`/api/v1/models/${encodeURIComponent(id.value)}`),
      manager.request<Record<string, string>>(`/api/v1/models/${encodeURIComponent(id.value)}/options`)
    ])
    if (!model?.name) throw { data: { error: 'Unable to load Model' } }
    form.name = model.name
    form.context_length = model.context_length || 0
    form.options = { ...(options || {}) }
    loaded.value = true
    baselineFingerprint.value = formFingerprint()
    loading.value = false
    if (model.gguf_path) {
      inspecting.value = true
      try {
        inspection.value = await manager.request<ModelInspection>('/api/v1/models/inspect', {
          method: 'POST',
          body: { gguf_path: model.gguf_path }
        })
      } catch {
        inspection.value = null
      } finally {
        inspecting.value = false
      }
    }
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to load Model'
  } finally {
    loading.value = false
  }
})

async function submit() {
  if (!canSubmit.value) return
  busy.value = true
  error.value = ''
  try {
    await manager.request(`/api/v1/models/${encodeURIComponent(id.value)}`, {
      method: 'PUT', body: { name: form.name, context_length: form.context_length, options: form.options }
    })
    await manager.refresh()
    await router.push('/models')
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to update Model'
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div v-if="loading" class="space-y-4" data-testid="model-edit-loading">
    <USkeleton class="h-44 w-full" />
    <USkeleton class="h-64 w-full" />
  </div>
  <div v-else-if="!loaded" class="space-y-5">
    <div class="flex flex-wrap items-start justify-between gap-4">
      <UPageHeader
        class="min-w-0 flex-1"
        headline="MODEL REGISTRY"
        title="Edit model"
        description="Edit reusable Model metadata and llama.cpp defaults. Instance lifecycle and overrides are configured separately."
      />
      <AppButton to="/models" intent="secondary">Back to Models</AppButton>
    </div>
    <Frame class="p-3" data-testid="model-edit-error">
      <div class="flex flex-wrap items-start gap-2">
        <StatusTag variant="failed">Unable to load Model</StatusTag>
        <p class="min-w-0 flex-1 text-xs leading-5 text-[var(--neutral-800)]">{{ error }}</p>
      </div>
    </Frame>
  </div>
  <ModelForm
    v-else
    :form="form"
    mode="edit"
    title="Edit model"
    description="Edit reusable Model metadata and llama.cpp defaults. Instance lifecycle and overrides are configured separately."
    submit-label="Save Model"
    :busy="busy"
    :error="error"
    :submit-disabled="!dirty"
    :dirty="dirty"
    :model-id="id"
    :inspection="inspection"
    :inspecting="inspecting"
    back-to="/models"
    back-label="Back to Models"
    @submit="submit"
  />
</template>
