import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import LogsPage from '~/pages/logs/index.vue'
import { useManager } from '~/composables/useManager'

enableAutoUnmount(afterEach)

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

function record(id: string, overrides: Record<string, unknown> = {}) {
  return {
    id: Number(id.replace(/\D/g, '')) || 1,
    request_id: id,
    trace_id: '11111111-2222-4333-8444-555555555555',
    call_type: 'chat_completion',
    started_at: Date.now() - 100,
    finished_at: Date.now(),
    instance_id: 'coder',
    endpoint: '/v1/chat/completions',
    streaming: false,
    status_code: 200,
    result: 'success',
    duration_ms: 100,
    prompt_tokens: 1,
    generated_tokens: 2,
    total_tokens: 3,
    queue_duration_ms: 0,
    load_duration_ms: 0,
    autoloaded: false,
    ...overrides
  } as any
}

function resetManager() {
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.models.value = [{ id: 'm1', name: 'Registered Model', gguf_path: 'model.gguf', total_bytes: 1, context_length: 4096 }]
  manager.instances.value = [{ id: 'coder', model_id: 'm1', name: 'Coder', enabled: true, autoload_enabled: true, always_on: false, priority: 'normal', eviction_enabled: true, idle_unload_seconds: 0, gpu_mode: 'auto', gpu_devices: [], tensor_split: '', request_log_mode: 'metadata' }]
  manager.runtimes.value = {}
  manager.runtimeTelemetry.value = {}
  manager.observabilityLive.value = null
  manager.runtimeEventsConnected.value = true
  return manager
}

beforeEach(() => {
  mocks.request.mockReset()
  resetManager()
})

describe('Request logs focused branch coverage', () => {
  it('covers request metadata helper fallbacks', async () => {
    mocks.request.mockResolvedValue({ items: [], has_more: false })
    const wrapper = await mountSuspended(LogsPage, { route: '/logs' })
    await flushPromises()
    const vm = wrapper.vm as any

    expect(vm.callTypeLabel('completion')).toBe('Completion')
    expect(vm.callTypeLabel('response')).toBe('Responses')
    expect(vm.callTypeLabel('embedding')).toBe('Embedding')
    expect(vm.callTypeLabel('unknown')).toBe('—')

    expect(vm.requestKeyAlias(record('1', { api_key: undefined }))).toBe('—')
    expect(vm.requestKeyAlias(record('2', { api_key: { id: 'key-id', name: '', prefix: '' } }))).toBe('key-id')
    expect(vm.requestKeyAlias(record('3', { api_key: { id: '', name: '', prefix: 'pk_alias' } }))).toBe('pk_alias')
    expect(vm.requestKeyAlias(record('4', { api_key: { id: '', name: '', prefix: '' } }))).toBe('—')

    expect(vm.requestModelName(record('5', { model_name: 'Captured Model' }))).toBe('Captured Model')
    expect(vm.requestModelName(record('6', { model_name: '', model_id: 'm1' }))).toBe('Registered Model')
    expect(vm.requestModelName(record('7', { model_name: '', model_id: 'deleted-model' }))).toBe('deleted-model')
    expect(vm.requestModelName(record('8', { model_name: '', model_id: '', instance_id: 'coder' }))).toBe('Registered Model')
    expect(vm.requestModelName(record('9', { model_name: '', model_id: '', instance_id: 'missing' }))).toBe('—')

    const pending = record('10', { finished_at: 0, result: '', status_code: 0 })
    expect(vm.isPending(pending)).toBe(true)
    expect(vm.resultLabel(pending)).toBe('pending')
    expect(vm.isPending(record('11', { finished_at: 1, result: 'pending' }))).toBe(true)
    expect(vm.resultLabel(record('12', { status_code: 0, result: 'error' }))).toBe('error')
    expect(vm.sessionCount(record('13', { session_id: '' }))).toBe(0)
    expect(vm.sessionCount(record('14', { session_id: 'session', session_total_count: 0 }))).toBe(1)
    expect(vm.sessionCount(record('15', { session_id: 'session', session_total_count: 4 }))).toBe(4)

    expect(vm.shortID('', 8)).toBe('—')
    expect(vm.shortID('short', 8)).toBe('short')
    expect(vm.shortID('a-very-long-id', 8)).toBe('a-very-…')
    expect(vm.formatDuration(undefined)).toBe('—')
    expect(vm.formatDuration(Number.NaN)).toBe('—')
    expect(vm.formatDuration(999)).toBe('999 ms')
    expect(vm.formatDuration(1000)).toBe('1.00 s')
    expect(vm.formatDuration(12_000)).toBe('12.0 s')
    expect(vm.formatTime(0)).toBe('—')

    vm.filters.window = 'all'
    expect(vm.sinceForWindow()).toBe(0)
    vm.filters.window = '15m'
    expect(vm.sinceForWindow()).toBeGreaterThan(0)
  })

  it('covers truncated session, start-time sorting and sidebar controls', async () => {
    const sessionID = 'session-truncated'
    const grouped = record('lcm_grouped', { session_id: sessionID, session_total_count: 5, duration_ms: 500 })
    const sessionRows = [
      record('lcm_b', { session_id: sessionID, session_total_count: 5, started_at: 200, finished_at: 0, duration_ms: 50 }),
      record('lcm_a', { session_id: sessionID, session_total_count: 5, started_at: 100, finished_at: 180, duration_ms: 80 })
    ]
    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/observability/requests?')) {
        if (path.includes(`session_id=${encodeURIComponent(sessionID)}`)) return { items: sessionRows, has_more: false }
        return { items: [grouped], has_more: false }
      }
      if (path === '/api/v1/observability/requests/lcm_grouped') return grouped
      return {}
    })

    const wrapper = await mountSuspended(LogsPage, { route: '/logs' })
    await flushPromises()
    await wrapper.get('[data-testid="request-detail-trigger"]').trigger('click')
    await flushPromises()
    expect(document.body.textContent).toContain('Showing 2 of 5 retained requests.')

    const vm = wrapper.vm as any
    vm.sessionSortMode = 'start_time'
    await flushPromises()
    expect(vm.sortedSessionRequests[0].request_id).toBe('lcm_a')
    vm.sessionSortMode = 'duration'
    await flushPromises()
    expect(vm.sortedSessionRequests[0].request_id).toBe('lcm_a')
    expect(vm.sessionDuration).toBe(100)

    const collapse = Array.from(document.body.querySelectorAll('button')).find(button => button.getAttribute('aria-label') === 'Collapse session sidebar') as HTMLButtonElement | undefined
    collapse?.click()
    await flushPromises()
    expect(document.body.textContent).toContain('Show session requests')
    const show = Array.from(document.body.querySelectorAll('button')).find(button => button.textContent?.includes('Show session requests')) as HTMLButtonElement | undefined
    show?.click()
    await flushPromises()
    expect(document.body.textContent).toContain(sessionID)
  })

  it('covers session errors, wrong route sessions and disconnected state', async () => {
    const manager = resetManager()
    manager.runtimeEventsConnected.value = false
    const sessionID = 'session-error'
    const grouped = record('lcm_session_error', { session_id: sessionID, session_total_count: 2 })
    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/observability/requests?')) {
        if (path.includes(`session_id=${encodeURIComponent(sessionID)}`)) throw { data: { error: 'session unavailable' } }
        if (path.includes('session_id=other-session')) throw {}
        return { items: [grouped], has_more: false }
      }
      if (path === '/api/v1/observability/requests/lcm_session_error') return grouped
      return {}
    })

    const wrapper = await mountSuspended(LogsPage, { route: '/logs' })
    await flushPromises()
    expect(wrapper.get('[data-testid="request-logs-live-state"]').text()).toBe('Disconnected')
    await wrapper.get('[data-testid="request-detail-trigger"]').trigger('click')
    await flushPromises()
    expect(document.body.textContent).toContain('session unavailable')

    const vm = wrapper.vm as any
    await vm.loadSessionRequests('other-session')
    await flushPromises()
    expect(vm.sessionError).toBe('Unable to load session requests')

    manager.user.value = null
    await flushPromises()
    expect(vm.routeReady).toBe(false)
  })
})
