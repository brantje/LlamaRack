import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import AdminIndexPage from '~/pages/admin/index.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

function resetManager() {
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.models.value = []
  manager.instances.value = []
  manager.runtimes.value = {}
  manager.profile.value = null
}

function button(wrapper: any, text: string) {
  const found = wrapper.findAll('button').find((candidate: any) => candidate.text().trim() === text)
  if (!found) throw new Error(`Missing button: ${text}`)
  return found
}

beforeEach(() => {
  mocks.request.mockReset()
  resetManager()
})

describe('Administration dashboard recovery state', () => {
  it('replaces failed skeletons with a scoped Retry state and recovers', async () => {
    let fail = true
    mocks.request.mockImplementation(async (path: string) => {
      if (path !== '/api/v1/admin/summary') return []
      if (fail) throw { data: { error: 'summary denied' } }
      return { users: { total: 3, enabled: 2 }, huggingface: { configured: true, prefix: 'hf_ab' }, llamacpp: { available: false } }
    })

    const wrapper = await mountSuspended(AdminIndexPage, { route: '/admin' })
    await flushPromises()

    expect(wrapper.get('[data-testid="admin-summary-error"]').text()).toContain('summary denied')
    expect(wrapper.find('[data-testid="admin-summary-loading"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="admin-summary-cards"]').exists()).toBe(false)

    fail = false
    await button(wrapper, 'Retry').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-testid="admin-summary-error"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="admin-summary-cards"]').text()).toContain('Unavailable')
  })

  it('turns malformed summary responses into an explicit recoverable error', async () => {
    mocks.request.mockResolvedValue({})
    const wrapper = await mountSuspended(AdminIndexPage, { route: '/admin' })
    await flushPromises()

    expect(wrapper.get('[data-testid="admin-summary-error"]').text()).toContain('Invalid administration summary response')
    expect(wrapper.find('[data-testid="admin-summary-loading"]').exists()).toBe(false)
  })
})
