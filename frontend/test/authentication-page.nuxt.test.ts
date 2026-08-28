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
  id: 'primary/provider',
  name: 'Authentik',
  enabled: true,
  issuer: 'https://auth.example.test/application/o/manager/',
  discovery_url: '',
  client_id: 'manager-client',
  scopes: ['openid', 'profile', 'email'],
  username_claim: 'preferred_username',
  authorization_endpoint: '',
  token_endpoint: '',
  jwks_url: '',
  secret_configured: true,
  last_tested_at: 1,
  last_test_succeeded: true
}

function resetManager(user = true) {
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = user ? { id: 1, username: 'admin', enabled: true } : null
  manager.localLoginEnabled.value = true
  manager.authProviders.value = []
  return manager
}

function wrapperButton(wrapper: any, text: string) {
  const button = wrapper.findAll('button').find((candidate: any) => candidate.text().trim() === text)
  if (!button) throw new Error(`Missing button: ${text}`)
  return button
}

function bodyButton(text: string) {
  const button = [...document.body.querySelectorAll<HTMLButtonElement>('button')].find(candidate => candidate.textContent?.trim() === text)
  if (!button) throw new Error(`Missing body button: ${text}`)
  return button
}

async function confirmDelete() {
  await flushPromises()
  const button = [...document.body.querySelectorAll<HTMLButtonElement>('[data-testid="confirmation-confirm"]')].at(-1)
  if (!button) throw new Error('Missing confirmation button')
  button.click()
  await flushPromises()
}

beforeEach(() => {
  mocks.request.mockReset()
  resetManager()
})

describe('Admin authentication page', () => {
  it('loads policy and providers and exercises save, test, add, edit and delete flows', async () => {
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/admin/auth/settings' && options?.method === 'PUT') return {}
      if (path === '/api/v1/admin/auth/settings') return authSettings
      if (path === '/api/v1/admin/auth/providers' && options?.method === 'POST') return provider
      if (path === '/api/v1/admin/auth/providers') return [provider]
      if (path === '/api/v1/admin/auth/providers/primary%2Fprovider/test' && options?.method === 'POST') return provider
      if (path === '/api/v1/admin/auth/providers/primary%2Fprovider' && options?.method === 'PUT') return provider
      if (path === '/api/v1/admin/auth/providers/primary%2Fprovider' && options?.method === 'DELETE') return {}
      if (path === '/api/v1/auth/providers') return { local_login_enabled: true, providers: [{ id: provider.id, name: provider.name }] }
      throw new Error(`unexpected path ${path}`)
    })

    const wrapper = await mountSuspended(AuthenticationPage, { route: false })
    await flushPromises()
    expect(wrapper.text()).toContain('Authentication')
    expect(wrapper.text()).toContain('Authentik')
    expect(wrapper.text()).toContain('Enabled')
    expect(wrapper.text()).toContain('Tested')
    expect(wrapper.text()).toContain('https://manager.example.test/api/v1/auth/oidc/primary%2Fprovider/callback')

    await wrapperButton(wrapper, 'Save authentication settings').trigger('click')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/admin/auth/settings', {
      method: 'PUT',
      body: {
        local_login_enabled: true,
        oidc_jit_provisioning_enabled: true,
        oidc_auto_link_enabled: false,
        external_url: 'https://manager.example.test/'
      }
    })
    expect(wrapper.text()).toContain('Authentication settings saved.')

    await wrapperButton(wrapper, 'Test').trigger('click')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/admin/auth/providers/primary%2Fprovider/test', { method: 'POST' })
    expect(wrapper.text()).toContain('Authentik configuration test passed.')

    await wrapperButton(wrapper, 'Add provider').trigger('click')
    await flushPromises()
    bodyButton('Save provider').click()
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/admin/auth/providers', expect.objectContaining({
      method: 'POST',
      body: expect.objectContaining({
        enabled: true,
        scopes: ['openid', 'profile', 'email'],
        username_claim: 'preferred_username'
      })
    }))
    expect(wrapper.text()).toContain('Provider added.')

    await wrapperButton(wrapper, 'Edit').trigger('click')
    await flushPromises()
    bodyButton('Save provider').click()
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/admin/auth/providers/primary%2Fprovider', expect.objectContaining({
      method: 'PUT',
      body: expect.objectContaining({ name: 'Authentik', client_id: 'manager-client', scopes: ['openid', 'profile', 'email'] })
    }))
    expect(wrapper.text()).toContain('Provider updated.')

    await wrapperButton(wrapper, 'Delete').trigger('click')
    await confirmDelete()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/admin/auth/providers/primary%2Fprovider', { method: 'DELETE' })
    wrapper.unmount()
  })

  it('renders empty, disabled, untested and callback-without-external-url states', async () => {
    let providerItems: any[] = []
    let settings = {
      ...authSettings,
      external_url: { value: '', source: 'default', editable: true }
    }
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/admin/auth/settings') return settings
      if (path === '/api/v1/admin/auth/providers') return providerItems
      throw new Error(path)
    })

    let wrapper = await mountSuspended(AuthenticationPage, { route: false })
    await flushPromises()
    expect(wrapper.text()).toContain('No OIDC providers')
    wrapper.unmount()

    resetManager()
    providerItems = [{ ...provider, enabled: false, last_test_succeeded: false, secret_configured: false }]
    wrapper = await mountSuspended(AuthenticationPage, { route: false })
    await flushPromises()
    expect(wrapper.text()).toContain('Disabled')
    expect(wrapper.text()).toContain('Needs test')
    expect(wrapper.text()).toContain('Configure External URL to generate callback URI')

    await wrapperButton(wrapper, 'Edit').trigger('click')
    await flushPromises()
    expect(document.body.textContent).toContain('Client secret')
    bodyButton('Cancel').click()
    await flushPromises()
    wrapper.unmount()
  })

  it('surfaces load, settings, provider-test, provider-save and provider-delete errors without weakening auth behavior', async () => {
    let mode: 'load-data' | 'load-message' | 'load-fallback' | 'ready' | 'settings' | 'test' | 'save' | 'delete' = 'load-data'
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (mode === 'load-data') throw { data: { error: 'auth load denied' } }
      if (mode === 'load-message') throw new Error('auth load exploded')
      if (mode === 'load-fallback') throw {}
      if (path === '/api/v1/admin/auth/settings' && options?.method === 'PUT') {
        if (mode === 'settings') throw { data: { error: 'settings denied' } }
        return {}
      }
      if (path === '/api/v1/admin/auth/settings') return authSettings
      if (path === '/api/v1/admin/auth/providers/primary%2Fprovider/test') {
        if (mode === 'test') throw new Error('test exploded')
        return provider
      }
      if (path === '/api/v1/admin/auth/providers' && options?.method === 'POST') {
        if (mode === 'save') throw {}
        return provider
      }
      if (path === '/api/v1/admin/auth/providers/primary%2Fprovider' && options?.method === 'DELETE') {
        if (mode === 'delete') throw { data: { error: 'delete denied' } }
        return {}
      }
      if (path === '/api/v1/admin/auth/providers') return [provider]
      if (path === '/api/v1/auth/providers') return { local_login_enabled: true, providers: [] }
      return {}
    })

    let wrapper = await mountSuspended(AuthenticationPage, { route: false })
    await flushPromises()
    expect(wrapper.text()).toContain('auth load denied')
    wrapper.unmount()

    resetManager()
    mode = 'load-message'
    wrapper = await mountSuspended(AuthenticationPage, { route: false })
    await flushPromises()
    expect(wrapper.text()).toContain('auth load exploded')
    wrapper.unmount()

    resetManager()
    mode = 'load-fallback'
    wrapper = await mountSuspended(AuthenticationPage, { route: false })
    await flushPromises()
    expect(wrapper.text()).toContain('Failed to load authentication settings')
    wrapper.unmount()

    resetManager()
    mode = 'ready'
    wrapper = await mountSuspended(AuthenticationPage, { route: false })
    await flushPromises()

    mode = 'settings'
    await wrapperButton(wrapper, 'Save authentication settings').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('settings denied')

    mode = 'test'
    await wrapperButton(wrapper, 'Test').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('test exploded')

    mode = 'save'
    await wrapperButton(wrapper, 'Add provider').trigger('click')
    await flushPromises()
    bodyButton('Save provider').click()
    await flushPromises()
    expect(wrapper.text()).toContain('Failed to save provider')

    bodyButton('Cancel').click()
    await flushPromises()
    mode = 'delete'
    await wrapperButton(wrapper, 'Delete').trigger('click')
    await confirmDelete()
    expect(wrapper.text()).toContain('delete denied')
    wrapper.unmount()
  })

  it('does not load management settings while signed out and honors delete cancellation', async () => {
    resetManager(false)
    mocks.request.mockResolvedValue([])
    let wrapper = await mountSuspended(AuthenticationPage, { route: false })
    await flushPromises()
    expect(mocks.request).not.toHaveBeenCalled()
    wrapper.unmount()

    resetManager()
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/admin/auth/settings') return authSettings
      if (path === '/api/v1/admin/auth/providers') return [provider]
      return {}
    })
    wrapper = await mountSuspended(AuthenticationPage, { route: false })
    await flushPromises()
    mocks.request.mockClear()
    await wrapperButton(wrapper, 'Delete').trigger('click')
    await flushPromises()
    const cancel = [...document.body.querySelectorAll<HTMLButtonElement>('[data-testid="confirmation-cancel"]')].at(-1)
    if (!cancel) throw new Error('Missing confirmation cancel button')
    cancel.click()
    await flushPromises()
    expect(mocks.request).not.toHaveBeenCalledWith('/api/v1/admin/auth/providers/primary%2Fprovider', { method: 'DELETE' })
    wrapper.unmount()
  })
})
