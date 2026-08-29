import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import LogsPage from '~/pages/logs/index.vue'
import { useManager } from '~/composables/useManager'

enableAutoUnmount(afterEach)

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

function record(requestID: string, startedAt: number) {
  return {
    id: startedAt,
    request_id: requestID,
    trace_id: '11111111-2222-4333-8444-555555555555',
    session_id: 'shared-session',
    session_total_count: 2,
    model_id: 'm1',
    model_name: 'Shared Model',
    call_type: 'chat_completion',
    started_at: startedAt,
    finished_at: startedAt + 100,
    instance_id: 'coder',
    endpoint: '/v1/chat/completions',
    streaming: false,
    status_code: 200,
    result: 'success',
    duration_ms: 100,
    prompt_tokens: 10,
    generated_tokens: 20,
    total_tokens: 30,
    queue_duration_ms: 0,
    load_duration_ms: 0,
    autoloaded: false
  }
}

beforeEach(() => {
  mocks.request.mockReset()
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.models.value = [{ id: 'm1', name: 'Shared Model', gguf_path: 'shared.gguf', total_bytes: 1, context_length: 4096 }]
  manager.instances.value = [{ id: 'coder', model_id: 'm1', name: 'Coder', enabled: true, autoload_enabled: true, always_on: false, priority: 'normal', eviction_enabled: true, idle_unload_seconds: 0, gpu_mode: 'auto', gpu_devices: [], tensor_split: '', request_log_mode: 'metadata' }]
  manager.observabilityLive.value = null
})

describe('Request log session rows', () => {
  it('keeps same-session requests as separate table rows and groups only in the sidepanel', async () => {
    const first = record('lcm_session_visible_1', 1000)
    const second = record('lcm_session_visible_2', 1100)
    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/observability/requests?')) {
        return { items: [first, second], limit: path.includes('session_id=') ? 100 : 25, offset: 0, has_more: false }
      }
      if (path.endsWith('/lcm_session_visible_1')) return first
      if (path.endsWith('/lcm_session_visible_2')) return second
      return {}
    })

    const wrapper = await mountSuspended(LogsPage, { route: '/logs' })
    await flushPromises()

    const table = wrapper.get('[data-testid="request-log-table"]')
    expect(table.text()).toContain('2 rows')
    const triggers = wrapper.findAll('[data-testid="request-detail-trigger"]')
    expect(triggers).toHaveLength(2)

    await triggers[0]!.trigger('click')
    await flushPromises()
    const sidebar = document.body.querySelector('[data-testid="request-sidebar"]')
    expect(sidebar?.textContent).toContain('lcm_session_visible_1')
    expect(sidebar?.textContent).toContain('lcm_session_visible_2')
  })
})
