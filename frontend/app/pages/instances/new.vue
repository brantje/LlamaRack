<script setup lang="ts">
const manager = useManager()
const router = useRouter()
const busy = ref(false)
const error = ref('')
const launchAfterCreate = ref(false)
const slugEdited = ref(false)
const confirmation = ref<{ request: (options: Record<string, string>) => Promise<boolean> } | null>(null)
const form = reactive({
  model_id: '', name: '', slug: '', enabled: true, always_on: false, autoload_enabled: true,
  priority: 'normal', eviction_enabled: true, idle_unload_seconds: 0,
  gpu_mode: 'auto', gpu_devices: [] as string[], tensor_split: '', options: {} as Record<string, string>
})
const modelItems = computed(() => manager.models.value.map(model => ({ label: model.name, value: model.id })))
const priorityItems = ['low', 'normal', 'high'].map(value => ({ label: value[0]!.toUpperCase() + value.slice(1), value }))

function slugify(value: string) {
  return value.toLowerCase().trim().replace(/[^\p{L}\p{N}]+/gu, '-').replace(/^-+|-+$/g, '')
}

watch(() => form.model_id, (modelID) => {
  const model = manager.models.value.find(item => item.id === modelID)
  if (!model) return
  slugEdited.value = false
  form.name = model.name
  form.slug = slugify(model.name)
})
watch(() => form.name, (name) => {
  if (!slugEdited.value) form.slug = slugify(name)
})

async function submit() {
  busy.value = true
  error.value = ''
  try {
    const instance = await manager.request<{ id: string }>('/api/v1/instances', {
      method: 'POST',
      body: {
        ...form,
        gpu_devices: form.gpu_mode === 'manual' ? form.gpu_devices : [],
        tensor_split: form.gpu_mode === 'manual' ? form.tensor_split.trim() : '',
        options: form.options
      }
    })
    if (launchAfterCreate.value) {
      const confirmed = await confirmation.value?.request({
        title: 'Launch Instance',
        description: 'Launching this Instance may stop other eligible idle Instances if fresh RAM/VRAM state shows that resource-pressure eviction is required.',
        confirmLabel: 'Launch Instance',
        cancelLabel: 'Keep stopped',
        color: 'primary'
      })
      if (!confirmed) {
        await manager.refresh()
        await router.push('/instances')
        return
      }
      await manager.request(`/api/v1/instances/${encodeURIComponent(instance.id)}/start`, { method: 'POST' })
    }
    await manager.refresh()
    await router.push('/instances')
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to create Instance'
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="space-y-5">
    <div class="flex items-start justify-between gap-6"><UPageHeader class="min-w-0 flex-1" headline="CONTROL PLANE" title="New Instance" description="Configure one durable llama-server process. The slug is the exact OpenAI model ID and defaults from the Instance name." /><UButton to="/instances" color="neutral" variant="soft">Back to Instances</UButton></div>
    <UCard class="max-w-5xl">
      <UAlert v-if="error" class="mb-5" color="error" variant="subtle" :description="error" />
      <UForm :state="form" class="space-y-6" @submit="submit">
        <div class="grid gap-4 md:grid-cols-2">
          <UFormField label="Registered Model" name="model_id" required><USelectMenu v-model="form.model_id" class="w-full" :items="modelItems" label-key="label" value-key="value" required /></UFormField>
          <UFormField label="Instance name" name="name" required><UInput v-model="form.name" data-testid="instance-name" class="w-full" required /></UFormField>
          <UFormField label="Instance slug" name="slug" description="Exact OpenAI model ID. Defaults from the name but can be customized." required><UInput v-model="form.slug" data-testid="instance-slug" class="w-full font-mono" required @update:model-value="slugEdited = true" /></UFormField>
        </div>
        <USeparator label="Lifecycle & scheduling" />
        <div class="grid gap-4 md:grid-cols-2"><UFormField label="Priority" name="priority"><USelectMenu v-model="form.priority" class="w-full" :items="priorityItems" label-key="label" value-key="value" /></UFormField><UFormField label="Idle unload timeout (seconds)" name="idle_unload_seconds"><UInputNumber v-model="form.idle_unload_seconds" class="w-full" :min="0" /></UFormField></div>
        <div class="space-y-3"><UCheckbox v-model="form.enabled" label="Enabled" /><UCheckbox v-model="form.always_on" label="Always On" description="Keep this Instance running whenever resources permit." /><UCheckbox v-model="form.autoload_enabled" label="Autoload on request" /><UCheckbox v-model="form.eviction_enabled" label="Allow resource-pressure eviction" description="Allow the manager to stop this Instance when RAM/VRAM is needed for another Instance." /></div>

        <USeparator label="Placement" />
        <HardwarePlacementEditor v-model:gpu-mode="form.gpu_mode" v-model:gpu-devices="form.gpu_devices" v-model:tensor-split="form.tensor_split" />

        <LlamaCppOptionsEditor v-model="form.options" scope="instance" :model-id="form.model_id" :default-open="false" />

        <UCheckbox v-model="launchAfterCreate" label="Launch after creation" />
        <div class="flex justify-end gap-2"><UButton to="/instances" color="neutral" variant="soft">Cancel</UButton><UButton type="submit" :loading="busy" :disabled="!form.model_id || !form.name || !form.slug">Create Instance</UButton></div>
      </UForm>
    </UCard>
    <AppConfirmationModal ref="confirmation" />
  </div>
</template>
