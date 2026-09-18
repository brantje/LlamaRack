import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import BenchmarkInstanceAction from '~/components/benchmark/BenchmarkInstanceAction.vue'
import BenchmarkHistory from '~/components/benchmark/BenchmarkHistory.vue'
import { benchmarkComparisonDifferences, benchmarkDuration, benchmarkGPULabel, benchmarkHeadline, benchmarkStatusVariant, useBenchmarks, type BenchmarkRun } from '~/composables/useBenchmarks'
import { useManager, type Instance, type Model } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn(), navigateTo: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))
mockNuxtImport('navigateTo', () => mocks.navigateTo)

const instance: Instance = {
  id: 'instance-1', slug: 'coder', model_id: 'model-1', name: 'Coder', enabled: true,
  autoload_enabled: false, always_on: false, priority: 'normal', eviction_enabled: true,
  idle_unload_seconds: 0, gpu_mode: 'manual', gpu_devices: ['CUDA0'], tensor_split: '1'
}
const model: Model = { id: 'model-1', slug: 'model', name: 'Model', gguf_path: 'model.gguf', total_bytes: 100, quantization: 'Q4_K_M', context_length: 4096 }
const workload = { id: 'standard-v1', version: 1, prompt_tokens: [512, 2048], generation_tokens: [128], repetitions: 5, warmup: true }
const capabilities = {
  available: true,
  version: 'b9999',
  fingerprint: 'bench-fp',
  output_format: 'json',
  supported_options: ['model', 'output', 'n-prompt', 'n-gen', 'repetitions'],
  workload: {
    version: 1,
    default: workload,
    fields: [
      { key: 'prompt_tokens', label: 'Prompt processing', kind: 'integer-list', minimum: 1, maximum: 1000000 },
      { key: 'generation_tokens', label: 'Generation', kind: 'integer-list', minimum: 1, maximum: 1000000 },
      { key: 'repetitions', label: 'Repetitions', kind: 'integer', minimum: 1, maximum: 100 },
      { key: 'warmup', label: 'Warm up', kind: 'boolean' }
    ]
  }
}

function runFixture(overrides: Partial<BenchmarkRun> = {}): BenchmarkRun {
  return {
    id: 'run-1', instance_id: 'instance-1', instance_slug_snapshot: 'coder', instance_name_snapshot: 'Coder',
    instance_config_snapshot: { schema_version: 1, gpu_mode: 'manual', gpu_devices: ['CUDA0'], tensor_split: '1', options: { 'ctx-size': '4096', threads: '4' }, sources: { 'ctx-size': 'instance', threads: 'instance' } },
    model_id: 'model-1', model_slug_snapshot: 'model', model_name_snapshot: 'Model',
    artifact_snapshot: { path: 'model.gguf', fingerprint: 'artifact-a', size: 100, quantization: 'Q4_K_M', architecture: 'llama' },
    workload_profile: structuredClone(workload),
    resolved_argv: ['/app/llama-bench', '--model', '/models/model.gguf'], mapping_differences: [], status: 'COMPLETED',
    created_at: '2026-09-08T20:00:00Z', started_at: '2026-09-08T20:00:01Z', completed_at: '2026-09-08T20:00:03Z',
    build: { llamarack_version: '1.1.0', runtime_variant: 'cuda', llama_cpp_build: 'b9999', llama_bench_version: 'b9999' },
    hardware_snapshot: { observed: { ram_total_bytes: 1000, ram_available_bytes: 500, collected_at: '2026-09-08T20:00:00Z', processes: [], gpus: [{ id: 'CUDA0', backend: 'CUDA', index: 0, name: 'RTX Test', total_bytes: 1000, used_bytes: 100, free_bytes: 900, utilization_pct: 0 }] }, cpu: { model: 'Test CPU', logical_threads: 16, architecture: 'amd64', os: 'linux' } },
    results: [
      { case_index: 0, case_id: 'pp-512', prompt_tokens: 512, generation_tokens: 0, repetitions: 5, average_tokens_per_second: 123.45 },
      { case_index: 1, case_id: 'tg-128', prompt_tokens: 0, generation_tokens: 128, repetitions: 5, average_tokens_per_second: 44.5 }
    ],
    ...overrides
  }
}

function resetManager() {
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.models.value = [model]
  manager.instances.value = [instance]
  manager.observabilityLive.value = null
  return manager
}

function bodyButton(text: string) {
  const button = [...document.body.querySelectorAll<HTMLButtonElement>('button')].find(item => item.textContent?.trim() === text)
  if (!button) throw new Error(`missing body button ${text}`)
  return button
}

enableAutoUnmount(afterEach)

beforeEach(() => {
  mocks.request.mockReset()
  mocks.navigateTo.mockReset()
  useState('benchmark-capabilities').value = null
  resetManager()
})

describe('benchmark frontend contract', () => {
  it('derives headline, duration, hardware, status and material comparison differences', () => {
    const left = runFixture()
    const right = runFixture({
      id: 'run-2',
      artifact_snapshot: { ...runFixture().artifact_snapshot, fingerprint: 'artifact-b' },
      workload_profile: { ...workload, repetitions: 3 },
      instance_config_snapshot: { ...runFixture().instance_config_snapshot, options: { 'ctx-size': '2048' } },
      build: { ...runFixture().build, runtime_variant: 'cpu', llama_cpp_build: 'b10000' },
      hardware_snapshot: { ...runFixture().hardware_snapshot, observed: { ...runFixture().hardware_snapshot.observed!, gpus: [{ ...runFixture().hardware_snapshot.observed!.gpus[0]!, name: 'Other GPU' }] } }
    })
    expect(benchmarkHeadline(left)).toEqual({ prompt: 123.45, generation: 44.5 })
    expect(benchmarkDuration(left)).toBe(2000)
    expect(benchmarkGPULabel(left)).toBe('RTX Test')
    expect(benchmarkStatusVariant('COMPLETED')).toBe('ready')
    expect(benchmarkStatusVariant('RUNNING')).toBe('pending')
    expect(benchmarkStatusVariant('FAILED')).toBe('failed')
    expect(benchmarkStatusVariant('CANCELLED')).toBe('neutral')
    expect(benchmarkComparisonDifferences(left, right)).toEqual(expect.arrayContaining(['Model artifact', 'Workload', 'Instance configuration', 'GPU hardware', 'Runtime backend', 'llama.cpp build']))
  })

  it('builds bounded API requests for list, create, get, cancel and delete', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path.includes('/api/v1/benchmarks?')) return { items: [], total: 0, limit: 20, offset: 10 }
      return runFixture()
    })
    const api = useBenchmarks()
    await api.list({ instanceID: 'instance-1', modelID: 'model-1', status: 'COMPLETED', limit: 20, offset: 10 })
    expect(mocks.request.mock.calls[0][0]).toContain('instance_id=instance-1')
    expect(mocks.request.mock.calls[0][0]).toContain('status=COMPLETED')
    await api.create(instance, workload)
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/instances/coder/benchmarks', { method: 'POST', body: { workload } })
    await api.get('run/1')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/benchmarks/run%2F1')
    await api.cancel('run/1')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/benchmarks/run%2F1/cancel', { method: 'POST' })
    await api.remove('run/1')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/benchmarks/run%2F1', { method: 'DELETE' })
  })

  it('renders the schema-driven Instance modal and sends only workload', async () => {
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/benchmarks/capabilities') return capabilities
      if (path.startsWith('/api/v1/llamacpp/config?')) return { effective: { values: { 'ctx-size': '4096', threads: '4' }, sources: { 'ctx-size': 'instance', threads: 'instance' } }, unsupported: [] }
      if (path === '/api/v1/hardware') return runFixture().hardware_snapshot.observed
      if (path === '/api/v1/instances/coder/benchmarks' && options?.method === 'POST') return runFixture({ status: 'QUEUED', results: [] })
      throw new Error(`unexpected ${path}`)
    })

    const wrapper = await mountSuspended(BenchmarkInstanceAction, { props: { instance, model } })
    await wrapper.get('[data-testid="instance-benchmark-action"]').trigger('click')
    await flushPromises()
    expect(document.body.textContent).toContain('Instance configuration · read only')
    expect(document.body.textContent).toContain('RTX Test')
    expect(document.body.textContent).toContain('--ctx-size')
    bodyButton('Run benchmark').click()
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/instances/coder/benchmarks', {
      method: 'POST',
      body: { workload: expect.objectContaining({ prompt_tokens: [512, 2048], generation_tokens: [128], repetitions: 5, warmup: true }) }
    })
    expect(mocks.navigateTo).toHaveBeenCalledWith('/benchmarks/run-1')
    wrapper.unmount()
  })

  it('shows backend admission errors instead of pretending the benchmark can run', async () => {
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/benchmarks/capabilities') return capabilities
      if (path.startsWith('/api/v1/llamacpp/config?')) return { effective: { values: {} }, unsupported: [] }
      if (path === '/api/v1/hardware') return runFixture().hardware_snapshot.observed
      if (options?.method === 'POST') throw Object.assign(new Error('conflict'), { data: { error: 'insufficient resources for benchmark' } })
      throw new Error(path)
    })
    const wrapper = await mountSuspended(BenchmarkInstanceAction, { props: { instance, model } })
    await wrapper.get('[data-testid="instance-benchmark-action"]').trigger('click')
    await flushPromises()
    bodyButton('Run benchmark').click()
    await flushPromises()
    expect(document.body.textContent).toContain('insufficient resources for benchmark')
    wrapper.unmount()
  })

  it('renders Instance history, older-config signal and cancellation', async () => {
    const running = runFixture({ status: 'RUNNING', results: [], completed_at: undefined, instance_config_snapshot: { ...runFixture().instance_config_snapshot, options: { 'ctx-size': '2048' } } })
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path.startsWith('/api/v1/benchmarks?')) return { items: [running, runFixture()], total: 2, limit: 10, offset: 0 }
      if (path.startsWith('/api/v1/llamacpp/config?')) return { effective: { values: { 'ctx-size': '4096', threads: '4' }, sources: {} }, unsupported: [] }
      if (path === '/api/v1/benchmarks/run-1/cancel' && options?.method === 'POST') return { ...running, status: 'CANCELLED' }
      throw new Error(`unexpected ${path}`)
    })
    const wrapper = await mountSuspended(BenchmarkHistory, { props: { instance, limit: 10 } })
    await flushPromises()
    expect(wrapper.text()).toContain('Older config')
    expect(wrapper.text()).toContain('RTX Test')
    expect(wrapper.text()).toContain('123.5 tok/s')
    const cancel = wrapper.findAll('button').find(button => button.text().trim() === 'Cancel')
    expect(cancel).toBeTruthy()
    await cancel!.trigger('click')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/benchmarks/run-1/cancel', { method: 'POST' })
    wrapper.unmount()
  })
})

it('compares actual hardware and executable identity while ignoring observations and property ordering', () => {
  const left = runFixture()
  const reordered = runFixture({ instance_config_snapshot: { schema_version: 1, gpu_mode: 'manual', gpu_devices: ['CUDA0'], tensor_split: '1', options: { threads: '4', 'ctx-size': '4096' }, sources: { threads: 'global' } } })
  expect(benchmarkComparisonDifferences(left, reordered)).toEqual([])
  const observed = structuredClone(left)
  observed.hardware_snapshot.observed!.gpus[0]!.free_bytes = 2
  observed.hardware_snapshot.observed!.ram_available_bytes = 1
  expect(benchmarkComparisonDifferences(left, observed)).toEqual([])
  const right = structuredClone(left)
  right.hardware_snapshot.cpu!.effective_threads = 3
  right.hardware_snapshot.observed!.ram_total_bytes = 9999
  right.hardware_snapshot.observed!.gpus[0]!.total_bytes = 9999
  right.build.llama_bench_fingerprint = 'other-build'
  expect(benchmarkComparisonDifferences(left, right)).toEqual(['GPU hardware', 'CPU hardware', 'Host memory', 'llama-bench build'])
  right.hardware_snapshot = {}
  expect(benchmarkComparisonDifferences(left, right)).toContain('CPU hardware')
})

it('labels admitted GPUs rather than every visible device and keeps CPU runs independent of unused GPUs', () => {
  const run = runFixture()
  run.instance_config_snapshot.gpu_devices = []
  run.hardware_snapshot.observed!.gpus.push({ ...run.hardware_snapshot.observed!.gpus[0]!, id: 'CUDA1', name: '' })
  run.hardware_snapshot.selected_devices = ['CUDA1']
  expect(benchmarkGPULabel(run)).toBe('CUDA1')
  run.hardware_snapshot.selected_devices = []
  expect(benchmarkGPULabel(run)).toBe('CPU / unknown GPU')
  delete run.hardware_snapshot.selected_devices
  run.instance_config_snapshot.options!['n-gpu-layers'] = '0'
  expect(benchmarkGPULabel(run)).toBe('CPU / unknown GPU')
  const other = structuredClone(run)
  other.hardware_snapshot.observed!.gpus = []
  expect(benchmarkComparisonDifferences(run, other)).toEqual([])
  run.hardware_snapshot = {}
  expect(benchmarkGPULabel(run)).toBe('CPU / unknown GPU')
})

it('reuses live hardware and cached capabilities and exposes missing-history fallbacks', async () => {
  const manager = resetManager()
  mocks.request.mockResolvedValue(capabilities)
  const api = useBenchmarks()
  await api.loadCapabilities(); await api.loadCapabilities()
  expect(mocks.request).toHaveBeenCalledTimes(1)
  await api.loadCapabilities(true)
  expect(mocks.request).toHaveBeenCalledTimes(2)
  manager.observabilityLive.value = { hardware: runFixture().hardware_snapshot.observed } as any
  expect(await api.hardware()).toEqual(runFixture().hardware_snapshot.observed)
  expect(mocks.request).toHaveBeenCalledTimes(2)
  expect(api.modelFor(runFixture(), [model])).toEqual(model)
  expect(api.modelFor(runFixture(), [])).toBeUndefined()
  expect(benchmarkHeadline(runFixture({ results: undefined }))).toEqual({ prompt: undefined, generation: undefined })
  expect(benchmarkHeadline(runFixture({ results: [{ case_index: 0, case_id: 'pg-10-2', prompt_tokens: 10, generation_tokens: 2, repetitions: 1, average_tokens_per_second: 20 }] }))).toEqual({ prompt: undefined, generation: undefined })
  expect(benchmarkDuration(runFixture({ started_at: 'invalid' }))).toBeUndefined()
  expect(benchmarkDuration(runFixture({ completed_at: '2000-01-01' }))).toBeUndefined()
  expect(benchmarkStatusVariant('QUEUED')).toBe('pending')
  expect(benchmarkStatusVariant()).toBe('neutral')
})
