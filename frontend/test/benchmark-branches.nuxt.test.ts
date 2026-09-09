import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises } from '@vue/test-utils'
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


enableAutoUnmount(afterEach)
afterEach(() => vi.restoreAllMocks())

function serveRuns(runs: BenchmarkRun[] = [benchmark()]) {
  mocks.request.mockImplementation(async (path: string, options?: { method?: string }) => {
    if (options?.method === 'DELETE') return {}
    if (path.endsWith('/cancel')) return { ...runs[0], status: 'CANCELLED' }
    if (path.startsWith('/api/v1/benchmarks?')) return { items: runs, total: 30 }
    return runs.find(run => path === `/api/v1/benchmarks/${run.id}`) || benchmark()
  })
}

it('filters and paginates history, translating All selections without leaking UI values', async () => {
  serveRuns([benchmark(), benchmark('run-2'), benchmark('run-3')])
  const wrapper = await mountSuspended(BenchmarksPage, { route: '/benchmarks?instance_id=instance-1&model_id=model-1&status=completed' })
  await flushPromises()
  const vm = wrapper.vm as any
  expect(mocks.request).toHaveBeenCalledWith('/api/v1/benchmarks?instance_id=instance-1&model_id=model-1&status=COMPLETED&limit=25&offset=0')
  await vm.page(1)
  expect(vm.offset).toBe(25)
  expect(vm.canPrevious).toBe(true)
  await vm.applyFilters()
  expect(vm.offset).toBe(0)
  expect(mocks.navigateTo).toHaveBeenLastCalledWith({ path: '/benchmarks', query: { instance_id: 'instance-1', model_id: 'model-1', status: 'COMPLETED' } }, { replace: true })
  vm.instanceID = '__all__'; vm.modelID = '__all__'; vm.status = '__all__'
  await vm.applyFilters()
  expect(mocks.request).toHaveBeenLastCalledWith('/api/v1/benchmarks?limit=25&offset=0')
  expect(mocks.navigateTo).toHaveBeenLastCalledWith({ path: '/benchmarks', query: {} }, { replace: true })
  await vm.page(-1)
  expect(vm.offset).toBe(0)
  vm.toggleCompare(benchmark('failed', { status: 'FAILED' }), true)
  expect(vm.selected).toEqual([])
  vm.toggleCompare(benchmark(), true); vm.toggleCompare(benchmark('run-2'), true); vm.toggleCompare(benchmark('run-3'), true)
  expect(vm.selected).toEqual(['run-1', 'run-2'])
  await flushPromises()
  expect(wrapper.get('[data-testid="compare-selected-benchmarks"]').attributes('href')).toContain('run-1,run-2')
  vm.toggleCompare(benchmark(), false)
  expect(vm.selected).toEqual(['run-2'])
  vm.toggleCompare(benchmark('run-2'), 'indeterminate')
  expect(vm.selected).toEqual([])
  vm.selected = ['run-1', 'deleted']
  await vm.load()
  expect(vm.selected).toEqual(['run-1'])
})

it.each([undefined, { message: 'transport failed' }, { data: { error: 'access denied' } }])('shows history load, cancellation and deletion failures (%j)', async (failure) => {
  serveRuns()
  const wrapper = await mountSuspended(BenchmarksPage, { route: '/benchmarks' })
  await flushPromises()
  const vm = wrapper.vm as any
  mocks.request.mockRejectedValue(failure)
  await vm.cancel(benchmark())
  expect(vm.error).toBe(failure?.data?.error || failure?.message || 'Unable to cancel benchmark.')
  expect(vm.mutating).toBe('')
  vm.deleting = benchmark()
  await vm.confirmDelete()
  expect(vm.error).toBe(failure?.data?.error || failure?.message || 'Unable to delete benchmark.')
  expect(vm.deleting.id).toBe('run-1')
  await vm.load()
  expect(wrapper.text()).toContain(failure?.data?.error || failure?.message || 'Unable to load benchmark history.')
  expect(vm.loading).toBe(false)
  vm.deleting = null
  mocks.request.mockClear()
  await vm.confirmDelete()
  expect(mocks.request).not.toHaveBeenCalled()
})

it('renders empty history, missing values and successful cancellation', async () => {
  serveRuns([benchmark('run-1', { status: 'QUEUED', build: {}, artifact_snapshot: { path: '', fingerprint: '', size: 0 }, results: undefined, created_at: 'bad', workload_profile: { repetitions: 1 } as any })])
  const wrapper = await mountSuspended(BenchmarksPage, { route: '/benchmarks' })
  await flushPromises()
  const vm = wrapper.vm as any
  expect(wrapper.text()).toContain('unknown quantization')
  expect(wrapper.text()).toContain('P — · G —')
  await vm.cancel(benchmark())
  expect(vm.error).toBe('')
  expect(mocks.request).toHaveBeenCalledWith('/api/v1/benchmarks/run-1/cancel', { method: 'POST' })
  mocks.request.mockResolvedValue({})
  await vm.load()
  expect(wrapper.text()).toContain('No benchmark runs')
})

it.each([BenchmarksPage, BenchmarkDetailPage])('formats absent, invalid and measured values without inventing measurements', async (component) => {
  serveRuns()
  const wrapper = await mountSuspended(component, { route: '/benchmarks/run-1' })
  await flushPromises()
  const vm = wrapper.vm as any
  expect(vm.formatDate()).toBe('—'); expect(vm.formatDate('bad')).toBe('—')
  expect(vm.formatRate()).toBe('—'); expect(vm.formatRate(NaN)).toBe('—'); expect(vm.formatRate(0)).toBe('0 tok/s')
  expect(vm.formatDuration()).toBe('—'); expect(vm.formatDuration(100)).toBe('100 ms'); expect(vm.formatDuration(1000)).toBe('1.00 s'); expect(vm.formatDuration(10000)).toBe('10.0 s')
  if (vm.bytes) {
    expect(vm.bytes()).toBe('—'); expect(vm.bytes(Infinity)).toBe('—'); expect(vm.bytes(-1)).toBe('0 B'); expect(vm.bytes(1024)).toBe('1.0 KiB'); expect(vm.bytes(10240)).toBe('10 KiB')
  }
})

it('polls active detail and stops polling after completion and unmount', async () => {
  let tick: (() => void) | undefined
  const interval = vi.spyOn(globalThis, 'setInterval').mockImplementation(((fn: () => void) => { tick = fn; return 123 }) as any)
  const clear = vi.spyOn(globalThis, 'clearInterval')
  serveRuns([benchmark('run-1', { status: 'RUNNING' })])
  const wrapper = await mountSuspended(BenchmarkDetailPage, { route: '/benchmarks/run-1' })
  await flushPromises()
  const vm = wrapper.vm as any
  expect(interval).toHaveBeenCalledWith(expect.any(Function), 3000)
  mocks.request.mockClear(); tick!(); await flushPromises()
  expect(mocks.request).toHaveBeenCalledWith('/api/v1/benchmarks/run-1')
  await vm.cancel()
  expect(mocks.request).toHaveBeenCalledWith('/api/v1/benchmarks/run-1/cancel', { method: 'POST' })
  vm.run.status = 'COMPLETED'
  mocks.request.mockClear(); tick!(); await flushPromises()
  expect(mocks.request).not.toHaveBeenCalled()
  wrapper.unmount()
  expect(clear).toHaveBeenCalledWith(123)
})

it.each([undefined, { message: 'offline' }, { data: { error: 'forbidden' } }])('preserves detail on mutation errors and reports load errors (%j)', async (failure) => {
  serveRuns()
  const wrapper = await mountSuspended(BenchmarkDetailPage, { route: '/benchmarks/run-1' })
  await flushPromises()
  const vm = wrapper.vm as any
  mocks.request.mockRejectedValue(failure)
  await vm.cancel()
  expect(vm.error).toBe(failure?.data?.error || failure?.message || 'Unable to cancel benchmark.')
  await vm.remove()
  expect(vm.error).toBe(failure?.data?.error || failure?.message || 'Unable to delete benchmark.')
  expect(vm.mutating).toBe(false)
  await vm.load()
  expect(wrapper.text()).toContain(failure?.data?.error || failure?.message || 'Unable to load benchmark.')
  vm.run = null
  mocks.request.mockClear(); await vm.cancel(); await vm.remove()
  expect(mocks.request).not.toHaveBeenCalled()
})

it('renders sparse historical detail with failure and mapping diagnostics', async () => {
  serveRuns([benchmark('run-1', { instance_name_snapshot: '', created_at: '', failure: 'executable failed', build: {}, hardware_snapshot: { observed: { gpus: [{ id: 'CUDA0' }] } as any }, artifact_snapshot: { path: '', fingerprint: '', size: 0 }, instance_config_snapshot: { schema_version: 1 }, results: [], mapping_differences: [{ key: 'port', severity: 'info', reason: 'ignored' }, { key: 'gpu', severity: 'blocking', reason: 'unsupported' }], workload_profile: {} as any })])
  const wrapper = await mountSuspended(BenchmarkDetailPage, { route: '/benchmarks/run-1' })
  await flushPromises()
  expect(wrapper.text()).toContain('executable failed')
  expect(wrapper.text()).toContain('No successful measurements')
  expect(wrapper.text()).toContain('CPU')
  expect(wrapper.text()).toContain('unsupported')
})

it('chooses a comparison candidate and handles no candidates', async () => {
  serveRuns([benchmark(), benchmark('run-2')])
  const wrapper = await mountSuspended(BenchmarkComparePage, { route: '/benchmarks/compare' })
  await flushPromises()
  const vm = wrapper.vm as any
  expect(vm.left.id).toBe('run-1')
  expect(vm.candidateItems.map((item: any) => item.value)).toEqual(['run-2'])
  await vm.chooseRight()
  expect(mocks.navigateTo).not.toHaveBeenCalled()
  vm.rightID = 'run-2'; await vm.chooseRight()
  expect(mocks.navigateTo).toHaveBeenCalledWith('/benchmarks/compare?ids=run-1,run-2')
  expect(wrapper.text()).toContain('Like-for-like inputs')
  mocks.request.mockResolvedValue({})
  await vm.load()
  expect(vm.left).toBeNull(); expect(vm.cases).toEqual([])
})

it.each(['RUNNING', 'FAILED'])('rejects %s comparison records', async (status) => {
  serveRuns([benchmark(), benchmark('run-2', { status: status as any })])
  const wrapper = await mountSuspended(BenchmarkComparePage, { route: '/benchmarks/compare?ids=run-1,run-2' })
  await flushPromises()
  expect(wrapper.text()).toContain('Only completed benchmark runs can be compared.')
})

it.each([undefined, { message: 'offline' }, { data: { error: 'forbidden' } }])('reports comparison fetch errors (%j)', async (failure) => {
  mocks.request.mockRejectedValue(failure)
  const wrapper = await mountSuspended(BenchmarkComparePage, { route: '/benchmarks/compare?ids=run-1' })
  await flushPromises()
  expect(wrapper.text()).toContain(failure?.data?.error || failure?.message || 'Unable to load benchmark comparison.')
})

it('compares disjoint, mixed, zero and missing measurements with explicit fallbacks', async () => {
  const result = (id: string, prompt: number, gen: number, speed?: number) => ({ case_id: id, case_index: 0, prompt_tokens: prompt, generation_tokens: gen, repetitions: 1, average_tokens_per_second: speed })
  serveRuns([benchmark('run-1', { results: [result('mixed', 10, 2, 0), result('tg', 0, 2, 20), result('pp', 10, 0), result('unmatched', 1, 0)] }), benchmark('run-2', { results: [result('mixed', 10, 2, 5), result('tg', 0, 2, 10), result('pp', 10, 0)] })])
  const wrapper = await mountSuspended(BenchmarkComparePage, { route: '/benchmarks/compare?ids=run-1,run-2' })
  await flushPromises()
  const vm = wrapper.vm as any
  expect(wrapper.text()).toContain('Mixed'); expect(wrapper.text()).toContain('Generation'); expect(wrapper.text()).toContain('Prompt processing')
  expect(wrapper.text()).toContain('+5.00'); expect(wrapper.text()).toContain('-10.00 (-50.0%)')
  expect(vm.rate(Infinity)).toBe('—'); expect(vm.delta(NaN, 3)).toBe('—'); expect(vm.delta(3, undefined)).toBe('—')
  expect(vm.formatDate()).toBe('—'); expect(vm.formatDate('bad')).toBe('—')
  expect(vm.configSummary(benchmark('x', { instance_config_snapshot: { schema_version: 1 } }))).toBe('No captured performance options')
  vm.left.results = undefined; vm.right.results = undefined
  await flushPromises()
  expect(wrapper.text()).toContain('do not share any benchmark case identities')
})

it.each([undefined, { message: 'offline' }, { data: { error: 'deleted' } }])('keeps candidate selection usable after a comparison request fails (%j)', async failure => {
  serveRuns([benchmark(), benchmark('run-2')])
  const wrapper = await mountSuspended(BenchmarkComparePage, { route: '/benchmarks/compare?ids=run-1' })
  await flushPromises()
  const vm = wrapper.vm as any
  vm.rightID = 'run-2'
  mocks.request.mockRejectedValue(failure)
  await vm.chooseRight()
  expect(wrapper.text()).toContain(failure?.data?.error || failure?.message || 'Unable to load benchmark comparison.')
  expect(vm.right).toBeNull()
  expect(mocks.navigateTo).not.toHaveBeenCalled()
  mocks.request.mockResolvedValue(benchmark('run-2', { status: 'RUNNING' }))
  await vm.chooseRight()
  expect(vm.right).toBeNull()
  expect(wrapper.find('[data-testid="benchmark-comparison-differences"]').exists()).toBe(false)
  expect(wrapper.text()).toContain('Only completed benchmark runs can be compared.')
  serveRuns([benchmark(), benchmark('run-2')])
  await vm.chooseRight()
  expect(vm.error).toBe('')
  expect(vm.right.id).toBe('run-2')
})
