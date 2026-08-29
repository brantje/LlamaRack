import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import DashboardPage from '~/pages/index.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

const gib = 1024 ** 3

function resetManager() {
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.models.value = []
  manager.instances.value = [{ id: 'coder', model_id: 'm1', name: 'Coder', enabled: true, autoload_enabled: true, always_on: false, priority: 'normal', eviction_enabled: true, idle_unload_seconds: 0, gpu_mode: 'auto', gpu_devices: [], tensor_split: '', request_log_mode: 'metadata' }]
  manager.runtimes.value = { m1: [{ instance_id: 'coder', model_id: 'm1', state: 'READY', pid: 10 }] }
  manager.observabilityLive.value = {
    collected_at: '2026-08-29T18:00:00Z',
    hardware: { ram_total_bytes: 32 * gib, ram_available_bytes: 16 * gib, collected_at: '2026-08-29T18:00:00Z', processes: [], gpus: [{ id: 'CUDA0', backend: 'cuda', index: 0, name: 'GPU', total_bytes: 16 * gib, used_bytes: 12 * gib, free_bytes: 4 * gib, utilization_pct: 50 }] },
    telemetry: [{ instance_id: 'coder', pid: 10, gpu_devices: ['CUDA0'], gpus: [{ device_id: 'CUDA0', vram_used_bytes: 10 * gib }], vram_used_bytes: 10 * gib, collected_at: '2026-08-29T18:00:00Z' }],
    gateway: { active: 2, queued: 1, requests: 0, successes: 0, errors: 0, active_api_keys: 0, prompt_tokens: 0, generated_tokens: 0, total_tokens: 0, since: 0 },
    requests: [{ id: 99, started_at: Date.now(), finished_at: 0, instance_id: 'coder', endpoint: '/v1/responses', streaming: true, status_code: 0, result: 'pending', duration_ms: 0, prompt_tokens: 0, generated_tokens: 0, total_tokens: 0, queue_duration_ms: 0, load_duration_ms: 0, autoloaded: false }]
  } as any
  return manager
}

beforeEach(() => {
  mocks.request.mockReset()
  resetManager()
})

function summary(windowSeconds = 900) {
  return {
    since: Date.now() - windowSeconds * 1000,
    requests: 10,
    successes: 9,
    errors: 1,
    active: 0,
    queued: 0,
    active_api_keys: 2,
    prompt_tokens: 1200,
    generated_tokens: 2300,
    total_tokens: 3500,
    lifecycle: { autoloads: 2, failed_starts: 1, load_duration_ms_total: 2400 },
    hardware: { hardware: useManager().observabilityLive.value!.hardware, telemetry: useManager().observabilityLive.value!.telemetry }
  }
}

function requestRecord() {
  return { id: 1, request_id: 'req-1', started_at: Date.now(), finished_at: Date.now(), instance_id: 'coder', endpoint: '/v1/chat/completions', api_key: { id: 'k1', name: 'Primary', prefix: 'pk_1' }, streaming: true, status_code: 200, result: 'success', duration_ms: 1500, ttft_ms: 250, prompt_tokens: 10, generated_tokens: 20, total_tokens: 30, tokens_per_second: 42.5, queue_duration_ms: 0, load_duration_ms: 0, autoloaded: true }
}

describe('Dashboard redesign', () => {
  it('renders the new flat dashboard hierarchy and retention-aware selector', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/settings/general') return { idle_unload_seconds: { value: 300, source: 'default', editable: true }, observability_retention_days: { value: 1, source: 'database', editable: true } }
      if (path.startsWith('/api/v1/observability/summary')) return summary()
      if (path.startsWith('/api/v1/observability/requests')) return { items: [requestRecord()] }
      return []
    })

    const wrapper = await mountSuspended(DashboardPage, { route: '/' })
    await flushPromises()

    expect(wrapper.text()).toContain('CONTROL PLANE')
    expect(wrapper.get('[data-testid="dashboard-system-logs"]').attributes('href')).toBe('/admin/logs')
    expect(wrapper.get('[data-testid="open-request-logs"]').attributes('href')).toBe('/logs')
    expect(wrapper.get('[data-testid="dashboard-range"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="dashboard-retention-note"]').text()).toContain('1 day')
    const kpis = wrapper.get('[data-testid="dashboard-observability-kpis"]').text()
    expect(kpis).toContain('Tokens · 15 min')
    expect(kpis).toContain('3.5')
    expect(kpis).toContain('90')
    expect(kpis).toContain('2 active')
    expect(kpis).toContain('1 streaming')

    const vram = wrapper.get('[data-testid="dashboard-vram-allocation"]')
    expect(vram.element.tagName).toBe('SECTION')
    expect(vram.text()).toContain('Unattributed process memory')
    expect(vram.text()).toContain('2.0 GiB')
    expect(vram.text()).toContain('Free')
    expect(vram.text()).toContain('4.0 GiB')
    expect(wrapper.get('[data-testid="dashboard-host-ram"]').text()).toContain('16 GiB / 32 GiB')

    const traffic = wrapper.get('[data-testid="dashboard-gateway-traffic"]').text()
    expect(traffic).toContain('TTFT')
    expect(traffic).toContain('tok/s')
    expect(traffic).toContain('stream')
    expect(traffic).toContain('autoload')
    expect(traffic).toContain('250 ms')
    expect(traffic).toContain('42.5')
  })

  it('refreshes summary and request history immediately when the range changes', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/settings/general') return { idle_unload_seconds: { value: 0, source: 'default', editable: true }, observability_retention_days: { value: 30, source: 'default', editable: true } }
      if (path.startsWith('/api/v1/observability/summary')) return summary(Number(new URL(`http://x${path}`).searchParams.get('window_seconds') || 900))
      if (path.startsWith('/api/v1/observability/requests')) return { items: [] }
      return []
    })

    const wrapper = await mountSuspended(DashboardPage, { route: '/' })
    await flushPromises()
    mocks.request.mockClear()

    ;(wrapper.vm as any).setSelectedWindow(3600)
    await flushPromises()

    expect(mocks.request).toHaveBeenCalledWith('/api/v1/observability/summary?window_seconds=3600')
    const historyCall = mocks.request.mock.calls.find(([path]) => String(path).startsWith('/api/v1/observability/requests?since='))
    expect(historyCall).toBeTruthy()
    const since = Number(new URL(`http://x${historyCall![0]}`).searchParams.get('since'))
    expect(Date.now() - since).toBeGreaterThanOrEqual(3_599_000)
    expect(Date.now() - since).toBeLessThan(3_602_000)
    expect(wrapper.get('[data-testid="dashboard-gateway"]').text()).toContain('Gateway · 1 hour')
    expect(wrapper.get('[data-testid="dashboard-gateway-traffic"]').text()).toContain('last 1 hour')
  })
})
