import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import LogsPage from '~/pages/logs/index.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

function request(id: string, overrides: Record<string, unknown> = {}) {
  const now = Date.now()
  return {
    id: Number(id.replace(/\D/g, '')) || 1,
    request_id: id,
    trace_id: '11111111-1111-4111-8111-111111111111',
    call_type: 'chat_completion',
    started_at: now,
    finished_at: now + 20,
    instance_id: 'coder',
    endpoint: '/v1/chat/completions',
    streaming: false,
    status_code: 200,
    result: 'success',
    duration_ms: 20,
    prompt_tokens: 2,
    generated_tokens: 3,
    total_tokens: 5,
    queue_duration_ms: 0,
    load_duration_ms: 0,
    autoloaded: false,
    ...overrides
  }
}

function resetManager(authenticated = true) {
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = authenticated ? { id: 1, username: 'admin', enabled: true } : null
  manager.models.value = []
  manager.instances.value = []
  manager.runtimes.value = {}
  manager.runtimeTelemetry.value = {}
  manager.observabilityLive.value = null
  manager.runtimeEventsConnected.value = true
  return manager
}

function liveSnapshot(items: any[]) {
  return {
    collected_at: new Date().toISOString(),
    hardware: { ram_total_bytes: 0, ram_available_bytes: 0, collected_at: '', processes: [], gpus: [] },
    telemetry: [],
    gateway: { since: Date.now() - 900_000, requests: items.length, successes: 0, errors: 0, active: 0, queued: 0, active_api_keys: 0, prompt_tokens: 0, generated_tokens: 0, total_tokens: 0, latency_ms: {}, ttft_ms: {} },
    requests: items
  } as any
}

beforeEach(() => {
  mocks.request.mockReset()
  resetManager()
  document.body.innerHTML = ''
})

describe('Request logs live updates', () => {
  it('refreshes the authoritative filtered history when WebSocket request state changes', async () => {
    const manager = resetManager()
    const original = request('lcm_1')
    const pending = request('lcm_2', { finished_at: 0, status_code: 0, result: '', duration_ms: 0, total_tokens: 0 })
    let history = [original]
    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/observability/requests?')) return { items: history, has_more: false }
      return {}
    })

    const wrapper = await mountSuspended(LogsPage, { route: '/logs' })
    await flushPromises()
    expect(wrapper.get('[data-testid="request-log-table"]').text()).toContain('lcm_1')
    expect(wrapper.get('[data-testid="request-logs-live-state"]').text()).toBe('Live')

    history = [pending, original]
    manager.observabilityLive.value = liveSnapshot([pending, original])
    await flushPromises()
    expect(wrapper.get('[data-testid="request-log-table"]').text()).toContain('lcm_2')
    expect(wrapper.get('[data-testid="request-log-table"]').text()).toContain('pending')

    const callsAfterPending = mocks.request.mock.calls.filter(call => String(call[0]).startsWith('/api/v1/observability/requests?')).length
    manager.observabilityLive.value = { ...liveSnapshot([pending, original]), collected_at: new Date(Date.now() + 1000).toISOString() }
    await flushPromises()
    expect(mocks.request.mock.calls.filter(call => String(call[0]).startsWith('/api/v1/observability/requests?'))).toHaveLength(callsAfterPending)

    const completed = { ...pending, finished_at: Date.now() + 40, status_code: 200, result: 'success', duration_ms: 40, total_tokens: 9 }
    history = [completed, original]
    manager.observabilityLive.value = liveSnapshot([completed, original])
    await flushPromises()
    const table = wrapper.get('[data-testid="request-log-table"]').text()
    expect(table).toContain('lcm_2')
    expect(table).toContain('200')
    expect(table).not.toContain('pending')
    wrapper.unmount()
  })

  it('can disable and re-enable live request refresh without disconnecting the shared stream', async () => {
    const manager = resetManager()
    const first = request('lcm_1')
    let history = [first]
    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/observability/requests?')) return { items: history, has_more: false }
      return {}
    })

    const wrapper = await mountSuspended(LogsPage, { route: '/logs' })
    await flushPromises()
    const toggle = wrapper.get('[data-testid="request-logs-live-toggle"]')
    expect(toggle.text()).toContain('Pause live')
    await toggle.trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="request-logs-live-state"]').text()).toBe('Live off')
    expect(wrapper.get('[data-testid="request-logs-live-toggle"]').text()).toContain('Enable live')
    expect(manager.runtimeEventsConnected.value).toBe(true)

    const callsWhilePaused = mocks.request.mock.calls.filter(call => String(call[0]).startsWith('/api/v1/observability/requests?')).length
    const second = request('lcm_2')
    history = [second, first]
    manager.observabilityLive.value = liveSnapshot(history)
    await flushPromises()
    expect(mocks.request.mock.calls.filter(call => String(call[0]).startsWith('/api/v1/observability/requests?'))).toHaveLength(callsWhilePaused)
    expect(wrapper.get('[data-testid="request-log-table"]').text()).not.toContain('lcm_2')

    await wrapper.get('[data-testid="request-logs-live-toggle"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="request-logs-live-state"]').text()).toBe('Live')
    expect(wrapper.get('[data-testid="request-log-table"]').text()).toContain('lcm_2')
    expect(manager.runtimeEventsConnected.value).toBe(true)
    wrapper.unmount()
  })

  it('pauses automatic live refresh while browsing an older page', async () => {
    const manager = resetManager()
    const first = request('lcm_1')
    const older = request('lcm_26')
    let firstPage = [first]
    mocks.request.mockImplementation(async (path: string) => {
      if (!path.startsWith('/api/v1/observability/requests?')) return {}
      const params = new URLSearchParams(path.split('?')[1])
      if (params.get('offset') === '25') return { items: [older], has_more: false }
      return { items: firstPage, has_more: true }
    })

    const wrapper = await mountSuspended(LogsPage, { route: '/logs' })
    await flushPromises()
    const next = wrapper.findAll('button').find(button => button.text().trim() === 'Next')
    expect(next).toBeTruthy()
    await next!.trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="request-log-table"]').text()).toContain('lcm_26')
    expect(wrapper.get('[data-testid="request-logs-live-state"]').text()).toContain('paused')
    expect(wrapper.get('[data-testid="request-logs-live-toggle"]').text()).toContain('Return to live')

    const callsBeforeLive = mocks.request.mock.calls.filter(call => String(call[0]).startsWith('/api/v1/observability/requests?')).length
    firstPage = [request('lcm_3'), first]
    manager.observabilityLive.value = liveSnapshot(firstPage)
    await flushPromises()
    expect(mocks.request.mock.calls.filter(call => String(call[0]).startsWith('/api/v1/observability/requests?'))).toHaveLength(callsBeforeLive)
    expect(wrapper.get('[data-testid="request-log-table"]').text()).toContain('lcm_26')

    await wrapper.get('[data-testid="request-logs-live-toggle"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="request-log-table"]').text()).toContain('lcm_3')
    expect(wrapper.get('[data-testid="request-logs-live-state"]').text()).toBe('Live')
    expect(wrapper.get('[data-testid="request-logs-live-toggle"]').text()).toContain('Pause live')
    wrapper.unmount()
  })

  it('offers reconnect instead of pause while the shared live stream is disconnected', async () => {
    const manager = resetManager()
    manager.runtimeEventsConnected.value = false
    mocks.request.mockResolvedValue({ items: [], has_more: false })

    const wrapper = await mountSuspended(LogsPage, { route: '/logs' })
    await flushPromises()
    expect(wrapper.get('[data-testid="request-logs-live-state"]').text()).toBe('Disconnected')
    expect(wrapper.get('[data-testid="request-logs-live-toggle"]').text()).toContain('Reconnect')
    expect(wrapper.get('[data-testid="request-logs-live-toggle"]').text()).not.toContain('Pause live')
    wrapper.unmount()
  })

  it('waits for authentication before loading protected request history', async () => {
    const manager = resetManager(false)
    mocks.request.mockResolvedValue({ items: [], has_more: false })

    const wrapper = await mountSuspended(LogsPage, { route: '/logs' })
    await flushPromises()
    expect(mocks.request).not.toHaveBeenCalled()

    manager.user.value = { id: 1, username: 'admin', enabled: true }
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledTimes(1)
    expect(String(mocks.request.mock.calls[0]?.[0])).toContain('/api/v1/observability/requests?')
    wrapper.unmount()
  })
})
