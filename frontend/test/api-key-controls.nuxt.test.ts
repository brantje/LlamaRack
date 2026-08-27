import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import APIPage from '~/pages/api.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

function resetAuthenticated() {
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
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: vi.fn() } })
  resetAuthenticated()
})

describe('API key controls', () => {
  it('disables, enables, rotates and retains revoked key history', async () => {
    let enabled = true
    let revokedAt: number | undefined
    let replacement: any
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/api-keys' && !options?.method) {
        return [
          { id: 'k1', name: 'LiteLLM', prefix: 'abc12345', enabled: revokedAt ? false : enabled, created_at: 1, revoked_at: revokedAt },
          ...(replacement ? [replacement] : [])
        ]
      }
      if (path === '/api/v1/api-keys/k1' && options?.method === 'PATCH') {
        enabled = options.body.enabled
        return undefined
      }
      if (path === '/api/v1/api-keys/k1/rotate' && options?.method === 'POST') {
        revokedAt = 10
        replacement = { id: 'k2', name: 'LiteLLM', prefix: 'new12345', enabled: true, created_at: 2 }
        return { key: replacement, secret: 'replacement-secret' }
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

    await wrapper.findAll('button').find(button => button.text() === 'Rotate')!.trigger('click')
    expect(document.body.textContent).toContain('The current secret will be revoked immediately')
    await clickConfirmation('confirm')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/api-keys/k1/rotate', { method: 'POST' })
    expect(wrapper.text()).toContain('replacement-secret')
    expect(wrapper.text()).toContain('Revoked')
    expect(wrapper.text()).toContain('new12345')
  })

  it('retains metadata after revoke and surfaces mutation errors', async () => {
    let revokedAt: number | undefined
    let mode: 'toggle-error' | 'revoke-error' | 'ok' = 'toggle-error'
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/api-keys' && !options?.method) {
        return [{ id: 'k1', name: 'SDK', prefix: 'abc12345', enabled: !revokedAt, created_at: 1, revoked_at: revokedAt }]
      }
      if (path === '/api/v1/api-keys/k1' && options?.method === 'PATCH' && mode === 'toggle-error') {
        throw { data: { error: 'toggle failed' } }
      }
      if (path === '/api/v1/api-keys/k1/revoke' && options?.method === 'POST') {
        if (mode === 'revoke-error') throw new Error('revoke failed')
        revokedAt = 20
        return undefined
      }
      return undefined
    })

    const wrapper = await mountSuspended(APIPage, { route: false })
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === 'Disable')!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('toggle failed')

    await wrapper.findAll('button').find(button => button.text() === 'Revoke')!.trigger('click')
    await clickConfirmation('cancel')
    expect(mocks.request).not.toHaveBeenCalledWith('/api/v1/api-keys/k1/revoke', { method: 'POST' })

    mode = 'revoke-error'
    await wrapper.findAll('button').find(button => button.text() === 'Revoke')!.trigger('click')
    await clickConfirmation('confirm')
    expect(wrapper.text()).toContain('revoke failed')

    mode = 'ok'
    await wrapper.findAll('button').find(button => button.text() === 'Revoke')!.trigger('click')
    expect(document.body.textContent).toContain('Revoked metadata is retained for history')
    await clickConfirmation('confirm')
    expect(wrapper.text()).toContain('SDK')
    expect(wrapper.text()).toContain('Revoked')
    expect(wrapper.findAll('button').some(button => button.text() === 'Revoke')).toBe(false)
  })
})
