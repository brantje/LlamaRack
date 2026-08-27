import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import AdminUsersPage from '~/pages/admin/users.vue'
import ProfilePage from '~/pages/profile.vue'
import AdminLlamaCppPage from '~/pages/admin/llamacpp.vue'
import AdminGeneralPage from '~/pages/admin/general.vue'
import AdminSidebar from '~/components/navigation/AdminSidebar.vue'
import { useManager, type Profile } from '~/composables/useManager'

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
      if (component.element && !seen.has(component.element)) {
        seen.add(component.element)
        out.push(component)
      }
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
  const found = [...document.body.querySelectorAll<HTMLButtonElement>('button')].find(candidate => candidate.textContent?.trim() === text)
  if (!found) throw new Error(`Missing modal button: ${text}`)
  return found
}

const profile: Profile = {
  path: '/app/llama-server',
  version: 'b9999',
  fingerprint: 'fingerprint',
  options: [{ key: 'ctx-size', value_hint: 'N', kind: 'integer' }]
}

function settingsResponse(overrides: Record<string, any> = {}) {
  const setting = (value: any, source = 'default', editable = true) => ({ value, source, editable })
  return {
    session_lifetime_seconds: setting(3600),
    login_protection_enabled: setting(true),
    login_failure_threshold: setting(5),
    login_lockout_seconds: setting(60),
    trusted_proxies: setting(''),
    allowed_origins: setting(''),
    external_url: setting(''),
    startup_timeout_seconds: setting(120),
    idle_unload_seconds: setting(0),
    always_on_reconcile_seconds: setting(0),
    runtime: { data_dir: '/data', models_dir: '/models', database_path: '/data/db', listen_addr: ':8000', llama_server_path: '/llama-server' },
    ...overrides
  }
}

beforeEach(() => {
  mocks.request.mockReset()
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
      resetManager()
      mocks.request.mockReset()
      mocks.request.mockRejectedValue(failure)
      const wrapper = await mountSuspended(AdminUsersPage, { route: false })
      await flushPromises()
      expect(wrapper.text()).toContain(message)
      wrapper.unmount()
    }

    resetManager(null)
    mocks.request.mockReset()
    const signedOut = await mountSuspended(AdminUsersPage, { route: false })
    await flushPromises()
    expect(mocks.request).not.toHaveBeenCalled()
    signedOut.unmount()
  })

  it('covers self-disable, enable, delete, reset and session-revoke branches', async () => {
    let users = [
      { id: 1, username: 'admin', enabled: true, created_at: 1, last_login_at: 2, active_sessions: 1 },
      { id: 2, username: 'operator', enabled: false, created_at: 1, active_sessions: 0 }
    ]
    let sessions: any = null
    let initializeCalls = 0
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/users' && !options) return users
      if (path === '/api/v1/users/1' && options?.method === 'PATCH') { users = users.map(u => u.id === 1 ? { ...u, enabled: false } : u); return {} }
      if (path === '/api/v1/users/2' && options?.method === 'PATCH') { users = users.map(u => u.id === 2 ? { ...u, enabled: true } : u); return {} }
      if (path === '/api/v1/users/2' && options?.method === 'DELETE') { users = users.filter(u => u.id !== 2); return {} }
      if (path === '/api/v1/users/1/password' && options?.method === 'POST') return {}
      if (path === '/api/v1/users/2/password' && options?.method === 'POST') return {}
      if (path === '/api/v1/users/2/sessions') return sessions
      if (path === '/api/v1/sessions/current%2Ftwo' && options?.method === 'DELETE') return {}
      if (path === '/api/v1/sessions/other' && options?.method === 'DELETE') return {}
      if (path === '/api/v1/auth/bootstrap') { initializeCalls++; return { required: true } }
      return []
    })

    const wrapper = await mountSuspended(AdminUsersPage, { route: false })
    await flushPromises()

    await rowButton(tableRow(wrapper, 'admin'), 'Disable').trigger('click')
    await confirmation('confirm')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/users/1', { method: 'PATCH', body: { enabled: false } })
    expect(initializeCalls).toBe(1)

    await rowButton(tableRow(wrapper, 'operator'), 'Enable').trigger('click')
    await confirmation('confirm')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/users/2', { method: 'PATCH', body: { enabled: true } })

    let operator = tableRow(wrapper, 'operator')
    await rowButton(operator, 'Reset password').trigger('click')
    await flushPromises()
    let modalInput = [...document.body.querySelectorAll<HTMLInputElement>('input[type="password"]')].at(-1)!
    modalInput.value = 'replacement-password'
    modalInput.dispatchEvent(new Event('input', { bubbles: true }))
    await flushPromises()
    modalButton('Reset password').click()
    await flushPromises()
    expect(wrapper.text()).toContain('Password reset for operator')

    await rowButton(tableRow(wrapper, 'admin'), 'Reset password').trigger('click')
    await flushPromises()
    modalInput = [...document.body.querySelectorAll<HTMLInputElement>('input[type="password"]')].at(-1)!
    modalInput.value = 'admin-replacement'
    modalInput.dispatchEvent(new Event('input', { bubbles: true }))
    await flushPromises()
    modalButton('Reset password').click()
    await flushPromises()
    expect(initializeCalls).toBe(2)

    sessions = null
    operator = tableRow(wrapper, 'operator')
    await rowButton(operator, 'Sessions').trigger('click')
    await flushPromises()
    expect(document.body.textContent).toContain('No active sessions')

    sessions = [
      { id: 'current/two', user_id: 2, created_at: 1, expires_at: 2, remote_address: '', user_agent: 'Chrome/1 Windows', current: true },
      { id: 'other', user_id: 2, created_at: 1, expires_at: 2, remote_address: 'x', user_agent: 'Firefox/1 Linux' }
    ]
    await rowButton(tableRow(wrapper, 'operator'), 'Sessions').trigger('click')
    await flushPromises()
    const revokeButtons = [...document.body.querySelectorAll<HTMLButtonElement>('button')].filter(candidate => candidate.textContent?.trim() === 'Revoke')
    revokeButtons[0]!.click()
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/sessions/current%2Ftwo', { method: 'DELETE' })
    expect(initializeCalls).toBe(3)

    const revokeAfterRefresh = [...document.body.querySelectorAll<HTMLButtonElement>('button')].filter(candidate => candidate.textContent?.trim() === 'Revoke')
    revokeAfterRefresh[1]!.click()
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/sessions/other', { method: 'DELETE' })

    await rowButton(tableRow(wrapper, 'operator'), 'Delete').trigger('click')
    await confirmation('confirm')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/users/2', { method: 'DELETE' })
    expect(wrapper.text()).not.toContain('operator')
    wrapper.unmount()
  })

  it('covers user mutation error fallbacks for delete, reset, sessions and revoke', async () => {
    const users = [{ id: 2, username: 'operator', enabled: true, created_at: 1, active_sessions: 1 }]
    let failurePath = ''
    let failure: any = {}
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/users' && !options) return users
      if (path === '/api/v1/users/2/sessions' && path !== failurePath) return [{ id: 's', user_id: 2, created_at: 1, expires_at: 2, remote_address: '', user_agent: '' }]
      if (path === failurePath) throw failure
      return {}
    })
    const wrapper = await mountSuspended(AdminUsersPage, { route: false })
    await flushPromises()

    failurePath = '/api/v1/users/2'
    failure = { data: { error: 'delete denied' } }
    await rowButton(tableRow(wrapper, 'operator'), 'Delete').trigger('click')
    await confirmation('confirm')
    expect(wrapper.text()).toContain('delete denied')

    failure = {}
    await rowButton(tableRow(wrapper, 'operator'), 'Delete').trigger('click')
    await confirmation('confirm')
    expect(wrapper.text()).toContain('Unable to delete user')

    failurePath = '/api/v1/users/2/password'
    failure = new Error('reset exploded')
    await rowButton(tableRow(wrapper, 'operator'), 'Reset password').trigger('click')
    await flushPromises()
    const resetInput = [...document.body.querySelectorAll<HTMLInputElement>('input[type="password"]')].at(-1)!
    resetInput.value = 'replacement-password'
    resetInput.dispatchEvent(new Event('input', { bubbles: true }))
    await flushPromises()
    modalButton('Reset password').click()
    await flushPromises()
    expect(wrapper.text()).toContain('reset exploded')

    failurePath = '/api/v1/users/2/sessions'
    failure = {}
    await rowButton(tableRow(wrapper, 'operator'), 'Sessions').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Unable to load sessions')

    failurePath = ''
    await rowButton(tableRow(wrapper, 'operator'), 'Sessions').trigger('click')
    await flushPromises()
    failurePath = '/api/v1/sessions/s'
    failure = { data: { error: 'revoke denied' } }
    const revoke = [...document.body.querySelectorAll<HTMLButtonElement>('button')].find(candidate => candidate.textContent?.trim() === 'Revoke')!
    revoke.click()
    await flushPromises()
    expect(wrapper.text()).toContain('revoke denied')
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

    const wrapper = await mountSuspended(ProfilePage, { route: false })
    await flushPromises()
    expect(wrapper.text()).toContain('No active sessions')
    expect(wrapper.text()).toContain('Never')

    sessions = [{ id: 'current', user_id: 1, created_at: 1, expires_at: 2, remote_address: '', user_agent: '', current: true }]
    wrapper.unmount()
    resetManager()
    const current = await mountSuspended(ProfilePage, { route: false })
    await flushPromises()
    await button(current, 'Sign out').trigger('click')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/auth/logout', { method: 'POST' })
    current.unmount()

    resetManager()
    sessions = [{ id: 'other', user_id: 1, created_at: 1, expires_at: 2, remote_address: 'x', user_agent: 'Safari/1 Mac OS X' }]
    const actions = await mountSuspended(ProfilePage, { route: false })
    await flushPromises()

    await button(actions, 'Revoke others').trigger('click')
    await confirmation('cancel')
    await button(actions, 'Log out everywhere').trigger('click')
    await confirmation('cancel')

    failPath = '/api/v1/me/sessions/revoke-others'
    fail = new Error('others exploded')
    await button(actions, 'Revoke others').trigger('click')
    await confirmation('confirm')
    expect(actions.text()).toContain('others exploded')

    fail = {}
    await button(actions, 'Revoke others').trigger('click')
    await confirmation('confirm')
    expect(actions.text()).toContain('Unable to revoke other sessions')

    failPath = '/api/v1/me/sessions/revoke-all'
    fail = { data: { error: 'all denied' } }
    await button(actions, 'Log out everywhere').trigger('click')
    await confirmation('confirm')
    expect(actions.text()).toContain('all denied')

    fail = {}
    await button(actions, 'Log out everywhere').trigger('click')
    await confirmation('confirm')
    expect(actions.text()).toContain('Unable to log out all sessions')
    actions.unmount()
  })

  it('covers profile and password fallback errors', async () => {
    for (const [failure, message] of [
      [{ data: { error: 'profile denied' } }, 'profile denied'],
      [{}, 'Unable to load profile']
    ] as const) {
      resetManager()
      mocks.request.mockReset()
      mocks.request.mockRejectedValue(failure)
      const wrapper = await mountSuspended(ProfilePage, { route: false })
      await flushPromises()
      expect(wrapper.text()).toContain(message)
      wrapper.unmount()
    }

    for (const [failure, message] of [
      [new Error('password exploded'), 'password exploded'],
      [{}, 'Unable to change password']
    ] as const) {
      resetManager()
      mocks.request.mockReset()
      mocks.request.mockImplementation(async (path: string, options?: any) => {
        if (path === '/api/v1/me') return { id: 1, username: 'admin', enabled: true, created_at: 1 }
        if (path === '/api/v1/me/sessions') return []
        if (path === '/api/v1/me/password' && options?.method === 'POST') throw failure
        return []
      })
      const wrapper = await mountSuspended(ProfilePage, { route: false })
      await flushPromises()
      const passwordInputs = inputs(wrapper).filter(component => component.props('type') === 'password')
      for (const [index, value] of ['current-password', 'replacement-password', 'replacement-password'].entries()) {
        passwordInputs[index].vm.$emit('update:modelValue', value)
      }
      await flushPromises()
      await wrapper.find('form').trigger('submit')
      await flushPromises()
      expect(wrapper.text()).toContain(message)
      wrapper.unmount()
    }
  })
})

describe('Phase 10 llama and general branch variants', () => {
  it('covers llama profile validation, unknown version, signed-out load and error fallbacks', async () => {
    let response: any = { profile: { ...profile, version: undefined }, effective: { global: {} } }
    let failure: any = null
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (failure) throw failure
      if (path === '/api/v1/llamacpp/config' && options?.method === 'PUT') return {}
      if (path === '/api/v1/llamacpp/config') return response
      return []
    })

    const wrapper = await mountSuspended(AdminLlamaCppPage, { route: false })
    await flushPromises()
    expect(wrapper.text()).toContain('unknown')
    expect(wrapper.text()).toContain('/app/llama-server')

    response = { profile: { path: '', options: [] }, effective: { global: null } }
    resetManager()
    const invalidPath = await mountSuspended(AdminLlamaCppPage, { route: false })
    await flushPromises()
    expect(invalidPath.text()).toContain('llama-server could not be discovered')
    invalidPath.unmount()

    response = { profile: { path: '/llama', options: null }, effective: { global: {} } }
    resetManager()
    const invalidOptions = await mountSuspended(AdminLlamaCppPage, { route: false })
    await flushPromises()
    expect(invalidOptions.text()).toContain('llama-server could not be discovered')
    invalidOptions.unmount()

    for (const [nextFailure, message] of [
      [{ data: { error: 'llama denied' } }, 'llama denied'],
      [new Error('llama exploded'), 'llama exploded'],
      [{}, 'Unable to load llama.cpp configuration']
    ] as const) {
      resetManager()
      failure = nextFailure
      const failed = await mountSuspended(AdminLlamaCppPage, { route: false })
      await flushPromises()
      expect(failed.text()).toContain(message)
      failed.unmount()
      failure = null
    }

    wrapper.unmount()
    await flushPromises()
    resetManager(null)
    await flushPromises()
    mocks.request.mockClear()
    const signedOut = await mountSuspended(AdminLlamaCppPage, { route: false })
    await flushPromises()
    expect(mocks.request).not.toHaveBeenCalled()
    signedOut.unmount()
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
    for (const [failure, message] of [
      [{ data: { error: 'save denied' } }, 'save denied'],
      [new Error('save exploded'), 'save exploded'],
      [{}, 'Unable to save llama.cpp defaults']
    ] as const) {
      saveFailure = failure
      await button(llama, 'Save defaults').trigger('click')
      await flushPromises()
      expect(llama.text()).toContain(message)
    }
    llama.unmount()

    resetManager(null)
    mocks.request.mockReset()
    const signedOut = await mountSuspended(AdminGeneralPage, { route: false })
    await flushPromises()
    expect(mocks.request).not.toHaveBeenCalled()
    signedOut.unmount()

    resetManager()
    let settings: any = settingsResponse()
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/settings/general' && options?.method === 'PUT') return []
      if (path === '/api/v1/settings/general') return settings
      if (path === '/api/v1/system') return { network: { effective_scheme: 'https', secure_cookie: true } }
      return []
    })
    const general = await mountSuspended(AdminGeneralPage, { route: false })
    await flushPromises()
    await button(general, 'Save changes').trigger('click')
    await flushPromises()
    expect(general.text()).toContain('Invalid manager settings response')
    general.unmount()
  })

  it('covers admin sidebar without a current user', async () => {
    resetManager(null)
    mocks.request.mockResolvedValue([])
    const sidebar = await mountSuspended(AdminSidebar, { route: '/admin' })
    await flushPromises()
    expect(sidebar.text()).toContain('Sign out')
    sidebar.unmount()
  })
})