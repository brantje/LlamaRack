<script setup lang="ts">
import type { TableColumn } from '@nuxt/ui'
import type { Instance } from '~/composables/useManager'

const manager = useManager()
const { models, instances, runtimes, profile } = manager
const readyWorkers = computed(() => Object.values(runtimes.value).flat().filter(x => x.state === 'READY').length)
const failedWorkers = computed(() => Object.values(runtimes.value).flat().filter(x => x.state === 'FAILED').length)

type FleetRow = Instance & { model_name: string; state: string; loading: string }
type BadgeColor = 'success' | 'error' | 'warning' | 'secondary' | 'neutral'

const statusColors: Record<string, BadgeColor> = {
  READY: 'success',
  FAILED: 'error',
  STARTING: 'warning',
  LOADING: 'warning',
  STOPPING: 'secondary',
  UNLOADED: 'neutral'
}

const fleetRows = computed<FleetRow[]>(() => instances.value.map(instance => ({
  ...instance,
  model_name: models.value.find(model => model.id === instance.model_id)?.name || instance.model_id,
  state: manager.instanceState(instance),
  loading: instance.always_on ? 'Always on' : instance.autoload_enabled ? 'Autoload' : 'Manual'
})))

const columns: TableColumn<FleetRow>[] = [
  { accessorKey: 'id', header: 'Instance / API model' },
  { accessorKey: 'state', header: 'Status' },
  { accessorKey: 'loading', header: 'Loading' },
  { accessorKey: 'priority', header: 'Priority' },
  { accessorKey: 'model_name', header: 'Registered model' }
]
</script>

<template>
  <div class="space-y-5">
    <div class="flex items-start justify-between gap-6">
      <UPageHeader
        class="min-w-0 flex-1"
        headline="LOCAL INFERENCE"
        title="Overview"
        description="Instance lifecycle and llama.cpp runtime status. Registered Models remain inventory/configuration only."
      />
      <UButton color="neutral" variant="soft" @click="manager.refresh">Refresh</UButton>
    </div>

    <div class="grid grid-cols-2 gap-3 xl:grid-cols-4">
      <UCard>
        <p class="text-sm text-muted">Registered models</p>
        <strong class="mt-2 block text-3xl">{{ models.length }}</strong>
        <p class="mt-1 text-xs text-muted">GGUF inventory</p>
      </UCard>
      <UCard>
        <p class="text-sm text-muted">Configured Instances</p>
        <strong class="mt-2 block text-3xl">{{ instances.length }}</strong>
        <p class="mt-1 text-xs text-muted">addressable API models</p>
      </UCard>
      <UCard>
        <p class="text-sm text-muted">Ready / failed workers</p>
        <strong class="mt-2 block text-3xl">{{ readyWorkers }} / {{ failedWorkers }}</strong>
        <p class="mt-1 text-xs text-muted">current runtime state</p>
      </UCard>
      <UCard>
        <p class="text-sm text-muted">llama.cpp</p>
        <strong class="mt-2 block truncate text-base">{{ profile?.version || 'Unavailable' }}</strong>
        <p class="mt-1 text-xs text-muted">{{ profile?.options.length || 0 }} discovered CLI options</p>
      </UCard>
    </div>

    <UCard>
      <template #header>
        <div class="flex items-start justify-between gap-4">
          <div>
            <p class="mb-1 text-xs font-extrabold tracking-[0.18em] text-dimmed">FLEET</p>
            <h2 class="text-xl font-bold">Instance activity</h2>
          </div>
          <UButton to="/instances" size="sm">Manage Instances</UButton>
        </div>
      </template>

      <UEmpty
        v-if="!instances.length"
        variant="naked"
        title="No Instances configured"
        description="Create an Instance for a registered Model to make it addressable for inference."
      />

      <UTable v-else :data="fleetRows" :columns="columns">
        <template #id-cell="{ row }">
          <div>
            <strong>{{ row.original.id }}</strong>
            <span class="mt-1 block text-xs text-muted">{{ row.original.name }}</span>
          </div>
        </template>
        <template #state-cell="{ row }">
          <UBadge :color="statusColors[row.original.state] || 'neutral'" variant="subtle" size="sm">
            {{ row.original.state }}
          </UBadge>
        </template>
      </UTable>
    </UCard>
  </div>
</template>
