import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import LogsPage from '~/pages/logs/index.vue'
import { useManager } from '~/composables/useManager'

enableAutoUnmount(afterEach)

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

function requestRecord(id: string, overrides: Record<string, unknown> = {}) {
  return {
    id: id.endsWith('2') ? 2 : 1,
    request_id: id,
    trace_id: `trace-${id}`,
    model_id: 'm1',
    model_name: 'Qwen Coder 7B',
    call_type: 'chat_completion',
    started_at: Date.now() - 1000,
    finished_at: Date.now(),
    instance_id: 'coder',
    endpoint: '/v1/chat/completions',
    api_key: { id: 'key-1', name: 'Home Assistant', prefix: 'ha_' },
    client_ip: '198.51.100.10',
    user_agent: 'HomeAssistant/2026.8',
    streaming: true,
    status_code: 200,
    result: 'success',
    duration_ms: 1000,
    ttft_ms: 40,
    prompt_tokens: 120,
    generated_tokens: 48,
    total_tokens: 168,
    prompt_tokens_per_second: 120,
    generation_tokens_per_second: 48,
    queue_duration_ms: 5,
    load_duration_ms: 0,
    autoloaded: false,
    ...overrides
  } as any
}

function seedManager() {
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.models.value = [{ id: 'm1', name: 'Qwen Coder 7B', gguf_path: 'coder.gguf', total_bytes: 1, context_length: 4096 }]
  manager.instances.value = [{ id: 'coder', model_id: 'm1', name: 'Coder', enabled: true, autoload_enabled: true, always_on: false, priority: 'normal', eviction_enabled: true, idle_unload_seconds: 0, gpu_mode: 'auto', gpu_devices: [], tensor_split: '', request_log_mode: 'metadata' }]
  manager.runtimes.value = {}
  manager.runtimeTelemetry.value = {}
  manager.observabilityLive.value = null
  manager.runtimeEventsConnected.value = true
}

beforeEach(() => {
  mocks.request.mockReset()
  seedManager()
})

describe('LiteLLM-style request detail layout', () => {
  it('uses two-column detail panels and exposes both token rates without a success banner', async () => {
    const item = requestRecord('lcm_single')
    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/observability/requests?')) return { items: [item], has_more: false }
      if (path === '/api/v1/observability/requests/lcm_single') return item
      return {}
    })

    const wrapper = await mountSuspended(LogsPage, { route: '/logs' })
    await flushPromises()

    const table = wrapper.get('[data-testid="request-log-table"]')
    expect(table.text()).toContain('Prompt tok/s')
    expect(table.text()).toContain('Gen tok/s')
    expect(table.text()).toContain('120.0 tok/s')
    expect(table.text()).toContain('48.0 tok/s')

    await wrapper.get('[data-testid="request-detail-trigger"]').trigger('click')
    await flushPromises()

    const sidebar = document.body.querySelector('[data-testid="request-sidebar"]')
    expect(sidebar).not.toBeNull()
    expect(sidebar?.textContent).toContain('1 request')
    expect(sidebar?.textContent).toContain('lcm_single')

    const overview = document.body.querySelector('[data-testid="request-detail-overview"]')
    const overviewGrid = document.body.querySelector('[data-testid="request-detail-overview-grid"]')
    const metrics = document.body.querySelector('[data-testid="request-detail-metrics"]')
    const metricsGrid = document.body.querySelector('[data-testid="request-detail-metrics-grid"]')
    expect(overview?.textContent).toContain('Request Details')
    expect(overview?.textContent).toContain('Model:')
    expect(overview?.textContent).toContain('Qwen Coder 7B')
    expect(overview?.textContent).toContain('Streaming:True')
    expect(overviewGrid?.className).toContain('lg:grid-cols-2')
    expect(metricsGrid?.className).toContain('lg:grid-cols-2')
    expect(metrics?.textContent).toContain('Tokens:168')
    expect(metrics?.textContent).toContain('120 prompt + 48 completion')
    expect(metrics?.textContent).toContain('Prompt Processing:120.0 tok/s')
    expect(metrics?.textContent).toContain('Generation Speed:48.0 tok/s')
    expect(document.body.querySelector('[data-testid="request-failure-banner"]')).toBeNull()

    const vm = wrapper.vm as any
    expect(vm.formatRate()).toBe('—')
    expect(vm.formatRate(Number.NaN)).toBe('—')
  })

  it('shows a failure banner only for failed requests', async () => {
    const item = requestRecord('lcm_failed', { result: 'error', status_code: 500, error: 'Worker unavailable' })
    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/observability/requests?')) return { items: [item], has_more: false }
      if (path === '/api/v1/observability/requests/lcm_failed') return item
      return {}
    })

    const wrapper = await mountSuspended(LogsPage, { route: '/logs' })
    await flushPromises()
    await wrapper.get('[data-testid="request-detail-trigger"]').trigger('click')
    await flushPromises()

    const failure = document.body.querySelector('[data-testid="request-failure-banner"]')
    expect(failure).not.toBeNull()
    expect(failure?.textContent).toContain('Request Failed')
    expect(failure?.textContent).toContain('Worker unavailable')
  })

  it('uses the same sidebar for every request in a multi-request session', async () => {
    const sessionID = 'ha-conversation-123'
    const first = requestRecord('lcm_session_1', { session_id: sessionID, session_total_count: 2, duration_ms: 1600 })
    const second = requestRecord('lcm_session_2', { session_id: sessionID, session_total_count: 2, duration_ms: 800, started_at: Date.now() - 500 })
    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/observability/requests?')) {
        if (path.includes(`session_id=${encodeURIComponent(sessionID)}`)) return { items: [first, second], has_more: false }
        return { items: [first], has_more: false }
      }
      if (path === '/api/v1/observability/requests/lcm_session_1') return first
      if (path === '/api/v1/observability/requests/lcm_session_2') return second
      return {}
    })

    const wrapper = await mountSuspended(LogsPage, { route: '/logs' })
    await flushPromises()
    await wrapper.get('[data-testid="request-detail-trigger"]').trigger('click')
    await flushPromises()

    const sidebar = document.body.querySelector('[data-testid="request-sidebar"]')
    expect(sidebar?.textContent).toContain('2 requests')
    expect(sidebar?.textContent).toContain(sessionID)
    expect(sidebar?.textContent).toContain('lcm_session_1')
    expect(sidebar?.textContent).toContain('lcm_session_2')
  })
})
