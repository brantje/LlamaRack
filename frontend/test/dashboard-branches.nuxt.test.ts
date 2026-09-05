import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import DashboardPage from '~/pages/index.vue'
import { useManager, type Instance } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

const gib = 1024 ** 3

function instance(id: string, overrides: Partial<Instance> = {}): Instance {
  return {
    id,
    slug: id,
    model_id: 'm1',
    name: id,
    enabled: true,
    autoload_enabled: true,
    always_on: false,
    priority: 'normal',
    eviction_enabled: true,
    idle_unload_seconds: 0,
    gpu_mode: 'auto',
    gpu_devices: [],
    tensor_split: '',
    request_log_mode: 'metadata',
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
  manager.models.value = [{ id: 'm1', slug: 'm1', name: 'Model', gguf_path: 'model.gguf', total_bytes: 1, context_length: 8192 }]
  manager.instances.value = []
  manager.runtimes.value = { m1: [] }
  manager.runtimeTelemetry.value = {}
  manager.observabilityLive.value = null
  return manager
}

beforeEach(() => {
  mocks.request.mockReset()
  resetManager()
})

describe('Dashboard edge branches', () => {
  it('covers runtime, memory, attribution and attention fallbacks', async () => {
    const manager = resetManager()
    manager.instances.value = [instance('starting'), instance('loading'), instance('always-idle', { always_on: true, idle_unload_seconds: 30 })]
    manager.runtimes.value = {
      m1: [
        { instance_id: 'starting', model_id: 'm1', state: 'STARTING' },
        { instance_id: 'loading', model_id: 'm1', state: 'LOADING' },
        { instance_id: 'failed', model_id: 'm1', state: 'FAILED' }
      ]
    }
    manager.observabilityLive.value = {
      collected_at: '2026-08-28T09:00:00Z',
      hardware: {
        ram_total_bytes: 100,
        ram_available_bytes: 5,
        collected_at: '2026-08-28T09:00:00Z',
        processes: [],
        gpus: [
          { id: 'ZERO', backend: 'cuda', index: 0, name: 'Zero total', total_bytes: 0, used_bytes: 10, free_bytes: 0, utilization_pct: 0 },
          { id: 'CUDA0', backend: 'cuda', index: 1, name: 'Over-attributed', total_bytes: 24 * gib, used_bytes: 30 * gib, free_bytes: 0, utilization_pct: 100 },
          { id: 'CUDA1', backend: 'cuda', index: 2, name: 'Single fallback', total_bytes: 24 * gib, used_bytes: gib, free_bytes: 23 * gib, utilization_pct: 5 },
          { id: 'CUDA2', backend: 'cuda', index: 3, name: 'Ambiguous fallback', total_bytes: 24 * gib, used_bytes: 0, free_bytes: 24 * gib, utilization_pct: 0 },
          { id: 'CUDA4', backend: 'cuda', index: 4, name: 'No process', total_bytes: 24 * gib, used_bytes: 0, free_bytes: 24 * gib, utilization_pct: 0 }
        ]
      },
      telemetry: [
        { instance_id: 'loading', pid: 12, gpu_devices: ['CUDA0'], gpus: [{ device_id: 'CUDA0', vram_used_bytes: 30 * gib }], vram_used_bytes: 30 * gib, collected_at: '2026-08-28T09:00:00Z' },
        { instance_id: 'starting', pid: 11, gpu_devices: ['CUDA1'], vram_used_bytes: 4 * gib, collected_at: '2026-08-28T09:00:00Z' },
        { instance_id: 'ambiguous', pid: 13, gpu_devices: ['CUDA2', 'CUDA3'], vram_used_bytes: 6 * gib, collected_at: '2026-08-28T09:00:00Z' }
      ]
    }

    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/observability/summary')) {
        return {
          requests: 2,
          successes: 0,
          errors: 2,
          active: 0,
          queued: 0,
          active_api_keys: 1,
          prompt_tokens: 0,
          generated_tokens: 0,
          total_tokens: 0,
          lifecycle: { autoloads: 0, failed_starts: 0, load_duration_ms_total: 0 },
          hardware: { hardware: manager.observabilityLive.value!.hardware, telemetry: manager.observabilityLive.value!.telemetry }
        }
      }
      if (path.startsWith('/api/v1/observability/requests')) {
        return { items: [
          { id: 1, started_at: 0, finished_at: 0, instance_id: 'starting', endpoint: '/v1/chat/completions', streaming: false, status_code: 0, result: 'error', duration_ms: 500, prompt_tokens: 0, generated_tokens: 0, total_tokens: 0, queue_duration_ms: 0, load_duration_ms: 0, autoloaded: false },
          { id: 2, started_at: Date.now(), finished_at: Date.now(), instance_id: 'loading', endpoint: '/v1/responses', api_key: { id: 'k2', name: '', prefix: '' }, streaming: true, status_code: 500, result: 'error', duration_ms: 10_000, prompt_tokens: 1, generated_tokens: 1, total_tokens: 2, queue_duration_ms: 0, load_duration_ms: 0, autoloaded: false }
        ] }
      }
      if (path === '/api/v1/settings/general') return { idle_unload_seconds: { value: 3600, source: 'default', editable: true } }
      return {}
    })

    const wrapper = await mountSuspended(DashboardPage, { route: '/' })
    await flushPromises()

    expect(wrapper.get('[data-testid="dashboard-running"]').text()).toContain('2 loading')
    expect(wrapper.get('[data-testid="dashboard-running"]').text()).toContain('1 failed')
    expect(wrapper.get('[data-testid="dashboard-idle"]').text()).toContain('1 h')
    expect(wrapper.get('[data-testid="dashboard-idle"]').text()).toContain('1 Instance override')
    expect(wrapper.get('[data-testid="dashboard-gateway"]').text()).toContain('1 key active')
    expect(wrapper.get('[data-testid="dashboard-vram"]').text()).toContain('28 GiB')

    const allocation = wrapper.get('[data-testid="dashboard-vram-allocation"]')
    expect(allocation.text()).toContain('24 GiB / 24 GiB')
    expect(wrapper.get('[data-testid="gpu-progress-CUDA0"]').text()).not.toContain('Free')
    expect(wrapper.get('[data-testid="gpu-progress-CUDA1"]').text()).toContain('4.0 GiB')
    expect(wrapper.get('[data-testid="gpu-progress-CUDA1"]').text()).toContain('20 GiB')
    expect(wrapper.get('[data-testid="gpu-progress-CUDA2"]').text()).toContain('24 GiB')
    expect(wrapper.get('[data-testid="gpu-progress-CUDA4"]').text()).toContain('Free')

    const traffic = wrapper.get('[data-testid="dashboard-gateway-traffic"]').text()
    expect(traffic).toContain('—')
    expect(traffic).toContain('500 ms')
    expect(traffic).toContain('10.0 s')

    const attention = wrapper.get('[data-testid="dashboard-attention"]').text()
    expect(attention).toContain('failed failed to start')
    expect(attention).toContain('managed llama-server process is in FAILED state')
    expect(attention).toContain('starting returned an error')
    expect(attention).toContain('/v1/chat/completions failed during the last 15 min')
    expect(attention).not.toContain('loading returned 500')
    expect(attention).toContain('always-idle is Always-On but unloaded')
    expect(attention).toContain('CUDA0 is at 100% VRAM')
    expect(attention).toContain('Host RAM is at 95%')
  })

  it('covers seconds formatting, missing request items and zero-memory fallbacks', async () => {
    const manager = resetManager()
    manager.instances.value = [instance('one')]
    manager.runtimes.value = { m1: [{ instance_id: 'one', model_id: 'm1', state: 'READY' }] }
    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/observability/summary')) {
        return {
          requests: 1,
          active_api_keys: 0,
          lifecycle: { autoloads: 0, failed_starts: 0, load_duration_ms_total: 0 },
          hardware: { hardware: { ram_total_bytes: 0, ram_available_bytes: 1, collected_at: '', processes: [], gpus: [] }, telemetry: [] }
        }
      }
      if (path.startsWith('/api/v1/observability/requests')) return {}
      if (path === '/api/v1/settings/general') return { idle_unload_seconds: { value: 61, source: 'default', editable: true } }
      return {}
    })

    const wrapper = await mountSuspended(DashboardPage, { route: '/' })
    await flushPromises()
    expect(wrapper.get('[data-testid="dashboard-idle"]').text()).toContain('61 sec')
    expect(wrapper.get('[data-testid="dashboard-vram"]').text()).toContain('0 B/ 0 B')
    expect(wrapper.get('[data-testid="dashboard-gateway-traffic"]').text()).toContain('No recent gateway traffic')
  })

  it('prefers structured dashboard API errors', async () => {
    resetManager()
    mocks.request.mockRejectedValue({ data: { error: 'observability forbidden' } })
    const wrapper = await mountSuspended(DashboardPage, { route: '/' })
    await flushPromises()
    expect(wrapper.text()).toContain('observability forbidden')
  })
})