<script setup lang="ts">
import type { BenchmarkResult, BenchmarkRun } from '~/composables/useBenchmarks'
import { benchmarkComparisonCompatible, benchmarkComparisonDifferences, benchmarkConfigChanges, benchmarkDuration, benchmarkEffectiveConfig, benchmarkGPULabel, benchmarkGPUs, benchmarkHeadline, benchmarkResultDepth, benchmarkStatusVariant, benchmarkTuningHints } from '~/composables/useBenchmarks'

const route = useRoute()
const benchmarks = useBenchmarks()
const manager = useManager()
const run = ref<BenchmarkRun | null>(null)
const baseline = ref<BenchmarkRun | null>(null)
const loading = ref(true)
const error = ref('')
const mutating = ref(false)
const deleteOpen = ref(false)
let timer: ReturnType<typeof setInterval> | undefined

const runID = computed(() => String(route.params.id || ''))
const baselineID = computed(() => typeof route.query.baseline === 'string' ? route.query.baseline : '')
const headline = computed(() => run.value ? benchmarkHeadline(run.value) : {})
const baselineHeadline = computed(() => baseline.value ? benchmarkHeadline(baseline.value) : {})
const duration = computed(() => run.value ? benchmarkDuration(run.value) : undefined)
const effective = computed(() => run.value ? benchmarkEffectiveConfig(run.value) : undefined)
const instanceOptions = computed(() => Object.entries(run.value?.instance_config_snapshot.options || {}).sort(([a], [b]) => a.localeCompare(b)))
const effectiveOptions = computed(() => Object.entries(effective.value?.options || {}).sort(([a], [b]) => a.localeCompare(b)))
const runChanges = computed(() => run.value ? benchmarkConfigChanges({ ...run.value, effective_benchmark_config: run.value.instance_config_snapshot, benchmark_overrides: {} }, run.value) : [])
const comparisonChanges = computed(() => run.value && baseline.value ? benchmarkConfigChanges(baseline.value, run.value) : [])
const comparisonDifferences = computed(() => run.value && baseline.value ? benchmarkComparisonDifferences(baseline.value, run.value) : [])
const comparisonCompatible = computed(() => run.value && baseline.value ? benchmarkComparisonCompatible(baseline.value, run.value) : false)
const gpus = computed(() => run.value ? benchmarkGPUs(run.value) : [])
const hints = computed(() => run.value ? benchmarkTuningHints(run.value) : [])
const active = computed(() => run.value?.status === 'QUEUED' || run.value?.status === 'RUNNING')
const currentInstance = computed(() => run.value ? manager.instances.value.find(item => item.id === run.value?.instance_id) : undefined)
const currentModel = computed(() => run.value ? manager.models.value.find(item => item.id === run.value?.model_id) : undefined)

function date(value?: string) { const d = value ? new Date(value) : null; return d && Number.isFinite(d.getTime()) ? d.toLocaleString() : '—' }
function rate(value?: number) { return value === undefined || !Number.isFinite(value) ? '—' : `${value.toLocaleString(undefined, { maximumFractionDigits: 2 })} tok/s` }
function elapsed(value?: number) { return value === undefined ? '—' : value < 1000 ? `${Math.round(value)} ms` : `${(value / 1000).toFixed(value >= 10000 ? 1 : 2)} s` }
function delta(value?: number, reference?: number) { if (value === undefined || reference === undefined || !Number.isFinite(value) || !Number.isFinite(reference)) return '—'; const d = value - reference; if (!reference) return `${d >= 0 ? '+' : ''}${d.toFixed(2)} tok/s`; const p = d / reference * 100; return `${d >= 0 ? '+' : ''}${d.toFixed(2)} tok/s (${p >= 0 ? '+' : ''}${p.toFixed(1)}%)` }
function kind(result: BenchmarkResult) { return result.prompt_tokens > 0 && result.generation_tokens > 0 ? 'Combined turn' : result.prompt_tokens > 0 ? 'Prompt processing' : result.generation_tokens > 0 ? 'Generation' : 'Unknown' }
function bytes(value?: number) { if (value === undefined || !Number.isFinite(value)) return '—'; const u = ['B', 'KiB', 'MiB', 'GiB', 'TiB']; let n = Math.max(0, value), i = 0; while (n >= 1024 && i < u.length - 1) { n /= 1024; i++ }; return `${n >= 10 || i === 0 ? n.toFixed(0) : n.toFixed(1)} ${u[i]}` }

async function load(silent = false) {
  if (!silent) loading.value = true
  error.value = ''
  try {
    run.value = await benchmarks.get(runID.value)
    baseline.value = baselineID.value && baselineID.value !== runID.value ? await benchmarks.get(baselineID.value).catch(() => null) : null
  } catch (value: any) { error.value = value?.data?.error || value?.message || 'Unable to load benchmark.' } finally { if (!silent) loading.value = false }
}
async function cancel() { if (!run.value) return; mutating.value = true; try { await benchmarks.cancel(run.value.id); await load(true) } catch (value: any) { error.value = value?.data?.error || value?.message || 'Unable to cancel benchmark.' } finally { mutating.value = false } }
async function remove() { if (!run.value) return; mutating.value = true; try { await benchmarks.remove(run.value.id); deleteOpen.value = false; await navigateTo('/benchmarks') } catch (value: any) { error.value = value?.data?.error || value?.message || 'Unable to delete benchmark.' } finally { mutating.value = false } }
watch(() => route.query.baseline, () => void load(true))
onMounted(() => { void load(); timer = setInterval(() => { if (active.value) void load(true) }, 3000) })
onUnmounted(() => { if (timer) clearInterval(timer) })
</script>

<template>
  <div class="space-y-6">
    <div class="flex flex-wrap items-start justify-between gap-4">
      <div><p class="text-[length:var(--font-size-kicker)] font-semibold uppercase tracking-[.18em] text-[var(--accent-700)]">BENCHMARK DETAIL</p><div class="mt-2 flex flex-wrap items-center gap-3"><h1 class="text-2xl font-semibold">{{ run?.instance_name_snapshot || runID }}</h1><StatusTag v-if="run" :variant="benchmarkStatusVariant(run.status)">{{ run.status }}</StatusTag></div><p v-if="run" class="mt-2 text-sm text-muted">{{ run.model_name_snapshot }} · {{ run.artifact_snapshot.quantization || 'unknown quantization' }} · {{ date(run.created_at) }}</p></div>
      <div class="flex flex-wrap gap-2"><AppButton to="/benchmarks" intent="secondary">Back to Benchmarks</AppButton><BenchmarkInstanceAction v-if="run?.status === 'COMPLETED' && currentInstance" :instance="currentInstance" :model="currentModel" :reference-run="run" trigger-label="Run another benchmark" /><AppButton v-if="run?.status === 'COMPLETED'" :to="`/benchmarks/compare?ids=${encodeURIComponent(run.id)}`" intent="secondary">Compare</AppButton><AppButton v-if="active" intent="secondary" tone="destructive" :loading="mutating" @click="cancel">Cancel</AppButton><AppButton v-else-if="run" intent="secondary" tone="destructive" :loading="mutating" @click="deleteOpen = true">Delete</AppButton></div>
    </div>
    <Frame v-if="error" class="p-3"><StatusTag variant="failed">Benchmark unavailable</StatusTag><p class="mt-2 text-xs text-muted">{{ error }}</p></Frame>
    <div v-if="loading" class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4"><USkeleton v-for="n in 4" :key="n" class="h-28 w-full" /></div>

    <template v-else-if="run">
      <section class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4" data-testid="benchmark-detail-summary"><Frame class="p-4"><p class="text-xs text-muted">Prompt processing</p><p class="mt-3 font-mono text-xl font-semibold">{{ rate(headline.prompt) }}</p></Frame><Frame class="p-4"><p class="text-xs text-muted">Generation</p><p class="mt-3 font-mono text-xl font-semibold">{{ rate(headline.generation) }}</p></Frame><Frame class="p-4"><p class="text-xs text-muted">Duration</p><p class="mt-3 font-mono text-xl font-semibold">{{ elapsed(duration) }}</p></Frame><Frame class="p-4"><p class="text-xs text-muted">Hardware</p><p class="mt-3 text-sm font-semibold">{{ benchmarkGPULabel(run) }}</p><p class="mt-2 font-mono text-xs text-muted">{{ run.build.runtime_variant || 'unknown backend' }}</p></Frame></section>

      <section v-if="baseline?.status === 'COMPLETED' && run.status === 'COMPLETED'" class="space-y-3" data-testid="benchmark-baseline-delta"><div class="flex flex-wrap items-end justify-between gap-3"><div><h2 class="text-base font-semibold">Compared with baseline</h2><p class="mt-1 text-xs text-muted">Runtime-only changes remain comparable; material inputs are called out.</p></div><AppButton :to="`/benchmarks/compare?ids=${encodeURIComponent(baseline.id)},${encodeURIComponent(run.id)}`" intent="secondary">Full comparison</AppButton></div><Frame class="p-4 space-y-4"><div class="flex flex-wrap gap-2"><StatusTag :variant="comparisonCompatible ? 'ready' : 'neutral'">{{ comparisonCompatible ? 'Comparable rerun' : 'Inputs differ' }}</StatusTag><p class="text-sm">{{ comparisonCompatible ? 'Workload, model artifact, hardware and build identity are compatible.' : comparisonDifferences.filter(value => value !== 'Runtime configuration').join(', ') }}</p></div><div class="grid gap-4 sm:grid-cols-2"><div><p class="text-xs text-muted">Prompt processing</p><p class="font-mono">{{ rate(baselineHeadline.prompt) }} → {{ rate(headline.prompt) }}</p><p class="font-mono text-xs">{{ delta(headline.prompt, baselineHeadline.prompt) }}</p></div><div><p class="text-xs text-muted">Generation</p><p class="font-mono">{{ rate(baselineHeadline.generation) }} → {{ rate(headline.generation) }}</p><p class="font-mono text-xs">{{ delta(headline.generation, baselineHeadline.generation) }}</p></div></div><table v-if="comparisonChanges.length" class="w-full text-left text-xs"><thead><tr><th>Setting</th><th>Baseline</th><th>New run</th></tr></thead><tbody><tr v-for="change in comparisonChanges" :key="change.key"><td class="font-mono">{{ change.label }}</td><td class="font-mono">{{ change.before }}</td><td class="font-mono">{{ change.after }}</td></tr></tbody></table></Frame></section>

      <Frame v-if="run.failure || run.diagnostic_output" class="p-4"><StatusTag :variant="run.failure ? 'failed' : 'neutral'">{{ run.failure ? 'Failure' : 'Diagnostics' }}</StatusTag><p v-if="run.failure" class="mt-2 text-sm">{{ run.failure }}</p><pre v-if="run.diagnostic_output" class="mt-2 max-h-48 overflow-auto whitespace-pre-wrap font-mono text-xs text-muted">{{ run.diagnostic_output }}</pre></Frame>

      <section class="space-y-3"><div><h2 class="text-base font-semibold">Captured Instance configuration and effective runtime</h2><p class="mt-1 text-xs text-muted">Instance snapshot, run-scoped overrides, and the final effective benchmark configuration are immutable history.</p></div><Frame class="p-4 space-y-4"><div><p class="text-xs font-semibold text-muted">Captured Instance configuration</p><dl class="mt-2 grid gap-3 sm:grid-cols-3"><div><dt class="text-xs text-muted">GPU mode</dt><dd class="font-mono">{{ run.instance_config_snapshot.gpu_mode || '—' }}</dd></div><div><dt class="text-xs text-muted">Devices</dt><dd class="font-mono">{{ run.instance_config_snapshot.gpu_devices?.join(', ') || 'Automatic' }}</dd></div><div><dt class="text-xs text-muted">Tensor split</dt><dd class="font-mono">{{ run.instance_config_snapshot.tensor_split || 'Automatic' }}</dd></div></dl></div><table v-if="instanceOptions.length" class="w-full text-left text-xs"><thead><tr><th>Instance option</th><th>Value</th><th>Source</th></tr></thead><tbody><tr v-for="[key, value] in instanceOptions" :key="key"><td class="font-mono">--{{ key }}</td><td class="font-mono">{{ value }}</td><td>{{ run.instance_config_snapshot.sources?.[key] || 'resolved' }}</td></tr></tbody></table><div class="border-t border-[var(--color-divider)] pt-3"><div class="flex gap-2"><p class="text-xs font-semibold text-muted">Effective benchmark configuration</p><StatusTag v-if="Object.keys(run.benchmark_overrides || {}).length" variant="pending">Run overrides</StatusTag></div><dl class="mt-2 grid gap-3 sm:grid-cols-3"><div><dt class="text-xs text-muted">GPU mode</dt><dd class="font-mono">{{ effective?.gpu_mode || '—' }}</dd></div><div><dt class="text-xs text-muted">Devices</dt><dd class="font-mono">{{ effective?.gpu_devices?.join(', ') || 'Automatic' }}</dd></div><div><dt class="text-xs text-muted">Tensor split</dt><dd class="font-mono">{{ effective?.tensor_split || 'Automatic' }}</dd></div></dl></div><table v-if="effectiveOptions.length" class="w-full text-left text-xs"><thead><tr><th>Effective option</th><th>Value</th><th>Source</th></tr></thead><tbody><tr v-for="[key, value] in effectiveOptions" :key="key"><td class="font-mono">--{{ key }}</td><td class="font-mono">{{ value }}</td><td>{{ effective?.sources?.[key] || 'resolved' }}</td></tr></tbody></table><table v-if="runChanges.length" data-testid="benchmark-run-overrides" class="w-full text-left text-xs"><thead><tr><th>Changed setting</th><th>Instance</th><th>Benchmark</th></tr></thead><tbody><tr v-for="change in runChanges" :key="change.key"><td class="font-mono">{{ change.label }}</td><td class="font-mono">{{ change.before }}</td><td class="font-mono">{{ change.after }}</td></tr></tbody></table></Frame></section>

      <section class="space-y-3"><div><h2 class="text-base font-semibold">Workload and command</h2></div><Frame class="p-4 space-y-3"><p class="text-sm font-medium">{{ run.workload_profile.name || run.workload_profile.id || 'Standard' }}</p><p class="font-mono text-xs">P {{ run.workload_profile.prompt_tokens?.join(', ') || '—' }} · G {{ run.workload_profile.generation_tokens?.join(', ') || '—' }} · repetitions {{ run.workload_profile.repetitions }}</p><pre class="overflow-x-auto whitespace-pre-wrap break-all border-t border-[var(--color-divider)] pt-3 font-mono text-xs">{{ run.resolved_argv.join(' ') }}</pre></Frame></section>

      <section class="space-y-3" data-testid="benchmark-tuning-guidance"><div><h2 class="text-base font-semibold">Settings worth testing</h2><p class="mt-1 text-xs text-muted">Change one supported runtime setting at a time.</p></div><Frame class="overflow-hidden"><table class="w-full text-left text-xs"><thead><tr><th class="p-3">Setting</th><th>Mostly affects</th><th>Why test it</th></tr></thead><tbody><tr v-for="hint in hints" :key="hint.key"><td class="p-3 font-mono">--{{ hint.key }}</td><td>{{ hint.impact }}</td><td>{{ hint.reason }}</td></tr></tbody></table></Frame></section>

      <section class="space-y-3"><div><h2 class="text-base font-semibold">Measurements</h2></div><Frame class="overflow-hidden"><div class="overflow-x-auto"><table class="w-full min-w-[820px] text-left text-xs"><thead><tr><th class="p-3">Case</th><th>Type</th><th>Prompt</th><th>Generation</th><th>Depth</th><th>Average</th></tr></thead><tbody><tr v-for="result in run.results || []" :key="`${result.case_index}:${result.case_id}`"><td class="p-3 font-mono">{{ result.case_id }}</td><td>{{ kind(result) }}</td><td class="font-mono">{{ result.prompt_tokens }}</td><td class="font-mono">{{ result.generation_tokens }}</td><td class="font-mono">{{ benchmarkResultDepth(result) }}</td><td class="font-mono">{{ rate(result.average_tokens_per_second) }}</td></tr><tr v-if="!run.results?.length"><td colspan="6" class="p-6 text-center text-muted">No successful measurements were stored for this run.</td></tr></tbody></table></div></Frame></section>

      <section class="space-y-3" data-testid="benchmark-hardware"><div><h2 class="text-base font-semibold">Hardware and build identity</h2></div><Frame class="p-4 space-y-3"><div class="grid gap-3 sm:grid-cols-2"><div v-for="gpu in gpus" :key="gpu.id"><p class="font-medium">{{ gpu.name || gpu.id }}</p><p class="font-mono text-xs text-muted">{{ gpu.backend }} · {{ bytes(gpu.total_bytes) }}</p></div><div><p class="font-medium">{{ run.hardware_snapshot.cpu?.model || 'CPU' }}</p><p class="font-mono text-xs text-muted">{{ run.hardware_snapshot.cpu?.architecture || '—' }} · {{ run.hardware_snapshot.cpu?.logical_threads || '—' }} logical threads</p></div></div><p class="border-t border-[var(--color-divider)] pt-3 font-mono text-xs">LlamaRack {{ run.build.llamarack_version || '—' }} · llama.cpp {{ run.build.llama_cpp_release || run.build.llama_cpp_build || '—' }} · llama-bench {{ run.build.llama_bench_version || '—' }}</p></Frame></section>
    </template>

    <UModal v-model:open="deleteOpen" title="Delete benchmark result"><template #body><p class="text-sm leading-6 text-muted">Delete this historical benchmark and its measurements? This cannot be undone.</p></template><template #footer><div class="flex w-full justify-end gap-2"><UButton color="neutral" variant="outline" @click="deleteOpen = false">Cancel</UButton><AppButton intent="primary" tone="destructive" :loading="mutating" @click="remove">Delete benchmark</AppButton></div></template></UModal>
  </div>
</template>
