<script setup lang="ts">
import type { TableColumn } from '@nuxt/ui'
import type { Model } from '~/composables/useManager'

const manager = useManager()
const { models, runtimes, profile, canOperate } = manager
const readyWorkers = computed(() => Object.values(runtimes.value).flat().filter(x => x.state === 'READY').length)
const failedWorkers = computed(() => Object.values(runtimes.value).flat().filter(x => x.state === 'FAILED').length)

type FleetRow = Model & { state: string; loading: string }
type BadgeColor = 'success' | 'error' | 'warning' | 'neutral' | 'secondary'

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
  <div class="grid gap-6">
    <UPageHeader headline="LOCAL INFERENCE" title="Overview" description="Lifecycle, routing and llama.cpp runtime status.">
      <template #links>
        <UButton label="Refresh" color="neutral" variant="outline" @click="manager.refresh" />
      </template>
    </UPageHeader>

    <div class="grid grid-cols-2 gap-3 xl:grid-cols-4">
      <UCard>
        <div class="grid gap-2">
          <span class="text-sm text-muted">Configured models</span>
          <strong class="text-3xl font-bold text-highlighted">{{ models.length }}</strong>
          <small class="text-muted">available through manager</small>
        </div>
      </UCard>
      <UCard>
        <div class="grid gap-2">
          <span class="text-sm text-muted">Ready workers</span>
          <strong class="text-3xl font-bold text-highlighted">{{ readyWorkers }}</strong>
          <small class="text-muted">serving inference</small>
        </div>
      </UCard>
      <UCard>
        <div class="grid gap-2">
          <span class="text-sm text-muted">Failed workers</span>
          <strong class="text-3xl font-bold text-highlighted">{{ failedWorkers }}</strong>
          <small class="text-muted">need attention</small>
        </div>
      </UCard>
      <UCard>
        <div class="grid min-w-0 gap-2">
          <span class="text-sm text-muted">llama.cpp</span>
          <strong class="truncate text-base font-bold text-highlighted">{{ profile?.version || 'Unavailable' }}</strong>
          <small class="text-muted">{{ profile?.options.length || 0 }} discovered CLI options</small>
        </div>
      </UCard>
    </div>

    <UCard>
      <template #header>
        <div class="flex flex-wrap items-start justify-between gap-4">
          <div>
            <p class="text-[11px] font-extrabold tracking-[0.18em] text-muted">FLEET</p>
            <h2 class="mt-1 text-xl font-bold text-highlighted">Model activity</h2>
          </div>
          <UButton v-if="canOperate" label="Manage models" to="/models" size="sm" />
        </div>
      </template>

      <UEmpty
        v-if="!models.length"
        title="No models configured"
        description="Add a local GGUF model to start serving requests."
      />

      <UTable v-else :data="fleetRows" :columns="columns" class="w-full">
        <template #model_id-cell="{ row }">
          <div>
            <strong class="text-highlighted">{{ row.original.model_id }}</strong>
            <span class="mt-1 block text-xs text-muted">{{ row.original.name }}</span>
          </div>
        </template>
        <template #state-cell="{ row }">
          <UBadge :label="row.original.state" :color="statusColors[row.original.state]" variant="subtle" size="sm" />
        </template>
        <template #gguf_path-cell="{ row }">
          <code class="text-xs text-toned">{{ row.original.gguf_path }}</code>
        </template>
      </UTable>
    </UCard>
  </div>
</template>
