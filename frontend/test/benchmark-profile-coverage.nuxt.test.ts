import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import BenchmarkInstanceAction from '~/components/benchmark/BenchmarkInstanceAction.vue'
import BenchmarkComparePage from '~/pages/benchmarks/compare.vue'
import {
  benchmarkConfigChanges,
  benchmarkControlledConfigChange,
  benchmarkTuningHints,
  type BenchmarkRun,
  type BenchmarkWorkloadProfile
} from '~/composables/useBenchmarks'
import { useManager, type Instance, type Model } from '~/composables/useManager'

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

const balanced: BenchmarkWorkloadProfile = {
  id: 'standard-v1', version: 1, name: 'Balanced', description: 'General purpose.', focus: 'Balance both phases.',
  tuning_hints: [{ key: 'batch-size', impact: 'Prompt processing', reason: 'Measure prompt throughput.' }],
  prompt_tokens: [512, 2048], generation_tokens: [128], repetitions: 5, warmup: true
}
const generation: BenchmarkWorkloadProfile = {
  id: 'generation-heavy-v1', version: 1, name: 'Generation-heavy', description: 'Longer generation.', focus: 'Sustained generation.',
  tuning_hints: [{ key: 'n-gpu-layers', impact: 'Generation', reason: 'Measure accelerator offload.' }],
  prompt_tokens: [512], generation_tokens: [128, 512], repetitions: 5, warmup: true
}

const capabilities = {
  available: true,
  version: 'b9999',
  fingerprint: 'bench-fp',
  output_format: 'json',
  supported_options: ['model', 'output', 'n-prompt', 'n-gen', 'repetitions'],
  workload: {
    version: 1,
    default: balanced,
    presets: [balanced, generation],
    fields: [
      { key: 'prompt_tokens', label: 'Prompt processing', kind: 'integer-list', minimum: 1, maximum: 1000000 },
      { key: 'generation_tokens', label: 'Generation', kind: 'integer-list', minimum: 1, maximum: 1000000 },
      { key: 'repetitions', label: 'Repetitions', kind: 'integer', minimum: 1, maximum: 100 },
      { key: 'warmup', label: 'Warm up', kind: 'boolean' }
    ]
  }
}

function run(id = 'run-a', overrides: Partial<BenchmarkRun> = {}): BenchmarkRun {
  return {
    id,
    instance_id: 'instance-1', instance_slug_snapshot: 'coder', instance_name_snapshot: 'Coder',
    instance_config_snapshot: {
      schema_version: 1, gpu_mode: 'manual', gpu_devices: ['CUDA0'], tensor_split: '1',
      options: { 'batch-size': '512', threads: '4' }, sources: { 'batch-size': 'instance', threads: 'instance' }
    },
    model_id: 'model-1', model_slug_snapshot: 'model', model_name_snapshot: 'Model',
    artifact_snapshot: { path: 'model.gguf', fingerprint: 'artifact-a', size: 100, quantization: 'Q4_K_M' },
    workload_profile: structuredClone(balanced),
    resolved_argv: ['/app/llama-bench'], mapping_differences: [], status: 'COMPLETED', created_at: '2026-09-09T09:00:00Z',
    started_at: '2026-09-09T09:00:01Z', completed_at: '2026-09-09T09:00:02Z',
    build: { runtime_variant: 'cuda', llama_cpp_build: 'b9999', llama_bench_version: 'b9999', llama_bench_fingerprint: 'bench-fp' },
    hardware_snapshot: {
      observed: {
        ram_total_bytes: 1024, ram_available_bytes: 512, collected_at: '2026-09-09T09:00:00Z', processes: [],
        gpus: [{ id: 'CUDA0', backend: 'CUDA', index: 0, name: 'RTX Test', total_bytes: 1024, used_bytes: 64, free_bytes: 960, utilization_pct: 0 }]
      },
      cpu: { model: 'Test CPU', logical_threads: 16, effective_threads: 4, architecture: 'amd64', os: 'linux' },
      selected_devices: ['CUDA0']
    },
    results: [
      { case_index: 0, case_id: 'pp-512', prompt_tokens: 512, generation_tokens: 0, repetitions: 5, average_tokens_per_second: 100 },
      { case_index: 1, case_id: 'tg-128', prompt_tokens: 0, generation_tokens: 128, repetitions: 5, average_tokens_per_second: 50 }
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
  manager.observabilityLive.value = null
  return manager
}

beforeEach(() => {
  mocks.request.mockReset()
  mocks.navigateTo.mockReset()
  useState('benchmark-capabilities').value = null
  resetManager()
})

function serveModal(caps: any = capabilities) {
  mocks.request.mockImplementation(async (path: string, options?: any) => {
    if (path === '/api/v1/benchmarks/capabilities') return caps
    if (path.startsWith('/api/v1/llamacpp/config?')) return {
      effective: {
        values: { 'ctx-size': '4096', 'batch-size': '512', threads: '4' },
        sources: { 'ctx-size': 'instance', 'batch-size': 'instance', threads: 'global' }
      }, unsupported: []
    }
    if (path === '/api/v1/hardware') return run().hardware_snapshot.observed
    if (path === '/api/v1/instances/coder/benchmarks' && options?.method === 'POST') return run('created', { status: 'QUEUED', results: [] })
    throw new Error(`unexpected ${path}`)
  })
}

describe('benchmark workload profile UI', () => {
  it('selects backend presets and switches to custom workload fields', async () => {
    serveModal()
    const wrapper = await mountSuspended(BenchmarkInstanceAction, { props: { instance, model } })
    await wrapper.get('[data-testid="instance-benchmark-action"]').trigger('click')
    await flushPromises()
    const vm = wrapper.vm as any

    expect(vm.presetProfiles).toHaveLength(2)
    expect(vm.selectedProfileID).toBe('standard-v1')
    expect(vm.selectedPreset.name).toBe('Balanced')
    expect(vm.profileItems.map((item: any) => item.value)).toEqual(['standard-v1', 'generation-heavy-v1', 'custom-v1'])
    expect(document.body.textContent).toContain('Settings this workload is useful for testing')

    vm.selectedProfileID = 'generation-heavy-v1'
    await flushPromises()
    expect(vm.selectedPreset.name).toBe('Generation-heavy')
    expect(vm.listValues.generation_tokens).toBe('128, 512')
    expect(vm.resolvedWorkload()).toEqual(expect.objectContaining({ id: 'generation-heavy-v1', generation_tokens: [128, 512] }))

    vm.selectedProfileID = 'custom-v1'
    await flushPromises()
    expect(document.body.querySelector('[data-testid="benchmark-custom-workload"]')).not.toBeNull()
    vm.listValues.prompt_tokens = '64, 64, 256'
    vm.listValues.generation_tokens = ''
    vm.scalarValues.repetitions = 3
    vm.booleanValues.warmup = false
    expect(vm.resolvedWorkload()).toEqual(expect.objectContaining({ id: 'custom-v1', prompt_tokens: [64, 64, 256], generation_tokens: [], repetitions: 3, warmup: false }))

    vm.listValues.prompt_tokens = '64, nope'
    expect(() => vm.resolvedWorkload()).toThrow('Prompt processing must be a comma-separated list of whole numbers.')

    expect(vm.bytes()).toBe('—')
    expect(vm.bytes(Infinity)).toBe('—')
    expect(vm.bytes(-4)).toBe('0 B')
    expect(vm.bytes(1024)).toBe('1.0 KiB')
    expect(vm.bytes(10 * 1024)).toBe('10 KiB')
  })

  it('renders unavailable capabilities and preparation errors safely', async () => {
    serveModal({ ...capabilities, available: false, reason: 'llama-bench missing' })
    const unavailable = await mountSuspended(BenchmarkInstanceAction, { props: { instance } })
    await unavailable.get('[data-testid="instance-benchmark-action"]').trigger('click')
    await flushPromises()
    expect(document.body.textContent).toContain('llama-bench missing')
    expect((unavailable.vm as any).canRun).toBe(false)
    unavailable.unmount()

    useState('benchmark-capabilities').value = null
    mocks.request.mockRejectedValueOnce(Object.assign(new Error('offline'), { data: { error: 'capability failed' } }))
    mocks.request.mockResolvedValue({})
    const failed = await mountSuspended(BenchmarkInstanceAction, { props: { instance } })
    await failed.get('[data-testid="instance-benchmark-action"]').trigger('click')
    await flushPromises()
    expect(document.body.textContent).toContain('capability failed')
    expect((failed.vm as any).loading).toBe(false)
  })
})

describe('benchmark tuning comparison presentation', () => {
  function serveComparison(left: BenchmarkRun, right: BenchmarkRun) {
    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/benchmarks?')) return { items: [left, right], total: 2, limit: 100, offset: 0 }
      if (path === `/api/v1/benchmarks/${left.id}`) return left
      if (path === `/api/v1/benchmarks/${right.id}`) return right
      throw new Error(path)
    })
  }

  it('explains a controlled one-setting experiment', async () => {
    const left = run('run-a')
    const right = structuredClone(run('run-b'))
    right.instance_config_snapshot.options!['batch-size'] = '1024'
    serveComparison(left, right)
    const wrapper = await mountSuspended(BenchmarkComparePage, { route: '/benchmarks/compare?ids=run-a,run-b' })
    await flushPromises()
    expect(wrapper.get('[data-testid="benchmark-controlled-change"]').text()).toContain('--batch-size')
    expect(wrapper.text()).toContain('512')
    expect(wrapper.text()).toContain('1024')
    expect(wrapper.text()).toContain('judge it per matching workload case')
  })

  it('refuses causal attribution when several settings changed', async () => {
    const left = run('run-a')
    const right = structuredClone(run('run-b'))
    right.instance_config_snapshot.options!['batch-size'] = '1024'
    right.instance_config_snapshot.options!.threads = '8'
    right.hardware_snapshot.cpu!.effective_threads = 8
    serveComparison(left, right)
    const wrapper = await mountSuspended(BenchmarkComparePage, { route: '/benchmarks/compare?ids=run-a,run-b' })
    await flushPromises()
    expect(wrapper.text()).toContain('Multiple settings changed')
    expect(wrapper.text()).toContain('Change one setting at a time')
    expect(wrapper.find('[data-testid="benchmark-controlled-change"]').exists()).toBe(false)
  })
})

describe('benchmark tuning helper edge branches', () => {
  it('covers custom change labels, non-tunable changes and legacy hint fallbacks', () => {
    const left = run()
    const right = structuredClone(left)
    right.instance_config_snapshot.gpu_mode = 'auto'
    right.instance_config_snapshot.gpu_devices = []
    right.instance_config_snapshot.tensor_split = ''
    right.instance_config_snapshot.options = { ...right.instance_config_snapshot.options, 'ctx-size': '8192', unknown: 'x' }
    const changes = benchmarkConfigChanges(left, right)
    expect(changes.map(change => change.key)).toEqual(expect.arrayContaining(['gpu_mode', 'gpu_devices', 'tensor-split', 'ctx-size', 'unknown']))
    expect(benchmarkControlledConfigChange(left, right)).toBeUndefined()

    const promptOnly = run()
    promptOnly.workload_profile = { prompt_tokens: [1024], generation_tokens: [], repetitions: 1, warmup: true }
    expect(benchmarkTuningHints(promptOnly)[0]?.key).toBe('batch-size')
    const generationOnly = run()
    generationOnly.workload_profile = { prompt_tokens: [], generation_tokens: [128], repetitions: 1, warmup: true }
    expect(benchmarkTuningHints(generationOnly)[0]?.key).toBe('n-gpu-layers')
    const mixed = run()
    mixed.workload_profile = { prompt_tokens: [64], generation_tokens: [64], repetitions: 1, warmup: true }
    expect(benchmarkTuningHints(mixed).length).toBeGreaterThan(1)
  })
})
