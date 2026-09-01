import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import LogsPage from '~/pages/logs/index.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

function seedManager() {
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.models.value = []
  manager.instances.value = []
  manager.runtimes.value = {}
  manager.runtimeTelemetry.value = {}
  manager.observabilityLive.value = null
}

beforeEach(() => {
  mocks.request.mockReset()
  mocks.request.mockImplementation(async (path: string) => path.startsWith('/api/v1/observability/requests?') ? { items: [], has_more: false } : {})
  seedManager()
})

describe('Request log filter collapse', () => {
  it('starts expanded, summarizes active filters after apply, and can reopen', async () => {
    const wrapper = await mountSuspended(LogsPage, { route: '/logs' })
    await flushPromises()
    expect(wrapper.get('[data-testid="request-log-filters-toggle"]').text()).toContain('Hide filters')
    expect(wrapper.get('[data-testid="request-log-active-filter-count"]').text()).toContain('0 active')

    const search = wrapper.get('input[placeholder="Request ID, session, trace, model…"]')
    await search.setValue('qwen')
    await wrapper.get('[data-testid="apply-request-log-filters"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="request-log-active-filter-count"]').text()).toContain('1 active')
    expect(wrapper.get('[data-testid="request-log-filters-toggle"]').text()).toContain('Edit filters')
    expect(mocks.request.mock.calls.some(call => String(call[0]).includes('search=qwen'))).toBe(true)

    await wrapper.get('[data-testid="request-log-filters-toggle"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="request-log-filters-toggle"]').text()).toContain('Hide filters')
  })
})
