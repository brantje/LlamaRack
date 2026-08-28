import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import DashboardPage from '~/pages/index.vue'
import { useManager, type Instance, type Model } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

function model(id = 'm1'): Model {
  return { id, name: `Model ${id}`, gguf_path: `${id}.gguf`, total_bytes: 4, context_length: 8192 }
}

function instance(id: string, overrides: Partial<Instance> = {}): Instance {
  return {
    id,
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
  manager.models.value = [model()]
  manager.instances.value = []
  manager.runtimes.value = { m1: [] }
  manager.runtimeTelemetry.value = {}
  manager.observabilityLive.value = null
  return manager
}

const gib = 1024 ** 3

beforeEach(() => {
  mocks.request.mockReset()
  resetManager()
})

describe('Phase 11 Dashboard', () => {
  it('renders live allocation, recent traffic and actionable attention items', async () => {
    const manager = resetManager()
    manager.instances.value = [
      instance('coder', { always_on: true }),
      instance('broken')
    ]
    manager.runtimes.value = {
      m1: [
        { instance_id: 'coder', model_id: 'm1', state: 'READY', pid: 101 },
        { instance_id: 'broken', model_id: 'm1', state: 'FAILED', last_error: 'CUDA allocation failed' }
      ]
    }
    manager.observabilityLive.value = {
      collected_at: '2026-08-28T08:00:00Z',
      hardware: {
        ram_total_bytes: 64 * gib,
        ram_available_bytes: 20 * gib,
        collected_at: '2026-08-28T08:00:00Z',
        processes: [],
        gpus: [
          { id: 'CUDA0', backend: 'cuda', index: 0, name: 'RTX 4090', total_bytes: 24 * gib, used_bytes: 23 * gib, free_bytes: gib, utilization_pct: 78 }
        ]
      },
      telemetry: [
        { instance_id: 'coder', pid: 101, gpu_devices: ['CUDA0'], gpus: [{ device_id: 'CUDA0', vram_used_bytes: 20 * gib }], vram_used_bytes: 20 * gib, collected_at: '2026-08-28T08:00:00Z' }
      ]
    }

    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/observability/summary')) {
        return {
          since: Date.now() - 900_000,
          requests: 12,
          successes: 11,
          errors: 1,
          active: 0,
          queued: 0,
          active_api_keys: 2,
          prompt_tokens: 100,
          generated_tokens: 200,
          total_tokens: 300,
          lifecycle: { autoloads: 3, failed_starts: 1, load_duration_ms_total: 500 },
          hardware: { hardware: manager.observabilityLive.value!.hardware, telemetry: manager.observabilityLive.value!.telemetry }
        }
      }
      if (path.startsWith('/api/v1/observability/requests')) {
        return { items: [
          { id: 2, started_at: Date.now(), finished_at: Date.now(), instance_id: 'broken', endpoint: '/v1/chat/completions', api_key: { id: 'k2', name: 'CI key', prefix: 'pk_ci' }, streaming: false, status_code: 503, result: 'error', duration_ms: 41, prompt_tokens: 0, generated_tokens: 0, total_tokens: 0, queue_duration_ms: 5, load_duration_ms: 5, autoloaded: true, error: 'worker unavailable' },
          { id: 1, started_at: Date.now(), finished_at: Date.now(), instance_id: 'coder', endpoint: '/v1/chat/completions', api_key: { id: 'k1', name: 'Primary key', prefix: 'pk_live' }, streaming: true, status_code: 200, result: 'success', duration_ms: 1840, prompt_tokens: 10, generated_tokens: 20, total_tokens: 30, queue_duration_ms: 0, load_duration_ms: 0, autoloaded: false }
        ] }
      }
      if (path === '/api/v1/settings/general') return { idle_unload_seconds: { value: 300, source: 'default', editable: true } }
      if (path === '/api/v1/models') return manager.models.value
      if (path === '/api/v1/instances') return manager.instances.value
      if (path === '/api/v1/llamacpp/profile') throw new Error('profile unavailable')
      return {}
    })

    const wrapper = await mountSuspended(DashboardPage, { route: '/' })
    await flushPromises()

    expect(wrapper.get('[data-testid="observability-dashboard"]').text()).toContain('Dashboard')
    expect(wrapper.get('[data-testid="dashboard-running"]').text()).toContain('1 / 2 Instances')
    expect(wrapper.get('[data-testid="dashboard-vram"]').text()).toContain('23 GiB')
    expect(wrapper.get('[data-testid="dashboard-gateway"]').text()).toContain('12')
    expect(wrapper.get('[data-testid="dashboard-idle"]').text()).toContain('5 min')
    const allocation = wrapper.get('[data-testid="dashboard-vram-allocation"]')
    expect(allocation.text()).toContain('coder')
    const progress = wrapper.get('[data-testid="gpu-progress-CUDA0"]')
    expect(progress.text()).toContain('coder')
    expect(progress.text()).toContain('20 GiB')
    expect(progress.text()).toContain('Unattributed')
    expect(progress.text()).toContain('3.0 GiB')
    expect(wrapper.get('[data-testid="dashboard-gateway-traffic"]').text()).toContain('Primary key')
    expect(wrapper.get('[data-testid="dashboard-gateway-traffic"]').text()).toContain('1.84 s')
    const attention = wrapper.get('[data-testid="dashboard-attention"]').text()
    expect(attention).toContain('broken failed to start')
    expect(attention).toContain('worker unavailable')
    expect(attention).toContain('CUDA0 is at 96% VRAM')

    manager.observabilityLive.value = {
      ...manager.observabilityLive.value!,
      hardware: {
        ...manager.observabilityLive.value!.hardware,
        gpus: [{ ...manager.observabilityLive.value!.hardware.gpus[0]!, used_bytes: 12 * gib, free_bytes: 12 * gib, utilization_pct: 25 }]
      }
    }
    await flushPromises()
    expect(wrapper.get('[data-testid="dashboard-vram"]').text()).toContain('12 GiB')
  })

  it('shows empty operational states and a management API failure without crashing', async () => {
    resetManager()
    mocks.request.mockRejectedValue(new Error('database unavailable'))
    const wrapper = await mountSuspended(DashboardPage, { route: '/' })
    await flushPromises()

    expect(wrapper.text()).toContain('Observability data unavailable')
    expect(wrapper.text()).toContain('database unavailable')
    expect(wrapper.get('[data-testid="dashboard-running"]').text()).toContain('0 / 0 Instances')
    expect(wrapper.get('[data-testid="dashboard-vram-allocation"]').text()).toContain('No GPU telemetry available')
    expect(wrapper.get('[data-testid="dashboard-gateway-traffic"]').text()).toContain('No recent gateway traffic')
    expect(wrapper.get('[data-testid="dashboard-attention"]').text()).toContain('Nothing needs attention')
  })
})
