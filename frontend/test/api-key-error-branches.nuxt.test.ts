import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import APIPage from '~/pages/api.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

function resetAuthenticated() {
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
  manager.profile.value = null
}

async function confirm(kind: 'confirm' | 'cancel' = 'confirm') {
  await flushPromises()
  const target = [...document.body.querySelectorAll<HTMLButtonElement>(`[data-testid="confirmation-${kind}"]`)].at(-1)
  if (!target) throw new Error(`Missing confirmation ${kind}`)
  target.click()
  await flushPromises()
}

function action(wrapper: any, label: string) {
  const found = wrapper.findAll('button').find((button: any) => button.text().trim() === label)
  if (!found) throw new Error(`Missing ${label} button`)
  return found
}

beforeEach(() => {
  mocks.request.mockReset()
  resetAuthenticated()
})

describe('API key branch coverage', () => {
  it('normalizes an empty response and covers every load error fallback', async () => {
    mocks.request.mockResolvedValueOnce(null)
    let wrapper = await mountSuspended(APIPage, { route: false })
    await flushPromises()
    expect(wrapper.text()).toContain('No API keys created yet.')
    wrapper.unmount()

    for (const [failure, message] of [
      [{ data: { error: 'key list denied' } }, 'key list denied'],
      [new Error('key list exploded'), 'key list exploded'],
      [{}, 'Unable to load API keys']
    ] as const) {
      resetAuthenticated()
      mocks.request.mockReset()
      mocks.request.mockRejectedValue(failure)
      wrapper = await mountSuspended(APIPage, { route: false })
      await flushPromises()
      expect(wrapper.text()).toContain(message)
      wrapper.unmount()
      await flushPromises()
    }
  })

  it('creates a key and covers every create error fallback', async () => {
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/api-keys' && options?.method === 'POST') return { key: { id: 'new', name: 'default', prefix: 'new12345', enabled: true, created_at: 1 }, secret: 'one-time-secret' }
      if (path === '/api/v1/api-keys') return []
      return undefined
    })
    let wrapper = await mountSuspended(APIPage, { route: false })
    await flushPromises()
    await action(wrapper, 'Create key').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('one-time-secret')
    wrapper.unmount()

    for (const [failure, message] of [
      [{ data: { error: 'create denied' } }, 'create denied'],
      [new Error('create exploded'), 'create exploded'],
      [{}, 'Unable to create API key']
    ] as const) {
      resetAuthenticated()
      mocks.request.mockReset()
      mocks.request.mockImplementation(async (path: string, options?: any) => {
        if (path === '/api/v1/api-keys' && options?.method === 'POST') throw failure
        if (path === '/api/v1/api-keys') return []
        return undefined
      })
      wrapper = await mountSuspended(APIPage, { route: false })
      await flushPromises()
      await action(wrapper, 'Create key').trigger('click')
      await flushPromises()
      expect(wrapper.text()).toContain(message)
      wrapper.unmount()
      await flushPromises()
    }
  })

  it('covers toggle, revoke and rotate fallback/cancel branches', async () => {
    const key = { id: 'k1', name: 'Client', prefix: 'abc12345', enabled: true, created_at: 1 }
    let mutation: 'toggle' | 'revoke' | 'rotate' = 'toggle'
    let failure: any = {}
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/api-keys' && !options?.method) return [key]
      if (mutation === 'toggle' && path === '/api/v1/api-keys/k1' && options?.method === 'PATCH') throw failure
      if (mutation === 'revoke' && path === '/api/v1/api-keys/k1/revoke' && options?.method === 'POST') throw failure
      if (mutation === 'rotate' && path === '/api/v1/api-keys/k1/rotate' && options?.method === 'POST') throw failure
      return undefined
    })

    const wrapper = await mountSuspended(APIPage, { route: false })
    await flushPromises()

    await action(wrapper, 'Disable').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Unable to update API key')

    mutation = 'revoke'
    await action(wrapper, 'Revoke').trigger('click')
    await confirm('cancel')
    expect(mocks.request).not.toHaveBeenCalledWith('/api/v1/api-keys/k1/revoke', { method: 'POST' })

    failure = {}
    await action(wrapper, 'Revoke').trigger('click')
    await confirm()
    expect(wrapper.text()).toContain('Unable to revoke API key')

    mutation = 'rotate'
    await action(wrapper, 'Rotate').trigger('click')
    await confirm('cancel')
    expect(mocks.request).not.toHaveBeenCalledWith('/api/v1/api-keys/k1/rotate', { method: 'POST' })

    failure = { data: { error: 'rotate denied' } }
    await action(wrapper, 'Rotate').trigger('click')
    await confirm()
    expect(wrapper.text()).toContain('rotate denied')

    failure = {}
    await action(wrapper, 'Rotate').trigger('click')
    await confirm()
    expect(wrapper.text()).toContain('Unable to rotate API key')
    wrapper.unmount()
  })
})
