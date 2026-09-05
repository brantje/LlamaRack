<script setup lang="ts">
const manager = useManager()
const route = useRoute()
const router = useRouter()
const originalSlug = computed(() => String(route.params.id || ''))
const durableID = ref('')
const busy = ref(false)
const loading = ref(true)
const loaded = ref(false)
const error = ref('')
const baseline = ref('')
const confirmation = ref<{ request: (options: Record<string, string>) => Promise<boolean> } | null>(null)
const form = reactive({
  model_id: '', name: '', slug: '', enabled: true, always_on: false, autoload_enabled: true,
  priority: 'normal', eviction_enabled: true, idle_unload_seconds: 0, max_pending_requests: 0,
  gpu_mode: 'auto', gpu_devices: [] as string[], tensor_split: '', request_log_mode: 'metadata', options: {} as Record<string, string>
})

function slugify(value: string) {
  return value.toLowerCase().trim().replace(/[^\p{L}\p{N}]+/gu, '-').replace(/^-+|-+$/g, '')
}

function serializeForm() {
  const options = Object.fromEntries(Object.entries(form.options).sort(([left], [right]) => left.localeCompare(right)))
  return JSON.stringify({
    model_id: form.model_id, name: form.name, slug: form.slug, enabled: form.enabled,
    always_on: form.always_on, autoload_enabled: form.autoload_enabled, priority: form.priority,
    eviction_enabled: form.eviction_enabled, idle_unload_seconds: form.idle_unload_seconds, max_pending_requests: form.max_pending_requests, gpu_mode: form.gpu_mode,
    gpu_devices: form.gpu_mode === 'manual' ? [...form.gpu_devices] : [],
    tensor_split: form.gpu_mode === 'manual' ? form.tensor_split.trim() : '',
    request_log_mode: form.request_log_mode, options
  })
}

const hasChanges = computed(() => Boolean(baseline.value) && serializeForm() !== baseline.value)

onMounted(async () => {
  try {
    const [instance, options] = await Promise.all([
      manager.request<any>(`/api/v1/instances/${encodeURIComponent(originalSlug.value)}`),
      manager.request<Record<string, string>>(`/api/v1/instances/${encodeURIComponent(originalSlug.value)}/options`)
    ])
    if (!instance?.name && !instance?.id) throw { data: { error: 'Unable to load Instance' } }
    durableID.value = instance.id
    Object.assign(form, instance, {
      slug: instance.slug || originalSlug.value,
      gpu_devices: [...(instance.gpu_devices || [])],
      request_log_mode: instance.request_log_mode || 'metadata',
      max_pending_requests: Number(instance.max_pending_requests) || 0,
      options: { ...(options || {}) }
    })
    baseline.value = serializeForm()
    loaded.value = true
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to load Instance'
  } finally {
    loading.value = false
  }
})

async function submit() {
  if (!form.model_id || !form.name.trim() || !form.slug.trim() || !hasChanges.value) return
  error.value = ''
  const nextSlug = slugify(form.slug || form.name)
  const rename = nextSlug !== originalSlug.value
  if (rename) {
    const confirmed = await confirmation.value?.request({
      title: 'Confirm Instance slug change',
      description: `Changing this Instance slug changes the OpenAI model ID from “${originalSlug.value}” to “${nextSlug}”. Existing clients using the old model ID and old bookmarks will break.`,
      confirmLabel: 'Change slug',
      confirmTone: 'destructive'
    })
    if (!confirmed) return
  }
  const runtime = manager.runtimeForInstance({ id: durableID.value, slug: originalSlug.value, model_id: form.model_id } as any)
  const running = !['UNLOADED', 'FAILED'].includes(runtime.state)
  if (running) {
    const confirmed = await confirmation.value?.request({
      title: 'Restart running Instance',
      description: 'This Instance is running. Saving runtime-affecting configuration will drain, stop and restart it, causing temporary unavailability.',
      confirmLabel: 'Save & apply',
      confirmTone: 'destructive'
    })
    if (!confirmed) return
  }

  busy.value = true
  try {
    const updated = await manager.request<{ id: string; slug: string }>(`/api/v1/instances/${encodeURIComponent(originalSlug.value)}`, {
      method: 'PUT',
      body: {
        model_id: form.model_id, name: form.name, slug: form.slug, enabled: form.enabled,
        always_on: form.always_on, autoload_enabled: form.autoload_enabled,
        priority: form.priority, eviction_enabled: form.eviction_enabled,
        idle_unload_seconds: form.idle_unload_seconds, max_pending_requests: form.max_pending_requests, gpu_mode: form.gpu_mode,
        gpu_devices: form.gpu_mode === 'manual' ? form.gpu_devices : [],
        tensor_split: form.gpu_mode === 'manual' ? form.tensor_split.trim() : '',
        request_log_mode: form.request_log_mode, options: form.options,
        restart_running: running, confirm_model_id_change: rename
      }
    })
    await manager.refresh()
    await router.push(rename ? `/instances/${encodeURIComponent(updated.slug)}/detail` : '/instances')
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to update Instance'
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div v-if="loading" class="space-y-4"><USkeleton class="h-12 w-full" /><USkeleton class="h-56 w-full" /></div>
  <div v-else-if="!loaded" class="space-y-5">
    <div class="flex flex-wrap items-start justify-between gap-4">
      <UPageHeader headline="CONTROL PLANE" title="Edit Instance" description="Configure one durable llama-server process. The slug is the exact OpenAI model ID and defaults from the Instance name." />
      <AppButton to="/instances" intent="secondary">Back to Instances</AppButton>
    </div>
    <Frame class="p-3" data-testid="instance-edit-load-error">
      <div class="flex flex-wrap items-start gap-2">
        <StatusTag variant="failed">Unable to load Instance</StatusTag>
        <p class="min-w-0 flex-1 text-xs text-muted">{{ error }}</p>
      </div>
    </Frame>
  </div>
  <InstanceForm
    v-else
    :form="form"
    title="Edit Instance"
    submit-label="Save Instance"
    :busy="busy"
    :error="error"
    :submit-disabled="!hasChanges"
    submit-disabled-reason="No changes to save."
    :dirty="hasChanges"
    :instance-id="durableID"
    @submit="submit"
  />
  <AppConfirmationModal ref="confirmation" />
</template>
