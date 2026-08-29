import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import AuthenticationPage from '~/pages/admin/authentication.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

const authSettings = {
  local_login_enabled: { value: true, source: 'default', editable: true },
  oidc_jit_provisioning_enabled: { value: true, source: 'default', editable: true },
  oidc_auto_link_enabled: { value: false, source: 'default', editable: true },
  external_url: { value: 'https://manager.example.test/', source: 'database', editable: true }
}

function resetManager() {
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.localLoginEnabled.value = true
  manager.authProviders.value = []
}

function bodyButton(text: string) {
  const button = [...document.body.querySelectorAll<HTMLButtonElement>('button')].find(candidate => candidate.textContent?.trim() === text)
  if (!button) throw new Error(`Missing body button: ${text}`)
  return button
}

beforeEach(() => {
  mocks.request.mockReset()
  resetManager()
})

describe('OIDC provider draft testing', () => {
  it('tests a new provider without saving it and shows probe errors in the modal', async () => {
    let failProbe = false
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/admin/auth/settings') return authSettings
      if (path === '/api/v1/admin/auth/providers' && !options?.method) return []
      if (path === '/api/v1/admin/auth/providers/test' && options?.method === 'POST') {
        if (failProbe) throw { data: { error: 'OIDC discovery failed' } }
        return { ok: true }
      }
      throw new Error(`unexpected path ${path}`)
    })

    const wrapper = await mountSuspended(AuthenticationPage, { route: false })
    await flushPromises()
    const add = wrapper.findAll('button').find(button => button.text().trim() === 'Add provider')
    if (!add) throw new Error('Missing Add provider button')
    await add.trigger('click')
    await flushPromises()

    const inputs = [...document.body.querySelectorAll<HTMLInputElement>('input')]
    expect(inputs.length).toBeGreaterThanOrEqual(10)
    inputs[0]!.value = 'Authentik'
    inputs[0]!.dispatchEvent(new Event('input'))
    inputs[1]!.value = 'https://auth.example.test/application/o/manager/'
    inputs[1]!.dispatchEvent(new Event('input'))
    inputs[3]!.value = 'manager-client'
    inputs[3]!.dispatchEvent(new Event('input'))
    inputs[4]!.value = 'client-secret'
    inputs[4]!.dispatchEvent(new Event('input'))
    await flushPromises()

    bodyButton('Test configuration').click()
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/admin/auth/providers/test', {
      method: 'POST',
      body: expect.objectContaining({
        name: 'Authentik',
        issuer: 'https://auth.example.test/application/o/manager/',
        client_id: 'manager-client',
        client_secret: 'client-secret',
        scopes: ['openid', 'profile', 'email']
      })
    })
    expect(document.body.textContent).toContain('Provider configuration test passed.')
    expect(mocks.request).not.toHaveBeenCalledWith('/api/v1/admin/auth/providers', expect.objectContaining({ method: 'POST' }))

    failProbe = true
    bodyButton('Test configuration').click()
    await flushPromises()
    expect(document.body.textContent).toContain('OIDC discovery failed')
    wrapper.unmount()
  })
})
