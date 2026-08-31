import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import ProfileAccountPage from '~/pages/profile/account.vue'
import ProfileAuthenticationPage from '~/pages/profile/authentication.vue'
import ProfileSessionsPage from '~/pages/profile/sessions.vue'
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
  it('renders the account and password sections on the account page', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/me') return { id: 1, username: 'john.doe', enabled: false, created_at: 10 }
      return []
    })

    const wrapper = await mountSuspended(ProfileAccountPage, { route: '/profile/account' })
    await flushPromises()

    expect(wrapper.text()).toContain('ACCOUNT')
    expect(wrapper.text()).toContain('View your local account details and change your password.')
    expect(wrapper.get('[data-testid="profile-avatar"]').text()).toBe('JD')
    expect(wrapper.get('[data-testid="profile-avatar"]').element.tagName).toBe('SPAN')
    expect(wrapper.get('[data-testid="profile-account"]').text()).toContain('Disabled')
    expect(wrapper.get('[data-testid="profile-account"]').text()).toContain('Never')
    const passwordSection = wrapper.get('[data-testid="profile-password"]')
    expect(passwordSection.text()).toContain('Changing the password signs out every other session.')
    expect(passwordSection.text()).toContain('Use at least 10 characters for the new password.')
    expect(passwordSection.text()).toContain('At least 10 characters.')
    for (const [inputId, toggleId] of [['profile-current-password', 'toggle-current-password'], ['profile-new-password', 'toggle-new-password'], ['profile-confirm-password', 'toggle-confirm-password']]) {
      const input = wrapper.get(`[data-testid="${inputId}"]`)
      const toggle = wrapper.get(`[data-testid="${toggleId}"]`)
      expect(input.attributes('type')).toBe('password')
      await toggle.trigger('click')
      expect(input.attributes('type')).toBe('text')
      expect(toggle.attributes('aria-pressed')).toBe('true')
      await toggle.trigger('click')
      expect(input.attributes('type')).toBe('password')
    }
    const changePassword = wrapper.findAll('button').find(button => button.text().trim() === 'Change password')!
    expect(changePassword.attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="profile-current-password"]').setValue('current-password')
    await wrapper.get('[data-testid="profile-new-password"]').setValue('new-password')
    await wrapper.get('[data-testid="profile-confirm-password"]').setValue('different-password')
    await flushPromises()
    expect(passwordSection.text()).toContain('New password confirmation does not match.')
    expect(changePassword.attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="profile-confirm-password"]').setValue('new-password')
    await flushPromises()
    expect(passwordSection.text()).not.toContain('New password confirmation does not match.')
    expect(changePassword.attributes('disabled')).toBeUndefined()
    expect(wrapper.text()).not.toContain('API keys')

    expect(mocks.request).toHaveBeenCalledWith('/api/v1/me')
    wrapper.unmount()
  })

  it('renders authentication sources on the authentication page', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/me/identities') return [{ id: 'oidc-1', provider_id: 'authentik', issuer: 'https://auth.example.test/', subject: 'sub', user_id: 1, created_at: 300 }]
      if (path === '/api/v1/auth/providers') return { providers: [{ id: 'authentik', name: 'Authentik' }] }
      return []
    })

    const wrapper = await mountSuspended(ProfileAuthenticationPage, { route: '/profile/authentication' })
    await flushPromises()

    expect(wrapper.get('[data-testid="profile-authentication-sources"]').text()).toContain('Authentik')
    expect(wrapper.get('[data-testid="profile-authentication-sources"]').text()).toContain('OIDC')
    expect(wrapper.get('[data-testid="profile-authentication-sources"]').text()).toContain('Administration → Authentication')
    wrapper.unmount()
  })

  it('renders session details on the sessions page', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/me/sessions') return [
        { id: 'current', user_id: 1, created_at: 100, expires_at: 9000, remote_address: '192.0.2.1', user_agent: 'Chrome/100 Mac OS X', current: true },
        { id: 'other', user_id: 1, created_at: 200, expires_at: 9000, remote_address: '', user_agent: 'Firefox/100 Linux' }
      ]
      return []
    })

    const wrapper = await mountSuspended(ProfileSessionsPage, { route: '/profile/sessions' })
    await flushPromises()

    expect(wrapper.get('[data-testid="profile-sessions"]').text()).toContain('Chrome on macOS')
    expect(wrapper.get('[data-testid="profile-sessions"]').text()).toContain('Firefox on Linux')
    expect(wrapper.get('[data-testid="profile-sessions"]').text()).toContain('Current')
    expect(wrapper.get('[data-testid="profile-sessions"]').text()).toContain('Unknown address')
    const sessionButtons = wrapper.get('[data-testid="profile-sessions"]').findAll('button')
    expect(sessionButtons.find(button => button.text().trim() === 'Revoke others')!.attributes('disabled')).toBeUndefined()
    expect(sessionButtons.find(button => button.text().trim() === 'Log out everywhere')!.attributes('disabled')).toBeUndefined()
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

    const account = await mountSuspended(ProfileAccountPage, { route: '/profile/account' })
    await flushPromises()
    expect(account.get('[data-testid="profile-avatar"]').text()).toBe('?')
    expect(account.get('[data-testid="profile-account"]').text()).toContain('Enabled')
    expect(account.text()).not.toContain('Email')
    expect(account.text()).not.toContain('Display name')
    expect(account.text()).not.toContain('Timezone')
    expect(account.text()).not.toContain('Role')
    account.unmount()

    const authentication = await mountSuspended(ProfileAuthenticationPage, { route: '/profile/authentication' })
    await flushPromises()
    expect(authentication.text()).toContain('No linked authentication sources')
    authentication.unmount()

    const sessions = await mountSuspended(ProfileSessionsPage, { route: '/profile/sessions' })
    await flushPromises()
    expect(sessions.text()).toContain('No active sessions')
    const sessionButtons = sessions.get('[data-testid="profile-sessions"]').findAll('button')
    expect(sessionButtons.find(button => button.text().trim() === 'Revoke others')!.attributes('disabled')).toBeDefined()
    expect(sessionButtons.find(button => button.text().trim() === 'Log out everywhere')!.attributes('disabled')).toBeDefined()
    sessions.unmount()
  })
})
