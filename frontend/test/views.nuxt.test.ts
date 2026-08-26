import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import App from '~/app.vue'
import IndexPage from '~/pages/index.vue'
import ModelsPage from '~/pages/models/index.vue'
import APIPage from '~/pages/api.vue'
import SettingsPage from '~/pages/settings.vue'
import { useManager, type Model } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

function model(overrides: Partial<Model> = {}): Model {
  return {
    id: 'm1', model_id: 'coder', name: 'Coder', gguf_path: 'coder.gguf', total_bytes: 4,
    enabled: true, autoload_enabled: true, always_on: false, priority: 'normal', routing_policy: 'least_active',
    ...overrides
  }
}

function resetState() {
  const manager = useManager()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', role: 'admin', enabled: true }
  manager.models.value = []
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
      if (path.endsWith('/models')) return []
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
      if (path.endsWith('/models')) return []
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
    expect(wrapper.text()).toContain('Sign out')
    await wrapper.findAll('button').find(button => button.text() === 'Sign out')!.trigger('click')
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
      model({ id: 'm1', model_id: 'ready', name: 'Ready Model', gguf_path: 'ready.gguf' }),
      model({ id: 'm2', model_id: 'failed', name: 'Failed Model', gguf_path: 'failed.gguf', autoload_enabled: false, always_on: true, priority: 'high', routing_policy: 'round_robin' }),
      model({ id: 'm3', model_id: 'manual', name: 'Manual Model', gguf_path: 'manual.gguf', autoload_enabled: false, priority: 'low' })
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
    expect(overview.text()).toContain('ready.gguf')
    expect(overview.text()).toContain('b123')

    const settings = await mountSuspended(SettingsPage, { route: false })
    expect(settings.text()).toContain('http://manager.test:8888')
    expect(settings.text()).toContain('/app/llama-server')
    manager.profile.value = null
    await nextTick()
    expect(settings.text()).toContain('could not be discovered')
  })

  it('covers empty and readonly overview states and refresh controls', async () => {
    const manager = resetState()
    manager.user.value = { id: 2, username: 'viewer', role: 'readonly', enabled: true }
    mocks.request.mockImplementation(async (path: string) => {
      if (path.endsWith('/models')) return []
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
    await settings.find('button').trigger('click')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/models')
  })
})

describe('models page', () => {
  it('shows fleet-only model management with a separate add-model route', async () => {
    const manager = resetState()
    manager.models.value = [model({ quantization: 'Q4_K_M' })]
    const wrapper = await mountSuspended(ModelsPage, { route: false })
    expect(wrapper.findAll('form')).toHaveLength(0)
    expect(wrapper.text()).toContain('Coder')
    expect(wrapper.text()).toContain('coder.gguf')
    expect(wrapper.text()).toContain('Q4_K_M')
    const addLinks = wrapper.findAll('a').filter(a => a.text() === 'Add model')
    expect(addLinks.length).toBeGreaterThan(0)
    expect(addLinks[0]!.attributes('href')).toBe('/models/new')
  })

  it('starts, stops, deletes, and reports action errors', async () => {
    const manager = resetState()
    manager.models.value = [
      model({ id: 'm1', model_id: 'coder' }),
      model({ id: 'm2', model_id: 'manual', name: 'Manual', gguf_path: 'manual.gguf', autoload_enabled: false, priority: 'low', routing_policy: 'round_robin' }),
      model({ id: 'm3', model_id: 'resident', name: 'Resident', gguf_path: 'resident.gguf', autoload_enabled: false, always_on: true, priority: 'high' })
    ]
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/models/m1/start') throw { data: { error: 'start failed' } }
      if (path.includes('/start') || path.includes('/stop') || path === '/api/v1/models/m1') return {}
      if (path === '/api/v1/models') return manager.models.value
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
    const deleteButton = wrapper.findAll('button').find(b => b.text() === 'Delete')!
    await deleteButton.trigger('click')
    await flushPromises()
    expect(confirm).toHaveBeenCalled()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/models/m1', { method: 'DELETE' })
  })

  it('supports cancelled deletion and readonly rendering', async () => {
    const manager = resetState()
    manager.models.value = [model({ autoload_enabled: false })]
    vi.stubGlobal('confirm', vi.fn(() => false))
    let wrapper = await mountSuspended(ModelsPage, { route: false })
    await wrapper.findAll('button').find(b => b.text() === 'Delete')!.trigger('click')
    await flushPromises()
    expect(mocks.request).not.toHaveBeenCalledWith('/api/v1/models/m1', { method: 'DELETE' })
    wrapper.unmount()

    manager.user.value = { id: 3, username: 'viewer', role: 'readonly', enabled: true }
    wrapper = await mountSuspended(ModelsPage, { route: false })
    expect(wrapper.text()).toContain('coder.gguf')
    expect(wrapper.findAll('a').some(a => a.text() === 'Add model')).toBe(false)
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
    expect(wrapper.text()).toContain('Disabled')
    await wrapper.find('input[placeholder="Key name"]').setValue('sdk')
    await wrapper.findAll('button').find(button => button.text() === 'Create key')!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('secret-value')
    await wrapper.findAll('button').find(button => button.text() === 'Copy')!.trigger('click')
    await flushPromises()
    expect(navigator.clipboard.writeText).toHaveBeenCalledWith('secret-value')
    await wrapper.findAll('button').find(b => b.text() === 'Revoke')!.trigger('click')
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
    await wrapper.findAll('button').find(button => button.text() === 'Create key')!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('key create failed')
  })
})