import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountSuspended, mockNuxtImport } from '@nuxt/test-utils/runtime'
import InstanceDetailPage from '~/pages/instances/[id]/detail.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

const gib = 1024 ** 3
const now = Date.parse('2026-09-01T12:00:00.000Z')
const bucket = Math.floor(now / 60_000) * 60_000
const instanceID = 'instance-gemma-uuid'
const instanceSlug = 'gemma-4'

function seed() {
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.models.value = [{ id: 'm1', slug: 'gemma', name: 'Gemma', gguf_path: 'gemma.gguf', total_bytes: 1, context_length: 32768 }]
  manager.instances.value = [{ id: instanceID, slug: instanceSlug, model_id: 'm1', name: 'Gemma 4', enabled: true, autoload_enabled: true, always_on: false, priority: 'normal', eviction_enabled: true, idle_unload_seconds: 0, gpu_mode: 'auto', gpu_devices: [], request_log_mode: 'metadata' }]
  manager.runtimes.value = { m1: [{ instance_id: instanceID, model_id: 'm1', state: 'READY', pid: 308, port: 12001, started_at: new Date(now - 65_000).toISOString() } as any] }
  manager.runtimeTelemetry.value = {
    [instanceID]: {
      instance_id: instanceID, pid: 308, gpu_devices: ['CUDA0'],
      gpus: [{ device_id: 'CUDA0', vram_used_bytes: 14 * gib }],
      vram_used_bytes: 14 * gib, gpu_utilization_pct: 97, cpu_percent: 125, memory_used_bytes: 2.5 * gib,
      collected_at: '2026-08-27T18:30:00Z',
      llama_metrics: {
        prompt_tokens_total: 120, prompt_seconds_total: 4, prompt_tokens_per_second: 30,
        predicted_tokens_total: 210, predicted_seconds_total: 7, predicted_tokens_per_second: 52.9,
        requests_processing: 2, requests_deferred: 1, context_tokens_max: 8192,
        decode_total: 70, busy_slots_per_decode: 1.5,
        spec_draft_tokens_total: 100, spec_accepted_tokens_total: 75, spec_drafts_total: 20,
        spec_accepted_tokens_per_position: { '0': 20, '1': 18 }, spec_acceptance_rate_pct: 75
      }
    } as any
  }
  manager.observabilityLive.value = {
    collected_at: new Date(now).toISOString(),
    hardware: {
      ram_total_bytes: 32 * gib, ram_available_bytes: 16 * gib, collected_at: new Date(now).toISOString(), processes: [],
      gpus: [{ id: 'CUDA0', backend: 'cuda', index: 0, name: 'RTX', total_bytes: 16 * gib, used_bytes: 15 * gib, free_bytes: 1 * gib, utilization_pct: 80 }]
    },
    telemetry: [manager.runtimeTelemetry.value[instanceID]!],
    gateway: { since: 0, requests: 0, successes: 0, errors: 0, active: 0, queued: 0, active_api_keys: 0, prompt_tokens: 0, generated_tokens: 0, total_tokens: 0, latency_ms: {}, ttft_ms: {} },
    requests: []
  }
  return manager
}

function series(metric: string) {
  const values: Record<string, number> = {
    requests: 3,
    prompt_tokens: 40,
    generated_tokens: 80,
    latency: 250,
    latency_p50: 200,
    latency_p95: 500,
    instance_context_tokens_max: 4096
  }
  return { metric, bucket_seconds: 60, items: [{ timestamp: bucket, value: values[metric] ?? 0 }] }
}

beforeEach(() => {
  mocks.request.mockReset()
  vi.spyOn(Date, 'now').mockReturnValue(now)
  mocks.request.mockImplementation(async (path: string) => {
    if (path === '/api/v1/settings/general') return { observability_retention_days: { value: 30, source: 'default', editable: true } }
    if (path.startsWith('/api/v1/observability/timeseries?')) {
      const metric = new URL(`http://x${path}`).searchParams.get('metric') || ''
      return series(metric)
    }
    if (path.startsWith('/api/v1/logs?')) return { instance_id: instanceID, entries: [] }
    return []
  })
  vi.stubGlobal('EventSource', undefined)
  seed()
})

describe('Instance detail page', () => {
  it('renders the live summary, bounded history charts, VRAM map and llama.cpp metrics', async () => {
    const wrapper = await mountSuspended(InstanceDetailPage, { route: `/instances/${instanceSlug}/detail` })
    await flushPromises()
    const text = wrapper.text()
    expect(text).toContain('Gemma 4')
    expect(text).toContain('Live runtime resources and llama.cpp performance for this Instance.')
    expect(text).toContain('READY')
    expect(text).toContain('Requests · 15 min')
    expect(text).toContain('120')
    expect(text).toContain('250 ms')
    expect(text).toContain('8,192 / 32,768')
    expect(text).toContain('CUDA0')
    expect(text).toContain('Global GPU usage')
    expect(text).toContain('97.0%')
    expect(text).toContain('14 GiB')
    expect(text).toContain('52.9 tok/s')
    expect(text).toContain('30.0 tok/s')
    expect(text).toContain('1.50')
    expect(text).toContain('75.0%')
    expect(text).toContain('Instance logs')
    expect(wrapper.get('[data-testid="instance-detail-history-range"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="instance-detail-chart-requests"]').text()).toContain('Requests per minute')
    expect(wrapper.get('[data-testid="instance-detail-chart-tokens"]').text()).toContain('Prompt / input')
    expect(wrapper.get('[data-testid="instance-detail-chart-latency"]').text()).toContain('p95')
    expect(wrapper.get('[data-testid="instance-detail-chart-context"]').text()).toContain('Context utilization')
    expect(wrapper.get('[data-testid="instance-detail-vram-allocation"]').text()).toContain(`${instanceID} (this Instance)`)
    expect(wrapper.get('[data-testid="instance-detail-spec-positions"]').text()).toContain('Position 1: 18')

    const historyCalls = mocks.request.mock.calls.map(([path]) => String(path)).filter(path => path.startsWith('/api/v1/observability/timeseries?'))
    expect(historyCalls).toHaveLength(7)
    for (const path of historyCalls) {
      const params = new URL(`http://x${path}`).searchParams
      expect(params.get('instance_id')).toBe(instanceID)
      expect(params.get('window_seconds')).toBe('900')
      expect(params.get('bucket_seconds')).toBe('60')
    }
    expect(mocks.request).toHaveBeenCalledWith(`/api/v1/logs?instance_id=${instanceID}&limit=2000`)
  })

  it('shows stopped-state guidance and leaves missing history as gaps/zeros', async () => {
    const manager = seed()
    manager.runtimes.value = { m1: [{ instance_id: instanceID, model_id: 'm1', state: 'UNLOADED' }] }
    manager.runtimeTelemetry.value = {}
    manager.observabilityLive.value = null
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/settings/general') return { observability_retention_days: { value: 1 } }
      if (path.startsWith('/api/v1/observability/timeseries?')) return { metric: 'empty', bucket_seconds: 60, items: [] }
      if (path.startsWith('/api/v1/logs?')) return { instance_id: instanceID, entries: [] }
      return []
    })
    const wrapper = await mountSuspended(InstanceDetailPage, { route: `/instances/${instanceSlug}/detail` })
    await flushPromises()
    expect(wrapper.text()).toContain('llama.cpp metrics unavailable while stopped')
    expect(wrapper.text()).toContain('Launch the Instance to populate throughput')
    expect(wrapper.text()).toContain('No retained samples in this range.')
    expect(wrapper.get('[data-testid="instance-detail-vram-allocation"]').text()).toContain('No GPU allocation is available')
  })

  it('shows startup backoff and last error on the runtime snapshot', async () => {
    const manager = seed()
    manager.runtimes.value = { m1: [{
      instance_id: instanceID,
      model_id: 'm1',
      state: 'FAILED',
      last_error: 'CUDA allocation failed',
      consecutive_start_failures: 2,
      retry_after: new Date(now + 45_000).toISOString()
    }] }
    manager.runtimeTelemetry.value = {}
    const wrapper = await mountSuspended(InstanceDetailPage, { route: `/instances/${instanceSlug}/detail` })
    await flushPromises()
    expect(wrapper.get('[data-testid="instance-detail-startup-backoff"]').text()).toContain('CUDA allocation failed')
    expect(wrapper.get('[data-testid="instance-detail-startup-backoff"]').text()).toContain('Retry in 45s (2 consecutive start failures)')
  })
})
