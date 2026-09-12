import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import BenchmarkDetailPage from '~/pages/benchmarks/[id].vue'
import { benchmarkComparisonDifferences, type BenchmarkRun } from '~/composables/useBenchmarks'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

function run(overrides: Partial<BenchmarkRun> = {}): BenchmarkRun {
  return {
    id: 'run-1',
    instance_id: 'instance-1',
    instance_slug_snapshot: 'coder',
    instance_name_snapshot: 'Coder',
    instance_config_snapshot: {
      schema_version: 1,
      gpu_mode: 'manual',
      gpu_devices: ['CUDA0'],
      options: { 'ctx-size': '4096' },
      sources: { 'ctx-size': 'instance' }
    },
    model_id: 'model-1',
    model_slug_snapshot: 'model',
    model_name_snapshot: 'Model',
    artifact_snapshot: { path: 'model.gguf', fingerprint: 'artifact', size: 100 },
    workload_profile: { prompt_tokens: [512], generation_tokens: [128], repetitions: 5, warmup: true },
    resolved_argv: ['/app/llama-bench'],
    status: 'COMPLETED',
    created_at: '2026-09-12T12:00:00Z',
    started_at: '2026-09-12T12:00:01Z',
    completed_at: '2026-09-12T12:00:02Z',
    build: { runtime_variant: 'cuda', llama_cpp_build: 'b1', llama_bench_version: 'b1' },
    hardware_snapshot: {
      selected_devices: ['CUDA0'],
      observed: {
        ram_total_bytes: 32_000,
        ram_available_bytes: 12_000,
        collected_at: '2026-09-12T12:00:00Z',
        processes: [],
        gpus: [
          { id: 'CUDA0', backend: 'cuda', index: 0, name: 'Selected GPU', total_bytes: 16_000, used_bytes: 4_000, free_bytes: 12_000, utilization_pct: 25, driver_version: '590.44.01', max_cuda_version: '13.1' },
          { id: 'CUDA1', backend: 'cuda', index: 1, name: 'Unselected GPU', total_bytes: 16_000, used_bytes: 2_000, free_bytes: 14_000, utilization_pct: 10, driver_version: '590.44.01', max_cuda_version: '13.1' }
        ]
      },
      cpu: { model: 'Test CPU', logical_threads: 16, effective_threads: 8, architecture: 'amd64', os: 'linux' }
    },
    results: [{ case_index: 0, case_id: 'pp-512', prompt_tokens: 512, generation_tokens: 0, repetitions: 5, average_tokens_per_second: 100 }],
    ...overrides
  }
}

beforeEach(() => {
  mocks.request.mockReset()
  useState('benchmark-capabilities').value = null
})

describe('benchmark immutable hardware identity', () => {
  it('renders only the admitted selected GPU on benchmark detail', async () => {
    const value = run()
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/benchmarks/run-1') return value
      throw new Error(path)
    })

    const wrapper = await mountSuspended(BenchmarkDetailPage, { route: '/benchmarks/run-1' })
    await flushPromises()
    expect(wrapper.text()).toContain('Selected GPU')
    expect(wrapper.text()).not.toContain('Unselected GPU')
    expect(wrapper.text()).toContain('driver 590.44.01')
    expect(wrapper.text()).toContain('max CUDA 13.1')
    wrapper.unmount()
  })

  it('does not present detected GPUs as benchmark hardware for a CPU-only run', async () => {
    const value = run({
      instance_config_snapshot: {
        ...run().instance_config_snapshot,
        gpu_devices: [],
        options: { 'ctx-size': '4096', 'n-gpu-layers': '0' }
      },
      hardware_snapshot: { ...run().hardware_snapshot, selected_devices: [] }
    })
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/benchmarks/run-1') return value
      throw new Error(path)
    })

    const wrapper = await mountSuspended(BenchmarkDetailPage, { route: '/benchmarks/run-1' })
    await flushPromises()
    expect(wrapper.text()).not.toContain('Selected GPU')
    expect(wrapper.text()).not.toContain('Unselected GPU')
    expect(wrapper.get('[data-testid="benchmark-detail-summary"]').text()).toContain('CPU / unknown GPU')
    wrapper.unmount()
  })

  it('compares driver compatibility but ignores volatile GPU observations', () => {
    const left = run()
    const volatileOnly = run({
      id: 'run-2',
      hardware_snapshot: {
        ...run().hardware_snapshot,
        observed: {
          ...run().hardware_snapshot.observed!,
          gpus: run().hardware_snapshot.observed!.gpus.map((gpu, index) => index === 0 ? { ...gpu, free_bytes: 1, used_bytes: 15_999, utilization_pct: 99 } : gpu)
        }
      }
    })
    expect(benchmarkComparisonDifferences(left, volatileOnly)).not.toContain('GPU hardware')
    expect(benchmarkComparisonDifferences(left, volatileOnly)).not.toContain('CUDA driver compatibility')

    const differentDriver = run({
      id: 'run-3',
      hardware_snapshot: {
        ...run().hardware_snapshot,
        observed: {
          ...run().hardware_snapshot.observed!,
          gpus: run().hardware_snapshot.observed!.gpus.map((gpu, index) => index === 0 ? { ...gpu, driver_version: '591.00.00' } : gpu)
        }
      }
    })
    expect(benchmarkComparisonDifferences(left, differentDriver)).toContain('CUDA driver compatibility')
  })
})
