<script setup lang="ts">
const manager = useManager()
const { models, runtimes, profile, canOperate } = manager
const readyWorkers = computed(() => Object.values(runtimes.value).flat().filter(x => x.state === 'READY').length)
const failedWorkers = computed(() => Object.values(runtimes.value).flat().filter(x => x.state === 'FAILED').length)

const tableRows = computed(() => models.value.map(model => ({
  id: model.id,
  model: model.model_id,
  name: model.name,
  status: manager.modelState(model),
  loading: model.always_on ? 'Always on' : model.autoload_enabled ? 'Autoload' : 'Manual',
  priority: model.priority,
  gguf: model.gguf_path
})))

const tableColumns = [
  { accessorKey: 'model', header: 'Model' },
  { accessorKey: 'status', header: 'Status' },
  { accessorKey: 'loading', header: 'Loading' },
  { accessorKey: 'priority', header: 'Priority' },
  { accessorKey: 'gguf', header: 'GGUF' }
]

function statusColor(state: string): 'primary' | 'error' | 'warning' | 'secondary' | 'neutral' {
  if (state === 'READY') return 'primary'
  if (state === 'FAILED') return 'error'
  if (state === 'STARTING' || state === 'LOADING') return 'warning'
  if (state === 'STOPPING') return 'secondary'
  return 'neutral'
}
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

      <UTable v-else :data="tableRows" :columns="tableColumns">
        <template #model-cell="{ row }">
          <div>
            <strong>{{ row.original.model }}</strong>
            <span class="mt-1 block text-xs text-muted">{{ row.original.name }}</span>
          </div>
        </template>
        <template #status-cell="{ row }">
          <UBadge :color="statusColor(row.original.status)" variant="subtle" size="sm">
            {{ row.original.status }}
          </UBadge>
        </template>
        <template #gguf-cell="{ row }">
          <span class="font-mono text-xs">{{ row.original.gguf }}</span>
        </template>
      </UTable>
    </UCard>
  </div>
</template>
