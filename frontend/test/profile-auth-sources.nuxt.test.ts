import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import ProfileAuthenticationPage from '~/pages/profile/authentication.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

function resetManager() {
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

async function confirm(kind: 'confirm' | 'cancel') {
  await flushPromises()
  const target = [...document.body.querySelectorAll<HTMLButtonElement>(`[data-testid="confirmation-${kind}"]`)].at(-1)
  if (!target) throw new Error(`Missing confirmation ${kind}`)
  target.click()
  await flushPromises()
}

function unlinkButton(wrapper: any) {
  const target = wrapper.findAll('button').find((button: any) => button.text().trim() === 'Unlink')
  if (!target) throw new Error('Missing Unlink button')
  return target
}

beforeEach(() => {
  mocks.request.mockReset()
  resetManager()
})

describe('profile authentication sources', () => {
  it('shows linked provider names and unlinks them', async () => {
    let identities = [{
      id: 'identity/one',
      provider_id: 'authentik',
      issuer: 'https://auth.example.test/application/o/manager/',
      subject: 'user-subject',
      user_id: 1,
      created_at: 100
    }]

    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/me/identities') return identities
      if (path === '/api/v1/auth/providers') return { local_login_enabled: true, providers: [{ id: 'authentik', name: 'Authentik' }] }
      if (path === '/api/v1/admin/auth/identities/identity%2Fone' && options?.method === 'DELETE') {
        identities = []
        return undefined
      }
      return []
    })

    const wrapper = await mountSuspended(ProfileAuthenticationPage, { route: '/profile/authentication' })
    await flushPromises()

    expect(wrapper.text()).toContain('Authentication sources')
    expect(wrapper.text()).toContain('Authentik')
    expect(wrapper.text()).toContain('https://auth.example.test/application/o/manager/')
    expect(wrapper.text()).toContain('OIDC')

    await unlinkButton(wrapper).trigger('click')
    await confirm('confirm')

    expect(mocks.request).toHaveBeenCalledWith('/api/v1/admin/auth/identities/identity%2Fone', { method: 'DELETE' })
    expect(wrapper.text()).toContain('Authentik unlinked.')
    expect(wrapper.text()).toContain('No linked authentication sources')
    wrapper.unmount()
  })

  it('uses the issuer as a fallback and handles cancel and unlink errors', async () => {
    const identity = {
      id: 'identity-two',
      provider_id: 'disabled-provider',
      issuer: 'https://disabled.example.test/',
      subject: 'subject-two',
      user_id: 1,
      created_at: 200
    }
    let failUnlink = false

    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/me/identities') return [identity]
      if (path === '/api/v1/auth/providers') return { local_login_enabled: true, providers: [] }
      if (path === '/api/v1/admin/auth/identities/identity-two' && options?.method === 'DELETE') {
        if (failUnlink) throw { data: { error: 'unlink denied' } }
        return undefined
      }
      return []
    })

    const wrapper = await mountSuspended(ProfileAuthenticationPage, { route: '/profile/authentication' })
    await flushPromises()
    expect(wrapper.text()).toContain('https://disabled.example.test/')

    await unlinkButton(wrapper).trigger('click')
    await confirm('cancel')
    expect(mocks.request).not.toHaveBeenCalledWith('/api/v1/admin/auth/identities/identity-two', { method: 'DELETE' })

    failUnlink = true
    await unlinkButton(wrapper).trigger('click')
    await confirm('confirm')
    expect(wrapper.text()).toContain('unlink denied')
    wrapper.unmount()
  })
})
