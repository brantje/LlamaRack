import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import BenchmarkInstanceAction from '~/components/benchmark/BenchmarkInstanceAction.vue'
import {
  benchmarkComparisonDifferences,
  benchmarkHeadline,
  benchmarkResultDepth,
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
const model: Model = { id: 'model-1', slug: 'model', name: 'Model', gguf_path: 'model.gguf', total_bytes: 100, quantization: 'Q4_K_M', context_length: 8192 }

const balanced: BenchmarkWorkloadProfile = {
  id: 'standard-v1', version: 2, name: 'Balanced', description: 'General purpose.', focus: 'Balance both phases.',
  prompt_tokens: [512, 2048], generation_tokens: [128], repetitions: 5, warmup: true
}

function run(overrides: Partial<BenchmarkRun> = {}): BenchmarkRun {
  return {
    id: 'run-a', instance_id: instance.id, instance_slug_snapshot: instance.slug, instance_name_snapshot: instance.name,
    instance_config_snapshot: { schema_version: 1, gpu_mode: 'manual', gpu_devices: ['CUDA0'], tensor_split: '1', options: { 'ctx-size': '8192' } },
    model_id: model.id, model_slug_snapshot: model.slug, model_name_snapshot: model.name,
    artifact_snapshot: { path: 'model.gguf', fingerprint: 'artifact', size: 100, quantization: 'Q4_K_M' },
    workload_profile: structuredClone(balanced), resolved_argv: ['/app/llama-bench'], status: 'COMPLETED', created_at: '2026-09-09T10:00:00Z',
    build: { runtime_variant: 'cuda', llama_cpp_build: 'b9999', llama_bench_version: 'b9999', llama_bench_fingerprint: 'bench' },
    hardware_snapshot: { observed: { ram_total_bytes: 1024, ram_available_bytes: 512, collected_at: '2026-09-09T10:00:00Z', processes: [], gpus: [] }, cpu: { model: 'CPU', logical_threads: 8, architecture: 'amd64', os: 'linux' } },
    results: [],
    ...overrides
  }
}

beforeEach(() => {
  mocks.request.mockReset()
  mocks.navigateTo.mockReset()
  useState('benchmark-capabilities').value = null
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.instances.value = [instance]
  manager.models.value = [model]
  manager.observabilityLive.value = null
})

describe('benchmark context depth helpers', () => {
  it('reads normalized and raw llama-bench depth and prefers depth zero for headlines', () => {
    expect(benchmarkResultDepth({ case_index: 0, case_id: 'tg-1', prompt_tokens: 0, generation_tokens: 1, context_depth: 2048, repetitions: 1 })).toBe(2048)
    expect(benchmarkResultDepth({ case_index: 0, case_id: 'tg-1-d4096', prompt_tokens: 0, generation_tokens: 1, repetitions: 1, raw_fields: { n_depth: '4096' } })).toBe(4096)
    expect(benchmarkResultDepth({ case_index: 0, case_id: 'tg-1', prompt_tokens: 0, generation_tokens: 1, repetitions: 1 })).toBe(0)

    const subject = run({ results: [
      { case_index: 0, case_id: 'pp-512-d2048', prompt_tokens: 512, generation_tokens: 0, context_depth: 2048, repetitions: 1, average_tokens_per_second: 80 },
      { case_index: 1, case_id: 'pp-512', prompt_tokens: 512, generation_tokens: 0, repetitions: 1, average_tokens_per_second: 100 },
      { case_index: 2, case_id: 'tg-128-d2048', prompt_tokens: 0, generation_tokens: 128, repetitions: 1, raw_fields: { n_depth: 2048 }, average_tokens_per_second: 40 },
      { case_index: 3, case_id: 'tg-128', prompt_tokens: 0, generation_tokens: 128, repetitions: 1, average_tokens_per_second: 50 }
    ] })
    expect(benchmarkHeadline(subject)).toEqual({ prompt: 100, generation: 50 })
  })

  it('treats a changed context-depth workload as a workload difference', () => {
    const left = run()
    const right = structuredClone(left)
    left.workload_profile.context_depths = [0, 2048]
    right.workload_profile.context_depths = [0, 4096]
    expect(benchmarkComparisonDifferences(left, right)).toContain('Workload')
  })
})

describe('benchmark context depth custom workload', () => {
  it('exposes context depth only when the backend advertises it', async () => {
    const capabilities = {
      available: true,
      version: 'b9999',
      fingerprint: 'bench',
      output_format: 'json',
      supported_options: ['model', 'output', 'n-prompt', 'n-gen', 'n-depth', 'repetitions'],
      workload: {
        version: 2,
        default: balanced,
        presets: [balanced],
        fields: [
          { key: 'prompt_tokens', label: 'Prompt processing', kind: 'integer-list', minimum: 1, maximum: 1000000 },
          { key: 'generation_tokens', label: 'Generation', kind: 'integer-list', minimum: 1, maximum: 1000000 },
          { key: 'context_depths', label: 'Context depths', kind: 'integer-list', minimum: 0, maximum: 1000000, advanced: true },
          { key: 'repetitions', label: 'Repetitions', kind: 'integer', minimum: 1, maximum: 100 },
          { key: 'warmup', label: 'Warm up', kind: 'boolean' }
        ]
      }
    }
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/benchmarks/capabilities') return capabilities
      if (path.startsWith('/api/v1/llamacpp/config?')) return { effective: { values: { 'ctx-size': '8192' }, sources: { 'ctx-size': 'instance' } }, unsupported: [] }
      if (path === '/api/v1/hardware') return { ram_total_bytes: 1024, ram_available_bytes: 512, collected_at: '2026-09-09T10:00:00Z', processes: [], gpus: [] }
      throw new Error(path)
    })

    const wrapper = await mountSuspended(BenchmarkInstanceAction, { props: { instance, model } })
    await wrapper.get('[data-testid="instance-benchmark-action"]').trigger('click')
    await flushPromises()
    const vm = wrapper.vm as any
    vm.selectedProfileID = 'custom-v1'
    await flushPromises()
    expect(document.body.textContent).toContain('Context depths')
    vm.listValues.prompt_tokens = ''
    vm.listValues.generation_tokens = '128'
    vm.listValues.context_depths = '0, 2048, 4096'
    vm.scalarValues.repetitions = 3
    vm.booleanValues.warmup = true
    expect(vm.resolvedWorkload()).toEqual(expect.objectContaining({ version: 2, generation_tokens: [128], context_depths: [0, 2048, 4096] }))
  })
})
