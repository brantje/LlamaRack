import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import LogsPage from '~/pages/logs/index.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

const pending = {
  id: 1,
  request_id: 'lcm_pending',
  trace_id: 'aee4ef30-0d78-40a5-b71c-ef0d9d04f47f',
  call_type: 'chat_completion',
  started_at: Date.now(),
  finished_at: 0,
  instance_id: 'coder',
  endpoint: '/v1/chat/completions',
  streaming: true,
  status_code: 0,
  result: 'pending',
  duration_ms: 0,
  prompt_tokens: 0,
  generated_tokens: 0,
  total_tokens: 0,
  queue_duration_ms: 0,
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
}

beforeEach(() => {
  mocks.request.mockReset()
  seedManager()
  document.body.innerHTML = ''
})

describe('Request logs review edge cases', () => {
  it('handles numeric status filters, sparse IDs and pending detail state', async () => {
    const legacy = { ...pending, id: 2, request_id: '', trace_id: '', result: 'success', status_code: 200, finished_at: Date.now() }
    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/observability/requests?')) return { items: [pending, legacy], limit: 25, offset: 0, has_more: false }
      if (path === '/api/v1/observability/requests/lcm_pending') return pending
      return {}
    })

    const wrapper = await mountSuspended(LogsPage, { route: '/logs' })
    await flushPromises()
    expect(wrapper.get('[data-testid="request-log-table"]').text()).toContain('pending')
    expect(wrapper.findAll('[data-testid="request-detail-trigger"]')).toHaveLength(1)

    ;(wrapper.vm as any).filters.status_code = 503
    await wrapper.get('[data-testid="apply-request-log-filters"]').trigger('click')
    await flushPromises()
    expect(mocks.request.mock.calls.some(call => String(call[0]).includes('status_code=503'))).toBe(true)

    await wrapper.get('[data-testid="request-detail-trigger"]').trigger('click')
    await flushPromises()
    expect(document.body.textContent).toContain('Request pending')
    expect(document.body.textContent).toContain('still in progress')
  })
})
