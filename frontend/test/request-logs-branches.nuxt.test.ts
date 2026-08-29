import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import LogsPage from '~/pages/logs/index.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

function baseRequest(id: string, overrides: Record<string, unknown> = {}) {
  return {
    id: 1,
    request_id: id,
    started_at: Date.now(),
    finished_at: Date.now(),
    endpoint: '/v1/chat/completions',
    streaming: false,
    status_code: 200,
    result: 'success',
    duration_ms: 1000,
    prompt_tokens: 0,
    generated_tokens: 0,
    total_tokens: 0,
    queue_duration_ms: 0,
    load_duration_ms: 0,
    autoloaded: false,
    ...overrides
  }
}

function seedManager() {
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.models.value = []
  manager.instances.value = [{ id: 'coder', model_id: 'm1', name: 'Coder', enabled: true, autoload_enabled: true, always_on: false, priority: 'normal', eviction_enabled: true, idle_unload_seconds: 0, gpu_mode: 'auto', gpu_devices: [], tensor_split: '', request_log_mode: 'metadata' }]
  manager.runtimes.value = {}
  manager.runtimeTelemetry.value = {}
  manager.observabilityLive.value = null
}

beforeEach(() => {
  mocks.request.mockReset()
  seedManager()
  document.body.innerHTML = ''
})

describe('Request logs branch coverage', () => {
  it('renders sparse/error rows and malformed retained content fallbacks', async () => {
    const rows = [
      baseRequest('lcm_sparse', { started_at: 0, status_code: 0, result: 'error', duration_ms: 50 }),
      baseRequest('lcm_id_key', { call_type: 'response', api_key: { id: 'key-id', name: '', prefix: '' }, duration_ms: 12_000, ttft_ms: Number.NaN }),
      baseRequest('lcm_prefix_key', { call_type: 'embedding', api_key: { id: '', name: '', prefix: 'pk_only' }, duration_ms: 999 }),
      baseRequest('lcm_empty_key', { call_type: 'completion', api_key: { id: '', name: '', prefix: '' }, total_tokens: 7 })
    ]
    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/observability/requests?')) return { items: rows, has_more: false }
      if (path === '/api/v1/observability/requests/lcm_sparse') {
        return {
          ...rows[0],
          request_body: 'not-json request',
          response_body: JSON.stringify({ choices: [{ message: {} }] })
        }
      }
      return {}
    })

    const wrapper = await mountSuspended(LogsPage, { route: '/logs' })
    await flushPromises()
    const text = wrapper.get('[data-testid="request-log-table"]').text()
    expect(text).toContain('50 ms')
    expect(text).toContain('12.0 s')
    expect(text).toContain('999 ms')
    expect(text).toContain('Responses')
    expect(text).toContain('Embedding')
    expect(text).toContain('Completion')
    expect(text).toContain('key-id')
    expect(text).toContain('pk_only')
    expect(text).toContain('API key')
    expect(text).toContain('—')

    await wrapper.findAll('[data-testid="request-detail-trigger"]')[0]!.trigger('click')
    await flushPromises()
    const body = document.body.textContent || ''
    expect(body).toContain('Request failed')
    expect(body).toContain('The request failed.')
    expect(body).toContain('Unresolved')
    expect(body).toContain('not-json request')
  })

  it('builds bounded server-side queries for every request-history filter', async () => {
    mocks.request.mockResolvedValue({ items: [], has_more: false })
    const wrapper = await mountSuspended(LogsPage, { route: '/logs' })
    await flushPromises()

    const vm = wrapper.vm as any
    vm.filters.window = 'all'
    vm.filters.instance_id = 'coder'
    vm.filters.endpoint = '/v1/responses'
    vm.filters.api_key_id = 'key-42'
    vm.filters.result = 'error'
    vm.filters.status_code = '503'
    vm.filters.streaming = 'true'
    vm.filters.search = 'needle'
    await wrapper.get('[data-testid="apply-request-log-filters"]').trigger('click')
    await flushPromises()

    const allHistoryCall = mocks.request.mock.calls.map(call => String(call[0])).find(path => path.includes('instance_id=coder')) || ''
    expect(allHistoryCall).toContain('endpoint=%2Fv1%2Fresponses')
    expect(allHistoryCall).toContain('api_key_id=key-42')
    expect(allHistoryCall).toContain('result=error')
    expect(allHistoryCall).toContain('status_code=503')
    expect(allHistoryCall).toContain('streaming=true')
    expect(allHistoryCall).toContain('search=needle')
    expect(allHistoryCall).not.toContain('since=')

    for (const window of ['15m', '24h', '7d']) {
      vm.filters.window = window
      await wrapper.get('[data-testid="apply-request-log-filters"]').trigger('click')
      await flushPromises()
    }
    const calls = mocks.request.mock.calls.map(call => String(call[0]))
    expect(calls.filter(path => path.includes('since=')).length).toBeGreaterThanOrEqual(3)
  })

  it('uses message errors and generic request/detail error fallbacks', async () => {
    let phase: 'history-message' | 'history-generic' | 'detail-message' | 'detail-generic' = 'history-message'
    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/observability/requests?')) {
        if (phase === 'history-message') throw new Error('history message')
        if (phase === 'history-generic') throw {}
        return { items: [baseRequest('lcm_detail')], has_more: false }
      }
      if (path === '/api/v1/observability/requests/lcm_detail') {
        if (phase === 'detail-message') throw new Error('detail message')
        throw {}
      }
      return {}
    })

    const wrapper = await mountSuspended(LogsPage, { route: '/logs' })
    await flushPromises()
    expect(wrapper.text()).toContain('history message')

    phase = 'history-generic'
    await wrapper.findAll('button').find(button => button.text() === 'Refresh')!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Unable to load request logs')

    phase = 'detail-message'
    await wrapper.findAll('button').find(button => button.text() === 'Refresh')!.trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="request-detail-trigger"]').trigger('click')
    await flushPromises()
    expect(document.body.textContent).toContain('detail message')

    phase = 'detail-generic'
    await wrapper.get('[data-testid="request-detail-trigger"]').trigger('click')
    await flushPromises()
    expect(document.body.textContent).toContain('Unable to load request details')
  })
})
