<script setup lang="ts">
const manager = useManager()
const route = useRoute()
const router = useRouter()
const id = computed(() => String(route.params.id || ''))
const busy = ref(false)
const loading = ref(true)
const error = ref('')
const form = reactive({ name: '', context_length: 0, options: {} as Record<string, string> })

onMounted(async () => {
  try {
    const [model, options] = await Promise.all([
      manager.request<any>(`/api/v1/models/${encodeURIComponent(id.value)}`),
      manager.request<Record<string, string>>(`/api/v1/models/${encodeURIComponent(id.value)}/options`)
    ])
    form.name = model.name
    form.context_length = model.context_length || 0
    form.options = { ...(options || {}) }
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to load Model'
  } finally {
    loading.value = false
  }
})

async function submit() {
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
        <div class="mb-1 text-[10px] font-medium uppercase tracking-[.1em] text-[var(--neutral-700)]">MODEL REGISTRY</div>
        <h1 class="font-heading text-[30px] font-semibold leading-none tracking-[-.015em] text-[var(--color-text)]">Edit model</h1>
        <p class="mt-2 max-w-3xl text-[15px] leading-[1.55] text-[var(--neutral-800)]">
          Edit reusable Model metadata and llama.cpp defaults. Instance lifecycle and overrides are configured separately.
        </p>
      </div>
      <AppButton to="/models" intent="secondary">Back to Models</AppButton>
    </div>

    <Frame v-if="error" class="border-[var(--accent-800)] p-4 text-sm text-[var(--accent-900)]">
      {{ error }}
    </Frame>

    <div v-if="loading" class="space-y-4" data-testid="model-edit-loading">
      <USkeleton class="h-44 w-full" />
      <USkeleton class="h-64 w-full" />
    </div>

    <UForm v-else :state="form" class="space-y-5" @submit="submit">
      <Frame class="p-5" data-testid="model-edit-metadata">
        <div class="mb-5">
          <div class="text-[10px] font-medium uppercase tracking-[.1em] text-[var(--neutral-700)]">MODEL METADATA</div>
          <h2 class="mt-1 font-heading text-[25px] font-semibold tracking-[-.015em] text-[var(--color-text)]">Model metadata</h2>
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

      <Frame class="p-5" data-testid="model-edit-defaults">
        <div class="mb-5">
          <div class="text-[10px] font-medium uppercase tracking-[.1em] text-[var(--neutral-700)]">LLAMA.CPP DEFAULTS</div>
          <h2 class="mt-1 font-heading text-[25px] font-semibold tracking-[-.015em] text-[var(--color-text)]">Model llama.cpp defaults</h2>
          <p class="mt-1 text-sm text-[var(--neutral-800)]">
            Reusable defaults inherited by every Instance of this Model unless that Instance overrides the flag.
          </p>
        </div>
        <LlamaCppOptionsEditor v-model="form.options" scope="model" :model-id="id" />
      </Frame>

      <div class="flex justify-end gap-2 border-t border-[var(--color-divider)] pt-4">
        <AppButton to="/models" intent="secondary">Cancel</AppButton>
        <AppButton type="submit" intent="primary" :loading="busy">Save Model</AppButton>
      </div>
    </UForm>
  </div>
</template>
