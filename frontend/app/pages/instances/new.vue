<script setup lang="ts">
const manager = useManager()
const router = useRouter()
const busy = ref(false)
const error = ref('')
const launchAfterCreate = ref(false)
const confirmation = ref<{ request: (options: Record<string, string>) => Promise<boolean> } | null>(null)
const form = reactive({
  model_id: '', name: '', slug: '', enabled: true, always_on: false, autoload_enabled: true,
  priority: 'normal', eviction_enabled: true, idle_unload_seconds: 0, max_pending_requests: 0,
  gpu_mode: 'auto', gpu_devices: [] as string[], tensor_split: '', request_log_mode: 'metadata', options: {} as Record<string, string>
})

async function submit() {
  if (!form.model_id || !form.name.trim() || !form.slug.trim()) return
  busy.value = true
  error.value = ''
  try {
    const instance = await manager.request<{ id: string; slug: string }>('/api/v1/instances', {
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
        confirmTone: 'default'
      })
      if (!confirmed) {
        await manager.refresh()
        await router.push('/instances')
        return
      }
      await manager.request(`/api/v1/instances/${encodeURIComponent(instance.slug)}/start`, { method: 'POST' })
    }
    await manager.refresh()
    await router.push(`/instances/${encodeURIComponent(instance.slug)}/detail`)
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to create Instance'
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <InstanceForm
    :form="form"
    title="New Instance"
    submit-label="Create Instance"
    :busy="busy"
    :error="error"
    show-launch-after-create
    v-model:launch-after-create="launchAfterCreate"
    @submit="submit"
  />
  <AppConfirmationModal ref="confirmation" />
</template>
