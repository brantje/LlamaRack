import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import AdminIndexPage from '~/pages/admin/index.vue'
import AdminHuggingFacePage from '~/pages/admin/huggingface.vue'
import AdminSystemPage from '~/pages/admin/system.vue'
import AdminGeneralPage from '~/pages/admin/general.vue'
import ProfilePage from '~/pages/profile.vue'
import AdminUsersPage from '~/pages/admin/users.vue'
import { useManager } from '~/composables/useManager'
import { storeManagementToken } from '~/composables/useManagerApi'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

function resetManager(user = true) {
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = user ? { id: 1, username: 'admin', enabled: true } : null
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

async function confirmHuggingFaceRemoval() {
  await flushPromises()
  const control = [...document.body.querySelectorAll<HTMLButtonElement>('[data-testid="confirmation-confirm"]')].at(-1)
  if (!control) throw new Error('Missing Hugging Face removal confirmation button')
  control.click()
  await flushPromises()
}
function row(wrapper: any, text: string) {
  const found = wrapper.findAll('tr').find((candidate: any) => candidate.text().includes(text))
  if (!found) throw new Error(`Missing row: ${text}`)
  return found
}
function rowButton(tableRow: any, text: string) {
  const found = tableRow.findAll('button').find((candidate: any) => candidate.text().trim() === text)
  if (!found) throw new Error(`Missing row button: ${text}`)
  return found
}
async function confirm(kind: 'confirm' | 'cancel') {
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

function settingsResponse(overrides: Record<string, any> = {}) {
  const setting = (value: any, source = 'default', editable = true) => ({ value, source, editable })
  return {
    session_lifetime_seconds: setting(3600), login_protection_enabled: setting(false),
    login_failure_threshold: setting(4), login_lockout_seconds: setting(60),
    trusted_proxies: setting(''), allowed_origins: setting(''), external_url: setting(''),
    startup_timeout_seconds: setting(120), idle_unload_seconds: setting(0), always_on_reconcile_seconds: setting(0),
    runtime: { data_dir: '/data', models_dir: '/models', database_path: '/data/db', listen_addr: ':8000', llama_server_path: '/llama-server' },
    ...overrides
  }
}

beforeEach(() => {
  mocks.request.mockReset()
  resetManager()
})

describe('Phase 10 branch coverage', () => {
  it('covers dashboard empty, unavailable, available and malformed summary branches', async () => {
    let response: any = { users: { total: 1, enabled: 0 }, huggingface: { configured: false }, llamacpp: { available: true } }
    mocks.request.mockImplementation(async (path: string) => path === '/api/v1/admin/summary' ? response : [])
    const wrapper = await mountSuspended(AdminIndexPage, { route: false })
    await flushPromises()
    expect(wrapper.text()).toContain('0 enabled')
    expect(wrapper.text()).toContain('1 total')
    expect(wrapper.text()).toContain('Not configured')
    expect(wrapper.text()).toContain('Available')
    expect(wrapper.find('[data-testid="admin-summary-cards"]').exists()).toBe(true)

    response = { users: { total: 2, enabled: 2 }, huggingface: { configured: true }, llamacpp: { available: true, version: 'b9999' } }
    await button(wrapper, 'Refresh').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('b9999')

    for (const malformed of [null, {}, { users: {} }, { users: {}, huggingface: {} }]) {
      response = malformed
      await button(wrapper, 'Refresh').trigger('click')
      await flushPromises()
      expect(wrapper.find('[data-testid="admin-summary-cards"]').exists()).toBe(false)
    }
    wrapper.unmount()

    resetManager(false)
    mocks.request.mockClear()
    const signedOut = await mountSuspended(AdminIndexPage, { route: false })
    await flushPromises()
    expect(mocks.request).not.toHaveBeenCalledWith('/api/v1/admin/summary')
    signedOut.unmount()
  })

  it('covers Hugging Face status normalization, save/remove and error variants', async () => {
    let mode: 'empty' | 'configured' | 'malformed' | 'data-error' | 'message-error' | 'fallback-error' = 'empty'
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path !== '/api/v1/huggingface/token') return []
      if (mode === 'data-error') throw { data: { error: 'hf denied' } }
      if (mode === 'message-error') throw new Error('hf exploded')
      if (mode === 'fallback-error') throw {}
      if (options?.method === 'DELETE') return {}
      if (mode === 'configured') return { configured: true, prefix: 123 }
      if (mode === 'malformed') return { configured: 'yes' }
      return { configured: false }
    })

    const wrapper = await mountSuspended(AdminHuggingFacePage, { route: false })
    await flushPromises()
    expect(wrapper.text()).toContain('Not configured')

    mode = 'configured'
    await wrapper.findAll('input').at(0)!.setValue('hf_secret')
    await button(wrapper, 'Save token').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Hugging Face token saved')
    expect(wrapper.text()).toContain('Configured')
    expect(wrapper.text()).toContain('configured…')

    mode = 'malformed'
    const passwordInput = wrapper.find('input[type="password"]')
    await passwordInput.setValue('hf_replace')
    await button(wrapper, 'Replace').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Not configured')

    for (const [nextMode, message] of [['data-error', 'hf denied'], ['message-error', 'hf exploded'], ['fallback-error', 'Unable to save Hugging Face token']] as const) {
      mode = nextMode
      await wrapper.find('input[type="password"]').setValue('hf_retry')
      await button(wrapper, 'Save token').trigger('click')
      await flushPromises()
      expect(wrapper.text()).toContain(message)
    }

    mode = 'configured'
    await wrapper.find('input[type="password"]').setValue('hf_again')
    await button(wrapper, 'Save token').trigger('click')
    await flushPromises()
    mode = 'data-error'
    await button(wrapper, 'Remove').trigger('click')
    await confirmHuggingFaceRemoval()
    expect(wrapper.text()).toContain('hf denied')
    mode = 'configured'
    await button(wrapper, 'Remove').trigger('click')
    await confirmHuggingFaceRemoval()
    expect(wrapper.text()).toContain('Not configured')
    wrapper.unmount()
  })

  it('covers system diagnostics value fallbacks, unavailable binary and malformed responses', async () => {
    let response: any = {
      manager: { uptime_seconds: 9, runtime: { database_path: '/db' } },
      network: { effective_scheme: 'http', secure_cookie: false, allowed_origins: { value: '' }, trusted_proxies: null, external_url: '' },
      llamacpp: { available: false }
    }
    mocks.request.mockImplementation(async (path: string) => path === '/api/v1/system' ? response : [])
    const wrapper = await mountSuspended(AdminSystemPage, { route: false })
    await flushPromises()
    expect(wrapper.text()).toContain('Disabled')
    expect(wrapper.text()).toContain('None')
    expect(wrapper.text()).toContain('llama-server is unavailable')

    response = {
      manager: { uptime_seconds: 10, runtime: {} },
      network: { effective_scheme: 'https', secure_cookie: true, allowed_origins: 'https://one.test', trusted_proxies: { value: '10.0.0.0/8' }, external_url: null },
      llamacpp: { available: true, path: '/llama', version: '', options: 0 }
    }
    await button(wrapper, 'Refresh').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Enabled')
    expect(wrapper.text()).toContain('https://one.test')
    expect(wrapper.text()).toContain('10.0.0.0/8')
    expect(wrapper.text()).toContain('unknown')

    for (const malformed of [null, {}, { manager: {} }, { manager: {}, network: {} }]) {
      response = malformed
      await button(wrapper, 'Refresh').trigger('click')
      await flushPromises()
      expect(wrapper.text()).not.toContain('Network security')
    }
    wrapper.unmount()
  })

  it('covers General invalid response and source/editable fallback branches', async () => {
    let settings: any = settingsResponse()
    let system: any = { network: { effective_scheme: 'http', secure_cookie: false } }
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/settings/general' && options?.method === 'PUT') return settings
      if (path === '/api/v1/settings/general') return settings
      if (path === '/api/v1/system') return system
      return []
    })
    const wrapper = await mountSuspended(AdminGeneralPage, { route: false })
    await flushPromises()
    expect(wrapper.text()).toContain('default')

    settings = { ...settingsResponse(), allowed_origins: { value: 'https://locked', source: 'environment', editable: false } }
    const sessionLifetime = components(wrapper, ['InputNumber', 'UInputNumber']).find(component => component.props('modelValue') === 3600)
    expect(sessionLifetime).toBeTruthy()
    sessionLifetime!.vm.$emit('update:modelValue', 3601)
    await flushPromises()
    await button(wrapper, 'Save changes').trigger('click')
    await flushPromises()

    settings = []
    sessionLifetime!.vm.$emit('update:modelValue', 3602)
    await flushPromises()
    await button(wrapper, 'Save changes').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Invalid manager settings response')
    wrapper.unmount()

    resetManager()
    settings = settingsResponse()
    system = {}
    const invalidLoad = await mountSuspended(AdminGeneralPage, { route: false })
    await flushPromises()
    expect(invalidLoad.text()).toContain('Invalid manager settings response')
    invalidLoad.unmount()
  })

  it('covers profile browser/platform labels, cancel, current logout and error fallbacks', async () => {
    const sessions = [
      { id: 'chrome', user_id: 1, created_at: 1, expires_at: 2, remote_address: '1', user_agent: 'Chrome/1 Windows' },
      { id: 'edge', user_id: 1, created_at: 1, expires_at: 2, remote_address: '2', user_agent: 'Edg/1 Mac OS X' },
      { id: 'safari', user_id: 1, created_at: 1, expires_at: 2, remote_address: '3', user_agent: 'Safari/1 Linux' },
      { id: 'unknown', user_id: 1, created_at: 1, expires_at: 2, remote_address: '4', user_agent: 'SomethingElse' },
      { id: 'current', user_id: 1, created_at: 1, expires_at: 2, remote_address: '5', user_agent: '', current: true }
    ]
    let failPath = ''
    let failMode: 'data' | 'message' | 'fallback' = 'data'
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === failPath) {
        if (failMode === 'data') throw { data: { error: 'session denied' } }
        if (failMode === 'message') throw new Error('session exploded')
        throw {}
      }
      if (path === '/api/v1/me') return { id: 1, username: 'admin', enabled: true, created_at: 0, last_login_at: 1 }
      if (path === '/api/v1/me/sessions') return sessions
      if (path === '/api/v1/auth/logout' && options?.method === 'POST') return {}
      return {}
    })
    const wrapper = await mountSuspended(ProfilePage, { route: false })
    await flushPromises()
    expect(wrapper.text()).toContain('Chrome on Windows')
    expect(wrapper.text()).toContain('Edge on macOS')
    expect(wrapper.text()).toContain('Safari on Linux')
    expect(wrapper.text()).toContain('Unknown client')
    expect(wrapper.text()).toContain('Never')

    const revokeButtons = wrapper.findAll('button').filter((candidate: any) => candidate.text().trim() === 'Revoke')
    await revokeButtons[0]!.trigger('click')
    await confirm('cancel')
    expect(mocks.request).not.toHaveBeenCalledWith('/api/v1/sessions/chrome', { method: 'DELETE' })

    failPath = '/api/v1/sessions/chrome'
    failMode = 'fallback'
    await revokeButtons[0]!.trigger('click')
    await confirm('confirm')
    expect(wrapper.text()).toContain('Unable to revoke session')
    failPath = ''

    storeManagementToken('profile-token', false)
    await button(wrapper, 'Sign out').trigger('click')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/auth/logout', { method: 'POST' })
    wrapper.unmount()
  })

  it('covers redesigned Users create, cancelled toggle and mutation error branches', async () => {
    const users = [
      { id: 1, username: 'admin', enabled: true, created_at: 1, last_login_at: 0 },
      { id: 2, username: 'disabled', enabled: false, created_at: 1 }
    ]
    let fail = ''
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/users' && options?.method === 'POST') throw {}
      if (path === '/api/v1/users') return users
      if (path === fail) throw new Error('mutation exploded')
      return {}
    })
    const wrapper = await mountSuspended(AdminUsersPage, { route: false })
    await flushPromises()
    expect(wrapper.text()).toContain('Disabled')
    expect(wrapper.text()).toContain('Never')

    await button(wrapper, 'Add user').trigger('click')
    await flushPromises()
    setNativeInput(modalInput('off'), 'valid-user')
    setNativeInput(modalInput('new-password', 0), '1234567890')
    setNativeInput(modalInput('new-password', 1), '1234567890')
    await flushPromises()
    const add = [...document.body.querySelectorAll<HTMLButtonElement>('button')].find(candidate => candidate.textContent?.trim() === 'Add user' && !candidate.disabled)
    add?.click()
    await flushPromises()
    expect(wrapper.text()).toContain('Unable to create user')

    const createCancel = [...document.body.querySelectorAll<HTMLButtonElement>('button')].find(candidate => candidate.textContent?.trim() === 'Cancel')
    createCancel?.click()
    await flushPromises()

    let disabledRow = row(wrapper, 'disabled')
    expect(disabledRow.text()).not.toContain('Delete')
    expect(disabledRow.text()).not.toContain('Sessions')
    await rowButton(disabledRow, 'Enable').trigger('click')
    await confirm('cancel')
    expect(mocks.request).not.toHaveBeenCalledWith('/api/v1/users/2', expect.objectContaining({ method: 'PATCH' }))

    fail = '/api/v1/users/2'
    disabledRow = row(wrapper, 'disabled')
    await rowButton(disabledRow, 'Enable').trigger('click')
    await confirm('confirm')
    expect(wrapper.text()).toContain('mutation exploded')

    fail = '/api/v1/users/2/password'
    disabledRow = row(wrapper, 'disabled')
    await rowButton(disabledRow, 'Reset password').trigger('click')
    await flushPromises()
    const reset = [...document.body.querySelectorAll<HTMLInputElement>('input[type="password"]')].at(-1)!
    setNativeInput(reset, 'replacement-password')
    await flushPromises()
    const resetButton = [...document.body.querySelectorAll<HTMLButtonElement>('button')].find(candidate => candidate.textContent?.trim() === 'Reset password' && !candidate.disabled)
    resetButton?.click()
    await flushPromises()
    expect(wrapper.text()).toContain('mutation exploded')
    wrapper.unmount()
  })
})
