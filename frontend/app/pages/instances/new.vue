<script setup lang="ts">
const manager = useManager()
const router = useRouter()
const busy = ref(false)
const error = ref('')
const launchAfterCreate = ref(false)
const form = reactive({
  model_id: '', name: '', enabled: true, always_on: false, autoload_enabled: true,
  priority: 'normal', eviction_enabled: true, idle_unload_seconds: 0,
  gpu_mode: 'auto', gpu_devices: '', tensor_split: '', options: ''
})
const modelItems = computed(() => manager.models.value.map(model => ({ label: model.name, value: model.id })))
const priorityItems = ['low', 'normal', 'high'].map(value => ({ label: value[0]!.toUpperCase() + value.slice(1), value }))
const gpuItems = [{ label: 'Automatic', value: 'auto' }, { label: 'Manual', value: 'manual' }]

function parseOptions(value: string) {
  const out: Record<string, string> = {}
  for (const line of value.split('\n')) {
    const trimmed = line.trim()
    if (!trimmed) continue
    const at = trimmed.indexOf('=')
    if (at <= 0) throw new Error(`Invalid option “${trimmed}”; use key=value`)
    out[trimmed.slice(0, at).trim()] = trimmed.slice(at + 1).trim()
  }
  return out
}

async function submit() {
  busy.value = true
  error.value = ''
  try {
    const instance = await manager.request<{ id: string }>('/api/v1/instances', {
      method: 'POST',
      body: {
        ...form,
        gpu_devices: form.gpu_mode === 'manual' ? form.gpu_devices.split(',').map(x => x.trim()).filter(Boolean) : [],
        tensor_split: form.tensor_split.trim(),
        options: parseOptions(form.options)
      }
    })
    if (launchAfterCreate.value) {
      if (form.eviction_enabled && !confirm('Launching this Instance may stop other idle Instances if resource-pressure eviction is required. Continue?')) {
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
    <div class="flex items-start justify-between gap-6">
      <UPageHeader class="min-w-0 flex-1" headline="CONTROL PLANE" title="New Instance" description="Configure one durable llama-server process. The Instance name is slugified into the OpenAI model ID." />
      <UButton to="/instances" color="neutral" variant="soft">Back to Instances</UButton>
    </div>
    <UCard class="max-w-4xl">
      <UAlert v-if="error" class="mb-5" color="error" variant="subtle" :description="error" />
      <UForm :state="form" class="space-y-6" @submit="submit">
        <div class="grid gap-4 md:grid-cols-2">
          <UFormField label="Registered Model" name="model_id" required><USelect v-model="form.model_id" class="w-full" :items="modelItems" label-key="label" value-key="value" required /></UFormField>
          <UFormField label="Instance name" name="name" description="Slugified into the exact OpenAI model ID." required><UInput v-model="form.name" class="w-full" required /></UFormField>
        </div>
        <USeparator label="Lifecycle & scheduling" />
        <div class="grid gap-4 md:grid-cols-2">
          <UFormField label="Priority" name="priority"><USelect v-model="form.priority" class="w-full" :items="priorityItems" label-key="label" value-key="value" /></UFormField>
          <UFormField label="Idle unload timeout (seconds)" name="idle_unload_seconds"><UInputNumber v-model="form.idle_unload_seconds" class="w-full" :min="0" /></UFormField>
        </div>
        <div class="space-y-3">
          <UCheckbox v-model="form.enabled" label="Enabled" />
          <UCheckbox v-model="form.always_on" label="Always On" />
          <UCheckbox v-model="form.autoload_enabled" label="Autoload on request" />
          <UCheckbox v-model="form.eviction_enabled" label="Allow resource-pressure eviction" />
        </div>
        <USeparator label="Placement" />
        <div class="grid gap-4 md:grid-cols-2">
          <UFormField label="GPU placement" name="gpu_mode"><USelect v-model="form.gpu_mode" class="w-full" :items="gpuItems" label-key="label" value-key="value" /></UFormField>
          <UFormField v-if="form.gpu_mode === 'manual'" label="GPU devices" name="gpu_devices" description="Comma-separated device IDs."><UInput v-model="form.gpu_devices" class="w-full" placeholder="0,1" /></UFormField>
          <UFormField label="Tensor split" name="tensor_split" description="Passed to llama.cpp when configured."><UInput v-model="form.tensor_split" class="w-full" placeholder="1,1" /></UFormField>
        </div>
        <USeparator label="llama.cpp overrides" />
        <UFormField label="Instance overrides" name="options" description="One key=value pair per line. These override Model defaults."><UTextarea v-model="form.options" class="w-full font-mono" :rows="7" placeholder="ctx-size=32768" /></UFormField>
        <UCheckbox v-model="launchAfterCreate" label="Launch after creation" />
        <div class="flex justify-end gap-2"><UButton to="/instances" color="neutral" variant="soft">Cancel</UButton><UButton type="submit" :loading="busy" :disabled="!form.model_id || !form.name">Create Instance</UButton></div>
      </UForm>
    </UCard>
  </div>
</template>
