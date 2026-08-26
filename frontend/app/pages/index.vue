<script setup lang="ts">
import type { TableColumn } from '@nuxt/ui'
import type { Model } from '~/composables/useManager'

const manager = useManager()
const { models, runtimes, profile, canOperate } = manager
const readyWorkers = computed(() => Object.values(runtimes.value).flat().filter(x => x.state === 'READY').length)
const failedWorkers = computed(() => Object.values(runtimes.value).flat().filter(x => x.state === 'FAILED').length)

type FleetRow = Model & { state: string; loading: string }
type BadgeColor = 'success' | 'error' | 'warning' | 'secondary' | 'neutral'

const statusColors: Record<string, BadgeColor> = {
  READY: 'success',
  FAILED: 'error',
  STARTING: 'warning',
  LOADING: 'warning',
  STOPPING: 'secondary',
  UNLOADED: 'neutral'
}

const fleetRows = computed<FleetRow[]>(() => models.value.map(model => ({
  ...model,
  state: manager.modelState(model),
  loading: model.always_on ? 'Always on' : model.autoload_enabled ? 'Autoload' : 'Manual'
})))

const columns: TableColumn<FleetRow>[] = [
  { accessorKey: 'model_id', header: 'Model' },
  { accessorKey: 'state', header: 'Status' },
  { accessorKey: 'loading', header: 'Loading' },
  { accessorKey: 'priority', header: 'Priority' },
  { accessorKey: 'gguf_path', header: 'GGUF' }
]
</script>

<template>
  <div class="space-y-5">
    <div class="flex items-start justify-between gap-6">
      <UPageHeader
        class="min-w-0 flex-1"
        headline="LOCAL INFERENCE"
        title="Overview"
        description="Lifecycle, routing and llama.cpp runtime status."
      />
      <UButton color="neutral" variant="soft" @click="manager.refresh">Refresh</UButton>
    </div>

    <div class="grid grid-cols-2 gap-3 xl:grid-cols-4">
      <UCard>
        <p class="text-sm text-muted">Configured models</p>
        <strong class="mt-2 block text-3xl">{{ models.length }}</strong>
        <p class="mt-1 text-xs text-muted">available through manager</p>
      </UCard>
      <UCard>
        <p class="text-sm text-muted">Ready workers</p>
        <strong class="mt-2 block text-3xl">{{ readyWorkers }}</strong>
        <p class="mt-1 text-xs text-muted">serving inference</p>
      </UCard>
      <UCard>
        <p class="text-sm text-muted">Failed workers</p>
        <strong class="mt-2 block text-3xl">{{ failedWorkers }}</strong>
        <p class="mt-1 text-xs text-muted">need attention</p>
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
            <h2 class="text-xl font-bold">Model activity</h2>
          </div>
          <UButton v-if="canOperate" to="/models" size="sm">Manage models</UButton>
        </div>
      </template>

      <UEmpty
        v-if="!models.length"
        title="No models configured"
        description="Add a local GGUF model to start serving requests."
      />

      <UTable v-else :data="fleetRows" :columns="columns">
        <template #model_id-cell="{ row }">
          <div>
            <strong>{{ row.original.model_id }}</strong>
            <span class="mt-1 block text-xs text-muted">{{ row.original.name }}</span>
          </div>
        </template>
        <template #state-cell="{ row }">
          <UBadge :color="statusColors[row.original.state] || 'neutral'" variant="subtle" size="sm">
            {{ row.original.state }}
          </UBadge>
        </template>
        <template #gguf_path-cell="{ row }">
          <span class="font-mono text-xs">{{ row.original.gguf_path }}</span>
        </template>
      </UTable>
    </UCard>
  </div>
</template>
