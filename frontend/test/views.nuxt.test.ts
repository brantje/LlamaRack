import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import App from '~/app.vue'
import IndexPage from '~/pages/index.vue'
import ModelsPage from '~/pages/models/index.vue'
import APIPage from '~/pages/api.vue'
import SettingsPage from '~/pages/settings.vue'
import { useManager, type Instance, type Model } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

function model(overrides: Partial<Model> = {}): Model {
  return { id: 'm1', name: 'Coder', gguf_path: 'coder.gguf', total_bytes: 4, context_length: 8192, ...overrides }
}
function instance(overrides: Partial<Instance> = {}): Instance {
  return { id: 'coder', model_id: 'm1', name: 'Coder', enabled: true, autoload_enabled: true, always_on: false, priority: 'normal', eviction_enabled: true, idle_unload_seconds: 0, gpu_mode: 'auto', gpu_devices: [], ...overrides }
}
function resetState() {
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
    expect(wrapper.text()).toContain('Create account')
    await wrapper.find('input[autocomplete="username"]').setValue('admin')
    await wrapper.find('input[type="password"]').setValue('correct-horse-battery')
    mocks.request.mockImplementation(async (path: string) => {
      if (path.endsWith('/auth/bootstrap')) return {}
      if (path.endsWith('/auth/login')) return { id: 1, username: 'admin', enabled: true }
      if (path.endsWith('/models') || path.endsWith('/instances')) return []
      if (path.endsWith('/llamacpp/profile')) throw new Error('unavailable')
      return []
    })
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(manager.user.value?.username).toBe('admin')
    expect(wrapper.text()).toContain('Sign out')
    wrapper.unmount()

    manager.user.value = { id: 1, username: 'admin', enabled: true }
    wrapper = await mountSuspended(App, { route: false })
    expect(wrapper.text()).toContain('llamacpp')
    expect(wrapper.text()).toContain('admin')
    expect(wrapper.findAll('a').some(a => a.text().includes('Models'))).toBe(true)
    expect(wrapper.findAll('a').some(a => a.text().includes('Instances'))).toBe(true)
  })

  it('retries initialization, signs in without bootstrap, and signs out', async () => {
    const manager = resetState()
    manager.backendError.value = 'offline'
    mocks.request.mockImplementation(async (path: string) => {
      if (path.endsWith('/auth/bootstrap')) return { required: false }
      if (path.endsWith('/me') || path.endsWith('/auth/login')) return { id: 1, username: 'admin', enabled: true }
      if (path.endsWith('/models') || path.endsWith('/instances')) return []
      if (path.endsWith('/llamacpp/profile')) throw new Error('no profile')
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
    await wrapper.find('input[autocomplete="username"]').setValue('admin')
    await wrapper.find('input[type="password"]').setValue('correct-horse-battery')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(wrapper.text()).toContain('Sign out')
    await wrapper.findAll('button').find(button => button.text() === 'Sign out')!.trigger('click')
    await flushPromises()
    expect(manager.user.value).toBeNull()
  })

  it('shows authentication errors from response data and Error messages', async () => {
    const manager = resetState(); manager.user.value = null
    mocks.request.mockRejectedValueOnce({ data: { error: 'bad credentials' } })
    let wrapper = await mountSuspended(App, { route: false })
    await wrapper.find('input[autocomplete="username"]').setValue('admin')
    await wrapper.find('input[type="password"]').setValue('wrong-password')
    await wrapper.find('form').trigger('submit'); await flushPromises()
    expect(wrapper.text()).toContain('bad credentials')
    wrapper.unmount()
    mocks.request.mockRejectedValueOnce(new Error('backend exploded'))
    wrapper = await mountSuspended(App, { route: false })
    await wrapper.find('input[autocomplete="username"]').setValue('admin')
    await wrapper.find('input[type="password"]').setValue('wrong-password')
    await wrapper.find('form').trigger('submit'); await flushPromises()
    expect(wrapper.text()).toContain('backend exploded')
  })
})

describe('overview and settings', () => {
  it('renders fleet state and capability information', async () => {
    const manager = resetState()
    manager.models.value = [model({ id: 'm1', name: 'Ready Model', gguf_path: 'ready.gguf' }), model({ id: 'm2', name: 'Failed Model', gguf_path: 'failed.gguf' })]
    manager.instances.value = [instance({ id: 'ready', model_id: 'm1', name: 'Ready', always_on: true }), instance({ id: 'failed', model_id: 'm2', name: 'Failed', autoload_enabled: false })]
    manager.runtimes.value = { m1: [{ instance_id: 'ready', model_id: 'm1', state: 'READY' }], m2: [{ instance_id: 'failed', model_id: 'm2', state: 'FAILED' }] }
    manager.profile.value = { path: '/app/llama-server', version: 'b123', fingerprint: 'abcdefghijklmnopqrstuvwxyz', options: [{ key: 'ctx-size' }] }
    const overview = await mountSuspended(IndexPage, { route: false })
    expect(overview.text()).toContain('Ready Model')
    expect(overview.text()).toContain('ready.gguf')
    const settings = await mountSuspended(SettingsPage, { route: false })
    expect(settings.text()).toContain('http://manager.test:8888')
    expect(settings.text()).toContain('/app/llama-server')
    manager.profile.value = null
    await settings.vm.$nextTick()
    expect(settings.text()).toContain('could not be discovered')
  })

  it('covers empty overview and refresh controls', async () => {
    resetState()
    mocks.request.mockImplementation(async (path: string) => {
      if (path.endsWith('/models') || path.endsWith('/instances')) return []
      if (path.endsWith('/llamacpp/profile')) throw new Error('no profile')
      return []
    })
    const overview = await mountSuspended(IndexPage, { route: false })
    expect(overview.text()).toContain('No models configured')
    await overview.find('button').trigger('click'); await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/models')
    const settings = await mountSuspended(SettingsPage, { route: false })
    await settings.find('button').trigger('click'); await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/instances')
  })
})

describe('models page', () => {
  it('shows registry metadata with only edit/delete management controls', async () => {
    const manager = resetState()
    manager.models.value = [model({ quantization: 'Q4_K_M', context_length: 32768 })]
    const wrapper = await mountSuspended(ModelsPage, { route: false })
    expect(wrapper.findAll('form')).toHaveLength(0)
    expect(wrapper.find('[data-testid="models-table"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('Coder')
    expect(wrapper.text()).toContain('coder.gguf')
    expect(wrapper.text()).toContain('Q4_K_M')
    expect(wrapper.text()).toContain('32,768')
    expect(wrapper.text()).not.toContain('Always on')
    expect(wrapper.findAll('button').some(b => ['Start', 'Stop', 'Logs'].includes(b.text()))).toBe(false)
    expect(wrapper.findAll('a').some(a => a.text() === 'Edit')).toBe(true)
  })

  it('deletes registry rows, reports errors, and honors cancellation', async () => {
    const manager = resetState(); manager.models.value = [model()]
    mocks.request.mockRejectedValueOnce({ data: { error: 'delete failed' } })
    const wrapper = await mountSuspended(ModelsPage, { route: false })
    await wrapper.findAll('button').find(b => b.text() === 'Delete')!.trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('delete failed')
    vi.mocked(globalThis.confirm).mockReturnValue(false)
    mocks.request.mockClear()
    await wrapper.findAll('button').find(b => b.text() === 'Delete')!.trigger('click'); await flushPromises()
    expect(mocks.request).not.toHaveBeenCalled()
  })

  it('refreshes and renders the empty registry state', async () => {
    const manager = resetState(); manager.models.value = []
    mocks.request.mockImplementation(async (path: string) => path.endsWith('/llamacpp/profile') ? Promise.reject(new Error('none')) : [])
    const wrapper = await mountSuspended(ModelsPage, { route: false })
    expect(wrapper.text()).toContain('No models registered')
    expect(wrapper.text()).toContain('Add model')
    await wrapper.findAll('button').find(b => b.text() === 'Refresh')!.trigger('click'); await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/models')
  })
})

describe('API page', () => {
  it('loads, creates, copies, and revokes API keys', async () => {
    resetState()
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/api-keys' && options?.method === 'POST') return { key: { id: 'k1', name: 'sdk', prefix: 'abc', enabled: true, created_at: 1 }, secret: 'secret-value' }
      if (path === '/api/v1/api-keys') return [{ id: 'k1', name: 'sdk', prefix: 'abc', enabled: true, created_at: 1 }, { id: 'k2', name: 'old', prefix: 'old', enabled: false, created_at: 1 }]
      if (path.endsWith('/revoke')) return {}
      return []
    })
    const wrapper = await mountSuspended(APIPage, { route: false }); await flushPromises()
    expect(wrapper.text()).toContain('Disabled')
    await wrapper.find('input[placeholder="Key name"]').setValue('sdk')
    await wrapper.findAll('button').find(button => button.text() === 'Create key')!.trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('secret-value')
    await wrapper.findAll('button').find(button => button.text() === 'Copy')!.trigger('click'); await flushPromises()
    expect(navigator.clipboard.writeText).toHaveBeenCalledWith('secret-value')
    await wrapper.findAll('button').find(b => b.text() === 'Revoke')!.trigger('click'); await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/api-keys/k1/revoke', { method: 'POST' })
  })

  it('handles API-key load and create errors', async () => {
    resetState(); mocks.request.mockRejectedValueOnce(new Error('key load failed'))
    let wrapper = await mountSuspended(APIPage, { route: false }); await flushPromises()
    expect(wrapper.text()).toContain('key load failed'); wrapper.unmount()
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/api-keys' && options?.method === 'POST') throw new Error('key create failed')
      return []
    })
    wrapper = await mountSuspended(APIPage, { route: false }); await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === 'Create key')!.trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('key create failed')
  })
})
