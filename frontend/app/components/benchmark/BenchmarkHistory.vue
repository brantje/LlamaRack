<script setup lang="ts">
import type { Instance } from '~/composables/useManager'
import type { BenchmarkRun, EffectiveLlamaConfig } from '~/composables/useBenchmarks'
import { benchmarkDuration, benchmarkGPULabel, benchmarkHeadline, benchmarkStatusVariant } from '~/composables/useBenchmarks'

const props = withDefaults(defineProps<{ instance: Instance; limit?: number }>(), { limit: 10 })
const benchmarks = useBenchmarks()
const runs = ref<BenchmarkRun[]>([])
const effective = ref<EffectiveLlamaConfig | null>(null)
const loading = ref(true)
const error = ref('')
let timer: ReturnType<typeof setInterval> | undefined

function formatDate(value?: string) {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isFinite(date.getTime()) ? date.toLocaleString() : '—'
}
function formatRate(value?: number) {
  return value === undefined || !Number.isFinite(value) ? '—' : `${value.toLocaleString(undefined, { maximumFractionDigits: 1 })} tok/s`
}
function formatDuration(value?: number) {
  if (value === undefined) return '—'
  if (value < 1000) return `${Math.round(value)} ms`
  return `${(value / 1000).toFixed(value >= 10_000 ? 1 : 2)} s`
}
function workload(run: BenchmarkRun) {
  const prompt = run.workload_profile.prompt_tokens?.join('/') || '—'
  const generation = run.workload_profile.generation_tokens?.join('/') || '—'
  return `P ${prompt} · G ${generation} · ×${run.workload_profile.repetitions}`
}
function olderConfiguration(run: BenchmarkRun) {
  const captured = run.instance_config_snapshot
  const currentOptions = effective.value?.effective?.values || {}
  if (JSON.stringify(captured.options || {}) !== JSON.stringify(currentOptions)) return true
  if ((captured.gpu_mode || '') !== (props.instance.gpu_mode || '')) return true
  if (JSON.stringify(captured.gpu_devices || []) !== JSON.stringify(props.instance.gpu_devices || [])) return true
  return (captured.tensor_split || '') !== (props.instance.tensor_split || '')
}

async function load(silent = false) {
  if (!silent) loading.value = true
  error.value = ''
  try {
    const [page, config] = await Promise.all([
      benchmarks.list({ instanceID: props.instance.id, limit: props.limit }),
      benchmarks.effectiveConfig(props.instance)
    ])
    runs.value = page.items || []
    effective.value = config
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to load benchmark history.'
  } finally {
    if (!silent) loading.value = false
  }
}

async function cancel(run: BenchmarkRun) {
  error.value = ''
  try {
    await benchmarks.cancel(run.id)
    await load(true)
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to cancel benchmark.'
  }
}

onMounted(() => {
  void load()
  timer = setInterval(() => {
    if (runs.value.some(run => run.status === 'QUEUED' || run.status === 'RUNNING')) void load(true)
  }, 3000)
})
onUnmounted(() => { if (timer) clearInterval(timer) })
</script>

<template>
  <section class="space-y-3" data-testid="instance-benchmark-history">
    <div class="flex flex-wrap items-end justify-between gap-3">
      <div>
        <h2 class="text-base font-semibold">Benchmark history</h2>
        <p class="mt-1 text-xs text-muted">Controlled llama-bench runs captured from this Instance configuration.</p>
      </div>
      <div class="flex gap-2">
        <AppButton to="/benchmarks" intent="secondary" size="xs">All benchmarks</AppButton>
        <AppButton intent="secondary" size="xs" :loading="loading" @click="load()">Refresh</AppButton>
      </div>
    </div>

    <Frame v-if="error" class="p-3"><div class="flex items-start gap-2"><StatusTag variant="failed">Benchmark history unavailable</StatusTag><p class="text-xs text-muted">{{ error }}</p></div></Frame>
    <Frame v-if="loading" class="p-4"><USkeleton class="h-24 w-full" /></Frame>
    <Frame v-else-if="!runs.length" class="p-6"><UEmpty variant="naked" title="No benchmark runs yet" description="Use Benchmark above to measure this saved Instance configuration." /></Frame>
    <Frame v-else class="overflow-hidden">
      <div class="overflow-x-auto">
        <table class="w-full min-w-[920px] text-left text-xs">
          <thead class="border-b border-[var(--color-divider)] text-muted">
            <tr><th class="px-4 py-3 font-medium">Run</th><th class="px-4 py-3 font-medium">Workload</th><th class="px-4 py-3 font-medium">Hardware</th><th class="px-4 py-3 font-medium">Prompt</th><th class="px-4 py-3 font-medium">Generation</th><th class="px-4 py-3 font-medium">Duration</th><th class="px-4 py-3 font-medium text-right">Actions</th></tr>
          </thead>
          <tbody class="divide-y divide-[var(--color-divider)]">
            <tr v-for="run in runs" :key="run.id">
              <td class="px-4 py-3 align-top">
                <div class="flex items-center gap-2"><StatusTag :variant="benchmarkStatusVariant(run.status)">{{ run.status }}</StatusTag><StatusTag v-if="olderConfiguration(run)" variant="neutral">Older config</StatusTag></div>
                <p class="mt-2 font-mono text-[11px]">{{ formatDate(run.created_at) }}</p>
                <p class="mt-1 text-muted">{{ run.artifact_snapshot.quantization || run.model_name_snapshot }}</p>
              </td>
              <td class="px-4 py-3 align-top font-mono">{{ workload(run) }}</td>
              <td class="px-4 py-3 align-top"><p>{{ benchmarkGPULabel(run) }}</p><p class="mt-1 font-mono text-muted">{{ run.build.llama_cpp_build || run.build.llama_bench_version || '—' }}</p></td>
              <td class="px-4 py-3 align-top font-mono tabular-nums">{{ formatRate(benchmarkHeadline(run).prompt) }}</td>
              <td class="px-4 py-3 align-top font-mono tabular-nums">{{ formatRate(benchmarkHeadline(run).generation) }}</td>
              <td class="px-4 py-3 align-top font-mono tabular-nums">{{ formatDuration(benchmarkDuration(run)) }}</td>
              <td class="px-4 py-3 align-top">
                <div class="flex justify-end gap-2">
                  <AppButton :to="`/benchmarks/${encodeURIComponent(run.id)}`" intent="secondary" size="xs">View</AppButton>
                  <AppButton v-if="run.status === 'QUEUED' || run.status === 'RUNNING'" intent="secondary" tone="destructive" size="xs" @click="cancel(run)">Cancel</AppButton>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </Frame>
  </section>
</template>
