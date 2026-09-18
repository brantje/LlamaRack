import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import BenchmarkInstanceAction from '~/components/benchmark/BenchmarkInstanceAction.vue'
import { useManager, type Instance, type Model } from '~/composables/useManager'
import type { BenchmarkCapabilities, BenchmarkRun, BenchmarkWorkloadProfile } from '~/composables/useBenchmarks'

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
const workload: BenchmarkWorkloadProfile = { id: 'standard-v1', version: 1, prompt_tokens: [512], generation_tokens: [128], repetitions: 5, warmup: true }
const capabilities: BenchmarkCapabilities = {
  available: true,
  runtime_options: [
    { key: 'batch_size', label: 'Batch size', kind: 'integer', option: 'batch-size', instance_option: 'batch-size', instance_editable: true, minimum: 1, maximum: 4194304 },
    { key: 'threads', label: 'Threads', kind: 'integer', option: 'threads', instance_option: 'threads', instance_editable: true, minimum: 1, maximum: 65536 },
    { key: 'flash_attention', label: 'Flash attention', kind: 'boolean', option: 'flash-attn', instance_option: 'flash-attn', instance_editable: true }
  ],
  workload: {
    version: 1,
    default: workload,
    presets: [workload],
    fields: [
      { key: 'prompt_tokens', label: 'Prompt tokens', kind: 'integer-list', minimum: 1 },
      { key: 'generation_tokens', label: 'Generation tokens', kind: 'integer-list', minimum: 1 },
      { key: 'repetitions', label: 'Repetitions', kind: 'integer', minimum: 1, maximum: 100 },
      { key: 'warmup', label: 'Warm up', kind: 'boolean' }
    ]
  }
}

function runFixture(id = 'run-1', runWorkload: BenchmarkWorkloadProfile = workload): BenchmarkRun {
  return {
    id,
    instance_id: instance.id,
    instance_slug_snapshot: instance.slug,
    instance_name_snapshot: instance.name,
    instance_config_snapshot: { schema_version: 1, gpu_mode: 'manual', gpu_devices: ['CUDA0'], tensor_split: '1', options: { 'batch-size': '512', threads: '4', 'flash-attn': 'off' } },
    effective_benchmark_config: { schema_version: 1, gpu_mode: 'manual', gpu_devices: ['CUDA0'], tensor_split: '1', options: { 'batch-size': '512', threads: '4', 'flash-attn': 'off' } },
    benchmark_overrides: {},
    model_id: model.id,
    model_slug_snapshot: model.slug,
    model_name_snapshot: model.name,
    artifact_snapshot: { path: 'model.gguf', fingerprint: 'artifact', size: 100 },
    workload_profile: structuredClone(runWorkload),
    resolved_argv: ['/app/llama-bench'],
    status: 'COMPLETED',
    created_at: '2026-09-12T10:00:00Z',
    build: {},
    hardware_snapshot: { observed: { ram_total_bytes: 1024, ram_available_bytes: 512, collected_at: '2026-09-12T10:00:00Z', processes: [], gpus: [{ id: 'CUDA0', backend: 'CUDA', index: 0, name: 'GPU', total_bytes: 1024, used_bytes: 100, free_bytes: 924, utilization_pct: 0 }] } },
    results: []
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
}

function serve(created = runFixture('created')) {
  mocks.request.mockImplementation(async (path: string, options?: any) => {
    if (path === '/api/v1/benchmarks/capabilities') return capabilities
    if (path.startsWith('/api/v1/llamacpp/config?')) return { effective: { values: { 'batch-size': '512', threads: '4', 'flash-attn': 'off' }, sources: { 'batch-size': 'instance', threads: 'instance', 'flash-attn': 'instance' } }, unsupported: [] }
    if (path === '/api/v1/hardware') return runFixture().hardware_snapshot.observed
    if (path === '/api/v1/instances/coder/benchmarks' && options?.method === 'POST') return created
    throw new Error(`unexpected ${path}`)
  })
}

beforeEach(() => {
  mocks.request.mockReset()
  mocks.navigateTo.mockReset()
  useState('benchmark-capabilities').value = null
  resetManager()
})

describe('benchmark runtime overrides', () => {
  it('populates capability-gated controls from Instance values and sends only changed runtime values', async () => {
    serve()
    const wrapper = await mountSuspended(BenchmarkInstanceAction, { props: { instance, model } })
    await wrapper.get('[data-testid="instance-benchmark-action"]').trigger('click')
    await flushPromises()
    const vm = wrapper.vm as any

    expect(document.body.querySelector('[data-testid="benchmark-runtime-controls"]')?.textContent).toContain('Runtime configuration')
    expect(vm.runtimeDraft.batch_size).toBe(512)
    expect(vm.runtimeDraft.threads).toBe(4)
    expect(vm.runtimeDraft.flash_attention).toBe(false)
    expect(document.body.textContent).not.toContain('Raw argv')

    vm.runtimeDraft.batch_size = 1024
    await wrapper.vm.$nextTick()
    expect(document.body.textContent).toContain('Override')
    expect(vm.runtimeOverrides()).toEqual({ batch_size: 1024 })

    await vm.runBenchmark()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/instances/coder/benchmarks', {
      method: 'POST',
      body: { workload: expect.objectContaining({ prompt_tokens: [512], generation_tokens: [128] }), runtime_overrides: { batch_size: 1024 } }
    })
  })

  it('resetting to the Instance value removes the override', async () => {
    serve()
    const wrapper = await mountSuspended(BenchmarkInstanceAction, { props: { instance, model } })
    await wrapper.get('[data-testid="instance-benchmark-action"]').trigger('click')
    await flushPromises()
    const vm = wrapper.vm as any
    vm.runtimeDraft.threads = 8
    expect(vm.runtimeOverrides()).toEqual({ threads: 8 })
    vm.resetRuntime(capabilities.runtime_options![1])
    expect(vm.runtimeDraft.threads).toBe(4)
    expect(vm.runtimeOverrides()).toEqual({})
  })

  it('reruns preserve the reference workload while runtime defaults still come from the current Instance', async () => {
    const referenceWorkload: BenchmarkWorkloadProfile = { id: 'custom-v1', version: 1, prompt_tokens: [1024], generation_tokens: [256], repetitions: 3, warmup: false }
    const reference = runFixture('baseline', referenceWorkload)
    serve(runFixture('rerun', referenceWorkload))
    const wrapper = await mountSuspended(BenchmarkInstanceAction, { props: { instance, model, referenceRun: reference } })
    await wrapper.get('[data-testid="instance-benchmark-action"]').trigger('click')
    await flushPromises()
    const vm = wrapper.vm as any

    expect(vm.selectedProfileID).toBe('custom-v1')
    expect(vm.listValues.prompt_tokens).toBe('1024')
    expect(vm.listValues.generation_tokens).toBe('256')
    expect(vm.scalarValues.repetitions).toBe(3)
    expect(vm.booleanValues.warmup).toBe(false)
    expect(vm.runtimeDraft.batch_size).toBe(512)

    await vm.runBenchmark()
    expect(mocks.navigateTo).toHaveBeenCalledWith('/benchmarks/rerun?baseline=baseline')
  })
})
