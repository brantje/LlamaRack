import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import AdminUsersPage from '~/pages/admin/users.vue'
import ProfileAccountPage from '~/pages/profile/account.vue'
import ProfileSessionsPage from '~/pages/profile/sessions.vue'
import AdminLlamaCppPage from '~/pages/admin/llamacpp.vue'
import AdminGeneralPage from '~/pages/admin/general.vue'
import AdminSidebar from '~/components/navigation/AdminSidebar.vue'
import LlamaCppOptionsEditor from '~/components/LlamaCppOptionsEditor.vue'
import { useManager, type Profile } from '~/composables/useManager'
import { clearManagementToken, storeManagementToken } from '~/composables/useManagerApi'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

function resetManager(user: { id: number; username: string; enabled: boolean } | null = { id: 1, username: 'admin', enabled: true }) {
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = user
  manager.models.value = []
  manager.instances.value = []
  manager.runtimes.value = {}
  manager.runtimeTelemetry.value = {}
  manager.profile.value = null
  return manager
}
function components(wrapper: any, names: string[]) {
  const out: any[] = []
  const seen = new Set<Element>()
  for (const name of names) {
    for (const component of wrapper.findAllComponents({ name })) {
      if (component.element && !seen.has(component.element)) { seen.add(component.element); out.push(component) }
    }
  }
  return out
}
function inputs(wrapper: any) { return components(wrapper, ['Input', 'UInput']) }
function button(wrapper: any, text: string) {
  const found = wrapper.findAll('button').find((candidate: any) => candidate.text().trim() === text)
  if (!found) throw new Error(`Missing button: ${text}`)
  return found
}
function tableRow(wrapper: any, text: string) {
  const found = wrapper.findAll('tr').find((candidate: any) => candidate.text().includes(text))
  if (!found) throw new Error(`Missing row: ${text}`)
  return found
}
function rowButton(row: any, text: string) {
  const found = row.findAll('button').find((candidate: any) => candidate.text().trim() === text)
  if (!found) throw new Error(`Missing row button: ${text}`)
  return found
}
async function confirmation(kind: 'confirm' | 'cancel') {
  await flushPromises()
  const target = [...document.body.querySelectorAll<HTMLButtonElement>(`[data-testid="confirmation-${kind}"]`)].at(-1)
  if (!target) throw new Error(`Missing confirmation ${kind}`)
  target.click()
  await flushPromises()
}
function modalButton(text: string) {
  const found = [...document.body.querySelectorAll<HTMLButtonElement>('button')].find(candidate => candidate.textContent?.trim() === text && !candidate.disabled)
  if (!found) throw new Error(`Missing modal button: ${text}`)
  return found
}
function setLastPassword(value: string) {
  const input = [...document.body.querySelectorAll<HTMLInputElement>('input[type="password"]')].at(-1)
  if (!input) throw new Error('Missing modal password input')
  input.value = value
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

const profile: Profile = {
  path: '/app/llama-server', version: 'b9999', fingerprint: 'fingerprint',
  options: [{ key: 'ctx-size', value_hint: 'N', kind: 'integer', description: 'Context size' }]
}
function settingsResponse(overrides: Record<string, any> = {}) {
  const setting = (value: any, source = 'default', editable = true) => ({ value, source, editable })
  return {
    session_lifetime_seconds: setting(3600), login_protection_enabled: setting(true), login_failure_threshold: setting(5), login_lockout_seconds: setting(60),
    trusted_proxies: setting(''), allowed_origins: setting(''), external_url: setting(''), startup_timeout_seconds: setting(120), idle_unload_seconds: setting(0), always_on_reconcile_seconds: setting(0),
    runtime: { data_dir: '/data', models_dir: '/models', database_path: '/data/db', listen_addr: ':8000', llama_server_path: '/llama-server' }, ...overrides
  }
}

beforeEach(() => {
  mocks.request.mockReset()
  clearManagementToken()
  resetManager()
})

describe('Phase 10 deep user branches', () => {
  it('covers null/failed user loads and the signed-out early return', async () => {
    mocks.request.mockResolvedValueOnce(null)
    const empty = await mountSuspended(AdminUsersPage, { route: false })
    await flushPromises()
    expect(empty.text()).toContain('No users')
    empty.unmount()

    for (const [failure, message] of [
      [{ data: { error: 'users denied' } }, 'users denied'],
      [new Error('users exploded'), 'users exploded'],
      [{}, 'Unable to load users']
    ] as const) {
      resetManager(); mocks.request.mockReset(); mocks.request.mockRejectedValue(failure)
      const wrapper = await mountSuspended(AdminUsersPage, { route: false })
      await flushPromises()
      expect(wrapper.text()).toContain(message)
      wrapper.unmount()
    }

    resetManager(null); mocks.request.mockReset()
    const signedOut = await mountSuspended(AdminUsersPage, { route: false })
    await flushPromises()
    expect(mocks.request).not.toHaveBeenCalled()
    signedOut.unmount()
  })

  it('covers self-disable, enable and both password-reset branches', async () => {
    let users = [
      { id: 1, username: 'admin', enabled: true, created_at: 1, last_login_at: 2 },
      { id: 2, username: 'operator', enabled: false, created_at: 1 }
    ]
    let initializeCalls = 0
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/users' && !options) return users
      if (path === '/api/v1/users/1' && options?.method === 'PATCH') { users = users.map(u => u.id === 1 ? { ...u, enabled: false } : u); return {} }
      if (path === '/api/v1/users/2' && options?.method === 'PATCH') { users = users.map(u => u.id === 2 ? { ...u, enabled: true } : u); return {} }
      if (path === '/api/v1/users/1/password' && options?.method === 'POST') return {}
      if (path === '/api/v1/users/2/password' && options?.method === 'POST') return {}
      if (path === '/api/v1/auth/bootstrap') { initializeCalls++; return { required: true } }
      return []
    })

    const manager = useManager()
    const wrapper = await mountSuspended(AdminUsersPage, { route: false })
    await flushPromises()
    await rowButton(tableRow(wrapper, 'admin'), 'Disable').trigger('click')
    await confirmation('confirm')
    expect(initializeCalls).toBe(1)

    manager.user.value = { id: 1, username: 'admin', enabled: true }
    await rowButton(tableRow(wrapper, 'operator'), 'Enable').trigger('click')
    await confirmation('confirm')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/users/2', { method: 'PATCH', body: { enabled: true } })

    await rowButton(tableRow(wrapper, 'operator'), 'Reset password').trigger('click')
    await flushPromises(); setLastPassword('replacement-password'); await flushPromises(); modalButton('Reset password').click(); await flushPromises()
    expect(wrapper.text()).toContain('Password reset for operator')

    manager.user.value = { id: 1, username: 'admin', enabled: true }
    await rowButton(tableRow(wrapper, 'admin'), 'Reset password').trigger('click')
    await flushPromises(); setLastPassword('admin-replacement'); await flushPromises(); modalButton('Reset password').click(); await flushPromises()
    expect(initializeCalls).toBe(2)
    wrapper.unmount()
  })

  it('covers user toggle and reset error fallbacks plus mismatched create passwords', async () => {
    const users = [{ id: 2, username: 'operator', enabled: true, created_at: 1 }]
    let failReset: any = null
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/users' && !options) return users
      if (path === '/api/v1/users/2' && options?.method === 'PATCH') throw { data: { error: 'toggle denied' } }
      if (path === '/api/v1/users/2/password' && options?.method === 'POST') throw failReset
      return {}
    })
    const wrapper = await mountSuspended(AdminUsersPage, { route: false })
    await flushPromises()

    await rowButton(tableRow(wrapper, 'operator'), 'Disable').trigger('click')
    await confirmation('confirm')
    expect(wrapper.text()).toContain('toggle denied')

    for (const [failure, message] of [[new Error('reset exploded'), 'reset exploded'], [{}, 'Unable to reset password']] as const) {
      failReset = failure
      await rowButton(tableRow(wrapper, 'operator'), 'Reset password').trigger('click')
      await flushPromises(); setLastPassword('replacement-password'); await flushPromises(); modalButton('Reset password').click(); await flushPromises()
      expect(wrapper.text()).toContain(message)
    }
    wrapper.unmount()
  })
})

describe('Phase 10 deep profile branches', () => {
  it('covers nullable sessions, current logout, revoke-others/all cancel and fallback errors', async () => {
    let sessions: any = null
    let failPath = ''
    let fail: any = null
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === failPath) throw fail
      if (path === '/api/v1/me') return { id: 1, username: 'admin', enabled: true, created_at: 0, last_login_at: 0 }
      if (path === '/api/v1/me/sessions') return sessions
      if (path === '/api/v1/auth/logout' && options?.method === 'POST') return {}
      if (path === '/api/v1/me/sessions/revoke-others') return {}
      if (path === '/api/v1/me/sessions/revoke-all') return {}
      if (path === '/api/v1/auth/bootstrap') return { required: true }
      return []
    })

    const wrapper = await mountSuspended(ProfileSessionsPage, { route: '/profile/sessions' })
    await flushPromises()
    expect(wrapper.text()).toContain('No active sessions')
    wrapper.unmount()

    sessions = [{ id: 'current', user_id: 1, created_at: 1, expires_at: 2, remote_address: '', user_agent: '', current: true }]
    resetManager(); storeManagementToken('profile-token', false)
    const current = await mountSuspended(ProfileSessionsPage, { route: '/profile/sessions' })
    await flushPromises(); await button(current, 'Sign out').trigger('click'); await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/auth/logout', { method: 'POST' })
    current.unmount()

    resetManager(); sessions = [{ id: 'other', user_id: 1, created_at: 1, expires_at: 2, remote_address: 'x', user_agent: 'Safari/1 Mac OS X' }]
    const actions = await mountSuspended(ProfileSessionsPage, { route: '/profile/sessions' })
    await flushPromises()
    await button(actions, 'Revoke others').trigger('click'); await confirmation('cancel')
    await button(actions, 'Log out everywhere').trigger('click'); await confirmation('cancel')

    failPath = '/api/v1/me/sessions/revoke-others'; fail = new Error('others exploded')
    await button(actions, 'Revoke others').trigger('click'); await confirmation('confirm')
    expect(actions.text()).toContain('others exploded')
    fail = {}
    await button(actions, 'Revoke others').trigger('click'); await confirmation('confirm')
    expect(actions.text()).toContain('Unable to revoke other sessions')

    failPath = '/api/v1/me/sessions/revoke-all'; fail = { data: { error: 'all denied' } }
    await button(actions, 'Log out everywhere').trigger('click'); await confirmation('confirm')
    expect(actions.text()).toContain('all denied')
    fail = {}
    await button(actions, 'Log out everywhere').trigger('click'); await confirmation('confirm')
    expect(actions.text()).toContain('Unable to log out all sessions')
    actions.unmount()
  })

  it('covers profile and password fallback errors', async () => {
    for (const [failure, message] of [[{ data: { error: 'profile denied' } }, 'profile denied'], [{}, 'Unable to load profile']] as const) {
      resetManager(); mocks.request.mockReset(); mocks.request.mockRejectedValue(failure)
      const wrapper = await mountSuspended(ProfileAccountPage, { route: '/profile/account' })
      await flushPromises(); expect(wrapper.text()).toContain(message); wrapper.unmount()
    }
    for (const [failure, message] of [[new Error('password exploded'), 'password exploded'], [{}, 'Unable to change password']] as const) {
      resetManager(); mocks.request.mockReset()
      mocks.request.mockImplementation(async (path: string, options?: any) => {
        if (path === '/api/v1/me') return { id: 1, username: 'admin', enabled: true, created_at: 1 }
        if (path === '/api/v1/me/password' && options?.method === 'POST') throw failure
        return []
      })
      const wrapper = await mountSuspended(ProfileAccountPage, { route: '/profile/account' })
      await flushPromises()
      const passwordInputs = inputs(wrapper).filter(component => component.props('type') === 'password')
      for (const [index, value] of ['current-password', 'replacement-password', 'replacement-password'].entries()) passwordInputs[index].vm.$emit('update:modelValue', value)
      await flushPromises(); await wrapper.find('form').trigger('submit'); await flushPromises()
      expect(wrapper.text()).toContain(message); wrapper.unmount()
    }
  })
})

describe('Phase 10 llama and general branch variants', () => {
  it('covers llama profile validation, unknown version and error fallbacks', async () => {
    let response: any = { profile: { ...profile, version: undefined }, effective: { global: {} } }
    let failure: any = null
    mocks.request.mockImplementation(async (path: string) => {
      if (failure) throw failure
      if (path === '/api/v1/llamacpp/config') return response
      return []
    })
    const wrapper = await mountSuspended(AdminLlamaCppPage, { route: false })
    await flushPromises()
    expect(wrapper.text()).toContain('unknown')
    expect(wrapper.text()).toContain('/app/llama-server')
    wrapper.unmount()

    response = { profile: { path: '', options: [] }, effective: { global: null } }; resetManager()
    const invalidPath = await mountSuspended(AdminLlamaCppPage, { route: false })
    await flushPromises(); expect(invalidPath.text()).toContain('llama-server could not be discovered'); invalidPath.unmount()

    response = { profile: { path: '/llama', options: null }, effective: { global: {} } }; resetManager()
    const invalidOptions = await mountSuspended(AdminLlamaCppPage, { route: false })
    await flushPromises(); expect(invalidOptions.text()).toContain('llama-server could not be discovered'); invalidOptions.unmount()

    for (const [nextFailure, message] of [[{ data: { error: 'llama denied' } }, 'llama denied'], [new Error('llama exploded'), 'llama exploded'], [{}, 'Unable to load llama.cpp configuration']] as const) {
      resetManager(); failure = nextFailure
      const failed = await mountSuspended(AdminLlamaCppPage, { route: false })
      await flushPromises(); expect(failed.text()).toContain(message); failed.unmount(); failure = null
    }
  })

  it('covers llama save error variants and General signed-out/invalid-save branches', async () => {
    let saveFailure: any = { data: { error: 'save denied' } }
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/llamacpp/config' && options?.method === 'PUT') throw saveFailure
      if (path === '/api/v1/llamacpp/config') return { profile, effective: { global: {} } }
      return []
    })
    const llama = await mountSuspended(AdminLlamaCppPage, { route: false })
    await flushPromises()
    llama.findComponent(LlamaCppOptionsEditor).vm.$emit('update:modelValue', { threads: '8' })
    await flushPromises()
    for (const [failure, message] of [[{ data: { error: 'save denied' } }, 'save denied'], [new Error('save exploded'), 'save exploded'], [{}, 'Unable to save llama.cpp defaults']] as const) {
      saveFailure = failure; await button(llama, 'Save defaults').trigger('click'); await flushPromises(); expect(llama.text()).toContain(message)
    }
    llama.unmount()

    resetManager(null); mocks.request.mockReset()
    const signedOut = await mountSuspended(AdminGeneralPage, { route: false })
    await flushPromises(); expect(mocks.request).not.toHaveBeenCalled(); signedOut.unmount()

    resetManager(); let settings: any = settingsResponse()
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/settings/general' && options?.method === 'PUT') return []
      if (path === '/api/v1/settings/general') return settings
      if (path === '/api/v1/system') return { network: { effective_scheme: 'https', secure_cookie: true } }
      return []
    })
    const general = await mountSuspended(AdminGeneralPage, { route: false })
    await flushPromises()
    const generalStartup = components(general, ['InputNumber', 'UInputNumber']).find(component => component.props('modelValue') === 120)
    expect(generalStartup).toBeTruthy()
    generalStartup!.vm.$emit('update:modelValue', 121)
    await flushPromises(); await button(general, 'Save changes').trigger('click'); await flushPromises()
    expect(general.text()).toContain('Invalid manager settings response')
    general.unmount()
  })

  it('covers the Administration secondary navigation active state without a current user', async () => {
    resetManager(null)
    const sidebar = await mountSuspended(AdminSidebar, { route: '/admin/llamacpp' })
    await flushPromises()
    expect(sidebar.text()).toContain('Administration')
    expect(sidebar.get('[data-testid="admin-nav-llama.cpp"]').classes()).toContain('bg-[var(--accent-100)]')
    expect(sidebar.text()).not.toContain('Sign out')
    sidebar.unmount()
  })
})
