<script setup lang="ts">
const manager = useManager()
const route = useRoute()
const router = useRouter()
const originalID = computed(() => String(route.params.id || ''))
const busy = ref(false)
const loading = ref(true)
const error = ref('')
const confirmation = ref<{ request: (options: Record<string, string>) => Promise<boolean> } | null>(null)
const form = reactive({
  model_id: '', name: '', enabled: true, always_on: false, autoload_enabled: true,
  priority: 'normal', eviction_enabled: true, idle_unload_seconds: 0,
  gpu_mode: 'auto', gpu_devices: [] as string[], tensor_split: '', options: {} as Record<string, string>
})
const modelItems = computed(() => manager.models.value.map(model => ({ label: model.name, value: model.id })))
const priorityItems = ['low', 'normal', 'high'].map(value => ({ label: value[0]!.toUpperCase() + value.slice(1), value }))

function slugify(value: string) {
  return value.toLowerCase().trim().replace(/[^\p{L}\p{N}._-]+/gu, '-').replace(/-+/g, '-').replace(/^[-._]+|[-._]+$/g, '')
}

onMounted(async () => {
  try {
    const [instance, options] = await Promise.all([
      manager.request<any>(`/api/v1/instances/${encodeURIComponent(originalID.value)}`),
      manager.request<Record<string, string>>(`/api/v1/instances/${encodeURIComponent(originalID.value)}/options`)
    ])
    Object.assign(form, instance, { gpu_devices: [...(instance.gpu_devices || [])], options: { ...(options || {}) } })
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to load Instance'
  } finally {
    loading.value = false
  }
})

async function submit() {
  error.value = ''
  const rename = slugify(form.name) !== originalID.value
  if (rename) {
    const confirmed = await confirmation.value?.request({
      title: 'Confirm Instance rename',
      description: `Renaming this Instance changes the OpenAI model ID from “${originalID.value}” to “${slugify(form.name)}”. Existing clients using the old model ID will break.`,
      confirmLabel: 'Continue',
      color: 'warning'
    })
    if (!confirmed) return
  }
  const runtime = manager.runtimeForInstance({ id: originalID.value, model_id: form.model_id } as any)
  const running = !['UNLOADED', 'FAILED'].includes(runtime.state)
  if (running) {
    const confirmed = await confirmation.value?.request({
      title: 'Restart running Instance',
      description: 'This Instance is running. Saving runtime-affecting configuration will drain, stop and restart it, causing temporary unavailability.',
      confirmLabel: 'Save & apply',
      color: 'warning'
    })
    if (!confirmed) return
  }

  busy.value = true
  try {
    await manager.request<{ id: string }>(`/api/v1/instances/${encodeURIComponent(originalID.value)}`, {
      method: 'PUT',
      body: {
        model_id: form.model_id, name: form.name, enabled: form.enabled,
        always_on: form.always_on, autoload_enabled: form.autoload_enabled,
        priority: form.priority, eviction_enabled: form.eviction_enabled,
        idle_unload_seconds: form.idle_unload_seconds, gpu_mode: form.gpu_mode,
        gpu_devices: form.gpu_mode === 'manual' ? form.gpu_devices : [],
        tensor_split: form.gpu_mode === 'manual' ? form.tensor_split.trim() : '', options: form.options,
        restart_running: running, confirm_model_id_change: rename
      }
    })
    await manager.refresh()
    await router.push('/instances')
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to update Instance'
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="space-y-5">
    <div class="flex items-start justify-between gap-6"><UPageHeader class="min-w-0 flex-1" headline="CONTROL PLANE" title="Edit Instance" :description="`Reconfigure ${originalID}. Changes to a running Instance are applied by automatic restart.`" /><UButton to="/instances" color="neutral" variant="soft">Back to Instances</UButton></div>
    <UCard class="max-w-5xl">
      <UAlert v-if="error" class="mb-5" color="error" variant="subtle" :description="error" />
      <div v-if="loading" class="space-y-3"><USkeleton class="h-10 w-full" /><USkeleton class="h-40 w-full" /></div>
      <UForm v-else :state="form" class="space-y-6" @submit="submit">
        <div class="grid gap-4 md:grid-cols-2"><UFormField label="Registered Model" name="model_id" required><USelectMenu v-model="form.model_id" class="w-full" :items="modelItems" label-key="label" value-key="value" required /></UFormField><UFormField label="Instance name" name="name" description="Changing this name also changes the OpenAI model ID." required><UInput v-model="form.name" class="w-full" required /></UFormField></div>
        <UAlert v-if="slugify(form.name) !== originalID" color="warning" variant="subtle" title="API-breaking rename" :description="`OpenAI model ID will change from ${originalID} to ${slugify(form.name) || '(invalid)'}.`" />
        <USeparator label="Lifecycle & scheduling" />
        <div class="grid gap-4 md:grid-cols-2"><UFormField label="Priority" name="priority"><USelectMenu v-model="form.priority" class="w-full" :items="priorityItems" label-key="label" value-key="value" /></UFormField><UFormField label="Idle unload timeout (seconds)" name="idle_unload_seconds"><UInputNumber v-model="form.idle_unload_seconds" class="w-full" :min="0" /></UFormField></div>
        <div class="space-y-3"><UCheckbox v-model="form.enabled" label="Enabled" /><UCheckbox v-model="form.always_on" label="Always On" description="Keep this Instance running whenever resources permit." /><UCheckbox v-model="form.autoload_enabled" label="Autoload on request" /><UCheckbox v-model="form.eviction_enabled" label="Allow resource-pressure eviction" description="Allow the manager to stop this Instance when RAM/VRAM is needed for another Instance." /></div>

        <USeparator label="Placement" />
        <HardwarePlacementEditor v-model:gpu-mode="form.gpu_mode" v-model:gpu-devices="form.gpu_devices" v-model:tensor-split="form.tensor_split" />

        <LlamaCppOptionsEditor v-model="form.options" scope="instance" :model-id="form.model_id" :instance-id="originalID" :default-open="false" />

        <div class="flex justify-end gap-2"><UButton to="/instances" color="neutral" variant="soft">Cancel</UButton><UButton type="submit" :loading="busy">Save & apply</UButton></div>
      </UForm>
    </UCard>
    <AppConfirmationModal ref="confirmation" />
  </div>
</template>
