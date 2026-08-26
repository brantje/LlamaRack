<script setup lang="ts">
import type { Instance } from '~/composables/useManager'

const manager = useManager()
const { instances, models } = manager
const pending = ref('')
const error = ref('')
const copiedInstanceId = ref('')
const logsOpen = ref(false)
const logLines = ref<string[]>([])
const logTitle = ref('')
const confirmation = ref<{ request: (options: Record<string, string>) => Promise<boolean> } | null>(null)

function modelName(id: string) {
  return models.value.find(model => model.id === id)?.name || id
}

async function copyInstanceId(instance: Instance) {
  error.value = ''
  try {
    await navigator.clipboard.writeText(instance.id)
    copiedInstanceId.value = instance.id
  } catch (value: any) {
    copiedInstanceId.value = ''
    error.value = value?.message || 'Unable to copy Instance ID'
  }
}

async function action(instance: Instance, operation: 'start' | 'stop' | 'restart' | 'kill' | 'duplicate') {
  if (operation === 'start' && instance.eviction_enabled) {
    const confirmed = await confirmation.value?.request({
      title: 'Launch Instance',
      description: 'Launching this Instance may stop other idle Instances if resource-pressure eviction is required.',
      confirmLabel: 'Launch Instance',
      color: 'primary'
    })
    if (!confirmed) return
  }
  if (operation === 'kill') {
    const confirmed = await confirmation.value?.request({
      title: 'Kill Instance',
      description: 'Kill this Instance immediately? Active requests may fail.',
      confirmLabel: 'Kill Instance',
      color: 'error'
    })
    if (!confirmed) return
  }
  pending.value = `${instance.id}:${operation}`
  error.value = ''
  try {
    await manager.request(`/api/v1/instances/${encodeURIComponent(instance.id)}/${operation}`, { method: 'POST' })
    await manager.refresh()
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || `Unable to ${operation} Instance`
  } finally {
    pending.value = ''
  }
}

async function remove(instance: Instance) {
  const confirmed = await confirmation.value?.request({
    title: 'Delete Instance',
    description: `Delete Instance “${instance.name}”? The registered Model and GGUF file are kept.`,
    confirmLabel: 'Delete Instance',
    color: 'error'
  })
  if (!confirmed) return
  pending.value = `${instance.id}:delete`
  error.value = ''
  try {
    await manager.request(`/api/v1/instances/${encodeURIComponent(instance.id)}`, { method: 'DELETE' })
    await manager.refresh()
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to delete Instance'
  } finally {
    pending.value = ''
  }
}

async function showLogs(instance: Instance) {
  error.value = ''
  try {
    const result = await manager.request<{ lines: string[] }>(`/api/v1/instances/${encodeURIComponent(instance.id)}/logs`)
    logLines.value = result.lines || []
    logTitle.value = `${instance.name} logs`
    logsOpen.value = true
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to load logs'
  }
}
</script>

<template>
  <div class="space-y-5">
    <div class="flex items-start justify-between gap-6">
      <UPageHeader class="min-w-0 flex-1" headline="CONTROL PLANE" title="Instances" description="Durable llama-server definitions. Instance IDs are the model IDs used by OpenAI-compatible clients." />
      <div class="flex flex-wrap justify-end gap-2"><UButton color="neutral" variant="soft" @click="manager.refresh">Refresh</UButton><UButton to="/instances/new">New Instance</UButton></div>
    </div>

    <UAlert v-if="error" color="error" variant="subtle" :description="error" />
    <UEmpty v-if="!instances.length" title="No Instances configured" description="Create an Instance for a registered Model. Stopped Instances remain here and can be launched later."><template #actions><UButton to="/instances/new" size="sm">New Instance</UButton></template></UEmpty>

    <div v-else class="grid gap-4 md:grid-cols-2 2xl:grid-cols-3">
      <UCard v-for="instance in instances" :key="instance.id" data-testid="instance-card">
        <div class="space-y-4">
          <div class="flex items-start justify-between gap-4">
            <div class="min-w-0">
              <h2 class="truncate text-lg font-bold text-highlighted">{{ instance.name }}</h2>
              <div class="mt-1 flex items-center gap-1">
                <code class="break-all font-mono text-xs text-muted" data-testid="instance-id">{{ instance.id }}</code>
                <UButton
                  :icon="copiedInstanceId === instance.id ? 'i-lucide-check' : 'i-lucide-copy'"
                  color="neutral"
                  variant="ghost"
                  size="xs"
                  :aria-label="copiedInstanceId === instance.id ? `Copied ${instance.id}` : `Copy ${instance.id}`"
                  :title="copiedInstanceId === instance.id ? 'Copied' : 'Copy model ID'"
                  data-testid="copy-instance-id"
                  @click="copyInstanceId(instance)"
                />
              </div>
              <p class="mt-1 text-sm text-muted">{{ modelName(instance.model_id) }}</p>
            </div>
            <UBadge :color="manager.instanceState(instance) === 'READY' ? 'success' : manager.instanceState(instance) === 'FAILED' ? 'error' : 'neutral'" variant="subtle">{{ manager.instanceState(instance) }}</UBadge>
          </div>
          <div class="grid grid-cols-2 gap-2 text-xs text-muted">
            <span>Priority: {{ instance.priority }}</span><span>GPU: {{ instance.gpu_mode }}</span><span>{{ instance.always_on ? 'Always On' : 'Not Always On' }}</span><span>{{ instance.autoload_enabled ? 'Autoload' : 'Manual load' }}</span><span class="col-span-2">{{ instance.eviction_enabled ? 'Resource-pressure eviction allowed' : 'Protected from resource-pressure eviction' }}</span>
          </div>
          <div class="flex flex-wrap gap-2">
            <UButton v-if="['UNLOADED', 'FAILED'].includes(manager.instanceState(instance))" size="xs" :loading="pending === `${instance.id}:start`" @click="action(instance, 'start')">Launch</UButton>
            <UButton v-else color="neutral" variant="soft" size="xs" :loading="pending === `${instance.id}:stop`" @click="action(instance, 'stop')">Stop</UButton>
            <UButton color="neutral" variant="soft" size="xs" :loading="pending === `${instance.id}:restart`" @click="action(instance, 'restart')">Restart</UButton>
            <UButton color="error" variant="soft" size="xs" :loading="pending === `${instance.id}:kill`" @click="action(instance, 'kill')">Kill</UButton>
            <UButton color="neutral" variant="soft" size="xs" :loading="pending === `${instance.id}:duplicate`" @click="action(instance, 'duplicate')">Duplicate</UButton>
            <UButton :to="`/instances/${encodeURIComponent(instance.id)}/edit`" color="neutral" variant="soft" size="xs">Edit</UButton>
            <UButton color="neutral" variant="soft" size="xs" @click="showLogs(instance)">Logs</UButton>
            <UButton color="error" variant="ghost" size="xs" :loading="pending === `${instance.id}:delete`" @click="remove(instance)">Delete</UButton>
          </div>
        </div>
      </UCard>
    </div>

    <UModal v-model:open="logsOpen" :title="logTitle"><template #body><pre class="max-h-[65vh] overflow-auto whitespace-pre-wrap rounded-md bg-elevated p-4 font-mono text-xs">{{ logLines.length ? logLines.join('\n') : 'No logs captured.' }}</pre></template></UModal>
    <AppConfirmationModal ref="confirmation" />
  </div>
</template>
