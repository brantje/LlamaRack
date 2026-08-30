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
  const target = [...document.body.querySelectorAll<HTMLButtonElement>(`[data-testid="confirmation-${kind}"]`)].at(-1)
  if (!target) throw new Error(`Missing confirmation ${kind}`)
  target.click()
  await flushPromises()
}
function modalInput(autocomplete: string, index = 0) {
  const matches = [...document.body.querySelectorAll<HTMLInputElement>(`input[autocomplete="${autocomplete}"]`)]
  const target = matches[index]
  if (!target) throw new Error(`Missing modal input ${autocomplete}[${index}]`)
  return target
}
function setNativeInput(input: HTMLInputElement, value: string) {
  input.value = value
  input.dispatchEvent(new Event('input', { bubbles: true }))
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
    runtime: { data_dir: '/config', models_dir: '/models', database_path: '/config/manager.db', listen_addr: ':8000', llama_server_path: '/app/llama-server' },
    ...overrides
  }
}

beforeEach(() => {
  mocks.request.mockReset()
  resetManager()
})

describe('Phase 10 administration dashboard and navigation', () => {
  it('renders summary variants, refresh errors and the redesign secondary navigation', async () => {
    let failSummary = false
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/admin/summary') {
        if (failSummary) throw { data: { error: 'summary denied' } }
        return { users: { total: 3, enabled: 2 }, huggingface: { configured: true, prefix: 'hf_ab' }, llamacpp: { available: false } }
      }
      return []
    })

    const dashboard = await mountSuspended(AdminIndexPage, { route: '/admin' })
    await flushPromises()
    expect(dashboard.text()).toContain('2 enabled')
    expect(dashboard.text()).toContain('3 total')
    expect(dashboard.text()).toContain('Configured')
    expect(dashboard.text()).toContain('hf_ab…')
    expect(dashboard.text()).toContain('Unavailable')

    failSummary = true
    await button(dashboard, 'Refresh').trigger('click')
    await flushPromises()
    expect(dashboard.text()).toContain('summary denied')

    const sidebar = await mountSuspended(AdminSidebar, { route: '/admin' })
    expect(sidebar.get('[data-testid="admin-secondary-nav"]').classes()).toContain('lg:w-[216px]')
    const labels = sidebar.findAll('a').map(link => link.text().split(/Security|Provider|Binary|Local|Read-only|Manager/)[0]?.trim()).filter(Boolean)
    expect(sidebar.text()).toContain('Dashboard')
    expect(sidebar.text()).toContain('General')
    expect(sidebar.text()).toContain('llama.cpp')
    expect(sidebar.text()).toContain('Hugging Face')
    expect(sidebar.text()).toContain('Users')
    expect(sidebar.text()).toContain('System')
    expect(sidebar.text()).toContain('Logs')
    expect(sidebar.text()).not.toContain('Back to manager')
    expect(sidebar.text()).not.toContain('Sign out')
    expect(labels.length).toBeGreaterThanOrEqual(7)
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
    expect(wrapper.text()).toContain('Streaming responses keep an Instance active')
    expect(wrapper.text()).toContain('https')
    expect(wrapper.text()).toContain('Enabled')
    expect(wrapper.text()).toContain('http://manager.test:8888/api/v1')
    expect(wrapper.findAll('[data-testid="setting-source"]').length).toBeGreaterThanOrEqual(10)
    expect(wrapper.text()).toContain('source: environment')
    expect(button(wrapper, 'Save changes').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('No changes to save.')

    const numbers = components(wrapper, ['InputNumber', 'UInputNumber'])
    const idle = numbers.find(component => component.props('modelValue') === 300)
    expect(idle).toBeTruthy()
    idle!.vm.$emit('update:modelValue', 600)
    await flushPromises()
    expect(button(wrapper, 'Save changes').attributes('disabled')).toBeUndefined()
    expect(wrapper.text()).toContain('Unsaved changes')
    await button(wrapper, 'Save changes').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Manager settings saved')
    expect(wrapper.text()).toContain('database')

    const allowedOrigin = inputs(wrapper).find(component => component.props('modelValue') === 'https://manager.test')
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
    expect(wrapper.text()).toContain('No changes to save.')
    const failureIdle = components(wrapper, ['InputNumber', 'UInputNumber']).find(component => component.props('modelValue') === 300)
    expect(failureIdle).toBeTruthy()
    failureIdle!.vm.$emit('update:modelValue', 301)
    await flushPromises()
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
    expect(wrapper.text()).toContain('Chrome on Windows')
    expect(wrapper.text()).toContain('Firefox on Linux')
    const passwordInputs = inputs(wrapper).filter(component => component.props('type') === 'password')
    passwordInputs[0].vm.$emit('update:modelValue', 'current-password')
    passwordInputs[1].vm.$emit('update:modelValue', 'new-password-123')
    passwordInputs[2].vm.$emit('update:modelValue', 'new-password-123')
    await flushPromises()
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(wrapper.text()).toContain('Password changed')
    await button(wrapper, 'Revoke').trigger('click')
    await clickConfirmation('confirm')
    await button(wrapper, 'Revoke others').trigger('click')
    await clickConfirmation('confirm')
    await button(wrapper, 'Log out everywhere').trigger('click')
    await clickConfirmation('confirm')
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
    wrapper.unmount()
  })
})

describe('Phase 10 users', () => {
  it('creates users, toggles them and resets passwords from the redesigned table', async () => {
    let users = [
      { id: 1, username: 'admin', enabled: true, created_at: 10, last_login_at: 20 },
      { id: 2, username: 'operator', enabled: true, created_at: 11 }
    ]
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/users' && options?.method === 'POST') {
        users = [...users, { id: 3, username: options.body.username, enabled: true, created_at: 12 }]
        return users.at(-1)
      }
      if (path === '/api/v1/users') return users
      if (path === '/api/v1/users/2' && options?.method === 'PATCH') {
        users = users.map(user => user.id === 2 ? { ...user, enabled: options.body.enabled } : user)
        return {}
      }
      if (path === '/api/v1/users/2/password' && options?.method === 'POST') return {}
      return []
    })

    const wrapper = await mountSuspended(AdminUsersPage, { route: '/admin/users' })
    await flushPromises()
    const headers = wrapper.get('[data-testid="admin-users-table"]').findAll('th').map(cell => cell.text())
    expect(headers).toEqual(['Username', 'Status', 'Created', 'Last login', 'Actions'])
    expect(wrapper.text()).toContain('operator')
    expect(wrapper.text()).toContain('Never')
    expect(wrapper.text()).toContain('Inference API keys are managed separately under API')

    await button(wrapper, 'Add user').trigger('click')
    await flushPromises()
    setNativeInput(modalInput('off'), 'new-user')
    setNativeInput(modalInput('new-password', 0), 'new-password-123')
    setNativeInput(modalInput('new-password', 1), 'new-password-123')
    await flushPromises()
    const add = [...document.body.querySelectorAll<HTMLButtonElement>('button')].find(candidate => candidate.textContent?.trim() === 'Add user' && !candidate.disabled)
    add?.click()
    await flushPromises()
    expect(wrapper.text()).toContain('Management user created')

    let operator = tableRow(wrapper, 'operator')
    expect(operator.text()).not.toContain('Sessions')
    expect(operator.text()).not.toContain('Delete')
    await rowButton(operator, 'Disable').trigger('click')
    await clickConfirmation('confirm')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/users/2', { method: 'PATCH', body: { enabled: false } })

    operator = tableRow(wrapper, 'operator')
    await rowButton(operator, 'Reset password').trigger('click')
    await flushPromises()
    const reset = [...document.body.querySelectorAll<HTMLInputElement>('input[type="password"]')].at(-1)!
    setNativeInput(reset, 'replacement-123')
    await flushPromises()
    const resetButton = [...document.body.querySelectorAll<HTMLButtonElement>('button')].find(candidate => candidate.textContent?.trim() === 'Reset password' && !candidate.disabled)
    resetButton?.click()
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/users/2/password', { method: 'POST', body: { password: 'replacement-123' } })
    wrapper.unmount()
  })

  it('surfaces create and toggle failures', async () => {
    const row = { id: 2, username: 'operator', enabled: false, created_at: 11 }
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/users' && !options) return [row]
      if (path === '/api/v1/users' && options?.method === 'POST') throw new Error('create exploded')
      if (path === '/api/v1/users/2' && options?.method === 'PATCH') throw {}
      return []
    })
    const wrapper = await mountSuspended(AdminUsersPage, { route: false })
    await flushPromises()
    await button(wrapper, 'Add user').trigger('click')
    await flushPromises()
    setNativeInput(modalInput('off'), 'new-user')
    setNativeInput(modalInput('new-password', 0), 'new-password-123')
    setNativeInput(modalInput('new-password', 1), 'new-password-123')
    await flushPromises()
    const add = [...document.body.querySelectorAll<HTMLButtonElement>('button')].find(candidate => candidate.textContent?.trim() === 'Add user' && !candidate.disabled)
    add?.click()
    await flushPromises()
    expect(wrapper.text()).toContain('create exploded')

    const operator = tableRow(wrapper, 'operator')
    await rowButton(operator, 'Enable').trigger('click')
    await clickConfirmation('confirm')
    expect(wrapper.text()).toContain('Unable to enable user')
    wrapper.unmount()
  })
})
