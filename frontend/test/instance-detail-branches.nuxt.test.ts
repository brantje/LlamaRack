import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountSuspended, mockNuxtImport } from '@nuxt/test-utils/runtime'
import InstanceDetailPage from '~/pages/instances/[id]/detail.vue'
import InstanceHistoryChart from '~/components/InstanceHistoryChart.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn(), navigateTo: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))
mockNuxtImport('navigateTo', () => mocks.navigateTo)

const gib = 1024 ** 3
const now = Date.now()
const bucket = Math.floor(now / 60_000) * 60_000

function seed(overrides: Record<string, unknown> = {}) {
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.models.value = [{ id: 'm1', name: 'Detail Model', gguf_path: 'detail.gguf', total_bytes: 4 * gib, context_length: 16384 }]
  manager.instances.value = [{
    id: 'detail', model_id: 'm1', name: 'Detail Instance', enabled: true, autoload_enabled: true,
    always_on: false, priority: 'normal', eviction_enabled: false, idle_unload_seconds: 0,
    gpu_mode: 'manual', gpu_devices: ['CUDA0'], request_log_mode: 'metadata', ...overrides
  } as any]
  manager.runtimes.value = { m1: [{
    instance_id: 'detail', model_id: 'm1', state: 'READY', pid: 9, port: 9001,
    started_at: new Date(now - 3_661_000).toISOString(), ready_at: new Date(now - 3_650_000).toISOString()
  } as any] }
  manager.runtimeTelemetry.value = {
    detail: {
      instance_id: 'detail', pid: 9, gpu_devices: ['CUDA0'],
      gpus: [{ device_id: 'CUDA0', vram_used_bytes: 8 * gib, utilization_pct: 40 }],
      vram_used_bytes: 8 * gib, gpu_utilization_pct: 40, cpu_percent: 9.5, memory_used_bytes: 0,
      collected_at: new Date(now).toISOString(),
      llama_metrics: {
        prompt_tokens_total: 0, prompt_seconds_total: 0, prompt_tokens_per_second: 0,
        predicted_tokens_total: 12_345, predicted_seconds_total: 10.5, predicted_tokens_per_second: 123.45,
        requests_processing: 0, requests_deferred: 0, context_tokens_max: 0,
        decode_total: 0, busy_slots_per_decode: 0,
        spec_draft_tokens_total: 0, spec_accepted_tokens_total: 0, spec_drafts_total: 0,
        spec_acceptance_rate_pct: 0
      }
    } as any,
    other: {
      instance_id: 'other', pid: 10, gpu_devices: ['CUDA0'], gpus: [{ device_id: 'CUDA0', vram_used_bytes: 2 * gib }],
      vram_used_bytes: 2 * gib, collected_at: new Date(now).toISOString()
    } as any
  }
  manager.observabilityLive.value = {
    collected_at: new Date(now).toISOString(),
    hardware: {
      ram_total_bytes: 32 * gib, ram_available_bytes: 16 * gib, collected_at: new Date(now).toISOString(), processes: [],
      gpus: [
        { id: 'CUDA0', backend: 'cuda', index: 0, name: 'Primary', total_bytes: 16 * gib, used_bytes: 12 * gib, free_bytes: 4 * gib, utilization_pct: 55 },
        { id: 'CUDA1', backend: 'cuda', index: 1, name: 'Other', total_bytes: 8 * gib, used_bytes: 1 * gib, free_bytes: 7 * gib, utilization_pct: 5 }
      ]
    },
    telemetry: [manager.runtimeTelemetry.value.detail!, manager.runtimeTelemetry.value.other!],
    gateway: { since: 0, requests: 0, successes: 0, errors: 0, active: 0, queued: 0, active_api_keys: 0, prompt_tokens: 0, generated_tokens: 0, total_tokens: 0, latency_ms: {}, ttft_ms: {} },
    requests: []
  }
  return manager
}

function history(metric: string) {
  const values: Record<string, number> = {
    requests: 2, prompt_tokens: 1_234, generated_tokens: 4_321, latency: 12_345,
    latency_p50: 999, latency_p95: 12_345, instance_context_tokens_max: 8192
  }
  return { metric, bucket_seconds: 60, items: [{ timestamp: bucket, value: values[metric] ?? 0 }] }
}

function installHappyRequests(manager = useManager()) {
  mocks.request.mockImplementation(async (path: string, options?: any) => {
    if (path === '/api/v1/settings/general') return { observability_retention_days: { value: 30 } }
    if (path.startsWith('/api/v1/observability/timeseries?')) {
      const metric = new URL(`http://x${path}`).searchParams.get('metric') || ''
      return history(metric)
    }
    if (path === '/api/v1/llamacpp/config?model_id=m1') return {
      effective: {
        values: { mmproj: '/models/helpers/mmproj.gguf', 'spec-draft-model': '/models/helpers/draft.gguf' },
        sources: { mmproj: 'model', 'spec-draft-model': 'detected' }
      }
    }
    if (path === '/api/v1/models/inspect' && options?.method === 'POST') return {
      dependencies: [
        { kind: 'mmproj', total_bytes: 512 * 1024 ** 2 },
        { kind: 'mtp', total_bytes: 256 * 1024 ** 2 }
      ]
    }
    if (path.startsWith('/api/v1/logs?')) return { instance_id: 'detail', entries: [] }
    if (path === '/api/v1/models') return manager.models.value
    if (path === '/api/v1/instances') return manager.instances.value
    if (path === '/api/v1/instances/detail/runtime') return manager.runtimes.value.m1?.[0] || { instance_id: 'detail', model_id: 'm1', state: 'UNLOADED' }
    if (path === '/api/v1/llamacpp/profile') throw new Error('profile unavailable')
    if (path.startsWith('/api/v1/instances/detail/') && options?.method === 'POST') return {}
    if (path === '/api/v1/instances/detail' && options?.method === 'DELETE') return {}
    return []
  })
}

async function confirmation(kind: 'confirm' | 'cancel') {
  await flushPromises()
  const button = [...document.body.querySelectorAll<HTMLButtonElement>(`[data-testid="confirmation-${kind}"]`)].at(-1)
  if (!button) throw new Error(`Missing confirmation ${kind}`)
  button.click()
  await flushPromises()
}

beforeEach(() => {
  mocks.request.mockReset()
  mocks.navigateTo.mockReset().mockResolvedValue(undefined)
  vi.stubGlobal('EventSource', undefined)
  const manager = seed()
  installHappyRequests(manager)
})

describe('Instance detail edge branches', () => {
  it('loads real companion config, process GPU attribution, all GPU maps and formatting extremes', async () => {
    const wrapper = await mountSuspended(InstanceDetailPage, { route: '/instances/detail/detail' })
    await flushPromises()
    const text = wrapper.text()
    expect(text).toContain('Instance GPU usage')
    expect(text).toContain('1h 1m')
    expect(text).toContain('12.3 s')
    expect(text).toContain('123.5 tok/s')
    expect(text).toContain('Vision projector')
    expect(text).toContain('MTP draft model')
    expect(text).toContain('/models/helpers/mmproj.gguf')
    expect(text).toContain('/models/helpers/draft.gguf')
    expect(text).toContain('--mmproj')
    expect(text).toContain('--spec-draft-model')
    expect(text).toContain('512 MiB')
    expect(text).toContain('256 MiB')
    expect(text).toContain('detail (this Instance)')
    expect(text).toContain('other')
    expect(text).toContain('Unattributed process memory')
    expect(text).toContain('CUDA1 · Other')
    expect(text).toContain('Free')
    expect(text).toContain('0 B')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/llamacpp/config?model_id=m1')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/models/inspect', { method: 'POST', body: { gguf_path: 'detail.gguf' } })
  })

  it('ignores inherited global helpers and clamps history to retained ranges', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/settings/general') return { observability_retention_days: { value: 1 } }
      if (path === '/api/v1/llamacpp/config?model_id=m1') return { effective: { values: { mmproj: '/global/mmproj.gguf' }, sources: { mmproj: 'global' } } }
      if (path.startsWith('/api/v1/observability/timeseries?')) return history(new URL(`http://x${path}`).searchParams.get('metric') || '')
      if (path.startsWith('/api/v1/logs?')) return { instance_id: 'detail', entries: [] }
      return []
    })
    const wrapper = await mountSuspended(InstanceDetailPage, { route: '/instances/detail/detail' })
    await flushPromises()
    expect(wrapper.find('[data-testid="instance-detail-companions"]').exists()).toBe(false)

    mocks.request.mockClear()
    ;(wrapper.vm as any).setSelectedWindow(604800)
    await flushPromises()
    expect(wrapper.text()).toContain('Requests · 24 hours')
    const historyCalls = mocks.request.mock.calls.map(([path]) => String(path)).filter(path => path.startsWith('/api/v1/observability/timeseries?'))
    expect(historyCalls).toHaveLength(7)
    for (const path of historyCalls) {
      const params = new URL(`http://x${path}`).searchParams
      expect(params.get('window_seconds')).toBe('86400')
      expect(params.get('bucket_seconds')).toBe('900')
      expect(params.get('instance_id')).toBe('detail')
    }
  })

  it('preserves direct launch/stop, confirmed kill and delete actions', async () => {
    const manager = seed()
    manager.runtimes.value = { m1: [{ instance_id: 'detail', model_id: 'm1', state: 'UNLOADED' }] }
    manager.runtimeTelemetry.value = {}
    manager.observabilityLive.value = null
    installHappyRequests(manager)
    const wrapper = await mountSuspended(InstanceDetailPage, { route: '/instances/detail/detail' })
    await flushPromises()

    await wrapper.findAll('button').find(button => button.text() === 'Launch')!.trigger('click')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/instances/detail/start', { method: 'POST' })

    manager.runtimes.value = { m1: [{ instance_id: 'detail', model_id: 'm1', state: 'READY', pid: 9 }] }
    await wrapper.vm.$nextTick()
    await wrapper.findAll('button').find(button => button.text() === 'Stop')!.trigger('click')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/instances/detail/stop', { method: 'POST' })

    await wrapper.findAll('button').find(button => button.text() === 'Kill')!.trigger('click')
    await confirmation('confirm')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/instances/detail/kill', { method: 'POST' })

    await wrapper.findAll('button').find(button => button.text() === 'Delete')!.trigger('click')
    await confirmation('confirm')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/instances/detail', { method: 'DELETE' })
    expect(mocks.navigateTo).toHaveBeenCalledWith('/instances')
  })

  it('keeps eviction and destructive confirmations cancellable and surfaces action errors', async () => {
    const manager = seed({ eviction_enabled: true })
    manager.runtimes.value = { m1: [{ instance_id: 'detail', model_id: 'm1', state: 'UNLOADED' }] }
    manager.runtimeTelemetry.value = {}
    manager.observabilityLive.value = null
    installHappyRequests(manager)
    const wrapper = await mountSuspended(InstanceDetailPage, { route: '/instances/detail/detail' })
    await flushPromises()
    mocks.request.mockClear()

    await wrapper.findAll('button').find(button => button.text() === 'Launch')!.trigger('click')
    await confirmation('cancel')
    expect(mocks.request).not.toHaveBeenCalledWith('/api/v1/instances/detail/start', expect.anything())

    await wrapper.findAll('button').find(button => button.text() === 'Kill')!.trigger('click')
    await confirmation('cancel')
    expect(mocks.request).not.toHaveBeenCalledWith('/api/v1/instances/detail/kill', expect.anything())

    await wrapper.findAll('button').find(button => button.text() === 'Delete')!.trigger('click')
    await confirmation('cancel')
    expect(mocks.request).not.toHaveBeenCalledWith('/api/v1/instances/detail', expect.anything())

    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/settings/general') return {}
      if (path.startsWith('/api/v1/observability/timeseries?')) return { metric: 'x', bucket_seconds: 60, items: [] }
      if (path.startsWith('/api/v1/logs?')) return { instance_id: 'detail', entries: [] }
      if (path === '/api/v1/instances/detail/start') throw { data: { error: 'launch denied' } }
      return []
    })
    await wrapper.findAll('button').find(button => button.text() === 'Launch')!.trigger('click')
    await confirmation('confirm')
    expect(wrapper.text()).toContain('launch denied')
  })

  it('reports history failures and missing Instances without crashing', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/settings/general') throw new Error('settings offline')
      if (path.startsWith('/api/v1/observability/timeseries?')) throw { data: { error: 'history offline' } }
      if (path.startsWith('/api/v1/logs?')) return { instance_id: 'detail', entries: [] }
      return []
    })
    const wrapper = await mountSuspended(InstanceDetailPage, { route: '/instances/detail/detail' })
    await flushPromises()
    expect(wrapper.text()).toContain('history offline')
    expect(wrapper.get('[data-testid="instance-detail-history-error"]').text()).toContain('Performance history unavailable')
    expect(wrapper.get('[data-testid="instance-detail-summary"]').text()).toContain('READY')
    expect(wrapper.find('[data-testid="instance-detail-error"]').exists()).toBe(false)

    const manager = useManager()
    manager.instances.value = []
    manager.models.value = []
    manager.runtimes.value = {}
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/models' || path === '/api/v1/instances') return []
      if (path === '/api/v1/llamacpp/profile') return {}
      return []
    })
    const missing = await mountSuspended(InstanceDetailPage, { route: '/instances/missing/detail' })
    await flushPromises()
    expect(missing.text()).toContain('Instance “missing” was not found.')
    expect(missing.find('[data-testid="instance-detail-summary"]').exists()).toBe(false)
    expect(missing.text()).not.toContain('READY')
    missing.unmount()
  })

  it('clears not-found once the Instance appears in manager state', async () => {
    const manager = useManager()
    manager.instances.value = []
    const wrapper = await mountSuspended(InstanceDetailPage, { route: '/instances/detail/detail' })
    await flushPromises()
    expect(wrapper.text()).toContain('Instance “detail” was not found.')
    expect(wrapper.find('[data-testid="instance-detail-summary"]').exists()).toBe(false)

    manager.instances.value = [{
      id: 'detail', model_id: 'm1', name: 'Detail Instance', enabled: true, autoload_enabled: true,
      always_on: false, priority: 'normal', eviction_enabled: false, idle_unload_seconds: 0,
      gpu_mode: 'manual', gpu_devices: ['CUDA0'], request_log_mode: 'metadata'
    } as any]
    await flushPromises()
    expect(wrapper.text()).not.toContain('was not found')
    expect(wrapper.text()).toContain('Detail Instance')
    expect(wrapper.find('[data-testid="instance-detail-summary"]').exists()).toBe(true)
    wrapper.unmount()
  })
})

describe('InstanceHistoryChart branches', () => {
  it('renders multi-series gaps, every token treatment and short/long duration formats', async () => {
    const wrapper = await mountSuspended(InstanceHistoryChart, {
      route: false,
      props: {
        valueFormat: 'duration',
        min: Number.NaN,
        series: [
          { label: 'Fast', token: 'accent' as const, points: [{ timestamp: bucket - 120_000, value: 250 }, { timestamp: bucket - 60_000, value: null }, { timestamp: bucket, value: 12_345 }] },
          { label: 'Strong', token: 'accent-strong' as const, points: [{ timestamp: bucket - 120_000, value: 500 }, { timestamp: bucket, value: 900 }] },
          { label: 'Neutral', token: 'neutral' as const, points: [{ timestamp: bucket - 120_000, value: Number.NaN }, { timestamp: bucket, value: 1000 }] }
        ]
      }
    })
    expect(wrapper.text()).toContain('Fast')
    expect(wrapper.text()).toContain('Strong')
    expect(wrapper.text()).toContain('Neutral')
    const titles = wrapper.findAll('title').map(node => node.text()).join(' ')
    expect(titles).toContain('250 ms')
    expect(titles).toContain('12.3 s')
    expect(titles).toContain('1.00 s')
    expect(wrapper.findAll('path').length).toBeGreaterThan(3)
  })

  it('covers single-point, empty, percent, token and number domains', async () => {
    const wrapper = await mountSuspended(InstanceHistoryChart, {
      route: false,
      props: { valueFormat: 'percent', max: 100, series: [{ label: 'One', points: [{ timestamp: 0, value: 150 }] }] }
    })
    expect(wrapper.findAll('circle')).toHaveLength(1)
    expect(wrapper.find('title').text()).toContain('150.0%')

    await wrapper.setProps({ valueFormat: 'tokens', max: undefined, min: 10, series: [{ label: 'Tokens', points: [{ timestamp: bucket, value: 5 }] }] })
    await flushPromises()
    expect(wrapper.find('title').text()).toContain('5')

    await wrapper.setProps({ valueFormat: 'number', min: 0, series: [{ label: 'Numbers', points: [{ timestamp: bucket - 60_000, value: 12.25 }, { timestamp: bucket, value: 123 }] }] })
    await flushPromises()
    const numberTitles = wrapper.findAll('title').map(node => node.text()).join(' ')
    expect(numberTitles).toContain('12.3')
    expect(numberTitles).toContain('123')

    await wrapper.setProps({ series: [{ label: 'Empty', points: [{ timestamp: bucket, value: null }] }] })
    await flushPromises()
    expect(wrapper.text()).toContain('No retained samples in this range.')
  })
})
