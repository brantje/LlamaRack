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
  external_url: { value: 'https://llamarack.example.test', source: 'environment', editable: false },
  frontend_url: { value: 'https://ui.example.test', source: 'database', editable: true }
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

beforeEach(() => {
  mocks.request.mockReset()
  resetManager()
})

describe('Admin authentication environment-owned settings', () => {
  it('omits environment-owned external_url when saving editable auth settings', async () => {
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/admin/auth/settings' && options?.method === 'PUT') return authSettings
      if (path === '/api/v1/admin/auth/settings') return authSettings
      if (path === '/api/v1/admin/auth/providers') return []
      if (path === '/api/v1/auth/providers') return { local_login_enabled: true, providers: [] }
      throw new Error(`unexpected path ${path}`)
    })

    const wrapper = await mountSuspended(AuthenticationPage, { route: false })
    await flushPromises()

    const save = wrapper.findAll('button').find(button => button.text().trim() === 'Save settings')
    expect(save).toBeDefined()
    await save!.trigger('click')
    await flushPromises()

    expect(mocks.request).toHaveBeenCalledWith('/api/v1/admin/auth/settings', {
      method: 'PUT',
      body: {
        local_login_enabled: true,
        oidc_jit_provisioning_enabled: true,
        oidc_auto_link_enabled: false,
        frontend_url: 'https://ui.example.test'
      }
    })
    expect(wrapper.text()).toContain('Authentication settings saved.')
    wrapper.unmount()
  })
})
