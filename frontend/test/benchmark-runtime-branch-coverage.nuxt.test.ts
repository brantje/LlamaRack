import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import BenchmarkInstanceAction from '~/components/benchmark/BenchmarkInstanceAction.vue'
import BenchmarkHistory from '~/components/benchmark/BenchmarkHistory.vue'
import BenchmarkDetailPage from '~/pages/benchmarks/[id].vue'
import { useManager, type Instance, type Model } from '~/composables/useManager'
import type { BenchmarkCapabilities, BenchmarkRun, BenchmarkRuntimeOption, BenchmarkWorkloadProfile } from '~/composables/useBenchmarks'

const mocks = vi.hoisted(() => ({ request: vi.fn(), navigateTo: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))
mockNuxtImport('navigateTo', () => mocks.navigateTo)

enableAutoUnmount(afterEach)
afterEach(() => vi.restoreAllMocks())

const instance: Instance = {
  id: 'instance-1', slug: 'coder', model_id: 'model-1', name: 'Coder', enabled: true,
  autoload_enabled: false, always_on: false, priority: 'normal', eviction_enabled: true,
  idle_unload_seconds: 0, gpu_mode: 'manual', gpu_devices: ['CUDA0'], tensor_split: '1'
}
const model: Model = { id: 'model-1', slug: 'model', name: 'Model', gguf_path: 'model.gguf', total_bytes: 100, quantization: 'Q4_K_M', context_length: 4096 }
const preset: BenchmarkWorkloadProfile = {
  id: 'standard-v1', version: 2, name: 'Standard', prompt_tokens: [512], generation_tokens: [128],
  combined_cases: [{ prompt_tokens: 32, generation_tokens: 8 }], context_depths: [1024], repetitions: 5, warmup: true,
  tuning_hints: [{ key: 'threads', impact: 'CPU', reason: 'test' }]
}
const runtimeOptions: BenchmarkRuntimeOption[] = [
  { key: 'gpu_devices', label: 'GPU devices', kind: 'device-list', instance_field: 'gpu_devices', instance_editable: true, choices: ['CUDA0', 'CUDA1'] },
  { key: 'tensor_split', label: 'Tensor split', kind: 'tensor-split', instance_field: 'tensor_split', instance_editable: true },
  { key: 'threads', label: 'Threads', kind: 'integer', instance_option: 'threads', instance_editable: true, minimum: 1, maximum: 16 },
  { key: 'flash_attention', label: 'Flash attention', kind: 'boolean', instance_option: 'flash-attn', instance_editable: true, default_value: 'yes' },
  { key: 'mode', label: 'Mode', kind: 'enum', instance_option: 'mode', instance_editable: false, default_value: 'auto', choices: ['auto', 'fast'] },
  { key: 'bad_integer', label: 'Bad integer', kind: 'integer', instance_option: 'bad-int', instance_editable: false, default_value: 'bogus' }
]
const capabilities: BenchmarkCapabilities = {
  available: true,
  runtime_options: runtimeOptions,
  workload: {
    version: 2,
    default: preset,
    presets: [preset],
    fields: [
      { key: 'prompt_tokens', label: 'Prompt tokens', kind: 'integer-list' },
      { key: 'generation_tokens', label: 'Generation tokens', kind: 'integer-list' },
      { key: 'combined_cases', label: 'Combined cases', kind: 'token-pair-list' },
      { key: 'context_depths', label: 'Context depths', kind: 'integer-list' },
      { key: 'repetitions', label: 'Repetitions', kind: 'integer', minimum: 1, maximum: 100 },
      { key: 'warmup', label: 'Warm up', kind: 'boolean' }
    ]
  }
}
const effectiveValues = { 'ctx-size': '4096', threads: '4', 'flash-attn': 'off', mode: 'base' }

function benchmark(id = 'run-1', overrides: Partial<BenchmarkRun> = {}): BenchmarkRun {
  return {
    id, instance_id: instance.id, instance_slug_snapshot: instance.slug, instance_name_snapshot: instance.name,
    instance_config_snapshot: { schema_version: 1, gpu_mode: 'manual', gpu_devices: ['CUDA0'], tensor_split: '1', options: { ...effectiveValues }, sources: { threads: 'instance' } },
    effective_benchmark_config: { schema_version: 1, gpu_mode: 'manual', gpu_devices: ['CUDA0'], tensor_split: '1', options: { ...effectiveValues }, sources: { threads: 'instance' } },
    benchmark_overrides: {},
    model_id: model.id, model_slug_snapshot: model.slug, model_name_snapshot: model.name,
    artifact_snapshot: { path: 'model.gguf', fingerprint: 'artifact', size: 100, quantization: 'Q4_K_M' },
    workload_profile: structuredClone(preset), resolved_argv: ['/app/llama-bench'], mapping_differences: [], status: 'COMPLETED',
    created_at: '2026-09-12T10:00:00Z', started_at: '2026-09-12T10:00:01Z', completed_at: '2026-09-12T10:00:03Z',
    build: { runtime_variant: 'cuda', llama_cpp_build: 'b1', llama_bench_version: 'b1' },
    hardware_snapshot: {
      observed: {
        ram_total_bytes: 4 * 1024, ram_available_bytes: 2 * 1024, collected_at: '2026-09-12T10:00:00Z', processes: [],
        gpus: [
          { id: 'CUDA0', backend: 'CUDA', index: 0, name: 'GPU 0', total_bytes: 1024, used_bytes: 100, free_bytes: 924, utilization_pct: 0 },
          { id: 'CUDA1', backend: 'CUDA', index: 1, name: 'GPU 1', total_bytes: 1024, used_bytes: 100, free_bytes: 924, utilization_pct: 0 }
        ]
      },
      cpu: { model: 'CPU', logical_threads: 8, effective_threads: 4, architecture: 'amd64', os: 'linux' },
      selected_devices: ['CUDA0']
    },
    results: [
      { case_index: 0, case_id: 'pp', prompt_tokens: 512, generation_tokens: 0, repetitions: 5, average_tokens_per_second: 100 },
      { case_index: 1, case_id: 'tg', prompt_tokens: 0, generation_tokens: 128, repetitions: 5, average_tokens_per_second: 50 }
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
  manager.instances.value = [instance]
  manager.models.value = [model]
  manager.runtimes.value = { 'model-1': [] }
  manager.runtimeTelemetry.value = {}
  manager.observabilityLive.value = null
}

function serve(history: BenchmarkRun[] = [benchmark()]) {
  mocks.request.mockImplementation(async (path: string, options?: any) => {
    if (path === '/api/v1/benchmarks/capabilities') return capabilities
    if (path.startsWith('/api/v1/llamacpp/config?')) return { effective: { values: { ...effectiveValues }, sources: { threads: 'instance', 'flash-attn': 'instance' } }, unsupported: [] }
    if (path === '/api/v1/hardware') return benchmark().hardware_snapshot.observed
    if (path.startsWith('/api/v1/benchmarks?')) return { items: history, total: history.length, limit: 10, offset: 0 }
    if (path.endsWith('/cancel')) return { ...history[0], status: 'CANCELLED' }
    if (options?.method === 'DELETE') return {}
    if (path === '/api/v1/instances/coder/benchmarks' && options?.method === 'POST') return benchmark('created')
    const id = decodeURIComponent(path.split('/').pop() || '')
    return history.find(run => run.id === id) || benchmark(id || 'run-1')
  })
}

beforeEach(() => {
  mocks.request.mockReset()
  mocks.navigateTo.mockReset()
  useState('benchmark-capabilities').value = null
  resetManager()
})

describe('benchmark runtime branch coverage', () => {
  it('covers workload parsers, runtime value kinds, validation and display fallbacks', async () => {
    serve()
    const wrapper = await mountSuspended(BenchmarkInstanceAction, { props: { instance, model } })
    await wrapper.get('[data-testid="instance-benchmark-action"]').trigger('click')
    await flushPromises()
    const vm = wrapper.vm as any

    expect(vm.formatCombinedCases('nope')).toBe('')
    expect(vm.formatCombinedCases([{ prompt_tokens: 1, generation_tokens: 2 }, { prompt_tokens: 3 }, { generation_tokens: 4 }, {}])).toBe('1:2')
    expect(vm.parseIntegerList('', 'Prompt')).toEqual([])
    expect(vm.parseIntegerList('1, 2', 'Prompt')).toEqual([1, 2])
    expect(() => vm.parseIntegerList('1.5', 'Prompt')).toThrow('whole numbers')
    expect(vm.parseTokenPairList('', 'Cases')).toEqual([])
    expect(vm.parseTokenPairList('10:2, 4:1', 'Cases')).toEqual([{ prompt_tokens: 10, generation_tokens: 2 }, { prompt_tokens: 4, generation_tokens: 1 }])
    expect(() => vm.parseTokenPairList('10', 'Cases')).toThrow('prompt:generation pairs')
    expect(() => vm.parseTokenPairList('0:2', 'Cases')).toThrow('positive whole-number')
    expect(() => vm.parseTokenPairList('2:1.5', 'Cases')).toThrow('positive whole-number')

    expect(vm.runtimeDisplay([])).toBe('Automatic')
    expect(vm.runtimeDisplay(['CUDA0', 'CUDA1'])).toBe('CUDA0, CUDA1')
    expect(vm.runtimeDisplay(undefined)).toBe('Automatic')
    expect(vm.runtimeDisplay(null)).toBe('Automatic')
    expect(vm.runtimeDisplay('')).toBe('Automatic')
    expect(vm.runtimeDisplay(false)).toBe('Off')
    expect(vm.runtimeDisplay(true)).toBe('On')
    expect(vm.runtimeDisplay(4)).toBe('4')
    expect(vm.bytes()).toBe('—')
    expect(vm.bytes(Infinity)).toBe('—')
    expect(vm.bytes(-1)).toBe('0 B')
    expect(vm.bytes(1024)).toBe('1.0 KiB')
    expect(vm.bytes(10240)).toBe('10 KiB')

    expect(vm.instanceRuntimeValue(runtimeOptions[0])).toEqual(['CUDA0'])
    expect(vm.instanceRuntimeValue(runtimeOptions[1])).toBe('1')
    expect(vm.instanceRuntimeValue(runtimeOptions[2])).toBe(4)
    expect(vm.instanceRuntimeValue(runtimeOptions[3])).toBe(false)
    expect(vm.instanceRuntimeValue(runtimeOptions[4])).toBe('base')
    expect(vm.instanceRuntimeValue(runtimeOptions[5])).toBe('')
    expect(vm.instanceRuntimeValue({ key: 'default-bool', label: 'Default bool', kind: 'boolean', instance_editable: false, default_value: 'yes' })).toBe(true)
    expect(vm.instanceRuntimeValue({ key: 'default-text', label: 'Default text', kind: 'enum', instance_editable: false, default_value: 'auto' })).toBe('auto')

    expect(vm.comparableRuntimeValue(runtimeOptions[2], 4)).toBe('4')
    expect(vm.comparableRuntimeValue(runtimeOptions[2], 'bad')).toBe('""')
    expect(vm.comparableRuntimeValue(runtimeOptions[3], 0)).toBe('false')
    expect(vm.comparableRuntimeValue(runtimeOptions[0], ['CUDA1'])).toBe('["CUDA1"]')
    expect(vm.comparableRuntimeValue(runtimeOptions[0], 'CUDA1')).toBe('[]')
    expect(vm.comparableRuntimeValue(runtimeOptions[4], ' fast ')).toBe('"fast"')

    vm.runtimeDraft.threads = 1.5
    expect(() => vm.runtimeOverrides()).toThrow('whole number')
    vm.runtimeDraft.threads = 0
    expect(() => vm.runtimeOverrides()).toThrow('at least 1')
    vm.runtimeDraft.threads = 17
    expect(() => vm.runtimeOverrides()).toThrow('at most 16')
    vm.runtimeDraft.threads = 8
    vm.runtimeDraft.gpu_devices = 'not-an-array'
    vm.runtimeDraft.flash_attention = true
    vm.runtimeDraft.tensor_split = ' 2,1 '
    vm.runtimeDraft.mode = ' fast '
    expect(vm.runtimeOverrides()).toMatchObject({ threads: 8, gpu_devices: [], flash_attention: true, tensor_split: '2,1', mode: 'fast' })
    vm.runtimeDraft.gpu_devices = ['CUDA1']
    expect(vm.runtimeOverrides().gpu_devices).toEqual(['CUDA1'])
    vm.resetRuntime(runtimeOptions[2])
    expect(vm.runtimeDraft.threads).toBe(4)

    expect(vm.currentGPUs).toHaveLength(1)
    await wrapper.setProps({ instance: { ...instance, gpu_devices: [] } })
    expect(vm.currentGPUs).toHaveLength(2)
  })

  it('covers custom and preset workload resolution including validation branches', async () => {
    serve()
    const wrapper = await mountSuspended(BenchmarkInstanceAction, { props: { instance, model } })
    await wrapper.get('[data-testid="instance-benchmark-action"]').trigger('click')
    await flushPromises()
    const vm = wrapper.vm as any

    vm.selectedProfileID = 'custom-v1'
    vm.listValues.prompt_tokens = '64, 128'
    vm.listValues.generation_tokens = ''
    vm.listValues.combined_cases = '32:8, 64:16'
    vm.listValues.context_depths = '1024, 2048'
    vm.scalarValues.repetitions = 3
    vm.booleanValues.warmup = false
    expect(vm.resolvedWorkload()).toMatchObject({
      id: 'custom-v1', version: 2, prompt_tokens: [64, 128], generation_tokens: [],
      combined_cases: [{ prompt_tokens: 32, generation_tokens: 8 }, { prompt_tokens: 64, generation_tokens: 16 }],
      context_depths: [1024, 2048], repetitions: 3, warmup: false
    })

    vm.listValues.prompt_tokens = '1.5'
    expect(() => vm.resolvedWorkload()).toThrow('whole numbers')
    vm.listValues.prompt_tokens = '64'
    vm.listValues.combined_cases = 'broken'
    expect(() => vm.resolvedWorkload()).toThrow('prompt:generation pairs')
    vm.listValues.combined_cases = '0:2'
    expect(() => vm.resolvedWorkload()).toThrow('positive whole-number')
    vm.listValues.combined_cases = ''
    vm.listValues.context_depths = ''
    expect(vm.resolvedWorkload().combined_cases).toEqual([])

    vm.selectedProfileID = 'standard-v1'
    const resolved = vm.resolvedWorkload()
    expect(resolved.prompt_tokens).toEqual([512])
    expect(resolved.combined_cases).toEqual([{ prompt_tokens: 32, generation_tokens: 8 }])
    expect(resolved.context_depths).toEqual([1024])
    expect(resolved.tuning_hints).toHaveLength(1)
    resolved.prompt_tokens.push(999)
    expect(preset.prompt_tokens).toEqual([512])

    const saved = vm.capabilities
    vm.capabilities = null
    expect(() => vm.resolvedWorkload()).toThrow('capabilities are unavailable')
    vm.capabilities = saved
  })

  it.each([
    [undefined, 'Unable to prepare this benchmark.'],
    [{ message: 'offline' }, 'offline'],
    [{ data: { error: 'blocked' } }, 'blocked']
  ])('surfaces every benchmark preparation error fallback (%j)', async (failure, expected) => {
    mocks.request.mockRejectedValue(failure)
    const wrapper = await mountSuspended(BenchmarkInstanceAction, { props: { instance, model } })
    await wrapper.get('[data-testid="instance-benchmark-action"]').trigger('click')
    await flushPromises()
    expect((wrapper.vm as any).error).toBe(expected)
    expect((wrapper.vm as any).loading).toBe(false)
  })

  it.each([
    [undefined, 'Unable to start benchmark.'],
    [{ message: 'launch failed' }, 'launch failed'],
    [{ data: { error: 'no capacity' } }, 'no capacity']
  ])('surfaces every benchmark launch error fallback (%j)', async (failure, expected) => {
    serve()
    const wrapper = await mountSuspended(BenchmarkInstanceAction, { props: { instance, model } })
    await wrapper.get('[data-testid="instance-benchmark-action"]').trigger('click')
    await flushPromises()
    mocks.request.mockRejectedValue(failure)
    await (wrapper.vm as any).runBenchmark()
    expect((wrapper.vm as any).error).toBe(expected)
    expect((wrapper.vm as any).submitting).toBe(false)
  })
})

describe('benchmark history branch coverage', () => {
  it('covers formatting, stale-config detection, polling and cancellation', async () => {
    let tick: (() => void) | undefined
    const interval = vi.spyOn(globalThis, 'setInterval').mockImplementation(((fn: () => void) => { tick = fn; return 123 }) as any)
    const clear = vi.spyOn(globalThis, 'clearInterval')
    const active = benchmark('run-active', { status: 'RUNNING' })
    const completed = benchmark('run-done')
    serve([active, completed])
    const wrapper = await mountSuspended(BenchmarkHistory, { props: { instance, limit: 7 } })
    await flushPromises()
    const vm = wrapper.vm as any

    expect(interval).toHaveBeenCalledWith(expect.any(Function), 3000)
    expect(vm.formatDate()).toBe('—')
    expect(vm.formatDate('bad')).toBe('—')
    expect(vm.formatDate('2026-09-12T10:00:00Z')).not.toBe('—')
    expect(vm.formatRate()).toBe('—')
    expect(vm.formatRate(NaN)).toBe('—')
    expect(vm.formatRate(0)).toBe('0 tok/s')
    expect(vm.formatDuration()).toBe('—')
    expect(vm.formatDuration(100)).toBe('100 ms')
    expect(vm.formatDuration(1000)).toBe('1.00 s')
    expect(vm.formatDuration(10000)).toBe('10.0 s')
    expect(vm.workload(benchmark('sparse', { workload_profile: { repetitions: 1 } as any }))).toBe('P — · G — · ×1')

    expect(vm.olderConfiguration(completed)).toBe(false)
    expect(vm.olderConfiguration(benchmark('options', { instance_config_snapshot: { ...completed.instance_config_snapshot, options: { threads: '8' } } }))).toBe(true)
    expect(vm.olderConfiguration(benchmark('mode', { instance_config_snapshot: { ...completed.instance_config_snapshot, gpu_mode: 'auto' } }))).toBe(true)
    expect(vm.olderConfiguration(benchmark('devices', { instance_config_snapshot: { ...completed.instance_config_snapshot, gpu_devices: [] } }))).toBe(true)
    expect(vm.olderConfiguration(benchmark('split', { instance_config_snapshot: { ...completed.instance_config_snapshot, tensor_split: '2' } }))).toBe(true)

    const listCalls = () => mocks.request.mock.calls.filter(([path]) => String(path).startsWith('/api/v1/benchmarks?')).length
    const before = listCalls()
    tick!()
    await flushPromises()
    expect(listCalls()).toBe(before + 1)
    vm.runs = [completed]
    const quietBefore = listCalls()
    tick!()
    await flushPromises()
    expect(listCalls()).toBe(quietBefore)

    await vm.cancel(active)
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/benchmarks/run-active/cancel', { method: 'POST' })
    mocks.request.mockImplementation(async (path: string) => path.startsWith('/api/v1/benchmarks?') ? {} : { effective: { values: { ...effectiveValues } } })
    await vm.load()
    expect(vm.runs).toEqual([])

    wrapper.unmount()
    expect(clear).toHaveBeenCalledWith(123)
  })

  it.each([
    [undefined, 'Unable to load benchmark history.', 'Unable to cancel benchmark.'],
    [{ message: 'offline' }, 'offline', 'offline'],
    [{ data: { error: 'forbidden' } }, 'forbidden', 'forbidden']
  ])('covers history load and cancel error fallbacks (%j)', async (failure, loadMessage, cancelMessage) => {
    mocks.request.mockRejectedValue(failure)
    const wrapper = await mountSuspended(BenchmarkHistory, { props: { instance } })
    await flushPromises()
    const vm = wrapper.vm as any
    expect(vm.error).toBe(loadMessage)
    expect(vm.loading).toBe(false)
    await vm.cancel(benchmark())
    expect(vm.error).toBe(cancelMessage)
  })
})

describe('benchmark detail branch coverage', () => {
  it('covers formatting and baseline-independent helper edges', async () => {
    serve([benchmark('run-1'), benchmark('run-2')])
    const wrapper = await mountSuspended(BenchmarkDetailPage, { route: '/benchmarks/run-1?baseline=run-2' })
    await flushPromises()
    const vm = wrapper.vm as any

    expect(vm.baseline.id).toBe('run-2')
    expect(vm.formatDate()).toBe('—')
    expect(vm.formatDate('bad')).toBe('—')
    expect(vm.formatRate()).toBe('—')
    expect(vm.formatRate(Infinity)).toBe('—')
    expect(vm.formatRate(12.5)).toContain('12.5')
    expect(vm.formatDuration()).toBe('—')
    expect(vm.formatDuration(500)).toBe('500 ms')
    expect(vm.formatDuration(1000)).toBe('1.00 s')
    expect(vm.formatDuration(10000)).toBe('10.0 s')
    expect(vm.delta()).toBe('—')
    expect(vm.delta(Infinity, 1)).toBe('—')
    expect(vm.delta(5, 0)).toBe('+5.00 tok/s')
    expect(vm.delta(-2, 0)).toBe('-2.00 tok/s')
    expect(vm.delta(12, 10)).toBe('+2.00 tok/s (+20.0%)')
    expect(vm.delta(8, 10)).toBe('-2.00 tok/s (-20.0%)')
    expect(vm.caseKind({ prompt_tokens: 1, generation_tokens: 1 })).toBe('Combined turn')
    expect(vm.caseKind({ prompt_tokens: 1, generation_tokens: 0 })).toBe('Prompt processing')
    expect(vm.caseKind({ prompt_tokens: 0, generation_tokens: 1 })).toBe('Generation')
    expect(vm.caseKind({ prompt_tokens: 0, generation_tokens: 0 })).toBe('Unknown')
    expect(vm.bytes()).toBe('—')
    expect(vm.bytes(NaN)).toBe('—')
    expect(vm.bytes(-1)).toBe('0 B')
    expect(vm.bytes(1024)).toBe('1.0 KiB')
    expect(vm.bytes(10240)).toBe('10 KiB')
    expect(vm.acceleratorRuntime({})).toBe('')
    expect(vm.acceleratorRuntime({ driver_version: '550' })).toBe('driver 550')
    expect(vm.acceleratorRuntime({ max_cuda_version: '12.4' })).toBe('max CUDA 12.4')
    expect(vm.acceleratorRuntime({ driver_version: '550', max_cuda_version: '12.4' })).toBe('driver 550 · max CUDA 12.4')

    vm.run = null
    const calls = mocks.request.mock.calls.length
    await vm.cancel()
    await vm.remove()
    expect(mocks.request.mock.calls.length).toBe(calls)
  })

  it('keeps detail usable when a requested baseline cannot be loaded', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/benchmarks/run-1') return benchmark('run-1')
      if (path === '/api/v1/benchmarks/missing') throw new Error('deleted baseline')
      throw new Error(`unexpected ${path}`)
    })
    const wrapper = await mountSuspended(BenchmarkDetailPage, { route: '/benchmarks/run-1?baseline=missing' })
    await flushPromises()
    const vm = wrapper.vm as any
    expect(vm.run.id).toBe('run-1')
    expect(vm.baseline).toBeNull()
    expect(vm.error).toBe('')
  })

  it('does not refetch the current run as its own baseline', async () => {
    serve([benchmark('run-1')])
    const wrapper = await mountSuspended(BenchmarkDetailPage, { route: '/benchmarks/run-1?baseline=run-1' })
    await flushPromises()
    const vm = wrapper.vm as any
    expect(vm.baseline).toBeNull()
    expect(mocks.request.mock.calls.filter(([path]) => path === '/api/v1/benchmarks/run-1')).toHaveLength(1)
  })
})
