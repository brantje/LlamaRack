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

const provider = {
  id: 'authentik-provider',
  name: 'Authentik',
  enabled: true,
  issuer: 'https://auth.example.test/application/o/manager',
  discovery_url: '',
  client_id: 'manager-client',
  scopes: ['openid', 'profile', 'email'],
  username_claim: 'preferred_username',
  secret_configured: true,
  last_test_succeeded: false
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

describe('OIDC provider edit testing', () => {
  it('keeps the test action inside the edit provider modal', async () => {
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/admin/auth/settings') return authSettings
      if (path === '/api/v1/admin/auth/providers' && !options?.method) return [provider]
      if (path === `/api/v1/admin/auth/providers/${provider.id}/test` && options?.method === 'POST') return provider
      throw new Error(`unexpected path ${path}`)
    })

    const wrapper = await mountSuspended(AuthenticationPage, { route: false })
    await flushPromises()

    const edit = wrapper.findAll('button').find(button => button.text().trim() === 'Edit')
    if (!edit) throw new Error('Missing Edit button')
    await edit.trigger('click')
    await flushPromises()

    bodyButton('Test configuration').click()
    await flushPromises()

    expect(mocks.request).toHaveBeenCalledWith(`/api/v1/admin/auth/providers/${provider.id}/test`, { method: 'POST' })
    expect(document.body.textContent).toContain('Provider configuration test passed.')
    wrapper.unmount()
  })
})
