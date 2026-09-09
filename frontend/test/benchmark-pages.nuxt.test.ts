import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import BenchmarksPage from '~/pages/benchmarks/index.vue'
import BenchmarkDetailPage from '~/pages/benchmarks/[id].vue'
import BenchmarkComparePage from '~/pages/benchmarks/compare.vue'
import InstanceDetailPage from '~/pages/instances/[id]/detail.vue'
import AppSidebar from '~/components/navigation/AppSidebar.vue'
import { useManager, type Instance, type Model } from '~/composables/useManager'
import type { BenchmarkRun } from '~/composables/useBenchmarks'

const mocks = vi.hoisted(() => ({ request: vi.fn(), navigateTo: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))
mockNuxtImport('navigateTo', () => mocks.navigateTo)

const instance: Instance = { id: 'instance-1', slug: 'coder', model_id: 'model-1', name: 'Coder', enabled: true, autoload_enabled: false, always_on: false, priority: 'normal', eviction_enabled: false, idle_unload_seconds: 0, gpu_mode: 'manual', gpu_devices: ['CUDA0'], tensor_split: '1' }
const model: Model = { id: 'model-1', slug: 'model', name: 'Model', gguf_path: 'model.gguf', total_bytes: 100, quantization: 'Q4_K_M', context_length: 4096 }

function benchmark(id = 'run-1', overrides: Partial<BenchmarkRun> = {}): BenchmarkRun {
  return {
    id, instance_id: 'instance-1', instance_slug_snapshot: 'coder', instance_name_snapshot: 'Coder',
    instance_config_snapshot: { schema_version: 1, gpu_mode: 'manual', gpu_devices: ['CUDA0'], tensor_split: '1', options: { 'ctx-size': '4096', threads: '4' }, sources: { 'ctx-size': 'instance' } },
    model_id: 'model-1', model_slug_snapshot: 'model', model_name_snapshot: 'Model', artifact_snapshot: { path: 'model.gguf', fingerprint: 'artifact-a', size: 100, quantization: 'Q4_K_M', architecture: 'llama' },
    workload_profile: { id: 'standard-v1', version: 1, prompt_tokens: [512], generation_tokens: [128], repetitions: 5, warmup: true },
    resolved_argv: ['/app/llama-bench', '--model', '/models/model.gguf'], mapping_differences: [], status: 'COMPLETED',
    created_at: '2026-09-08T20:00:00Z', started_at: '2026-09-08T20:00:01Z', completed_at: '2026-09-08T20:00:03Z',
    build: { llamarack_version: '1.1.0', llamarack_commit: 'abc', runtime_variant: 'cuda', llama_cpp_release: 'b9999', llama_cpp_build: 'b9999', llama_bench_version: 'b9999' },
    hardware_snapshot: { observed: { ram_total_bytes: 2000, ram_available_bytes: 1000, collected_at: '2026-09-08T20:00:00Z', processes: [], gpus: [{ id: 'CUDA0', backend: 'CUDA', index: 0, name: 'RTX Test', total_bytes: 1000, used_bytes: 100, free_bytes: 900, utilization_pct: 0 }] }, cpu: { model: 'Test CPU', logical_threads: 16, architecture: 'amd64', os: 'linux' } },
    results: [{ case_index: 0, case_id: 'pp-512', prompt_tokens: 512, generation_tokens: 0, repetitions: 5, average_tokens_per_second: 100, stddev_tokens_per_second: 1 }, { case_index: 1, case_id: 'tg-128', prompt_tokens: 0, generation_tokens: 128, repetitions: 5, average_tokens_per_second: 50, stddev_tokens_per_second: 0.5 }],
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
  return manager
}

beforeEach(() => {
  mocks.request.mockReset()
  mocks.navigateTo.mockReset()
  useState('benchmark-capabilities').value = null
  resetManager()
})

describe('benchmark pages', () => {
  it.each([
    { component: BenchmarksPage, route: '/benchmarks' },
    { component: BenchmarkDetailPage, route: '/benchmarks/run-1' }
  ])('confirms deletion with the shared destructive treatment on $route', async ({ component, route }) => {
    mocks.request.mockImplementation(async (path: string, options?: { method?: string }) => {
      if (options?.method === 'DELETE') return {}
      if (path.startsWith('/api/v1/benchmarks?')) return { items: [benchmark()], total: 1 }
      if (path === '/api/v1/benchmarks/run-1') return benchmark()
      throw new Error(path)
    })
    const wrapper = await mountSuspended(component, { route, attachTo: document.body })
    try {
      await flushPromises()
      await wrapper.findAll('button').find(button => button.text() === 'Delete')!.trigger('click')
      await vi.waitFor(() => expect(document.body.querySelector('[role="dialog"]')).not.toBeNull())
      const dialog = document.body.querySelector('[role="dialog"]')!
      expect(dialog.textContent).toContain('This cannot be undone.')
      expect(mocks.request.mock.calls.some(([, options]) => options?.method === 'DELETE')).toBe(false)
      const confirm = [...dialog.querySelectorAll('button')].find(button => button.textContent?.trim() === 'Delete benchmark')!
      expect(confirm.className).toContain('bg-[var(--color-danger)]')
      expect(confirm.className).toContain('text-[var(--color-on-danger)]')
      confirm.click()
      await flushPromises()
      expect(mocks.request).toHaveBeenCalledWith('/api/v1/benchmarks/run-1', { method: 'DELETE' })
    } finally {
      wrapper.unmount()
    }
  })

  it('renders global persistent history and navigation', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/benchmarks?')) return { items: [benchmark()], total: 1, limit: 25, offset: 0 }
      throw new Error(path)
    })
    const wrapper = await mountSuspended(BenchmarksPage, { route: '/benchmarks' })
    await flushPromises()
    expect(wrapper.get('[data-testid="benchmark-history-table"]').text()).toContain('RTX Test')
    expect(wrapper.text()).toContain('100 tok/s')
    expect(wrapper.text()).toContain('Coder')
    wrapper.unmount()

    const sidebar = await mountSuspended(AppSidebar, { route: '/benchmarks' })
    expect(sidebar.text()).toContain('Benchmarks')
    expect(sidebar.findAll('a').some(link => link.attributes('href') === '/benchmarks')).toBe(true)
    sidebar.unmount()
  })

  it('renders immutable detail, measurements, build identity and diagnostics', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/benchmarks/run-1') return benchmark('run-1', { diagnostic_output: 'bounded diagnostic output' })
      throw new Error(path)
    })
    const wrapper = await mountSuspended(BenchmarkDetailPage, { route: '/benchmarks/run-1' })
    await flushPromises()
    expect(wrapper.get('[data-testid="benchmark-detail-summary"]').text()).toContain('100 tok/s')
    expect(wrapper.text()).toContain('Captured Instance configuration')
    expect(wrapper.text()).toContain('--ctx-size')
    expect(wrapper.text()).toContain('pp-512')
    expect(wrapper.text()).toContain('bounded diagnostic output')
    expect(wrapper.text()).toContain('b9999')
    wrapper.unmount()
  })

  it('compares matching cases and visibly identifies material input differences', async () => {
    const left = benchmark('run-1')
    const right = benchmark('run-2', {
      artifact_snapshot: { ...benchmark().artifact_snapshot, fingerprint: 'artifact-b' },
      instance_config_snapshot: { ...benchmark().instance_config_snapshot, options: { 'ctx-size': '2048' } },
      build: { ...benchmark().build, llama_cpp_build: 'b10000' },
      results: [{ case_index: 0, case_id: 'pp-512', prompt_tokens: 512, generation_tokens: 0, repetitions: 5, average_tokens_per_second: 120 }]
    })
    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/benchmarks?')) return { items: [left, right], total: 2, limit: 100, offset: 0 }
      if (path === '/api/v1/benchmarks/run-1') return left
      if (path === '/api/v1/benchmarks/run-2') return right
      throw new Error(path)
    })
    const wrapper = await mountSuspended(BenchmarkComparePage, { route: '/benchmarks/compare?ids=run-1,run-2' })
    await flushPromises()
    expect(wrapper.get('[data-testid="benchmark-comparison-differences"]').text()).toContain('Inputs differ')
    expect(wrapper.text()).toContain('Model artifact')
    expect(wrapper.text()).toContain('+20.00 (+20.0%)')
    expect(wrapper.text()).toContain('No overall winner')
    wrapper.unmount()
  })

  it('adds Benchmark action and benchmark history to Instance detail without changing runtime controls', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/settings/general') return { observability_retention_days: { value: 30 } }
      if (path.startsWith('/api/v1/observability/timeseries?')) return { metric: 'test', bucket_seconds: 60, items: [] }
      if (path.startsWith('/api/v1/llamacpp/config?')) return { effective: { values: { 'ctx-size': '4096' }, sources: { 'ctx-size': 'instance' } }, unsupported: [] }
      if (path.startsWith('/api/v1/benchmarks?')) return { items: [], total: 0, limit: 10, offset: 0 }
      throw new Error(path)
    })
    const wrapper = await mountSuspended(InstanceDetailPage, { route: '/instances/coder/detail' })
    await flushPromises()
    expect(wrapper.get('[data-testid="instance-benchmark-action"]').text()).toContain('Benchmark')
    expect(wrapper.get('[data-testid="instance-benchmark-history"]').text()).toContain('Benchmark history')
    expect(wrapper.text()).toContain('Launch')
    expect(wrapper.text()).toContain('Performance history')
    wrapper.unmount()
  })
})
