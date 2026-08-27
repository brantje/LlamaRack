import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import AdminIndexPage from '~/pages/admin/index.vue'
import AdminGeneralPage from '~/pages/admin/general.vue'
import AdminUsersPage from '~/pages/admin/users.vue'
import ProfilePage from '~/pages/profile.vue'
import AdminSidebar from '~/components/navigation/AdminSidebar.vue'
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
  if (!found) throw new Error(`Missing table row: ${text}`)
  return found
}
function rowButton(row: any, text: string) {
  const found = row.findAll('button').find((candidate: any) => candidate.text().trim() === text)
  if (!found) throw new Error(`Missing row button: ${text}`)
  return found
}

async function clickConfirmation(kind: 'confirm' | 'cancel') {
  await flushPromises()
  const buttons = [...document.body.querySelectorAll<HTMLButtonElement>(`[data-testid="confirmation-${kind}"]`)]
  const target = buttons.at(-1)
  if (!target) throw new Error(`Missing confirmation ${kind}`)
  target.click()
  await flushPromises()
}

function generalSettings(overrides: Record<string, any> = {}) {
  const setting = (value: any, source = 'default', editable = true) => ({ value, source, editable })
  return {
    session_lifetime_seconds: setting(86400),
    login_protection_enabled: setting(true),
    login_failure_threshold: setting(5),
    login_lockout_seconds: setting(900),
    trusted_proxies: setting('', 'default'),
    allowed_origins: setting('https://manager.test', 'environment', false),
    external_url: setting('https://manager.test'),
    startup_timeout_seconds: setting(180),
    idle_unload_seconds: setting(300),
    always_on_reconcile_seconds: setting(15),
    runtime: {
      data_dir: '/config', models_dir: '/models', database_path: '/config/manager.db',
      listen_addr: ':8000', llama_server_path: '/app/llama-server'
    },
    ...overrides
  }
}

beforeEach(() => {
  mocks.request.mockReset()
  resetManager()
})

describe('Phase 10 administration dashboard and navigation', () => {
  it('renders summary variants, refresh errors and complete admin navigation', async () => {
    let failSummary = false
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/admin/summary') {
        if (failSummary) throw { data: { error: 'summary denied' } }
        return {
          users: { total: 3, enabled: 2 },
          huggingface: { configured: true, prefix: 'hf_ab' },
          llamacpp: { available: false }
        }
      }
      return []
    })

    const dashboard = await mountSuspended(AdminIndexPage, { route: '/admin' })
    await flushPromises()
    expect(dashboard.text()).toContain('2 enabled / 3 total')
    expect(dashboard.text()).toContain('Configured')
    expect(dashboard.text()).toContain('Unavailable')

    failSummary = true
    await button(dashboard, 'Refresh').trigger('click')
    await flushPromises()
    expect(dashboard.text()).toContain('summary denied')

    const sidebar = await mountSuspended(AdminSidebar, { route: '/admin' })
    expect(sidebar.text()).toContain('administration')
    expect(sidebar.text()).toContain('Sign out')
    const menu = components(sidebar, ['NavigationMenu', 'UNavigationMenu'])[0]
    const labels = (menu?.props('items') || []).map((item: any) => item.label).filter(Boolean)
    expect(labels).toEqual(expect.arrayContaining(['Dashboard', 'Users', 'Hugging Face', 'General', 'llama.cpp', 'System', 'Back to manager']))

    sidebar.unmount()
    dashboard.unmount()
  })
})

describe('Phase 10 general settings', () => {
  it('loads effective settings, preserves environment ownership and saves editable values', async () => {
    let stored = generalSettings()
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/settings/general' && options?.method === 'PUT') {
        expect(options.body.allowed_origins).toBeUndefined()
        expect(options.body.idle_unload_seconds).toBe(600)
        stored = generalSettings({ idle_unload_seconds: { value: 600, source: 'database', editable: true } })
        return stored
      }
      if (path === '/api/v1/settings/general') return stored
      if (path === '/api/v1/system') return { network: { effective_scheme: 'https', secure_cookie: true } }
      return []
    })

    const wrapper = await mountSuspended(AdminGeneralPage, { route: '/admin/general' })
    await flushPromises()
    expect(wrapper.text()).toContain('Global idle unload (seconds)')
    expect(wrapper.text()).toContain('300 seconds (5 minutes)')
    expect(wrapper.text()).toContain('https')
    expect(wrapper.text()).toContain('Enabled')
    expect(wrapper.text()).toContain('http://manager.test:8888/api/v1')

    const numbers = components(wrapper, ['InputNumber', 'UInputNumber'])
    const idle = numbers.find(component => component.props('modelValue') === 300)
    expect(idle).toBeTruthy()
    idle!.vm.$emit('update:modelValue', 600)
    await flushPromises()
    await button(wrapper, 'Save changes').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Manager settings saved')
    expect(wrapper.text()).toContain('database')

    const textInputs = inputs(wrapper)
    const allowedOrigin = textInputs.find(component => component.props('modelValue') === 'https://manager.test')
    expect(allowedOrigin?.props('disabled')).toBe(true)
    wrapper.unmount()
  })

  it('surfaces load and save failures', async () => {
    let saveFailure = false
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/settings/general' && options?.method === 'PUT') {
        if (saveFailure) throw new Error('save settings exploded')
        return generalSettings()
      }
      if (path === '/api/v1/settings/general') return generalSettings()
      if (path === '/api/v1/system') return { network: { effective_scheme: 'http', secure_cookie: false } }
      return []
    })
    const wrapper = await mountSuspended(AdminGeneralPage, { route: false })
    await flushPromises()
    expect(wrapper.text()).toContain('Disabled')
    saveFailure = true
    await button(wrapper, 'Save changes').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('save settings exploded')
    wrapper.unmount()

    mocks.request.mockRejectedValue({ data: { error: 'settings denied' } })
    const failed = await mountSuspended(AdminGeneralPage, { route: false })
    await flushPromises()
    expect(failed.text()).toContain('settings denied')
    failed.unmount()
  })
})

describe('Phase 10 profile', () => {
  it('changes passwords and revokes sessions through the profile workflow', async () => {
    const sessions = [
      { id: 'current', user_id: 1, created_at: 100, expires_at: 9000, remote_address: '192.0.2.1', user_agent: 'Chrome/100 Windows', current: true },
      { id: 'other/session', user_id: 1, created_at: 200, expires_at: 9000, remote_address: '', user_agent: 'Firefox/100 Linux' }
    ]
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/me') return { id: 1, username: 'admin', enabled: true, created_at: 10 }
      if (path === '/api/v1/me/sessions') return sessions
      if (path === '/api/v1/me/password' && options?.method === 'POST') return {}
      if (path === '/api/v1/sessions/other%2Fsession' && options?.method === 'DELETE') return {}
      if (path === '/api/v1/me/sessions/revoke-others' && options?.method === 'POST') return { revoked: 1 }
      if (path === '/api/v1/me/sessions/revoke-all' && options?.method === 'POST') return {}
      if (path === '/api/v1/models' || path === '/api/v1/instances') return []
      return []
    })

    const wrapper = await mountSuspended(ProfilePage, { route: '/profile' })
    await flushPromises()
    expect(wrapper.text()).toContain('admin')
    expect(wrapper.text()).toContain('Never')
    expect(wrapper.text()).toContain('Chrome on Windows')
    expect(wrapper.text()).toContain('Firefox on Linux')
    expect(wrapper.text()).toContain('Unknown address')

    const passwordInputs = inputs(wrapper).filter(component => component.props('type') === 'password')
    passwordInputs[0].vm.$emit('update:modelValue', 'current-password')
    passwordInputs[1].vm.$emit('update:modelValue', 'new-password-123')
    passwordInputs[2].vm.$emit('update:modelValue', 'different-password')
    await flushPromises()
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(wrapper.text()).toContain('confirmation does not match')

    passwordInputs[2].vm.$emit('update:modelValue', 'new-password-123')
    await flushPromises()
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(wrapper.text()).toContain('Password changed')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/me/password', expect.objectContaining({ method: 'POST' }))

    await button(wrapper, 'Revoke').trigger('click')
    await clickConfirmation('confirm')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/sessions/other%2Fsession', { method: 'DELETE' })

    await button(wrapper, 'Revoke others').trigger('click')
    await clickConfirmation('confirm')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/me/sessions/revoke-others', { method: 'POST' })

    await button(wrapper, 'Log out everywhere').trigger('click')
    await clickConfirmation('confirm')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/me/sessions/revoke-all', { method: 'POST' })
    wrapper.unmount()
  })

  it('surfaces profile load and password errors', async () => {
    mocks.request.mockRejectedValueOnce(new Error('profile exploded')).mockRejectedValueOnce(new Error('sessions exploded'))
    const failed = await mountSuspended(ProfilePage, { route: false })
    await flushPromises()
    expect(failed.text()).toContain('profile exploded')
    failed.unmount()

    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/me') return { id: 1, username: 'admin', enabled: true, created_at: 10, last_login_at: 20 }
      if (path === '/api/v1/me/sessions') return []
      if (path === '/api/v1/me/password' && options?.method === 'POST') throw { data: { error: 'password denied' } }
      return []
    })
    const wrapper = await mountSuspended(ProfilePage, { route: false })
    await flushPromises()
    const passwordInputs = inputs(wrapper).filter(component => component.props('type') === 'password')
    for (const [index, value] of ['current-password', 'new-password-123', 'new-password-123'].entries()) passwordInputs[index].vm.$emit('update:modelValue', value)
    await flushPromises()
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(wrapper.text()).toContain('password denied')
    expect(wrapper.text()).toContain('No active sessions')
    wrapper.unmount()
  })
})

describe('Phase 10 users', () => {
  it('creates users, toggles them, resets passwords and manages sessions', async () => {
    let users = [
      { id: 1, username: 'admin', enabled: true, created_at: 10, last_login_at: 20, active_sessions: 1 },
      { id: 2, username: 'operator', enabled: true, created_at: 11, active_sessions: 2 }
    ]
    const sessionRows = [
      { id: 's1', user_id: 2, created_at: 100, expires_at: 1000, remote_address: '198.51.100.2', user_agent: 'Edg/100 Windows' },
      { id: 's2', user_id: 2, created_at: 101, expires_at: 1001, remote_address: '', user_agent: 'Safari/17 Mac OS X', current: true }
    ]
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/users' && options?.method === 'POST') {
        users = [...users, { id: 3, username: options.body.username, enabled: true, created_at: 12, active_sessions: 0 }]
        return users.at(-1)
      }
      if (path === '/api/v1/users') return users
      if (path === '/api/v1/users/2' && options?.method === 'PATCH') {
        users = users.map(user => user.id === 2 ? { ...user, enabled: options.body.enabled } : user)
        return {}
      }
      if (path === '/api/v1/users/2' && options?.method === 'DELETE') { users = users.filter(user => user.id !== 2); return {} }
      if (path === '/api/v1/users/2/password' && options?.method === 'POST') return {}
      if (path === '/api/v1/users/2/sessions') return sessionRows
      if (path === '/api/v1/sessions/s1' && options?.method === 'DELETE') return {}
      if (path === '/api/v1/sessions/s2' && options?.method === 'DELETE') return {}
      return []
    })

    const wrapper = await mountSuspended(AdminUsersPage, { route: '/admin/users' })
    await flushPromises()
    expect(wrapper.text()).toContain('operator')
    expect(wrapper.text()).toContain('Never')

    const userInputs = inputs(wrapper)
    const username = userInputs.find(component => component.props('autocomplete') === 'off')!
    const passwordInputs = userInputs.filter(component => component.props('autocomplete') === 'new-password')
    username.vm.$emit('update:modelValue', 'new-user')
    passwordInputs[0].vm.$emit('update:modelValue', 'new-password-123')
    passwordInputs[1].vm.$emit('update:modelValue', 'new-password-123')
    await flushPromises()
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(wrapper.text()).toContain('Management user created')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/users', expect.objectContaining({ method: 'POST' }))

    let operator = tableRow(wrapper, 'operator')
    await rowButton(operator, 'Disable').trigger('click')
    await clickConfirmation('confirm')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/users/2', { method: 'PATCH', body: { enabled: false } })

    operator = tableRow(wrapper, 'operator')
    await rowButton(operator, 'Sessions').trigger('click')
    await flushPromises()
    expect(document.body.textContent).toContain('Edge on Windows')
    expect(document.body.textContent).toContain('Safari on macOS')
    const revokeButtons = [...document.body.querySelectorAll<HTMLButtonElement>('button')].filter(candidate => candidate.textContent?.trim() === 'Revoke')
    revokeButtons[0]?.click()
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/sessions/s1', { method: 'DELETE' })

    operator = tableRow(wrapper, 'operator')
    await rowButton(operator, 'Reset password').trigger('click')
    await flushPromises()
    const modalPassword = [...document.body.querySelectorAll<HTMLInputElement>('input[type="password"]')].at(-1)
    if (modalPassword) {
      modalPassword.value = 'replacement-123'
      modalPassword.dispatchEvent(new Event('input', { bubbles: true }))
      await flushPromises()
    }
    const modalReset = [...document.body.querySelectorAll<HTMLButtonElement>('button')].find(candidate => candidate.textContent?.trim() === 'Reset password' && !candidate.disabled)
    modalReset?.click()
    await flushPromises()

    operator = tableRow(wrapper, 'operator')
    await rowButton(operator, 'Delete').trigger('click')
    await clickConfirmation('confirm')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/users/2', { method: 'DELETE' })
    wrapper.unmount()
  })

  it('surfaces create, session and mutation failures', async () => {
    const row = { id: 2, username: 'operator', enabled: false, created_at: 11, active_sessions: 0 }
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/users' && !options) return [row]
      if (path === '/api/v1/users' && options?.method === 'POST') throw new Error('create exploded')
      if (path === '/api/v1/users/2/sessions') throw { data: { error: 'sessions denied' } }
      if (path === '/api/v1/users/2' && options?.method === 'PATCH') throw {}
      return []
    })
    const wrapper = await mountSuspended(AdminUsersPage, { route: false })
    await flushPromises()

    const userInputs = inputs(wrapper)
    userInputs.find(component => component.props('autocomplete') === 'off')!.vm.$emit('update:modelValue', 'new-user')
    const passwordInputs = userInputs.filter(component => component.props('autocomplete') === 'new-password')
    passwordInputs[0].vm.$emit('update:modelValue', 'new-password-123')
    passwordInputs[1].vm.$emit('update:modelValue', 'new-password-123')
    await flushPromises()
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(wrapper.text()).toContain('create exploded')

    let operator = tableRow(wrapper, 'operator')
    await rowButton(operator, 'Sessions').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('sessions denied')

    operator = tableRow(wrapper, 'operator')
    await rowButton(operator, 'Enable').trigger('click')
    await clickConfirmation('confirm')
    expect(wrapper.text()).toContain('Unable to enable user')
    wrapper.unmount()
  })
})
