import type { HardwareSnapshot, Instance, Model } from '~/composables/useManager'

export type BenchmarkStatus = 'QUEUED' | 'RUNNING' | 'COMPLETED' | 'FAILED' | 'CANCELLED'
export type BenchmarkTuningHint = { key: string; impact: string; reason: string }
export type BenchmarkCombinedCase = { prompt_tokens: number; generation_tokens: number }
export type BenchmarkWorkloadProfile = {
  id?: string
  version?: number
  name?: string
  description?: string
  focus?: string
  tuning_hints?: BenchmarkTuningHint[]
  prompt_tokens: number[]
  generation_tokens: number[]
  combined_cases?: BenchmarkCombinedCase[]
  context_depths?: number[]
  repetitions: number
  warmup: boolean
}
export type BenchmarkWorkloadField = {
  key: string
  label: string
  kind: 'integer-list' | 'token-pair-list' | 'integer' | 'boolean' | string
  minimum?: number
  maximum?: number
  advanced?: boolean
  description?: string
}
export type BenchmarkRuntimeOption = {
  key: string
  label: string
  kind: 'integer' | 'boolean' | 'enum' | 'device-list' | 'tensor-split' | string
  option?: string
  instance_option?: string
  instance_field?: 'gpu_devices' | 'tensor_split' | string
  instance_editable: boolean
  benchmark_only?: boolean
  choices?: string[]
  default_value?: string
  minimum?: number
  maximum?: number
  description?: string
}
export type BenchmarkRuntimeOverrides = {
  context_size?: number
  gpu_devices?: string[]
  tensor_split?: string
  gpu_layers?: number
  batch_size?: number
  ubatch_size?: number
  threads?: number
  cache_type_k?: string
  cache_type_v?: string
  flash_attention?: boolean
  kv_offload?: boolean
  split_mode?: string
  main_gpu?: number
  n_cpu_moe?: number
  mmap?: boolean
  mlock?: boolean
}
export type BenchmarkCapabilities = {
  available: boolean
  reason?: string
  version?: string
  fingerprint?: string
  output_format?: string
  supported_options?: string[]
  runtime_options?: BenchmarkRuntimeOption[]
  workload: { version: number; default: BenchmarkWorkloadProfile; presets?: BenchmarkWorkloadProfile[]; fields: BenchmarkWorkloadField[] }
}
export type BenchmarkConfigSnapshot = {
  schema_version: number
  gpu_mode?: string
  gpu_devices?: string[]
  tensor_split?: string
  options?: Record<string, string>
  sources?: Record<string, string>
}
export type BenchmarkArtifactSnapshot = {
  path: string
  fingerprint: string
  size: number
  quantization?: string
  architecture?: string
  shard_count?: number
  expected_shards?: number
  files?: Array<{ path: string; size: number; sha256: string }>
  dependencies?: Array<{ kind: string; name: string; quantization?: string; files?: Array<{ path: string; size: number; sha256: string }> }>
}
export type BenchmarkMappingDifference = { key: string; value?: string; severity: string; reason: string }
export type BenchmarkResult = {
  case_index: number
  case_id: string
  prompt_tokens: number
  generation_tokens: number
  context_depth?: number
  repetitions: number
  average_ns?: number
  stddev_ns?: number
  average_tokens_per_second?: number
  stddev_tokens_per_second?: number
  raw_fields?: Record<string, unknown>
}
export type BenchmarkRun = {
  id: string
  instance_id: string
  instance_slug_snapshot: string
  instance_name_snapshot: string
  instance_config_snapshot: BenchmarkConfigSnapshot
  benchmark_overrides?: BenchmarkRuntimeOverrides
  effective_benchmark_config?: BenchmarkConfigSnapshot
  model_id: string
  model_slug_snapshot: string
  model_name_snapshot: string
  artifact_snapshot: BenchmarkArtifactSnapshot
  workload_profile: BenchmarkWorkloadProfile
  resolved_argv: string[]
  mapping_differences?: BenchmarkMappingDifference[]
  status: BenchmarkStatus
  created_at: string
  started_at?: string
  completed_at?: string
  build: {
    llamarack_version?: string
    llamarack_commit?: string
    runtime_variant?: string
    llama_cpp_release?: string
    llama_cpp_build?: string
    llama_bench_version?: string
    llama_bench_fingerprint?: string
  }
  hardware_snapshot: {
    observed?: HardwareSnapshot
    cpu?: { model?: string; logical_threads?: number; effective_threads?: number; architecture?: string; os?: string }
    selected_devices?: string[]
  }
  benchmark_schema_version?: number
  parser_schema_version?: number
  failure?: string
  diagnostic_output?: string
  results?: BenchmarkResult[]
}
export type BenchmarkPage = { items: BenchmarkRun[]; total: number; limit: number; offset: number }
export type BenchmarkListFilter = { instanceID?: string; modelID?: string; status?: BenchmarkStatus | ''; limit?: number; offset?: number }
export type EffectiveLlamaConfig = { effective?: { values?: Record<string, string>; sources?: Record<string, string> }; unsupported?: string[] }
export type BenchmarkConfigChange = { key: string; label: string; before: string; after: string; tunable: boolean }

const legacyControlledTuningOptions = new Set([
  'n-gpu-layers', 'batch-size', 'ubatch-size', 'threads', 'cache-type-k', 'cache-type-v',
  'flash-attn', 'kv-offload', 'no-kv-offload', 'split-mode', 'main-gpu', 'n-cpu-moe',
  'cpu-moe', 'mmap', 'no-mmap', 'mlock', 'override-tensor'
])

const fallbackPromptHints: BenchmarkTuningHint[] = [
  { key: 'batch-size', impact: 'Prompt processing', reason: 'Logical batch size is a primary prefill-throughput tuning knob.' },
  { key: 'ubatch-size', impact: 'Prompt processing', reason: 'Micro-batch size trades prompt throughput against working-memory pressure.' },
  { key: 'flash-attn', impact: 'Prompt + memory', reason: 'Flash attention can change attention-heavy prompt throughput and memory use.' },
  { key: 'n-gpu-layers', impact: 'Prompt processing', reason: 'GPU offload can reduce CPU work when sufficient VRAM is available.' }
]
const fallbackGenerationHints: BenchmarkTuningHint[] = [
  { key: 'n-gpu-layers', impact: 'Generation', reason: 'Keeping repeated model work on the accelerator can improve generation when VRAM permits.' },
  { key: 'tensor-split', impact: 'Multi-GPU generation', reason: 'A better split can reduce imbalance when the model spans multiple GPUs.' },
  { key: 'cache-type-k', impact: 'Generation + KV memory', reason: 'KV precision changes memory pressure and can affect generation throughput.' },
  { key: 'cache-type-v', impact: 'Generation + KV memory', reason: 'KV precision changes memory pressure and can affect generation throughput.' },
  { key: 'flash-attn', impact: 'Generation + memory', reason: 'Flash attention can change attention cost and memory use as context grows.' }
]

export function benchmarkEffectiveConfig(run: BenchmarkRun) { return run.effective_benchmark_config || run.instance_config_snapshot }
export function benchmarkStatusVariant(status?: BenchmarkStatus): 'ready' | 'pending' | 'neutral' | 'failed' {
  if (status === 'COMPLETED') return 'ready'
  if (status === 'QUEUED' || status === 'RUNNING') return 'pending'
  if (status === 'FAILED') return 'failed'
  return 'neutral'
}
export function benchmarkResultDepth(result: BenchmarkResult) {
  if (Number.isFinite(result.context_depth)) return Number(result.context_depth)
  const raw = result.raw_fields?.n_depth
  const value = typeof raw === 'number' ? raw : Number(raw)
  return Number.isFinite(value) ? value : 0
}
export function benchmarkHeadline(run: BenchmarkRun) {
  const results = run.results || []
  const promptRows = results.filter(result => result.prompt_tokens > 0 && result.generation_tokens === 0)
  const generationRows = results.filter(result => result.generation_tokens > 0 && result.prompt_tokens === 0)
  const prompt = (promptRows.find(result => benchmarkResultDepth(result) === 0) || promptRows[0])?.average_tokens_per_second
  const generation = (generationRows.find(result => benchmarkResultDepth(result) === 0) || generationRows[0])?.average_tokens_per_second
  return { prompt, generation }
}
export function benchmarkGPUs(run: BenchmarkRun) {
  const gpus = run.hardware_snapshot?.observed?.gpus || []
  if (benchmarkEffectiveConfig(run)?.options?.['n-gpu-layers'] === '0') return []
  const ordered = (ids: string[]) => ids.flatMap((id) => { const gpu = gpus.find(candidate => candidate.id === id); return gpu ? [gpu] : [] })
  const admitted = run.hardware_snapshot?.selected_devices
  if (admitted !== undefined) return ordered(admitted)
  const configured = benchmarkEffectiveConfig(run)?.gpu_devices || []
  return configured.length ? ordered(configured) : gpus
}
export function benchmarkGPULabel(run: BenchmarkRun) {
  const names = benchmarkGPUs(run).map(gpu => gpu.name || gpu.id).filter(Boolean)
  return names.length ? [...new Set(names)].join(', ') : 'CPU / unknown GPU'
}
export function benchmarkDuration(run: BenchmarkRun) {
  if (!run.started_at || !run.completed_at) return undefined
  const start = new Date(run.started_at).getTime(); const end = new Date(run.completed_at).getTime()
  return Number.isFinite(start) && Number.isFinite(end) && end >= start ? end - start : undefined
}
function workloadComparisonValue(workload: BenchmarkWorkloadProfile) {
  return { prompt_tokens: workload.prompt_tokens, generation_tokens: workload.generation_tokens, combined_cases: workload.combined_cases || [], context_depths: workload.context_depths || [], repetitions: workload.repetitions, warmup: workload.warmup }
}
function benchmarkAcceleratorDriverIdentity(run: BenchmarkRun) {
  return benchmarkGPUs(run).map((gpu) => { const identity = gpu as typeof gpu & { driver_version?: string; max_cuda_version?: string }; return { id: gpu.id, backend: gpu.backend, driver_version: identity.driver_version, max_cuda_version: identity.max_cuda_version } })
}
function staticCPUIdentity(run: BenchmarkRun) {
  const cpu = run.hardware_snapshot?.cpu
  return cpu ? { model: cpu.model, logical_threads: cpu.logical_threads, architecture: cpu.architecture, os: cpu.os } : cpu
}
export function benchmarkComparisonDifferences(left: BenchmarkRun, right: BenchmarkRun) {
  const differences: string[] = []
  if (left.model_id !== right.model_id || left.model_slug_snapshot !== right.model_slug_snapshot) differences.push('Model identity')
  if (left.artifact_snapshot.fingerprint !== right.artifact_snapshot.fingerprint) differences.push('Model artifact')
  if (canonicalBenchmarkValue(workloadComparisonValue(left.workload_profile)) !== canonicalBenchmarkValue(workloadComparisonValue(right.workload_profile))) differences.push('Workload')
  const config = (run: BenchmarkRun) => ({ ...benchmarkEffectiveConfig(run), sources: undefined })
  if (canonicalBenchmarkValue(config(left)) !== canonicalBenchmarkValue(config(right))) differences.push(left.effective_benchmark_config || right.effective_benchmark_config ? 'Runtime configuration' : 'Instance configuration')
  const gpus = (run: BenchmarkRun) => benchmarkGPUs(run).map(({ id, name, backend, total_bytes }) => ({ id, name, backend, total_bytes }))
  if (canonicalBenchmarkValue(gpus(left)) !== canonicalBenchmarkValue(gpus(right))) differences.push('GPU hardware')
  if (canonicalBenchmarkValue(benchmarkAcceleratorDriverIdentity(left)) !== canonicalBenchmarkValue(benchmarkAcceleratorDriverIdentity(right))) differences.push('CUDA driver compatibility')
  const staticCPUChanged = canonicalBenchmarkValue(staticCPUIdentity(left)) !== canonicalBenchmarkValue(staticCPUIdentity(right))
  const configuredThreadsChanged = benchmarkEffectiveConfig(left).options?.threads !== benchmarkEffectiveConfig(right).options?.threads
  const unexplainedEffectiveThreadsChanged = left.hardware_snapshot?.cpu?.effective_threads !== right.hardware_snapshot?.cpu?.effective_threads && !configuredThreadsChanged
  if (staticCPUChanged || unexplainedEffectiveThreadsChanged) differences.push('CPU hardware')
  if (left.hardware_snapshot?.observed?.ram_total_bytes !== right.hardware_snapshot?.observed?.ram_total_bytes) differences.push('Host memory')
  if (left.build.runtime_variant !== right.build.runtime_variant) differences.push('Runtime backend')
  if (left.build.llama_cpp_build !== right.build.llama_cpp_build) differences.push('llama.cpp build')
  if (left.build.llama_bench_fingerprint !== right.build.llama_bench_fingerprint || left.build.llama_bench_version !== right.build.llama_bench_version) differences.push('llama-bench build')
  return differences
}
export function benchmarkTuningHints(run: BenchmarkRun): BenchmarkTuningHint[] {
  if (run.workload_profile.tuning_hints?.length) return run.workload_profile.tuning_hints
  const hasPrompt = (run.workload_profile.prompt_tokens || []).length > 0
  const hasGeneration = (run.workload_profile.generation_tokens || []).length > 0
  if (hasPrompt && !hasGeneration) return fallbackPromptHints
  if (hasGeneration && !hasPrompt) return fallbackGenerationHints
  return [...fallbackPromptHints.slice(0, 3), ...fallbackGenerationHints.slice(0, 3)]
}
function valueLabel(value: unknown) {
  if (Array.isArray(value)) return value.length ? value.join(', ') : 'Automatic'
  if (value === undefined || value === null || value === '') return 'Automatic'
  return String(value)
}
export function benchmarkConfigChanges(left: BenchmarkRun, right: BenchmarkRun): BenchmarkConfigChange[] {
  const changes: BenchmarkConfigChange[] = []
  const a = benchmarkEffectiveConfig(left); const b = benchmarkEffectiveConfig(right)
  const add = (key: string, label: string, before: unknown, after: unknown, tunable: boolean) => {
    if (canonicalBenchmarkValue(before) === canonicalBenchmarkValue(after)) return
    changes.push({ key, label, before: valueLabel(before), after: valueLabel(after), tunable })
  }
  add('gpu_mode', 'GPU mode', a.gpu_mode, b.gpu_mode, false)
  add('gpu_devices', 'GPU devices', a.gpu_devices || [], b.gpu_devices || [], Boolean(left.benchmark_overrides?.gpu_devices || right.benchmark_overrides?.gpu_devices))
  add('tensor-split', '--tensor-split', a.tensor_split, b.tensor_split, left.benchmark_overrides?.tensor_split !== undefined || right.benchmark_overrides?.tensor_split !== undefined)
  const keys = new Set([...Object.keys(a.options || {}), ...Object.keys(b.options || {})])
  for (const key of [...keys].sort()) {
    const newRunTunable = a.sources?.[key] === 'benchmark' || b.sources?.[key] === 'benchmark'
    const legacyRun = !left.effective_benchmark_config && !right.effective_benchmark_config
    add(key, `--${key}`, a.options?.[key], b.options?.[key], newRunTunable || (legacyRun && legacyControlledTuningOptions.has(key)))
  }
  return changes
}
export function benchmarkControlledConfigChange(left: BenchmarkRun, right: BenchmarkRun): BenchmarkConfigChange | undefined {
  const changes = benchmarkConfigChanges(left, right)
  if (changes.length !== 1 || !changes[0]?.tunable) return undefined
  const blockers = benchmarkComparisonDifferences(left, right).filter(value => value !== 'Runtime configuration' && value !== 'Instance configuration')
  return blockers.length === 0 ? changes[0] : undefined
}
export function benchmarkComparisonCompatible(left: BenchmarkRun, right: BenchmarkRun) {
  return benchmarkComparisonDifferences(left, right).filter(value => value !== 'Runtime configuration').length === 0
}
function canonicalBenchmarkValue(value: unknown): string {
  if (Array.isArray(value)) return JSON.stringify(value.map(item => canonicalBenchmarkValue(item)))
  if (value && typeof value === 'object') return JSON.stringify(Object.entries(value).filter(([, item]) => item !== undefined).sort(([a], [b]) => a.localeCompare(b)).map(([key, item]) => [key, canonicalBenchmarkValue(item)]))
  return JSON.stringify(value) ?? ''
}

export function useBenchmarks() {
  const manager = useManager()
  const capabilities = useState<BenchmarkCapabilities | null>('benchmark-capabilities', () => null)
  async function loadCapabilities(force = false) { if (!force && capabilities.value) return capabilities.value; capabilities.value = await manager.request<BenchmarkCapabilities>('/api/v1/benchmarks/capabilities'); return capabilities.value }
  function listPath(filter: BenchmarkListFilter = {}) { const query = new URLSearchParams(); if (filter.instanceID) query.set('instance_id', filter.instanceID); if (filter.modelID) query.set('model_id', filter.modelID); if (filter.status) query.set('status', filter.status); query.set('limit', String(filter.limit ?? 50)); query.set('offset', String(filter.offset ?? 0)); return `/api/v1/benchmarks?${query.toString()}` }
  async function list(filter: BenchmarkListFilter = {}) { return manager.request<BenchmarkPage>(listPath(filter)) }
  async function get(id: string) { return manager.request<BenchmarkRun>(`/api/v1/benchmarks/${encodeURIComponent(id)}`) }
  async function create(instance: Pick<Instance, 'slug'>, workload?: BenchmarkWorkloadProfile, runtimeOverrides: BenchmarkRuntimeOverrides = {}) {
    const body: { workload?: BenchmarkWorkloadProfile; runtime_overrides?: BenchmarkRuntimeOverrides } = { workload }
    if (Object.keys(runtimeOverrides).length) body.runtime_overrides = runtimeOverrides
    return manager.request<BenchmarkRun>(`/api/v1/instances/${encodeURIComponent(instance.slug)}/benchmarks`, { method: 'POST', body })
  }
  async function cancel(id: string) { return manager.request<BenchmarkRun>(`/api/v1/benchmarks/${encodeURIComponent(id)}/cancel`, { method: 'POST' }) }
  async function remove(id: string) { return manager.request(`/api/v1/benchmarks/${encodeURIComponent(id)}`, { method: 'DELETE' }) }
  async function effectiveConfig(instance: Pick<Instance, 'id' | 'model_id'>) { const query = new URLSearchParams({ model_id: instance.model_id, instance_id: instance.id }); return manager.request<EffectiveLlamaConfig>(`/api/v1/llamacpp/config?${query.toString()}`) }
  async function hardware() { if (manager.observabilityLive.value?.hardware) return manager.observabilityLive.value.hardware; return manager.request<HardwareSnapshot>('/api/v1/hardware') }
  function modelFor(run: BenchmarkRun, models: Model[]) { return models.find(model => model.id === run.model_id) }
  return { capabilities, loadCapabilities, list, get, create, cancel, remove, effectiveConfig, hardware, modelFor }
}
