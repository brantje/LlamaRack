<script setup lang="ts">
import type { Instance } from '~/composables/useManager'

type ImportStatus = {
  id: string
  job_id: string
  model_id: string
  instance_id?: string
  state: string
  error?: string
  start_when_ready: boolean
}

const manager = useManager()
const { instances, models } = manager
const pending = ref('')
const error = ref('')
const logsOpen = ref(false)
const logInstanceId = ref('')
const logTitle = ref('')
const importStates = ref<Record<string, ImportStatus>>({})
const confirmation = ref<{ request: (options: Record<string, string>) => Promise<boolean> } | null>(null)
let importTimer: ReturnType<typeof setTimeout> | undefined
let disposed = false

function modelName(id: string) {
  return models.value.find(model => model.id === id)?.name || id
}

function importFor(instance: Instance) {
  return importStates.value[instance.id]
}

function instanceState(instance: Instance) {
  const imported = importFor(instance)
  if (imported && imported.state !== 'COMPLETED') return imported.state
  return manager.instanceState(instance)
}

function stateColor(state: string) {
  if (state === 'READY') return 'success'
  if (state === 'FAILED' || state === 'CANCELLED') return 'error'
  if (state === 'DOWNLOADING') return 'primary'
  return 'neutral'
}

function importBlocked(instance: Instance) {
  const imported = importFor(instance)
  return Boolean(imported && imported.state !== 'COMPLETED')
}

function scheduleImportRefresh(active: boolean) {
  if (importTimer) {
    clearTimeout(importTimer)
    importTimer = undefined
  }
  if (!active || disposed) return
  importTimer = setTimeout(() => {
    importTimer = undefined
    void refreshImportStates()
  }, 1000)
}

async function refreshImportStates() {
  try {
    const previousActive = Object.values(importStates.value).some(item => item.state === 'DOWNLOADING')
    const items = await manager.request<ImportStatus[]>('/api/v1/imports') || []
    importStates.value = Object.fromEntries(items.filter(item => item.instance_id).map(item => [item.instance_id!, item]))
    const active = items.some(item => item.instance_id && item.state === 'DOWNLOADING')
    if (previousActive && !active) await manager.refresh()
    scheduleImportRefresh(active)
  } catch {
    // Runtime controls still work normally if import status cannot be refreshed.
    scheduleImportRefresh(false)
  }
}

async function action(instance: Instance, operation: 'start' | 'stop' | 'restart' | 'kill' | 'duplicate') {
  if (importBlocked(instance)) return
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
    await refreshImportStates()
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to delete Instance'
  } finally {
    pending.value = ''
  }
}

function showLogs(instance: Instance) {
  error.value = ''
  logInstanceId.value = instance.id
  logTitle.value = `${instance.name} logs`
  logsOpen.value = true
}

onMounted(() => {
  // Always perform one import-status read so completed Hugging Face metadata
  // warnings remain visible after a page reload. Polling continues only while
  // an import is actively downloading.
  void refreshImportStates()
})

onBeforeUnmount(() => {
  disposed = true
  if (importTimer) clearTimeout(importTimer)
})
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
                <AppCopyButton
                  :text="instance.id"
                  icon-only
                  color="neutral"
                  variant="ghost"
                  size="xs"
                  error-message="Unable to copy Instance ID. Select the ID and copy it manually."
                  data-testid="copy-instance-id"
                  @copied="error = ''"
                  @error="message => error = message"
                />
              </div>
              <p class="mt-1 text-sm text-muted">{{ modelName(instance.model_id) }}</p>
            </div>
            <UBadge :color="stateColor(instanceState(instance))" variant="subtle">{{ instanceState(instance) }}</UBadge>
          </div>
          <UAlert v-if="importFor(instance)?.state === 'DOWNLOADING'" color="primary" variant="subtle" title="Model is downloading" :description="importFor(instance)?.start_when_ready ? 'This Instance will launch automatically when the verified GGUF download completes.' : 'The Instance will become launchable when the verified GGUF download completes.'" />
          <UAlert v-else-if="importFor(instance)?.state === 'FAILED' || importFor(instance)?.state === 'CANCELLED'" color="error" variant="subtle" :title="`Import ${importFor(instance)?.state.toLowerCase()}`" :description="importFor(instance)?.error || 'Open Downloads to retry or inspect this import.'" />
          <UAlert v-else-if="importFor(instance)?.state === 'COMPLETED' && importFor(instance)?.error" data-testid="import-metadata-warning" color="warning" variant="subtle" title="Import warning" :description="importFor(instance)?.error" />
          <div class="grid grid-cols-2 gap-2 text-xs text-muted">
            <span>Priority: {{ instance.priority }}</span><span>GPU: {{ instance.gpu_mode }}</span><span>{{ instance.always_on ? 'Always On' : 'Not Always On' }}</span><span>{{ instance.autoload_enabled ? 'Autoload' : 'Manual load' }}</span><span class="col-span-2">{{ instance.eviction_enabled ? 'Resource-pressure eviction allowed' : 'Protected from resource-pressure eviction' }}</span>
          </div>
          <InstanceRuntimeTelemetry :state="instanceState(instance)" :telemetry="manager.telemetryForInstance(instance)" />
          <div class="flex flex-wrap gap-2">
            <UButton v-if="importBlocked(instance)" to="/downloads" color="primary" variant="soft" size="xs">View download</UButton>
            <template v-else>
              <UButton v-if="['UNLOADED', 'FAILED'].includes(instanceState(instance))" size="xs" :loading="pending === `${instance.id}:start`" @click="action(instance, 'start')">Launch</UButton>
              <UButton v-else color="neutral" variant="soft" size="xs" :loading="pending === `${instance.id}:stop`" @click="action(instance, 'stop')">Stop</UButton>
              <UButton color="neutral" variant="soft" size="xs" :loading="pending === `${instance.id}:restart`" @click="action(instance, 'restart')">Restart</UButton>
              <UButton color="error" variant="soft" size="xs" :loading="pending === `${instance.id}:kill`" @click="action(instance, 'kill')">Kill</UButton>
              <UButton color="neutral" variant="soft" size="xs" :loading="pending === `${instance.id}:duplicate`" @click="action(instance, 'duplicate')">Duplicate</UButton>
            </template>
            <UButton :to="`/instances/${encodeURIComponent(instance.id)}/detail`" color="neutral" variant="soft" size="xs">Details</UButton>
            <UButton :to="`/instances/${encodeURIComponent(instance.id)}/edit`" color="neutral" variant="soft" size="xs">Edit</UButton>
            <UButton v-if="!importBlocked(instance)" color="neutral" variant="soft" size="xs" @click="showLogs(instance)">Logs</UButton>
            <UButton color="error" variant="ghost" size="xs" :loading="pending === `${instance.id}:delete`" @click="remove(instance)">Delete</UButton>
          </div>
        </div>
      </UCard>
    </div>

    <UModal v-model:open="logsOpen" :title="logTitle" :ui="{ content: 'w-[calc(100vw-2rem)] max-w-none sm:max-w-6xl' }">
      <template #body>
        <InstanceLogViewer v-if="logsOpen && logInstanceId" :instance-id="logInstanceId" embedded />
      </template>
    </UModal>
    <AppConfirmationModal ref="confirmation" />
  </div>
</template>
