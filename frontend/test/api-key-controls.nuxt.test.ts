import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import APIPage from '~/pages/api.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

function resetAdmin() {
  const manager = useManager()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', role: 'admin', enabled: true }
  manager.models.value = []
  manager.runtimes.value = {}
  manager.profile.value = null
  return manager
}

beforeEach(() => {
  mocks.request.mockReset()
  vi.stubGlobal('confirm', vi.fn(() => true))
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: vi.fn() } })
  resetAdmin()
})

describe('API key controls', () => {
  it('disables, enables, and permanently revokes a key', async () => {
    let enabled = true
    let exists = true
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/api-keys' && !options?.method) {
        return exists ? [{ id: 'k1', name: 'LiteLLM', prefix: 'abc12345', enabled, created_at: 1 }] : []
      }
      if (path === '/api/v1/api-keys/k1' && options?.method === 'PATCH') {
        enabled = options.body.enabled
        return undefined
      }
      if (path === '/api/v1/api-keys/k1/revoke' && options?.method === 'POST') {
        exists = false
        return undefined
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

    await wrapper.findAll('button').find(button => button.text() === 'Enable')!.trigger('click')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/api-keys/k1', { method: 'PATCH', body: { enabled: true } })
    expect(wrapper.text()).toContain('Enabled')

    await wrapper.findAll('button').find(button => button.text() === 'Revoke')!.trigger('click')
    await flushPromises()
    expect(confirm).toHaveBeenCalledWith('Revoke API key "LiteLLM"? It will be permanently removed.')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/api-keys/k1/revoke', { method: 'POST' })
    expect(wrapper.text()).toContain('No API keys created yet.')
  })

  it('keeps keys on cancelled revoke and surfaces mutation errors', async () => {
    let mode: 'toggle-error' | 'revoke-error' = 'toggle-error'
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/api-keys' && !options?.method) {
        return [{ id: 'k1', name: 'SDK', prefix: 'abc12345', enabled: true, created_at: 1 }]
      }
      if (path === '/api/v1/api-keys/k1' && options?.method === 'PATCH' && mode === 'toggle-error') {
        throw { data: { error: 'toggle failed' } }
      }
      if (path === '/api/v1/api-keys/k1/revoke' && options?.method === 'POST' && mode === 'revoke-error') {
        throw new Error('revoke failed')
      }
      return undefined
    })

    const wrapper = await mountSuspended(APIPage, { route: false })
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === 'Disable')!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('toggle failed')

    vi.stubGlobal('confirm', vi.fn(() => false))
    await wrapper.findAll('button').find(button => button.text() === 'Revoke')!.trigger('click')
    await flushPromises()
    expect(mocks.request).not.toHaveBeenCalledWith('/api/v1/api-keys/k1/revoke', { method: 'POST' })

    vi.stubGlobal('confirm', vi.fn(() => true))
    mode = 'revoke-error'
    await wrapper.findAll('button').find(button => button.text() === 'Revoke')!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('revoke failed')
  })
})
