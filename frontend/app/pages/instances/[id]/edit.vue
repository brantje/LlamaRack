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
  model_id: '', name: '', slug: '', enabled: true, always_on: false, autoload_enabled: true,
  priority: 'normal', eviction_enabled: true, idle_unload_seconds: 0,
  gpu_mode: 'auto', gpu_devices: [] as string[], tensor_split: '', request_log_mode: 'metadata', options: {} as Record<string, string>
})

function slugify(value: string) {
  return value.toLowerCase().trim().replace(/[^\p{L}\p{N}]+/gu, '-').replace(/^-+|-+$/g, '')
}

onMounted(async () => {
  try {
    const [instance, options] = await Promise.all([
      manager.request<any>(`/api/v1/instances/${encodeURIComponent(originalID.value)}`),
      manager.request<Record<string, string>>(`/api/v1/instances/${encodeURIComponent(originalID.value)}/options`)
    ])
    Object.assign(form, instance, {
      slug: instance.id || originalID.value,
      gpu_devices: [...(instance.gpu_devices || [])],
      request_log_mode: instance.request_log_mode || 'metadata',
      options: { ...(options || {}) }
    })
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to load Instance'
  } finally {
    loading.value = false
  }
})

async function submit() {
  if (!form.model_id || !form.name.trim() || !form.slug.trim()) return
  error.value = ''
  const nextID = slugify(form.slug || form.name)
  const rename = nextID !== originalID.value
  if (rename) {
    const confirmed = await confirmation.value?.request({
      title: 'Confirm Instance rename',
      description: `Renaming this Instance changes the OpenAI model ID from “${originalID.value}” to “${nextID}”. Existing clients using the old model ID will break.`,
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
        model_id: form.model_id, name: form.name, slug: form.slug, enabled: form.enabled,
        always_on: form.always_on, autoload_enabled: form.autoload_enabled,
        priority: form.priority, eviction_enabled: form.eviction_enabled,
        idle_unload_seconds: form.idle_unload_seconds, gpu_mode: form.gpu_mode,
        gpu_devices: form.gpu_mode === 'manual' ? form.gpu_devices : [],
        tensor_split: form.gpu_mode === 'manual' ? form.tensor_split.trim() : '',
        request_log_mode: form.request_log_mode, options: form.options,
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
  <InstanceForm
    :form="form"
    title="Edit Instance"
    submit-label="Save Instance"
    :busy="busy"
    :error="error"
    :loading="loading"
    @submit="submit"
  />
  <AppConfirmationModal ref="confirmation" />
</template>
