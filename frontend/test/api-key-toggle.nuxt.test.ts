import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import APIPage from '~/pages/api.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

function seedAdmin() {
  const manager = useManager()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', role: 'admin', enabled: true }
  manager.models.value = []
  manager.artifacts.value = []
  manager.runtimes.value = {}
  manager.profile.value = null
  return manager
}

beforeEach(() => {
  mocks.request.mockReset()
  vi.stubGlobal('confirm', vi.fn(() => true))
  seedAdmin()
})

describe('API key state controls', () => {
  it('disables and re-enables keys without removing them', async () => {
    let enabled = true
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/api-keys/k1' && options?.method === 'PATCH') {
        enabled = options.body.enabled
        return undefined
      }
      if (path === '/api/v1/api-keys') {
        return [{ id: 'k1', name: 'sdk', prefix: 'abc12345', enabled, created_at: 1 }]
      }
      return []
    })

    const wrapper = await mountSuspended(APIPage, { route: false })
    await flushPromises()
    expect(wrapper.text()).toContain('Enabled')

    await wrapper.findAll('button').find(button => button.text() === 'Disable')!.trigger('click')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/api-keys/k1', { method: 'PATCH', body: { enabled: false } })
    expect(wrapper.text()).toContain('Disabled')
    expect(wrapper.findAll('button').some(button => button.text() === 'Enable')).toBe(true)

    await wrapper.findAll('button').find(button => button.text() === 'Enable')!.trigger('click')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/api-keys/k1', { method: 'PATCH', body: { enabled: true } })
    expect(wrapper.text()).toContain('Enabled')
  })

  it('keeps the revoke route and removes the key from the list', async () => {
    let exists = true
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/api-keys/k1/revoke' && options?.method === 'POST') {
        exists = false
        return undefined
      }
      if (path === '/api/v1/api-keys') {
        return exists ? [{ id: 'k1', name: 'sdk', prefix: 'abc12345', enabled: false, created_at: 1 }] : []
      }
      return []
    })

    const wrapper = await mountSuspended(APIPage, { route: false })
    await flushPromises()
    expect(wrapper.text()).toContain('sdk')
    expect(wrapper.text()).toContain('Revoked keys are permanently removed')

    await wrapper.findAll('button').find(button => button.text() === 'Revoke')!.trigger('click')
    await flushPromises()
    expect(confirm).toHaveBeenCalledWith('Revoke API key "sdk"? It will be permanently removed.')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/api-keys/k1/revoke', { method: 'POST' })
    expect(wrapper.text()).toContain('No API keys created yet.')
  })

  it('handles toggle failures and cancelled revoke', async () => {
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/api-keys/k1' && options?.method === 'PATCH') throw new Error('toggle failed')
      if (path === '/api/v1/api-keys') return [{ id: 'k1', name: 'sdk', prefix: 'abc12345', enabled: true, created_at: 1 }]
      return []
    })

    const wrapper = await mountSuspended(APIPage, { route: false })
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === 'Disable')!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('toggle failed')

    vi.mocked(confirm).mockReturnValueOnce(false)
    await wrapper.findAll('button').find(button => button.text() === 'Revoke')!.trigger('click')
    await flushPromises()
    expect(mocks.request).not.toHaveBeenCalledWith('/api/v1/api-keys/k1/revoke', { method: 'POST' })
  })
})
