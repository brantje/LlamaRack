<script setup lang="ts">
import type { BenchmarkRun } from '~/composables/useBenchmarks'
import { benchmarkDuration, benchmarkGPULabel, benchmarkHeadline, benchmarkStatusVariant } from '~/composables/useBenchmarks'

const route = useRoute()
const benchmarks = useBenchmarks()
const run = ref<BenchmarkRun | null>(null)
const loading = ref(true)
const error = ref('')
const mutating = ref(false)
const deleteOpen = ref(false)
let timer: ReturnType<typeof setInterval> | undefined

const runID = computed(() => String(route.params.id || ''))
const headline = computed(() => run.value ? benchmarkHeadline(run.value) : {})
const duration = computed(() => run.value ? benchmarkDuration(run.value) : undefined)
const configOptions = computed(() => Object.entries(run.value?.instance_config_snapshot.options || {}).sort(([left], [right]) => left.localeCompare(right)))
const gpus = computed(() => run.value?.hardware_snapshot?.observed?.gpus || [])
const active = computed(() => run.value?.status === 'QUEUED' || run.value?.status === 'RUNNING')

function formatDate(value?: string) {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isFinite(date.getTime()) ? date.toLocaleString() : '—'
}
function formatRate(value?: number) { return value === undefined || !Number.isFinite(value) ? '—' : `${value.toLocaleString(undefined, { maximumFractionDigits: 2 })} tok/s` }
function formatDuration(value?: number) {
  if (value === undefined) return '—'
  return value < 1000 ? `${Math.round(value)} ms` : `${(value / 1000).toFixed(value >= 10_000 ? 1 : 2)} s`
}
function bytes(value?: number) {
  if (value === undefined || !Number.isFinite(value)) return '—'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let amount = Math.max(0, value)
  let index = 0
  while (amount >= 1024 && index < units.length - 1) { amount /= 1024; index++ }
  return `${amount >= 10 || index === 0 ? amount.toFixed(0) : amount.toFixed(1)} ${units[index]}`
}

async function load(silent = false) {
  if (!silent) loading.value = true
  error.value = ''
  try { run.value = await benchmarks.get(runID.value) } catch (value: any) { error.value = value?.data?.error || value?.message || 'Unable to load benchmark.' } finally { if (!silent) loading.value = false }
}
async function cancel() {
  if (!run.value) return
  mutating.value = true
  error.value = ''
  try { run.value = await benchmarks.cancel(run.value.id); await load(true) } catch (value: any) { error.value = value?.data?.error || value?.message || 'Unable to cancel benchmark.' } finally { mutating.value = false }
}
async function remove() {
  if (!run.value) return
  mutating.value = true
  error.value = ''
  try { await benchmarks.remove(run.value.id); deleteOpen.value = false; await navigateTo('/benchmarks') } catch (value: any) { error.value = value?.data?.error || value?.message || 'Unable to delete benchmark.' } finally { mutating.value = false }
}

onMounted(() => {
  void load()
  timer = setInterval(() => { if (active.value) void load(true) }, 3000)
})
onUnmounted(() => { if (timer) clearInterval(timer) })
</script>

<template>
  <div class="space-y-6">
    <div class="flex flex-wrap items-start justify-between gap-4">
      <div>
        <p class="text-[length:var(--font-size-kicker)] font-semibold uppercase tracking-[.18em] text-[var(--accent-700)]">BENCHMARK DETAIL</p>
        <div class="mt-2 flex flex-wrap items-center gap-3"><h1 class="text-2xl font-semibold">{{ run?.instance_name_snapshot || runID }}</h1><StatusTag v-if="run" :variant="benchmarkStatusVariant(run.status)">{{ run.status }}</StatusTag></div>
        <p v-if="run" class="mt-2 text-sm text-muted">{{ run.model_name_snapshot }} · {{ run.artifact_snapshot.quantization || 'unknown quantization' }} · {{ formatDate(run.created_at) }}</p>
      </div>
      <div class="flex flex-wrap gap-2"><AppButton to="/benchmarks" intent="secondary">Back to Benchmarks</AppButton><AppButton v-if="run?.status === 'COMPLETED'" :to="`/benchmarks/compare?ids=${encodeURIComponent(run.id)}`" intent="secondary">Compare</AppButton><AppButton v-if="active" intent="secondary" tone="destructive" :loading="mutating" @click="cancel">Cancel</AppButton><AppButton v-else-if="run" intent="secondary" tone="destructive" :loading="mutating" @click="deleteOpen = true">Delete</AppButton></div>
    </div>

    <Frame v-if="error" class="p-3"><div class="flex items-start gap-2"><StatusTag variant="failed">Benchmark unavailable</StatusTag><p class="text-xs text-muted">{{ error }}</p></div></Frame>
    <div v-if="loading" class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4"><USkeleton v-for="n in 4" :key="n" class="h-28 w-full" /></div>

    <template v-else-if="run">
      <section class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4" data-testid="benchmark-detail-summary">
        <Frame class="p-4"><p class="text-xs uppercase tracking-[.12em] text-muted">Prompt processing</p><p class="mt-3 font-mono text-xl font-semibold tabular-nums">{{ formatRate(headline.prompt) }}</p></Frame>
        <Frame class="p-4"><p class="text-xs uppercase tracking-[.12em] text-muted">Generation</p><p class="mt-3 font-mono text-xl font-semibold tabular-nums">{{ formatRate(headline.generation) }}</p></Frame>
        <Frame class="p-4"><p class="text-xs uppercase tracking-[.12em] text-muted">Duration</p><p class="mt-3 font-mono text-xl font-semibold tabular-nums">{{ formatDuration(duration) }}</p></Frame>
        <Frame class="p-4"><p class="text-xs uppercase tracking-[.12em] text-muted">Hardware</p><p class="mt-3 text-sm font-semibold">{{ benchmarkGPULabel(run) }}</p><p class="mt-2 font-mono text-xs text-muted">{{ run.build.runtime_variant || 'unknown backend' }}</p></Frame>
      </section>

      <Frame v-if="run.failure || run.diagnostic_output" class="p-4">
        <div class="flex items-start gap-2"><StatusTag :variant="run.failure ? 'failed' : 'neutral'">{{ run.failure ? 'Failure' : 'Diagnostics' }}</StatusTag><div class="min-w-0 flex-1"><p v-if="run.failure" class="text-sm">{{ run.failure }}</p><pre v-if="run.diagnostic_output" class="mt-2 max-h-48 overflow-auto whitespace-pre-wrap break-words font-mono text-xs text-muted">{{ run.diagnostic_output }}</pre></div></div>
      </Frame>

      <section class="space-y-3">
        <div><h2 class="text-base font-semibold">Captured Instance configuration</h2><p class="mt-1 text-xs text-muted">Immutable configuration used for this run; current Instance edits do not change it.</p></div>
        <Frame class="p-4">
          <dl class="grid gap-x-8 gap-y-3 text-sm sm:grid-cols-2 lg:grid-cols-4"><div><dt class="text-xs text-muted">Instance</dt><dd class="mt-1">{{ run.instance_name_snapshot }} <span class="font-mono text-xs text-muted">{{ run.instance_slug_snapshot }}</span></dd></div><div><dt class="text-xs text-muted">GPU mode</dt><dd class="mt-1 font-mono">{{ run.instance_config_snapshot.gpu_mode || '—' }}</dd></div><div><dt class="text-xs text-muted">Devices</dt><dd class="mt-1 font-mono">{{ run.instance_config_snapshot.gpu_devices?.join(', ') || 'Automatic' }}</dd></div><div><dt class="text-xs text-muted">Tensor split</dt><dd class="mt-1 font-mono">{{ run.instance_config_snapshot.tensor_split || 'Automatic' }}</dd></div></dl>
          <div v-if="configOptions.length" class="mt-4 overflow-x-auto border-t border-[var(--color-divider)] pt-3"><table class="w-full text-left text-xs"><thead class="text-muted"><tr><th class="pb-2 pr-4 font-medium">Option</th><th class="pb-2 pr-4 font-medium">Value</th><th class="pb-2 font-medium">Source</th></tr></thead><tbody class="divide-y divide-[var(--color-divider)]"><tr v-for="[key, value] in configOptions" :key="key"><td class="py-2 pr-4 font-mono">--{{ key }}</td><td class="py-2 pr-4 font-mono">{{ value }}</td><td class="py-2 text-muted">{{ run.instance_config_snapshot.sources?.[key] || 'resolved' }}</td></tr></tbody></table></div>
        </Frame>
      </section>

      <section class="space-y-3">
        <div><h2 class="text-base font-semibold">Workload and command</h2><p class="mt-1 text-xs text-muted">Backend-owned workload profile and the exact resolved argv retained for auditability.</p></div>
        <Frame class="p-4 space-y-4"><dl class="grid gap-x-8 gap-y-3 text-sm sm:grid-cols-2 lg:grid-cols-4"><div><dt class="text-xs text-muted">Profile</dt><dd class="mt-1 font-mono">{{ run.workload_profile.id || 'standard' }} v{{ run.workload_profile.version || 1 }}</dd></div><div><dt class="text-xs text-muted">Prompt tokens</dt><dd class="mt-1 font-mono">{{ run.workload_profile.prompt_tokens?.join(', ') || '—' }}</dd></div><div><dt class="text-xs text-muted">Generation tokens</dt><dd class="mt-1 font-mono">{{ run.workload_profile.generation_tokens?.join(', ') || '—' }}</dd></div><div><dt class="text-xs text-muted">Repetitions</dt><dd class="mt-1 font-mono">{{ run.workload_profile.repetitions }}</dd></div></dl><pre class="overflow-x-auto whitespace-pre-wrap break-all border-t border-[var(--color-divider)] pt-3 font-mono text-xs">{{ run.resolved_argv.join(' ') }}</pre></Frame>
        <Frame v-if="run.mapping_differences?.length" class="p-4"><div class="space-y-2"><div v-for="difference in run.mapping_differences" :key="`${difference.key}:${difference.reason}`" class="flex items-start gap-2"><StatusTag :variant="difference.severity === 'blocking' ? 'failed' : 'neutral'">{{ difference.severity }}</StatusTag><p class="text-xs"><code class="font-mono">--{{ difference.key }}</code> · {{ difference.reason }}</p></div></div></Frame>
      </section>

      <section class="space-y-3">
        <div><h2 class="text-base font-semibold">Measurements</h2><p class="mt-1 text-xs text-muted">Tool-native prompt and generation cases remain separate.</p></div>
        <Frame class="overflow-hidden"><div class="overflow-x-auto"><table class="w-full min-w-[760px] text-left text-xs"><thead class="border-b border-[var(--color-divider)] text-muted"><tr><th class="px-4 py-3 font-medium">Case</th><th class="px-4 py-3 font-medium">Prompt tokens</th><th class="px-4 py-3 font-medium">Generation tokens</th><th class="px-4 py-3 font-medium">Repetitions</th><th class="px-4 py-3 font-medium">Average</th><th class="px-4 py-3 font-medium">Stddev</th></tr></thead><tbody class="divide-y divide-[var(--color-divider)]"><tr v-for="result in run.results || []" :key="`${result.case_index}:${result.case_id}`"><td class="px-4 py-3 font-mono">{{ result.case_id }}</td><td class="px-4 py-3 font-mono tabular-nums">{{ result.prompt_tokens }}</td><td class="px-4 py-3 font-mono tabular-nums">{{ result.generation_tokens }}</td><td class="px-4 py-3 font-mono tabular-nums">{{ result.repetitions }}</td><td class="px-4 py-3 font-mono tabular-nums">{{ formatRate(result.average_tokens_per_second) }}</td><td class="px-4 py-3 font-mono tabular-nums">{{ formatRate(result.stddev_tokens_per_second) }}</td></tr><tr v-if="!run.results?.length"><td colspan="6" class="px-4 py-8 text-center text-muted">No successful measurements were stored for this run.</td></tr></tbody></table></div></Frame>
      </section>

      <section class="space-y-3">
        <div><h2 class="text-base font-semibold">Hardware and build identity</h2><p class="mt-1 text-xs text-muted">Captured at benchmark admission/start for historical reproducibility.</p></div>
        <Frame class="p-4 space-y-4"><div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3"><div v-for="gpu in gpus" :key="gpu.id"><p class="font-medium">{{ gpu.name || gpu.id }}</p><p class="mt-1 font-mono text-xs text-muted">{{ gpu.backend }} · {{ gpu.id }}</p><p class="mt-1 font-mono text-xs">{{ bytes(gpu.free_bytes) }} free / {{ bytes(gpu.total_bytes) }}</p></div><div><p class="font-medium">{{ run.hardware_snapshot.cpu?.model || 'CPU' }}</p><p class="mt-1 font-mono text-xs text-muted">{{ run.hardware_snapshot.cpu?.architecture || '—' }} · {{ run.hardware_snapshot.cpu?.os || '—' }} · {{ run.hardware_snapshot.cpu?.logical_threads || '—' }} threads</p></div></div><dl class="grid gap-x-8 gap-y-3 border-t border-[var(--color-divider)] pt-3 text-sm sm:grid-cols-2 lg:grid-cols-4"><div><dt class="text-xs text-muted">LlamaRack</dt><dd class="mt-1 font-mono">{{ run.build.llamarack_version || '—' }}</dd></div><div><dt class="text-xs text-muted">Commit</dt><dd class="mt-1 truncate font-mono">{{ run.build.llamarack_commit || '—' }}</dd></div><div><dt class="text-xs text-muted">llama.cpp</dt><dd class="mt-1 font-mono">{{ run.build.llama_cpp_release || run.build.llama_cpp_build || '—' }}</dd></div><div><dt class="text-xs text-muted">llama-bench</dt><dd class="mt-1 font-mono">{{ run.build.llama_bench_version || '—' }}</dd></div></dl></Frame>
      </section>
    </template>

    <UModal v-model:open="deleteOpen" title="Delete benchmark result"><template #body><p class="text-sm leading-6 text-muted">Delete this historical benchmark and its measurements? This cannot be undone.</p></template><template #footer><div class="flex w-full justify-end gap-2"><UButton color="neutral" variant="outline" @click="deleteOpen = false">Cancel</UButton><UButton color="error" :loading="mutating" @click="remove">Delete benchmark</UButton></div></template></UModal>
  </div>
</template>
