<script setup lang="ts">
import type { BenchmarkRun, BenchmarkStatus } from '~/composables/useBenchmarks'
import { benchmarkDuration, benchmarkGPULabel, benchmarkHeadline, benchmarkStatusVariant } from '~/composables/useBenchmarks'

const manager = useManager()
const benchmarks = useBenchmarks()
const route = useRoute()
const loading = ref(true)
const error = ref('')
const runs = ref<BenchmarkRun[]>([])
const total = ref(0)
const limit = 25
const offset = ref(0)
const instanceID = ref(typeof route.query.instance_id === 'string' ? route.query.instance_id : '')
const modelID = ref(typeof route.query.model_id === 'string' ? route.query.model_id : '')
const status = ref<BenchmarkStatus | ''>(typeof route.query.status === 'string' ? route.query.status.toUpperCase() as BenchmarkStatus : '')
const selected = ref<string[]>([])
const deleting = ref<BenchmarkRun | null>(null)
const mutating = ref('')
const deleteOpen = computed({ get: () => Boolean(deleting.value), set: (value: boolean) => { if (!value) deleting.value = null } })

const statusItems = [
  { label: 'All statuses', value: '' },
  { label: 'Queued', value: 'QUEUED' },
  { label: 'Running', value: 'RUNNING' },
  { label: 'Completed', value: 'COMPLETED' },
  { label: 'Failed', value: 'FAILED' },
  { label: 'Cancelled', value: 'CANCELLED' }
]
const instanceItems = computed(() => [{ label: 'All Instances', value: '' }, ...manager.instances.value.map(item => ({ label: item.name, value: item.id }))])
const modelItems = computed(() => [{ label: 'All Models', value: '' }, ...manager.models.value.map(item => ({ label: item.name, value: item.id }))])
const canPrevious = computed(() => offset.value > 0)
const canNext = computed(() => offset.value + runs.value.length < total.value)
const comparisonURL = computed(() => selected.value.length === 2 ? `/benchmarks/compare?ids=${selected.value.map(encodeURIComponent).join(',')}` : '')

function formatDate(value?: string) {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isFinite(date.getTime()) ? date.toLocaleString() : '—'
}
function formatRate(value?: number) { return value === undefined || !Number.isFinite(value) ? '—' : `${value.toLocaleString(undefined, { maximumFractionDigits: 1 })} tok/s` }
function formatDuration(value?: number) {
  if (value === undefined) return '—'
  return value < 1000 ? `${Math.round(value)} ms` : `${(value / 1000).toFixed(value >= 10_000 ? 1 : 2)} s`
}
function workload(run: BenchmarkRun) {
  return `P ${run.workload_profile.prompt_tokens?.join('/') || '—'} · G ${run.workload_profile.generation_tokens?.join('/') || '—'} · ×${run.workload_profile.repetitions}`
}
function toggleCompare(run: BenchmarkRun, checked: boolean | 'indeterminate') {
  if (run.status !== 'COMPLETED') return
  const next = new Set(selected.value)
  if (checked === true) {
    if (next.size >= 2 && !next.has(run.id)) return
    next.add(run.id)
  } else next.delete(run.id)
  selected.value = [...next]
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    if (!manager.instances.value.length && !manager.models.value.length) await manager.refresh()
    const page = await benchmarks.list({ instanceID: instanceID.value, modelID: modelID.value, status: status.value, limit, offset: offset.value })
    runs.value = page.items || []
    total.value = page.total || 0
    selected.value = selected.value.filter(id => runs.value.some(run => run.id === id && run.status === 'COMPLETED'))
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to load benchmark history.'
  } finally {
    loading.value = false
  }
}
async function applyFilters() {
  offset.value = 0
  await navigateTo({ path: '/benchmarks', query: { ...(instanceID.value ? { instance_id: instanceID.value } : {}), ...(modelID.value ? { model_id: modelID.value } : {}), ...(status.value ? { status: status.value } : {}) } }, { replace: true })
  await load()
}
async function page(direction: -1 | 1) {
  offset.value = Math.max(0, offset.value + direction * limit)
  await load()
}
async function cancel(run: BenchmarkRun) {
  mutating.value = run.id
  error.value = ''
  try { await benchmarks.cancel(run.id); await load() } catch (value: any) { error.value = value?.data?.error || value?.message || 'Unable to cancel benchmark.' } finally { mutating.value = '' }
}
async function confirmDelete() {
  if (!deleting.value) return
  const run = deleting.value
  mutating.value = run.id
  error.value = ''
  try { await benchmarks.remove(run.id); deleting.value = null; selected.value = selected.value.filter(id => id !== run.id); await load() } catch (value: any) { error.value = value?.data?.error || value?.message || 'Unable to delete benchmark.' } finally { mutating.value = '' }
}

onMounted(() => { void load() })
</script>

<template>
  <div class="space-y-6">
    <div class="flex flex-wrap items-start justify-between gap-4">
      <div>
        <p class="text-[length:var(--font-size-kicker)] font-semibold uppercase tracking-[.18em] text-[var(--accent-700)]">BENCHMARKS</p>
        <h1 class="mt-2 text-2xl font-semibold">Benchmark history</h1>
        <p class="mt-2 max-w-3xl text-sm text-muted">Persistent llama-bench results captured from saved Instance configurations. Start new runs from an Instance detail page.</p>
      </div>
      <AppButton v-if="comparisonURL" :to="comparisonURL" intent="primary" data-testid="compare-selected-benchmarks">Compare selected (2)</AppButton>
    </div>

    <Frame class="p-4">
      <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <UFormField label="Instance"><USelect v-model="instanceID" :items="instanceItems" value-key="value" label-key="label" class="w-full" /></UFormField>
        <UFormField label="Model"><USelect v-model="modelID" :items="modelItems" value-key="value" label-key="label" class="w-full" /></UFormField>
        <UFormField label="Status"><USelect v-model="status" :items="statusItems" value-key="value" label-key="label" class="w-full" /></UFormField>
        <div class="flex items-end"><UButton color="neutral" variant="outline" class="w-full justify-center" :loading="loading" @click="applyFilters">Apply filters</UButton></div>
      </div>
    </Frame>

    <Frame v-if="error" class="p-3"><div class="flex items-start gap-2"><StatusTag variant="failed">Benchmarks unavailable</StatusTag><p class="text-xs text-muted">{{ error }}</p></div></Frame>
    <div v-if="loading" class="space-y-3"><USkeleton v-for="n in 4" :key="n" class="h-16 w-full" /></div>
    <Frame v-else-if="!runs.length" class="p-8"><UEmpty variant="naked" title="No benchmark runs" description="Open an Instance and choose Benchmark to create the first run." /></Frame>
    <Frame v-else class="overflow-hidden" data-testid="benchmark-history-table">
      <div class="overflow-x-auto">
        <table class="w-full min-w-[1120px] text-left text-xs">
          <thead class="border-b border-[var(--color-divider)] text-muted"><tr><th class="w-10 px-4 py-3"></th><th class="px-4 py-3 font-medium">Run</th><th class="px-4 py-3 font-medium">Instance / Model</th><th class="px-4 py-3 font-medium">Workload</th><th class="px-4 py-3 font-medium">Hardware</th><th class="px-4 py-3 font-medium">Prompt</th><th class="px-4 py-3 font-medium">Generation</th><th class="px-4 py-3 font-medium">Duration</th><th class="px-4 py-3 font-medium text-right">Actions</th></tr></thead>
          <tbody class="divide-y divide-[var(--color-divider)]">
            <tr v-for="run in runs" :key="run.id">
              <td class="px-4 py-3 align-top"><UCheckbox :model-value="selected.includes(run.id)" :disabled="run.status !== 'COMPLETED' || (selected.length >= 2 && !selected.includes(run.id))" aria-label="Select benchmark for comparison" @update:model-value="value => toggleCompare(run, value)" /></td>
              <td class="px-4 py-3 align-top"><StatusTag :variant="benchmarkStatusVariant(run.status)">{{ run.status }}</StatusTag><p class="mt-2 font-mono text-[11px]">{{ formatDate(run.created_at) }}</p></td>
              <td class="px-4 py-3 align-top"><p class="font-medium">{{ run.instance_name_snapshot }}</p><p class="mt-1 text-muted">{{ run.model_name_snapshot }} · {{ run.artifact_snapshot.quantization || 'unknown quantization' }}</p></td>
              <td class="px-4 py-3 align-top font-mono">{{ workload(run) }}</td>
              <td class="px-4 py-3 align-top"><p>{{ benchmarkGPULabel(run) }}</p><p class="mt-1 font-mono text-muted">{{ run.build.llama_cpp_build || run.build.llama_bench_version || '—' }}</p></td>
              <td class="px-4 py-3 align-top font-mono tabular-nums">{{ formatRate(benchmarkHeadline(run).prompt) }}</td>
              <td class="px-4 py-3 align-top font-mono tabular-nums">{{ formatRate(benchmarkHeadline(run).generation) }}</td>
              <td class="px-4 py-3 align-top font-mono tabular-nums">{{ formatDuration(benchmarkDuration(run)) }}</td>
              <td class="px-4 py-3 align-top"><div class="flex justify-end gap-2"><AppButton :to="`/benchmarks/${encodeURIComponent(run.id)}`" intent="secondary" size="xs">View</AppButton><AppButton v-if="run.status === 'QUEUED' || run.status === 'RUNNING'" intent="secondary" tone="destructive" size="xs" :loading="mutating === run.id" @click="cancel(run)">Cancel</AppButton><AppButton v-else intent="secondary" tone="destructive" size="xs" :loading="mutating === run.id" @click="deleting = run">Delete</AppButton></div></td>
            </tr>
          </tbody>
        </table>
      </div>
      <div class="flex flex-wrap items-center justify-between gap-3 border-t border-[var(--color-divider)] px-4 py-3"><p class="text-xs text-muted">Showing {{ offset + 1 }}–{{ Math.min(offset + runs.length, total) }} of {{ total }}</p><div class="flex gap-2"><AppButton intent="secondary" size="xs" :disabled="!canPrevious" @click="page(-1)">Previous</AppButton><AppButton intent="secondary" size="xs" :disabled="!canNext" @click="page(1)">Next</AppButton></div></div>
    </Frame>

    <UModal v-model:open="deleteOpen" title="Delete benchmark result">
      <template #body><p class="text-sm leading-6 text-muted">Delete this historical benchmark run and its measurements? This cannot be undone.</p></template>
      <template #footer><div class="flex w-full justify-end gap-2"><UButton color="neutral" variant="outline" @click="deleting = null">Cancel</UButton><UButton color="error" :loading="!!deleting && mutating === deleting.id" data-testid="confirm-delete-benchmark" @click="confirmDelete">Delete benchmark</UButton></div></template>
    </UModal>
  </div>
</template>
