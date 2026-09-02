import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import APIPage from '~/pages/api.vue'
import { useManager, type APIKey } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

function sampleKey(overrides: Partial<APIKey> = {}): APIKey {
  return {
    id: 'k1',
    name: 'sdk',
    prefix: 'sk-abcd1234',
    enabled: true,
    key_type: 'inference',
    owner_kind: 'user',
    owner_id: 1,
    owner_name: 'admin',
    owner_enabled: true,
    created_at: 1,
    ...overrides
  }
}

function seedAuthenticated() {
  const manager = useManager()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.models.value = []
  manager.instances.value = []
  manager.runtimes.value = {}
  manager.profile.value = null
  return manager
}

async function clickConfirmation(kind: 'confirm' | 'cancel') {
  await flushPromises()
  const buttons = [...document.body.querySelectorAll<HTMLButtonElement>(`[data-testid="confirmation-${kind}"]`)]
  const button = buttons.at(-1)
  if (!button) throw new Error(`Missing confirmation ${kind} button`)
  button.click()
  await flushPromises()
}

beforeEach(() => {
  mocks.request.mockReset()
  seedAuthenticated()
})

describe('API key state controls', () => {
  it('disables and re-enables active keys without removing metadata', async () => {
    let enabled = true
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/api-keys/k1' && options?.method === 'PATCH') {
        enabled = options.body.enabled
        return undefined
      }
      if (path === '/api/v1/api-keys') return [sampleKey({ enabled })]
      return []
    })

    const wrapper = await mountSuspended(APIPage, { route: false })
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === 'Disable')!.trigger('click')
    await clickConfirmation('confirm')
    expect(wrapper.text()).toContain('Disabled')
    expect(wrapper.text()).toContain('sdk')
    await wrapper.findAll('button').find(button => button.text() === 'Enable')!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Enabled')
  })

  it('keeps keys listed after disable instead of deleting them', async () => {
    let enabled = true
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/api-keys/k1' && options?.method === 'PATCH') {
        enabled = options.body.enabled
        return undefined
      }
      if (path === '/api/v1/api-keys') return [sampleKey({ enabled })]
      return []
    })

    const wrapper = await mountSuspended(APIPage, { route: false })
    await flushPromises()
    expect(wrapper.text()).toContain('Disable keeps a key for later')
    expect(wrapper.findAll('button').some(button => button.text() === 'Revoke')).toBe(false)

    await wrapper.findAll('button').find(button => button.text() === 'Disable')!.trigger('click')
    await clickConfirmation('confirm')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/api-keys/k1', { method: 'PATCH', body: { enabled: false } })
    expect(wrapper.text()).toContain('sdk')
    expect(wrapper.text()).toContain('Disabled')
    expect(wrapper.text()).not.toContain('No API keys created yet.')
  })

  it('handles toggle failures and cancelled disable', async () => {
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/api-keys/k1' && options?.method === 'PATCH') throw new Error('toggle failed')
      if (path === '/api/v1/api-keys') return [sampleKey()]
      return []
    })

    const wrapper = await mountSuspended(APIPage, { route: false })
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === 'Disable')!.trigger('click')
    await clickConfirmation('confirm')
    expect(wrapper.text()).toContain('toggle failed')

    await wrapper.findAll('button').find(button => button.text() === 'Disable')!.trigger('click')
    await clickConfirmation('cancel')
    expect(mocks.request.mock.calls.filter(call => call[0] === '/api/v1/api-keys/k1').length).toBe(1)
  })
})
