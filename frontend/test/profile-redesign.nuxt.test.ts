import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import ProfilePage from '~/pages/profile.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

function resetManager(username = 'john.doe') {
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username, enabled: true }
  manager.models.value = []
  manager.instances.value = []
  manager.runtimes.value = {}
  manager.runtimeTelemetry.value = {}
  manager.profile.value = null
}

beforeEach(() => {
  mocks.request.mockReset()
  resetManager()
})

describe('Profile redesign', () => {
  it('renders the flat account, password, authentication-source and session sections', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/me') return { id: 1, username: 'john.doe', enabled: false, created_at: 10 }
      if (path === '/api/v1/me/sessions') return [
        { id: 'current', user_id: 1, created_at: 100, expires_at: 9000, remote_address: '192.0.2.1', user_agent: 'Chrome/100 Mac OS X', current: true },
        { id: 'other', user_id: 1, created_at: 200, expires_at: 9000, remote_address: '', user_agent: 'Firefox/100 Linux' }
      ]
      if (path === '/api/v1/me/identities') return [{ id: 'oidc-1', provider_id: 'authentik', issuer: 'https://auth.example.test/', subject: 'sub', user_id: 1, created_at: 300 }]
      if (path === '/api/v1/auth/providers') return { providers: [{ id: 'authentik', name: 'Authentik' }] }
      return []
    })

    const wrapper = await mountSuspended(ProfilePage, { route: false })
    await flushPromises()

    expect(wrapper.text()).toContain('ACCOUNT')
    expect(wrapper.text()).toContain('Manage your local account password and active management sessions.')
    expect(wrapper.get('[data-testid="profile-avatar"]').text()).toBe('JD')
    expect(wrapper.get('[data-testid="profile-account"]').text()).toContain('Disabled')
    expect(wrapper.get('[data-testid="profile-account"]').text()).toContain('Never')
    expect(wrapper.get('[data-testid="profile-password"]').text()).toContain('Changing the password signs out every other session.')
    expect(wrapper.get('[data-testid="profile-authentication-sources"]').text()).toContain('Authentik')
    expect(wrapper.get('[data-testid="profile-authentication-sources"]').text()).toContain('OIDC')
    expect(wrapper.get('[data-testid="profile-sessions"]').text()).toContain('Chrome on macOS')
    expect(wrapper.get('[data-testid="profile-sessions"]').text()).toContain('Firefox on Linux')
    expect(wrapper.get('[data-testid="profile-sessions"]').text()).toContain('Current')
    expect(wrapper.get('[data-testid="profile-sessions"]').text()).toContain('Unknown address')
    expect(wrapper.text()).not.toContain('API keys')

    expect(mocks.request).toHaveBeenCalledWith('/api/v1/me')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/me/sessions')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/me/identities')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/auth/providers')
    wrapper.unmount()
  })

  it('renders neutral empty states and the avatar fallback without inventing account fields', async () => {
    resetManager('')
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/me') return { id: 1, username: '', enabled: true, created_at: 10, last_login_at: 20 }
      if (path === '/api/v1/me/sessions') return null
      if (path === '/api/v1/me/identities') return null
      if (path === '/api/v1/auth/providers') return null
      return []
    })

    const wrapper = await mountSuspended(ProfilePage, { route: false })
    await flushPromises()

    expect(wrapper.get('[data-testid="profile-avatar"]').text()).toBe('?')
    expect(wrapper.get('[data-testid="profile-account"]').text()).toContain('Enabled')
    expect(wrapper.text()).toContain('No linked authentication sources')
    expect(wrapper.text()).toContain('No active sessions')
    expect(wrapper.text()).not.toContain('Email')
    expect(wrapper.text()).not.toContain('Display name')
    expect(wrapper.text()).not.toContain('Timezone')
    expect(wrapper.text()).not.toContain('Role')
    wrapper.unmount()
  })
})
