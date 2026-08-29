import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import LogsPage from '~/pages/logs/index.vue'
import DashboardPage from '~/pages/index.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

const traceID = 'aee4ef30-0d78-40a5-b71c-ef0d9d04f47f'
const request = {
  id: 1,
  request_id: 'lcm_abc123',
  trace_id: traceID,
  call_type: 'chat_completion',
  started_at: Date.now() - 1000,
  finished_at: Date.now(),
  instance_id: 'coder',
  endpoint: '/v1/chat/completions',
  api_key: { id: 'key-1', name: 'Primary key', prefix: 'pk_live' },
  client_ip: '198.51.100.10',
  user_agent: 'request-test/1.0',
  streaming: true,
  status_code: 200,
  result: 'success',
  duration_ms: 1000,
  ttft_ms: 35,
  prompt_tokens: 10,
  generated_tokens: 20,
  total_tokens: 30,
  prompt_tokens_per_second: 100,
  generation_tokens_per_second: 50,
  queue_duration_ms: 10,
  load_duration_ms: 0,
  autoloaded: false
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
  return manager
}

beforeEach(() => {
  mocks.request.mockReset()
  seedManager()
  document.body.innerHTML = ''
})

describe('Request logs', () => {
  it('renders manager-native request fields, trace links and server-side search', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/observability/requests?')) return { items: [request], limit: 25, offset: 0, has_more: false }
      return {}
    })
    const wrapper = await mountSuspended(LogsPage, { route: '/logs' })
    await flushPromises()

    const table = wrapper.get('[data-testid="request-log-table"]')
    expect(table.text()).toContain('Chat Completion')
    expect(table.text()).toContain('lcm_abc123')
    expect(table.text()).toContain(traceID)
    expect(table.text()).toContain('Primary key')
    expect(table.text()).toContain('1.00 s')
    expect(wrapper.find(`a[href="/logs?trace_id=${traceID}"]`).exists()).toBe(true)
    expect(String(mocks.request.mock.calls[0]![0])).toContain('limit=25')

    const search = wrapper.get('[data-testid="request-log-filters"] input[placeholder="Request ID, trace, model…"]')
    await search.setValue('coder')
    await wrapper.get('[data-testid="apply-request-log-filters"]').trigger('click')
    await flushPromises()
    expect(mocks.request.mock.calls.some(call => String(call[0]).includes('search=coder'))).toBe(true)
  })

  it('uses the trace query as a server filter and supports clearing it', async () => {
    mocks.request.mockResolvedValue({ items: [request], limit: 25, offset: 0, has_more: false })
    const wrapper = await mountSuspended(LogsPage, { route: `/logs?trace_id=${traceID}` })
    await flushPromises()
    expect(wrapper.get('[data-testid="trace-filter"]').text()).toContain(traceID)
    expect(wrapper.get('[data-testid="request-log-table"]').text()).toContain('Oldest first for this trace')
    expect(mocks.request.mock.calls.some(call => String(call[0]).includes(`trace_id=${traceID}`))).toBe(true)
    const clear = wrapper.get('[data-testid="trace-filter"] button')
    await clear.trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="trace-filter"]').exists()).toBe(false)
  })

  it('paginates bounded history and handles empty and API error states', async () => {
    let failHistory = false
    mocks.request.mockImplementation(async (path: string) => {
      if (!path.startsWith('/api/v1/observability/requests?')) return {}
      if (failHistory) throw { data: { error: 'history unavailable' } }
      if (path.includes('offset=25')) return { items: [], limit: 25, offset: 25, has_more: false }
      return { items: [request], limit: 25, offset: 0, has_more: true }
    })
    const wrapper = await mountSuspended(LogsPage, { route: '/logs' })
    await flushPromises()
    let buttons = wrapper.get('[data-testid="request-log-table"]').findAll('button')
    await buttons.find(button => button.text() === 'Next')!.trigger('click')
    await flushPromises()
    expect(mocks.request.mock.calls.some(call => String(call[0]).includes('offset=25'))).toBe(true)
    expect(wrapper.text()).toContain('No matching requests')

    buttons = wrapper.get('[data-testid="request-log-table"]').findAll('button')
    await buttons.find(button => button.text() === 'Previous')!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('lcm_abc123')

    failHistory = true
    await wrapper.findAll('button').find(button => button.text() === 'Refresh')!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('history unavailable')
  })

  it('opens the right-side detail slideover and explains metadata-only content', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/observability/requests?')) return { items: [request], limit: 25, offset: 0, has_more: false }
      if (path === '/api/v1/observability/requests/lcm_abc123') return request
      return {}
    })
    const wrapper = await mountSuspended(LogsPage, { route: '/logs' })
    await flushPromises()
    await wrapper.get('[data-testid="request-detail-trigger"]').trigger('click')
    await flushPromises()
    const bodyText = document.body.textContent || wrapper.text()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/observability/requests/lcm_abc123')
    expect(bodyText).toContain('Content not recorded')
    expect(bodyText).toContain('198.51.100.10')
    expect(bodyText).toContain('50.0 tok/s')
  })

  it('renders full payload messages, tools and JSON in detail', async () => {
    const full = {
      ...request,
      request_body: JSON.stringify({ model: 'coder', messages: [{ role: 'user', content: 'hello full log' }], tools: [{ type: 'function', function: { name: 'lookup' } }] }),
      response_body: JSON.stringify({ choices: [{ message: { role: 'assistant', content: 'hi', tool_calls: [{ id: 'call-1', type: 'function', function: { name: 'lookup', arguments: '{}' } }] } }] })
    }
    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/observability/requests?')) return { items: [request], limit: 25, offset: 0, has_more: false }
      if (path === '/api/v1/observability/requests/lcm_abc123') return full
      return {}
    })
    const wrapper = await mountSuspended(LogsPage, { route: '/logs' })
    await flushPromises()
    await wrapper.get('[data-testid="request-detail-trigger"]').trigger('click')
    await flushPromises()
    expect(document.body.textContent).toContain('hello full log')
    expect(document.body.textContent).toContain('lookup')
    const jsonButton = Array.from(document.body.querySelectorAll('button')).find(button => button.textContent?.trim() === 'JSON') as HTMLButtonElement | undefined
    jsonButton?.click()
    await flushPromises()
    expect(document.body.textContent).toContain('Request JSON')
  })

  it('opens a deep-linked request and shows detail API failures', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/observability/requests?')) return { items: [], limit: 25, offset: 0, has_more: false }
      if (path === '/api/v1/observability/requests/lcm_missing') throw { data: { error: 'request detail unavailable' } }
      return {}
    })
    await mountSuspended(LogsPage, { route: '/logs?request_id=lcm_missing' })
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/observability/requests/lcm_missing')
    expect(document.body.textContent).toContain('request detail unavailable')
  })

  it('links Dashboard request traffic and the Open logs action into the explorer', async () => {
    const manager = seedManager()
    manager.observabilityLive.value = {
      collected_at: new Date().toISOString(),
      hardware: { ram_total_bytes: 0, ram_available_bytes: 0, collected_at: '', processes: [], gpus: [] },
      telemetry: [],
      requests: [{ ...request, status_code: 503, result: 'error', error: 'worker unavailable' }],
      gateway: { since: Date.now() - 900000, requests: 1, successes: 0, errors: 1, active: 0, queued: 0, active_api_keys: 1, prompt_tokens: 10, generated_tokens: 20, total_tokens: 30 }
    } as any
    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/observability/summary')) return { ...manager.observabilityLive.value!.gateway, lifecycle: {}, hardware: { hardware: manager.observabilityLive.value!.hardware, telemetry: [] } }
      if (path.startsWith('/api/v1/observability/requests')) return { items: [request] }
      if (path === '/api/v1/settings/general') return { idle_unload_seconds: { value: 0, source: 'default', editable: true } }
      return {}
    })
    const wrapper = await mountSuspended(DashboardPage, { route: '/' })
    await flushPromises()
    expect(wrapper.get('[data-testid="open-request-logs"]').attributes('href')).toBe('/logs')
    expect(wrapper.find('a[href="/logs?request_id=lcm_abc123"]').exists()).toBe(true)
  })
})
