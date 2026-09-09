import type { HardwareSnapshot, Instance, Model } from '~/composables/useManager'

export type BenchmarkStatus = 'QUEUED' | 'RUNNING' | 'COMPLETED' | 'FAILED' | 'CANCELLED'

export type BenchmarkWorkloadProfile = {
  id?: string
  version?: number
  prompt_tokens: number[]
  generation_tokens: number[]
  repetitions: number
  warmup: boolean
}

export type BenchmarkWorkloadField = {
  key: string
  label: string
  kind: 'integer-list' | 'integer' | 'boolean' | string
  minimum?: number
  maximum?: number
  advanced?: boolean
  description?: string
}

export type BenchmarkCapabilities = {
  available: boolean
  reason?: string
  version?: string
  fingerprint?: string
  output_format?: string
  supported_options?: string[]
  workload: {
    version: number
    default: BenchmarkWorkloadProfile
    fields: BenchmarkWorkloadField[]
  }
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

export type BenchmarkMappingDifference = {
  key: string
  value?: string
  severity: string
  reason: string
}

export type BenchmarkResult = {
  case_index: number
  case_id: string
  prompt_tokens: number
  generation_tokens: number
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

export type BenchmarkPage = {
  items: BenchmarkRun[]
  total: number
  limit: number
  offset: number
}

export type BenchmarkListFilter = {
  instanceID?: string
  modelID?: string
  status?: BenchmarkStatus | ''
  limit?: number
  offset?: number
}

export type EffectiveLlamaConfig = {
  effective?: {
    values?: Record<string, string>
    sources?: Record<string, string>
  }
  unsupported?: string[]
}

export function benchmarkStatusVariant(status?: BenchmarkStatus): 'ready' | 'pending' | 'neutral' | 'failed' {
  if (status === 'COMPLETED') return 'ready'
  if (status === 'QUEUED' || status === 'RUNNING') return 'pending'
  if (status === 'FAILED') return 'failed'
  return 'neutral'
}

export function benchmarkHeadline(run: BenchmarkRun) {
  const results = run.results || []
  const prompt = results.find(result => result.prompt_tokens > 0 && result.generation_tokens === 0)?.average_tokens_per_second
  const generation = results.find(result => result.generation_tokens > 0 && result.prompt_tokens === 0)?.average_tokens_per_second
  return { prompt, generation }
}

function benchmarkGPUs(run: BenchmarkRun) {
  const gpus = run.hardware_snapshot?.observed?.gpus || []
  if (run.instance_config_snapshot?.options?.['n-gpu-layers'] === '0') return []
  const admitted = run.hardware_snapshot?.selected_devices
  const selected = new Set(admitted || run.instance_config_snapshot?.gpu_devices || [])
  return admitted || selected.size ? gpus.filter(gpu => selected.has(gpu.id)) : gpus
}

export function benchmarkGPULabel(run: BenchmarkRun) {
  const names = benchmarkGPUs(run).map(gpu => gpu.name || gpu.id).filter(Boolean)
  return names.length ? [...new Set(names)].join(', ') : 'CPU / unknown GPU'
}

export function benchmarkDuration(run: BenchmarkRun) {
  if (!run.started_at || !run.completed_at) return undefined
  const start = new Date(run.started_at).getTime()
  const end = new Date(run.completed_at).getTime()
  if (!Number.isFinite(start) || !Number.isFinite(end) || end < start) return undefined
  return end - start
}

export function benchmarkComparisonDifferences(left: BenchmarkRun, right: BenchmarkRun) {
  const differences: string[] = []
  if (left.artifact_snapshot.fingerprint !== right.artifact_snapshot.fingerprint) differences.push('Model artifact')
  if (canonicalBenchmarkValue(left.workload_profile) !== canonicalBenchmarkValue(right.workload_profile)) differences.push('Workload')
  const config = (run: BenchmarkRun) => ({ ...run.instance_config_snapshot, sources: undefined })
  if (canonicalBenchmarkValue(config(left)) !== canonicalBenchmarkValue(config(right))) differences.push('Instance configuration')
  const gpus = (run: BenchmarkRun) => benchmarkGPUs(run).map(({ id, name, backend, total_bytes }) => ({ id, name, backend, total_bytes }))
  if (canonicalBenchmarkValue(gpus(left)) !== canonicalBenchmarkValue(gpus(right))) differences.push('GPU hardware')
  if (canonicalBenchmarkValue(left.hardware_snapshot?.cpu) !== canonicalBenchmarkValue(right.hardware_snapshot?.cpu)) differences.push('CPU hardware')
  if (left.hardware_snapshot?.observed?.ram_total_bytes !== right.hardware_snapshot?.observed?.ram_total_bytes) differences.push('Host memory')
  if (left.build.runtime_variant !== right.build.runtime_variant) differences.push('Runtime backend')
  if (left.build.llama_cpp_build !== right.build.llama_cpp_build) differences.push('llama.cpp build')
  if (left.build.llama_bench_fingerprint !== right.build.llama_bench_fingerprint || left.build.llama_bench_version !== right.build.llama_bench_version) differences.push('llama-bench build')
  return differences
}

function canonicalBenchmarkValue(value: unknown): string {
  if (Array.isArray(value)) return JSON.stringify(value.map(item => canonicalBenchmarkValue(item)))
  if (value && typeof value === 'object') return JSON.stringify(Object.entries(value).filter(([, item]) => item !== undefined).sort(([a], [b]) => a.localeCompare(b)).map(([key, item]) => [key, canonicalBenchmarkValue(item)]))
  return JSON.stringify(value) ?? ''
}

export function useBenchmarks() {
  const manager = useManager()
  const capabilities = useState<BenchmarkCapabilities | null>('benchmark-capabilities', () => null)

  async function loadCapabilities(force = false) {
    if (!force && capabilities.value) return capabilities.value
    capabilities.value = await manager.request<BenchmarkCapabilities>('/api/v1/benchmarks/capabilities')
    return capabilities.value
  }

  function listPath(filter: BenchmarkListFilter = {}) {
    const query = new URLSearchParams()
    if (filter.instanceID) query.set('instance_id', filter.instanceID)
    if (filter.modelID) query.set('model_id', filter.modelID)
    if (filter.status) query.set('status', filter.status)
    query.set('limit', String(filter.limit ?? 50))
    query.set('offset', String(filter.offset ?? 0))
    return `/api/v1/benchmarks?${query.toString()}`
  }

  async function list(filter: BenchmarkListFilter = {}) {
    return manager.request<BenchmarkPage>(listPath(filter))
  }

  async function get(id: string) {
    return manager.request<BenchmarkRun>(`/api/v1/benchmarks/${encodeURIComponent(id)}`)
  }

  async function create(instance: Pick<Instance, 'slug'>, workload?: BenchmarkWorkloadProfile) {
    return manager.request<BenchmarkRun>(`/api/v1/instances/${encodeURIComponent(instance.slug)}/benchmarks`, {
      method: 'POST',
      body: { workload }
    })
  }

  async function cancel(id: string) {
    return manager.request<BenchmarkRun>(`/api/v1/benchmarks/${encodeURIComponent(id)}/cancel`, { method: 'POST' })
  }

  async function remove(id: string) {
    return manager.request(`/api/v1/benchmarks/${encodeURIComponent(id)}`, { method: 'DELETE' })
  }

  async function effectiveConfig(instance: Pick<Instance, 'id' | 'model_id'>) {
    const query = new URLSearchParams({ model_id: instance.model_id, instance_id: instance.id })
    return manager.request<EffectiveLlamaConfig>(`/api/v1/llamacpp/config?${query.toString()}`)
  }

  async function hardware() {
    if (manager.observabilityLive.value?.hardware) return manager.observabilityLive.value.hardware
    return manager.request<HardwareSnapshot>('/api/v1/hardware')
  }

  function modelFor(run: BenchmarkRun, models: Model[]) {
    return models.find(model => model.id === run.model_id)
  }

  return {
    capabilities,
    loadCapabilities,
    list,
    get,
    create,
    cancel,
    remove,
    effectiveConfig,
    hardware,
    modelFor
  }
}
