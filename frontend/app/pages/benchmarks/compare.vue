<script setup lang="ts">
import type { BenchmarkResult, BenchmarkRun } from '~/composables/useBenchmarks'
import { benchmarkComparisonDifferences, benchmarkGPULabel } from '~/composables/useBenchmarks'

const route = useRoute()
const benchmarks = useBenchmarks()
const loading = ref(true)
const error = ref('')
const left = ref<BenchmarkRun | null>(null)
const right = ref<BenchmarkRun | null>(null)
const candidates = ref<BenchmarkRun[]>([])
const rightID = ref('')

const queryIDs = computed(() => {
  const raw = typeof route.query.ids === 'string' ? route.query.ids : ''
  return raw.split(',').map(value => value.trim()).filter(Boolean).slice(0, 2)
})
const candidateItems = computed(() => candidates.value.filter(run => run.id !== left.value?.id).map(run => ({ label: `${run.instance_name_snapshot} · ${formatDate(run.created_at)} · ${benchmarkGPULabel(run)}`, value: run.id })))
const differences = computed(() => left.value && right.value ? benchmarkComparisonDifferences(left.value, right.value) : [])
const cases = computed(() => {
  if (!left.value || !right.value) return []
  const rightByCase = new Map((right.value.results || []).map(result => [result.case_id, result]))
  return (left.value.results || []).flatMap(leftResult => {
    const rightResult = rightByCase.get(leftResult.case_id)
    return rightResult ? [{ id: leftResult.case_id, left: leftResult, right: rightResult }] : []
  })
})

function formatDate(value?: string) {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isFinite(date.getTime()) ? date.toLocaleString() : '—'
}
function rate(value?: number) { return value === undefined || !Number.isFinite(value) ? '—' : `${value.toLocaleString(undefined, { maximumFractionDigits: 2 })} tok/s` }
function delta(a?: number, b?: number) {
  if (a === undefined || b === undefined || !Number.isFinite(a) || !Number.isFinite(b)) return '—'
  const change = b - a
  if (a === 0) return `${change >= 0 ? '+' : ''}${change.toFixed(2)}`
  const percent = change / a * 100
  return `${change >= 0 ? '+' : ''}${change.toFixed(2)} (${percent >= 0 ? '+' : ''}${percent.toFixed(1)}%)`
}
function caseKind(result: BenchmarkResult) {
  if (result.prompt_tokens > 0 && result.generation_tokens === 0) return 'Prompt processing'
  if (result.generation_tokens > 0 && result.prompt_tokens === 0) return 'Generation'
  return 'Mixed'
}
function configSummary(run: BenchmarkRun) {
  const config = run.instance_config_snapshot
  const values = config.options || {}
  const keys = ['ctx-size', 'n-gpu-layers', 'batch-size', 'ubatch-size', 'threads', 'cache-type-k', 'cache-type-v', 'flash-attn']
  return keys.filter(key => values[key] !== undefined).map(key => `--${key} ${values[key]}`).join(' · ') || 'No captured performance options'
}

async function load() {
  loading.value = true
  error.value = ''
  left.value = null
  right.value = null
  try {
    const page = await benchmarks.list({ status: 'COMPLETED', limit: 100 })
    candidates.value = page.items || []
    const ids = queryIDs.value
    if (!ids.length) {
      left.value = candidates.value[0] || null
      right.value = null
      return
    }
    const first = await benchmarks.get(ids[0]!)
    if (first.status !== 'COMPLETED') throw new Error('Only completed benchmark runs can be compared.')
    left.value = first
    if (ids[1]) {
      const second = await benchmarks.get(ids[1])
      if (second.status !== 'COMPLETED') throw new Error('Only completed benchmark runs can be compared.')
      right.value = second
      rightID.value = second.id
    } else {
      right.value = null
      rightID.value = ''
    }
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to load benchmark comparison.'
  } finally {
    loading.value = false
  }
}
async function chooseRight() {
  if (!left.value || !rightID.value) return
  error.value = ''
  try {
    const candidate = await benchmarks.get(rightID.value)
    if (candidate.status !== 'COMPLETED') throw new Error('Only completed benchmark runs can be compared.')
    await navigateTo(`/benchmarks/compare?ids=${encodeURIComponent(left.value.id)},${encodeURIComponent(candidate.id)}`)
    right.value = candidate
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to load benchmark comparison.'
  }
}

watch(() => route.query.ids, () => { void load() })
onMounted(() => { void load() })
</script>

<template>
  <div class="space-y-6">
    <div class="flex flex-wrap items-start justify-between gap-4"><div><p class="text-[length:var(--font-size-kicker)] font-semibold uppercase tracking-[.18em] text-[var(--accent-700)]">BENCHMARK COMPARISON</p><h1 class="mt-2 text-2xl font-semibold">Compare runs</h1><p class="mt-2 max-w-3xl text-sm text-muted">Only matching llama-bench cases are compared numerically. Material input differences are shown before the measurements.</p></div><AppButton to="/benchmarks" intent="secondary">Back to Benchmarks</AppButton></div>

    <Frame v-if="error" class="p-3"><div class="flex items-start gap-2"><StatusTag variant="failed">Comparison unavailable</StatusTag><p class="text-xs text-muted">{{ error }}</p></div></Frame>
    <div v-if="loading" class="space-y-3"><USkeleton class="h-28 w-full" /><USkeleton class="h-48 w-full" /></div>

    <template v-else-if="left">
      <Frame v-if="!right" class="p-4">
        <div class="grid gap-4 sm:grid-cols-[1fr_auto] sm:items-end"><UFormField label="Compare with"><USelect v-model="rightID" :items="candidateItems" value-key="value" label-key="label" class="w-full" placeholder="Select a completed run" /></UFormField><UButton color="primary" :disabled="!rightID" @click="chooseRight">Compare</UButton></div>
      </Frame>

      <template v-else>
        <Frame class="p-4">
          <div class="grid gap-5 lg:grid-cols-2">
            <section><p class="text-xs font-semibold uppercase tracking-[.12em] text-muted">Run A</p><h2 class="mt-2 text-lg font-semibold">{{ left.instance_name_snapshot }}</h2><p class="mt-1 text-sm text-muted">{{ left.model_name_snapshot }} · {{ left.artifact_snapshot.quantization || 'unknown quantization' }}</p><p class="mt-2 font-mono text-xs">{{ formatDate(left.created_at) }} · {{ benchmarkGPULabel(left) }}</p></section>
            <section><p class="text-xs font-semibold uppercase tracking-[.12em] text-muted">Run B</p><h2 class="mt-2 text-lg font-semibold">{{ right.instance_name_snapshot }}</h2><p class="mt-1 text-sm text-muted">{{ right.model_name_snapshot }} · {{ right.artifact_snapshot.quantization || 'unknown quantization' }}</p><p class="mt-2 font-mono text-xs">{{ formatDate(right.created_at) }} · {{ benchmarkGPULabel(right) }}</p></section>
          </div>
        </Frame>

        <Frame class="p-4" data-testid="benchmark-comparison-differences">
          <div class="flex flex-wrap items-center gap-2"><StatusTag :variant="differences.length ? 'neutral' : 'ready'">{{ differences.length ? 'Inputs differ' : 'Like-for-like inputs' }}</StatusTag><p class="text-sm">{{ differences.length ? differences.join(', ') : 'Artifact, workload, captured configuration, GPU identity, backend and llama.cpp build match.' }}</p></div>
          <div v-if="differences.length" class="mt-4 grid gap-4 border-t border-[var(--color-divider)] pt-4 lg:grid-cols-2"><div><p class="text-xs font-medium text-muted">Run A configuration</p><p class="mt-1 text-xs font-mono leading-5">{{ configSummary(left) }}</p></div><div><p class="text-xs font-medium text-muted">Run B configuration</p><p class="mt-1 text-xs font-mono leading-5">{{ configSummary(right) }}</p></div></div>
        </Frame>

        <section class="space-y-3"><div><h2 class="text-base font-semibold">Matching benchmark cases</h2><p class="mt-1 text-xs text-muted">Delta is Run B minus Run A. No overall winner is inferred when inputs differ.</p></div><Frame class="overflow-hidden"><div class="overflow-x-auto"><table class="w-full min-w-[760px] text-left text-xs"><thead class="border-b border-[var(--color-divider)] text-muted"><tr><th class="px-4 py-3 font-medium">Case</th><th class="px-4 py-3 font-medium">Type</th><th class="px-4 py-3 font-medium">Tokens</th><th class="px-4 py-3 font-medium">Run A</th><th class="px-4 py-3 font-medium">Run B</th><th class="px-4 py-3 font-medium">Δ / Δ%</th></tr></thead><tbody class="divide-y divide-[var(--color-divider)]"><tr v-for="entry in cases" :key="entry.id"><td class="px-4 py-3 font-mono">{{ entry.id }}</td><td class="px-4 py-3">{{ caseKind(entry.left) }}</td><td class="px-4 py-3 font-mono">P {{ entry.left.prompt_tokens }} / G {{ entry.left.generation_tokens }}</td><td class="px-4 py-3 font-mono tabular-nums">{{ rate(entry.left.average_tokens_per_second) }}</td><td class="px-4 py-3 font-mono tabular-nums">{{ rate(entry.right.average_tokens_per_second) }}</td><td class="px-4 py-3 font-mono tabular-nums">{{ delta(entry.left.average_tokens_per_second, entry.right.average_tokens_per_second) }}</td></tr><tr v-if="!cases.length"><td colspan="6" class="px-4 py-8 text-center text-muted">These runs do not share any benchmark case identities.</td></tr></tbody></table></div></Frame></section>
      </template>
    </template>
  </div>
</template>
