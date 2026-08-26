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
  <div class="space-y-5">
    <div class="flex items-start justify-between gap-6">
      <UPageHeader class="min-w-0 flex-1" headline="MODEL REGISTRY" title="Edit Model" description="Edit reusable Model metadata and llama.cpp defaults. Instance lifecycle and overrides are configured separately." />
      <UButton to="/models" color="neutral" variant="soft">Back to Models</UButton>
    </div>
    <UCard class="max-w-4xl">
      <UAlert v-if="error" class="mb-5" color="error" variant="subtle" :description="error" />
      <div v-if="loading" class="space-y-3"><USkeleton class="h-10 w-full" /><USkeleton class="h-40 w-full" /></div>
      <UForm v-else :state="form" class="space-y-6" @submit="submit">
        <UFormField label="Model name" name="name" required><UInput v-model="form.name" class="w-full" required /></UFormField>
        <UFormField label="Context capability" name="context_length" description="Maximum context supported by this registered artifact/configuration. Use 0 when unknown."><UInputNumber v-model="form.context_length" class="w-full" :min="0" /></UFormField>
        <USeparator label="Model llama.cpp defaults" />
        <LlamaCppOptionsEditor v-model="form.options" scope="model" :model-id="id" />
        <div class="flex justify-end gap-2"><UButton to="/models" color="neutral" variant="soft">Cancel</UButton><UButton type="submit" :loading="busy">Save Model</UButton></div>
      </UForm>
    </UCard>
  </div>
</template>
