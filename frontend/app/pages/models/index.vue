<script setup lang="ts">
const manager = useManager()
const { models } = manager
const message = ref('')
const pending = ref<string | null>(null)

function formatBytes(value: number) {
  if (!value) return '—'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let size = value
  let unit = 0
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024
    unit++
  }
  return `${size >= 10 || unit === 0 ? size.toFixed(0) : size.toFixed(1)} ${units[unit]}`
}

function contextLabel(value: number) {
  return value > 0 ? value.toLocaleString() : 'Unknown'
}

async function remove(id: string) {
  if (!confirm('Delete this registered model? Its Instance definitions will also be deleted. The GGUF file will not be removed.')) return
  pending.value = id
  message.value = ''
  try {
    await manager.request(`/api/v1/models/${encodeURIComponent(id)}`, { method: 'DELETE' })
    await manager.refresh()
  } catch (error: any) {
    message.value = error?.data?.error || error?.message || 'Unable to delete model'
  } finally {
    pending.value = null
  }
}
</script>

<template>
  <div class="space-y-5">
    <div class="flex items-start justify-between gap-6">
      <UPageHeader
        class="min-w-0 flex-1"
        headline="MODEL REGISTRY"
        title="Models"
        description="Registered GGUF inventory and reusable llama.cpp defaults. Runtime lifecycle is managed from Instances."
      />
      <div class="flex flex-wrap justify-end gap-2">
        <UButton color="neutral" variant="soft" @click="manager.refresh">Refresh</UButton>
        <UButton to="/models/new">Add model</UButton>
      </div>
    </div>

    <UAlert v-if="message" color="error" variant="subtle" :description="message" />

    <UEmpty v-if="!models.length" title="No models registered" description="Register a local GGUF file to get started.">
      <template #actions><UButton to="/models/new" size="sm">Add model</UButton></template>
    </UEmpty>

    <UCard v-else :ui="{ body: 'p-0 sm:p-0' }">
      <div class="overflow-x-auto">
        <table class="w-full text-left text-sm" data-testid="models-table">
          <thead class="border-b border-default bg-elevated/40 text-xs uppercase tracking-wide text-dimmed">
            <tr>
              <th class="px-4 py-3 font-semibold">Name</th>
              <th class="px-4 py-3 font-semibold">Path</th>
              <th class="px-4 py-3 font-semibold">Size</th>
              <th class="px-4 py-3 font-semibold">Quantization</th>
              <th class="px-4 py-3 font-semibold">Context capability</th>
              <th class="px-4 py-3 text-right font-semibold">Actions</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-default">
            <tr v-for="model in models" :key="model.id" data-testid="model-row">
              <td class="px-4 py-3 font-semibold text-highlighted">{{ model.name }}</td>
              <td class="max-w-md px-4 py-3 font-mono text-xs text-muted"><span class="break-all">{{ model.gguf_path }}</span></td>
              <td class="whitespace-nowrap px-4 py-3">{{ formatBytes(model.total_bytes) }}</td>
              <td class="whitespace-nowrap px-4 py-3">{{ model.quantization || '—' }}</td>
              <td class="whitespace-nowrap px-4 py-3">{{ contextLabel(model.context_length) }}</td>
              <td class="px-4 py-3">
                <div class="flex justify-end gap-2">
                  <UButton :to="`/models/${model.id}/edit`" color="neutral" variant="soft" size="xs">Edit</UButton>
                  <UButton color="error" variant="soft" size="xs" :loading="pending === model.id" @click="remove(model.id)">Delete</UButton>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </UCard>
  </div>
</template>
