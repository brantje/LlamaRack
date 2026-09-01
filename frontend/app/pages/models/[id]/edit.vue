<script setup lang="ts">
import { companionOptionKeys, type ModelInspection } from '~/utils/modelCompanions'

const manager = useManager()
const route = useRoute()
const router = useRouter()
const id = computed(() => String(route.params.id || ''))
const busy = ref(false)
const loading = ref(true)
const loaded = ref(false)
const inspecting = ref(false)
const error = ref('')
const form = reactive({ name: '', context_length: 0, options: {} as Record<string, string> })
const inspection = ref<ModelInspection | null>(null)
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
const overrides = computed(() => form.options['llama.cpp'] || {})
const overrideCount = computed(() =>
  Object.keys(form.options).filter(key => !companionOptionKeys.includes(key)).length
)

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
  <div class="space-y-6">
    <div class="flex flex-wrap items-start justify-between gap-5">
      <div class="min-w-0 flex-1">
        <div class="mb-1 text-[length:var(--font-size-kicker)] font-medium uppercase tracking-[.1em] text-[var(--neutral-700)]">MODEL REGISTRY</div>
        <h1 class="font-heading text-[length:var(--font-size-screen-title)] font-semibold leading-none tracking-[-.015em] text-[var(--color-text)]">Edit model</h1>
        <p class="mt-2 max-w-3xl text-[length:var(--font-size-body)] leading-[1.55] text-[var(--neutral-800)]">
          Edit reusable Model metadata and llama.cpp defaults. Instance lifecycle and overrides are configured separately.
        </p>
      </div>
      <AppButton to="/models" intent="secondary">Back to Models</AppButton>
    </div>

    <Frame v-if="error" class="p-3" data-testid="model-edit-error">
      <div class="flex flex-wrap items-start gap-2">
        <StatusTag variant="failed">Model update failed</StatusTag>
        <p class="min-w-0 flex-1 text-xs leading-5 text-[var(--neutral-800)]">{{ error }}</p>
      </div>
    </Frame>

    <div v-if="loading" class="space-y-4" data-testid="model-edit-loading">
      <USkeleton class="h-44 w-full" />
      <USkeleton class="h-64 w-full" />
    </div>

    <UForm v-else-if="loaded" :state="form" class="space-y-5" @submit="submit">
      <Frame class="p-5" data-testid="model-edit-metadata">
        <div class="mb-5">
          <div class="text-[length:var(--font-size-kicker)] font-medium uppercase tracking-[.1em] text-[var(--neutral-700)]">MODEL METADATA</div>
          <h2 class="mt-1 font-heading text-[length:var(--font-size-h3)] font-semibold tracking-[-.015em] text-[var(--color-text)]">Model metadata</h2>
        </div>
        <div class="grid gap-4 md:grid-cols-2">
          <UFormField label="Model name" name="name" required>
            <UInput v-model="form.name" class="w-full" required />
          </UFormField>
          <UFormField
            label="Context capability"
            name="context_length"
            description="Maximum context supported by this registered artifact/configuration. Use 0 when unknown."
          >
            <UInputNumber v-model="form.context_length" class="w-full font-mono tabular-nums" :min="0" />
          </UFormField>
        </div>
      </Frame>

      <ModelCompanionFiles
        v-model="form.options"
        :inspection="inspection"
        :inspecting="inspecting"
        testid="model-edit-companions"
        description="Detected from this Model's GGUF. Disable to clear the extra flags; Enable restores inspect defaults."
      />

      <Frame class="p-5" data-testid="model-edit-defaults" collapsible subtitle="LLAMA.CPP" title="Model llama.cpp defaults" :description="`Reusable across every Instance of this Model. ${overrideCount} overrides configured, click to expand.`">
        <div class="mb-5">
          <div class="text-[length:var(--font-size-kicker)] font-medium uppercase tracking-[.1em] text-[var(--neutral-700)]">LLAMA.CPP DEFAULTS</div>
          <h2 class="mt-1 font-heading text-[length:var(--font-size-h3)] font-semibold tracking-[-.015em] text-[var(--color-text)]">Model llama.cpp defaults</h2>
          <p class="mt-1 text-sm text-[var(--neutral-800)]">
            Reusable defaults inherited by every Instance of this Model unless that Instance overrides the flag.
          </p>
        </div>
        <LlamaCppOptionsEditor v-model="form.options" scope="model" :model-id="id" :exclude-keys="companionOptionKeys" />
      </Frame>

      <div class="flex flex-wrap items-center gap-2 border-t border-[var(--color-divider)] pt-4">
        <p class="mr-auto text-xs text-[var(--neutral-700)]" data-testid="model-edit-submit-hint">
          {{ !valid ? 'Required: Model name.' : dirty ? 'Unsaved changes.' : 'No changes to save.' }}
        </p>
        <AppButton to="/models" intent="secondary">Cancel</AppButton>
        <AppButton type="submit" intent="primary" :loading="busy" :disabled="!canSubmit">Save Model</AppButton>
      </div>
    </UForm>
  </div>
</template>
