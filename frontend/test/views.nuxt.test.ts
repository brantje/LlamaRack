import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import App from '~/app.vue'
import IndexPage from '~/pages/index.vue'
import ModelsPage from '~/pages/models/index.vue'
import APIPage from '~/pages/api.vue'
import AdminSystemPage from '~/pages/admin/system.vue'
import { useManager, type Instance, type Model } from '~/composables/useManager'
import { clearManagementToken } from '~/composables/useManagerApi'

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

async function clickConfirmation(kind: 'confirm' | 'cancel') {
  await flushPromises()
  const buttons = [...document.body.querySelectorAll<HTMLButtonElement>(`[data-testid="confirmation-${kind}"]`)]
  const button = buttons.at(-1)
  if (!button) throw new Error(`Missing confirmation ${kind} button`)
  button.click()
  await flushPromises()
}

beforeEach(() => {
  mocks.request.mockReset()
  mocks.request.mockResolvedValue([])
  clearManagementToken()
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: vi.fn() } })
  resetState()
})

describe('application shell', () => {
  it('renders backend failure, bootstrap/login, and signed-in navigation states', { timeout: 15_000 }, async () => {
    const manager = resetState()
    manager.backendError.value = 'connection refused'
    let wrapper = await mountSuspended(App, { route: false })
    expect(wrapper.text()).toContain('LlamaRack connection failed')
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
      if (path.endsWith('/auth/login')) return {
        access_token: 'view-login-jwt', token_type: 'Bearer', expires_at: 99,
        user: { id: 1, username: 'admin', enabled: true }
      }
      if (path.endsWith('/models') || path.endsWith('/instances')) return []
      if (path.endsWith('/llamacpp/profile')) throw new Error('unavailable')
      if (path.endsWith('/auth/ws-ticket')) return { ticket: 'view-ticket' }
      return []
    })
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(manager.user.value?.username).toBe('admin')
    expect(wrapper.text()).toContain('Sign out')
    expect(wrapper.text()).toContain('Administration')
    wrapper.unmount()

    clearManagementToken()
    manager.user.value = null
    manager.bootstrapRequired.value = false
    mocks.request.mockReset().mockRejectedValueOnce({ data: { error: 'bad credentials' } })
    wrapper = await mountSuspended(App, { route: false })
    expect(wrapper.text()).toContain('Welcome back')
    await wrapper.find('input[autocomplete="username"]').setValue('admin')
    await wrapper.find('input[type="password"]').setValue('wrong-password')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(wrapper.text()).toContain('bad credentials')
  })
})

describe('overview and administration', () => {
  it('renders instance fleet health on the dashboard and read-only system diagnostics', async () => {
    const manager = resetState()
    manager.models.value = [model({ id: 'm1', name: 'Ready Model' }), model({ id: 'm2', name: 'Failed Model' })]
    manager.instances.value = [instance({ id: 'ready', model_id: 'm1', name: 'Ready', always_on: true }), instance({ id: 'failed', model_id: 'm2', name: 'Failed', autoload_enabled: false })]
    manager.runtimes.value = { m1: [{ instance_id: 'ready', model_id: 'm1', state: 'READY' }], m2: [{ instance_id: 'failed', model_id: 'm2', state: 'FAILED' }] }
    const overview = await mountSuspended(IndexPage, { route: false })
    expect(overview.text()).toContain('Dashboard')
    expect(overview.text()).toContain('1 / 2 Instances')
    expect(overview.text()).toContain('failed failed to start')
    expect(overview.text()).toContain('VRAM ALLOCATION')

    mocks.request.mockResolvedValueOnce({
      manager: { uptime_seconds: 42, runtime: { data_dir: '/config', models_dir: '/models' } },
      network: { effective_scheme: 'https', secure_cookie: true, allowed_origins: { value: 'https://manager.test' }, trusted_proxies: { value: '' }, external_url: { value: '' } },
      llamacpp: { available: true, path: '/app/llama-server', version: 'b123', fingerprint: 'abc', options: 12 }
    })
    const system = await mountSuspended(AdminSystemPage, { route: false })
    await flushPromises()
    expect(system.text()).toContain('/config')
    expect(system.text()).toContain('Secure session cookie')
    expect(system.text()).toContain('/app/llama-server')
  })
})

describe('models page', () => {
  it('shows registry metadata with edit/delete management controls and confirms deletion', async () => {
    const manager = resetState()
    manager.models.value = [model({ quantization: 'Q4_K_M', context_length: 32768 })]
    const wrapper = await mountSuspended(ModelsPage, { route: false })
    expect(wrapper.find('[data-testid="models-table"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('Coder')
    expect(wrapper.text()).toContain('Q4_K_M')
    expect(wrapper.text()).not.toContain('Always on')

    mocks.request.mockClear()
    await wrapper.findAll('button').find(button => button.text() === 'Delete')!.trigger('click')
    expect(mocks.request).not.toHaveBeenCalled()
    await clickConfirmation('cancel')
    expect(mocks.request).not.toHaveBeenCalled()
  })
})

describe('API page', () => {
  it('creates, copies and rotates keys without revoke', async () => {
    resetState()
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/api-keys' && options?.method === 'POST') return { key: { id: 'k1', name: 'sdk', prefix: 'sk-abc12345', enabled: true, key_type: 'inference', owner_kind: 'user', owner_id: 1, owner_name: 'admin', owner_enabled: true, created_at: 1 }, secret: 'secret-value' }
      if (path === '/api/v1/api-keys') return [{ id: 'k1', name: 'sdk', prefix: 'sk-abc12345', enabled: true, key_type: 'inference', owner_kind: 'user', owner_id: 1, owner_name: 'admin', owner_enabled: true, created_at: 1 }]
      if (path === '/api/v1/users') return [{ id: 1, username: 'admin', enabled: true }]
      if (path === '/api/v1/admin/service-accounts') return []
      return []
    })
    const wrapper = await mountSuspended(APIPage, { route: false })
    await flushPromises()
    expect(wrapper.findAll('button').some(button => button.text() === 'Revoke')).toBe(false)

    await wrapper.get('[data-testid="create-key"]').trigger('click')
    await flushPromises()
    const save = [...document.body.querySelectorAll<HTMLButtonElement>('[data-testid="api-key-save"]')].at(-1)
    save?.click()
    await flushPromises()
    expect(document.body.textContent).toContain('secret-value')
    const copy = [...document.body.querySelectorAll<HTMLButtonElement>('[data-testid="copy-key"]')].at(-1)
    copy?.click()
    await flushPromises()
    expect(navigator.clipboard.writeText).toHaveBeenCalledWith('secret-value')
  })
})
