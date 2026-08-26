import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import App from '~/app.vue'
import IndexPage from '~/pages/index.vue'
import ModelsPage from '~/pages/models.vue'
import APIPage from '~/pages/api.vue'
import SettingsPage from '~/pages/settings.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

function resetState() {
  const manager = useManager()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', role: 'admin', enabled: true }
  manager.models.value = []
  manager.artifacts.value = []
  manager.runtimes.value = {}
  manager.profile.value = null
  return manager
}

beforeEach(() => {
  mocks.request.mockReset()
  mocks.request.mockResolvedValue([])
  vi.stubGlobal('confirm', vi.fn(() => true))
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: vi.fn() } })
  resetState()
})

describe('application shell', () => {
  it('renders backend failure, authentication, and signed-in states', async () => {
    const manager = resetState()
    manager.backendError.value = 'connection refused'
    let wrapper = await mountSuspended(App, { route: false })
    expect(wrapper.text()).toContain('Manager connection failed')
    expect(wrapper.text()).toContain('connection refused')
    wrapper.unmount()

    manager.backendError.value = ''
    manager.user.value = null
    manager.bootstrapRequired.value = true
    wrapper = await mountSuspended(App, { route: false })
    expect(wrapper.text()).toContain('Create administrator')
    await wrapper.find('input[autocomplete="username"]').setValue('admin')
    await wrapper.find('input[type="password"]').setValue('correct-horse-battery')
    mocks.request.mockImplementation(async (path: string) => {
      if (path.endsWith('/auth/bootstrap')) return {}
      if (path.endsWith('/auth/login')) return { id: 1, username: 'admin', role: 'admin', enabled: true }
      if (path.endsWith('/models') || path.endsWith('/artifacts')) return []
      if (path.endsWith('/llamacpp/profile')) throw new Error('unavailable')
      return []
    })
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(manager.user.value?.username).toBe('admin')
    expect(wrapper.text()).toContain('Sign out')
    wrapper.unmount()

    manager.user.value = { id: 1, username: 'admin', role: 'admin', enabled: true }
    wrapper = await mountSuspended(App, { route: false })
    expect(wrapper.text()).toContain('llamacpp')
    expect(wrapper.text()).toContain('admin')
    expect(wrapper.findAll('a').some(a => a.text().includes('Models'))).toBe(true)
    wrapper.unmount()
  })

  it('retries backend initialization, signs in without bootstrap, and signs out', async () => {
    const manager = resetState()
    manager.backendError.value = 'offline'
    mocks.request.mockImplementation(async (path: string) => {
      if (path.endsWith('/auth/bootstrap')) return { required: false }
      if (path.endsWith('/me')) return { id: 1, username: 'admin', role: 'admin', enabled: true }
      if (path.endsWith('/models') || path.endsWith('/artifacts')) return []
      if (path.endsWith('/llamacpp/profile')) throw new Error('no profile')
      if (path.endsWith('/auth/login')) return { id: 1, username: 'admin', role: 'admin', enabled: true }
      if (path.endsWith('/auth/logout')) return {}
      return []
    })
    let wrapper = await mountSuspended(App, { route: false })
    await wrapper.find('button').trigger('click')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/auth/bootstrap')
    wrapper.unmount()

    manager.backendError.value = ''
    manager.user.value = null
    manager.bootstrapRequired.value = false
    wrapper = await mountSuspended(App, { route: false })
    expect(wrapper.text()).toContain('Welcome back')
    expect(wrapper.find('input[type="password"]').attributes('autocomplete')).toBe('current-password')
    await wrapper.find('input[autocomplete="username"]').setValue('admin')
    await wrapper.find('input[type="password"]').setValue('correct-horse-battery')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(mocks.request).not.toHaveBeenCalledWith('/api/v1/auth/bootstrap', expect.anything())
    expect(wrapper.text()).toContain('Sign out')
    await wrapper.find('button.text-button').trigger('click')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/auth/logout', { method: 'POST' })
    expect(manager.user.value).toBeNull()
  })

  it('shows authentication errors from response data and Error messages', async () => {
    const manager = resetState()
    manager.user.value = null
    mocks.request.mockRejectedValueOnce({ data: { error: 'bad credentials' } })
    let wrapper = await mountSuspended(App, { route: false })
    await wrapper.find('input[autocomplete="username"]').setValue('admin')
    await wrapper.find('input[type="password"]').setValue('wrong-password')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(wrapper.text()).toContain('bad credentials')
    wrapper.unmount()

    mocks.request.mockRejectedValueOnce(new Error('backend exploded'))
    wrapper = await mountSuspended(App, { route: false })
    await wrapper.find('input[autocomplete="username"]').setValue('admin')
    await wrapper.find('input[type="password"]').setValue('wrong-password')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(wrapper.text()).toContain('backend exploded')
  })
})

describe('overview and settings', () => {
  it('renders fleet state and capability information', async () => {
    const manager = resetState()
    manager.models.value = [
      { id: 'm1', model_id: 'ready', display_name: 'Ready Model', artifact_id: 'a1', artifact_path: 'ready.gguf', enabled: true, autoload_enabled: true, always_on: false, priority: 'normal', routing_policy: 'least_active' },
      { id: 'm2', model_id: 'failed', artifact_id: 'a2', artifact_path: 'failed.gguf', enabled: true, autoload_enabled: false, always_on: true, priority: 'high', routing_policy: 'round_robin' },
      { id: 'm3', model_id: 'manual', artifact_id: 'a3', artifact_path: 'manual.gguf', enabled: true, autoload_enabled: false, always_on: false, priority: 'low', routing_policy: 'least_active' }
    ]
    manager.runtimes.value = {
      m1: [{ instance_id: 'i1', model_id: 'm1', state: 'READY' }],
      m2: [{ instance_id: 'i2', model_id: 'm2', state: 'FAILED' }]
    }
    manager.profile.value = { path: '/app/llama-server', version: 'b123', fingerprint: 'abcdefghijklmnopqrstuvwxyz', options: [{ key: 'ctx-size' }] }
    const overview = await mountSuspended(IndexPage, { route: false })
    expect(overview.text()).toContain('Ready Model')
    expect(overview.text()).toContain('Always on')
    expect(overview.text()).toContain('Manual')
    expect(overview.text()).toContain('b123')
    expect(overview.text()).toContain('1 discovered CLI options')

    const settings = await mountSuspended(SettingsPage, { route: false })
    expect(settings.text()).toContain('http://manager.test:8888')
    expect(settings.text()).toContain('/app/llama-server')
    expect(settings.text()).toContain('abcdefghijklmnopqrstuvwxyz'.slice(0, 24))
    manager.profile.value = null
    await nextTick()
    expect(settings.text()).toContain('could not be discovered')
  })

  it('covers empty and readonly overview states and refresh controls', async () => {
    const manager = resetState()
    manager.user.value = { id: 2, username: 'viewer', role: 'readonly', enabled: true }
    mocks.request.mockImplementation(async (path: string) => {
      if (path.endsWith('/models') || path.endsWith('/artifacts')) return []
      if (path.endsWith('/llamacpp/profile')) throw new Error('no profile')
      return []
    })
    const overview = await mountSuspended(IndexPage, { route: false })
    expect(overview.text()).toContain('No models configured')
    expect(overview.text()).not.toContain('Manage models')
    await overview.find('button').trigger('click')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/models')

    const settings = await mountSuspended(SettingsPage, { route: false })
    expect(settings.text()).toContain('could not be discovered')
    await settings.find('button').trigger('click')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/artifacts')
  })
})

describe('models page', () => {
  it('registers artifacts and creates models', async () => {
    const manager = resetState()
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/artifacts/register') return { id: 'a1', display_name: 'Coder', local_path: 'coder.gguf', total_bytes: 4, quantization: 'Q4_K_M' }
      if (path === '/api/v1/models' && options?.method === 'POST') return { id: 'm1' }
      if (path === '/api/v1/models' || path === '/api/v1/artifacts') return []
      if (path === '/api/v1/llamacpp/profile') throw new Error('no profile')
      return []
    })
    const wrapper = await mountSuspended(ModelsPage, { route: false })
    const forms = wrapper.findAll('form')
    await forms[0]!.find('input').setValue('/models/coder.gguf')
    await forms[0]!.trigger('submit')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/artifacts/register', expect.objectContaining({ method: 'POST' }))

    manager.artifacts.value = [
      { id: 'a1', display_name: 'Coder', local_path: 'coder.gguf', total_bytes: 4, quantization: 'Q4_K_M' },
      { id: 'a2', display_name: 'Plain', local_path: 'plain.gguf', total_bytes: 4 }
    ]
    await nextTick()
    expect(wrapper.text()).toContain('Plain')
    const currentForms = wrapper.findAll('form')
    await currentForms[1]!.find('input').setValue('coder')
    await currentForms[1]!.find('select').setValue('a1')
    await currentForms[1]!.trigger('submit')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/models', expect.objectContaining({ method: 'POST' }))
  })

  it('starts, stops, deletes, and reports action errors', async () => {
    const manager = resetState()
    manager.models.value = [
      { id: 'm1', model_id: 'coder', artifact_id: 'a1', artifact_path: 'coder.gguf', enabled: true, autoload_enabled: true, always_on: false, priority: 'normal', routing_policy: 'least_active' },
      { id: 'm2', model_id: 'manual', display_name: 'Manual', artifact_id: 'a2', artifact_path: 'manual.gguf', enabled: true, autoload_enabled: false, always_on: false, priority: 'low', routing_policy: 'round_robin' },
      { id: 'm3', model_id: 'resident', display_name: 'Resident', artifact_id: 'a3', artifact_path: 'resident.gguf', enabled: true, autoload_enabled: false, always_on: true, priority: 'high', routing_policy: 'least_active' }
    ]
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/models/m1/start') throw { data: { error: 'start failed' } }
      if (path.includes('/start') || path.includes('/stop') || path === '/api/v1/models/m1') return {}
      if (path === '/api/v1/models' || path === '/api/v1/artifacts') return manager.models.value
      if (path.includes('/runtime')) return []
      if (path.includes('/profile')) throw new Error('none')
      return []
    })
    const wrapper = await mountSuspended(ModelsPage, { route: false })
    expect(wrapper.text()).toContain('manual')
    expect(wrapper.text()).toContain('always on')
    const buttons = wrapper.findAll('button')
    const startButtons = buttons.filter(b => b.text() === 'Start')
    await startButtons[0]!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('start failed')
    await startButtons[1]!.trigger('click')
    await flushPromises()
    await buttons.find(b => b.text() === 'Stop')!.trigger('click')
    await flushPromises()
    await buttons.find(b => b.text() === 'Delete')!.trigger('click')
    await flushPromises()
    expect(confirm).toHaveBeenCalled()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/models/m1', { method: 'DELETE' })
  })

  it('covers form errors, cancelled deletion, and readonly rendering', async () => {
    const manager = resetState()
    manager.models.value = [{ id: 'm1', model_id: 'coder', artifact_id: 'a1', artifact_path: 'coder.gguf', enabled: true, autoload_enabled: false, always_on: false, priority: 'normal', routing_policy: 'least_active' }]
    manager.artifacts.value = [{ id: 'a1', display_name: 'Coder', local_path: 'coder.gguf', total_bytes: 4 }]
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/artifacts/register') throw new Error('artifact failed')
      if (path === '/api/v1/models' && options?.method === 'POST') throw { data: { error: 'model failed' } }
      return []
    })
    let wrapper = await mountSuspended(ModelsPage, { route: false })
    let forms = wrapper.findAll('form')
    await forms[0]!.find('input').setValue('/models/bad.gguf')
    await forms[0]!.trigger('submit')
    await flushPromises()
    expect(wrapper.text()).toContain('artifact failed')
    forms = wrapper.findAll('form')
    await forms[1]!.find('input').setValue('bad-model')
    await forms[1]!.find('select').setValue('a1')
    await forms[1]!.trigger('submit')
    await flushPromises()
    expect(wrapper.text()).toContain('model failed')

    vi.stubGlobal('confirm', vi.fn(() => false))
    await wrapper.findAll('button').find(b => b.text() === 'Delete')!.trigger('click')
    await flushPromises()
    expect(mocks.request).not.toHaveBeenCalledWith('/api/v1/models/m1', { method: 'DELETE' })
    wrapper.unmount()

    manager.user.value = { id: 3, username: 'viewer', role: 'readonly', enabled: true }
    wrapper = await mountSuspended(ModelsPage, { route: false })
    expect(wrapper.text()).toContain('coder.gguf')
    expect(wrapper.findAll('form')).toHaveLength(0)
    expect(wrapper.findAll('button').some(b => ['Start', 'Stop', 'Delete'].includes(b.text()))).toBe(false)
  })
})

describe('API page', () => {
  it('loads, creates, copies, and revokes API keys for admins', async () => {
    resetState()
    let listed = false
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/api-keys' && options?.method === 'POST') return { key: { id: 'k1', name: 'sdk', prefix: 'abc', enabled: true, created_at: 1 }, secret: 'secret-value' }
      if (path === '/api/v1/api-keys') { listed = true; return [{ id: 'k1', name: 'sdk', prefix: 'abc', enabled: true, created_at: 1 }, { id: 'k2', name: 'old', prefix: 'old', enabled: false, created_at: 1 }] }
      if (path.endsWith('/revoke')) return {}
      return []
    })
    const wrapper = await mountSuspended(APIPage, { route: false })
    await flushPromises()
    expect(listed).toBe(true)
    expect(wrapper.text()).toContain('Revoked')
    await wrapper.find('.inline-create input').setValue('sdk')
    await wrapper.find('.inline-create button').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('secret-value')
    await wrapper.find('.secret-box button').trigger('click')
    await flushPromises()
    expect(navigator.clipboard.writeText).toHaveBeenCalledWith('secret-value')
    const revoke = wrapper.findAll('button').find(b => b.text() === 'Revoke')!
    await revoke.trigger('click')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/api-keys/k1/revoke', { method: 'POST' })
  })

  it('hides key management for non-admins and handles load/create errors', async () => {
    const manager = resetState()
    manager.user.value = { id: 2, username: 'operator', role: 'operator', enabled: true }
    let wrapper = await mountSuspended(APIPage, { route: false })
    expect(wrapper.text()).toContain('Only administrators')
    wrapper.unmount()

    manager.user.value = { id: 1, username: 'admin', role: 'admin', enabled: true }
    mocks.request.mockRejectedValueOnce(new Error('key load failed'))
    wrapper = await mountSuspended(APIPage, { route: false })
    await flushPromises()
    expect(wrapper.text()).toContain('key load failed')
    wrapper.unmount()

    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/api-keys' && options?.method === 'POST') throw new Error('key create failed')
      return []
    })
    wrapper = await mountSuspended(APIPage, { route: false })
    await flushPromises()
    await wrapper.find('.inline-create button').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('key create failed')
  })
})
