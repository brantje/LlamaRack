import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mockNuxtImport } from '@nuxt/test-utils/runtime'
import { useManager, type Model } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))

mockNuxtImport('useManagerApi', () => () => ({
  request: mocks.request,
  apiBase: { value: 'http://manager.test:8888' }
}))

function model(overrides: Partial<Model> = {}): Model {
  return {
    id: 'm1',
    model_id: 'coder',
    name: 'Coder',
    gguf_path: 'coder.gguf',
    total_bytes: 4,
    enabled: true,
    autoload_enabled: true,
    always_on: false,
    priority: 'normal',
    eviction_enabled: true,
    idle_unload_seconds: 0,
    routing_policy: 'least_active',
    ...overrides
  }
}

function resetManager() {
  const manager = useManager()
  manager.user.value = null
  manager.initialized.value = false
  manager.bootstrapRequired.value = false
  manager.models.value = []
  manager.runtimes.value = {}
  manager.profile.value = null
  manager.backendError.value = ''
  return manager
}

describe('useManager', () => {
  beforeEach(() => {
    mocks.request.mockReset()
    resetManager()
  })

  it('initializes a first-run manager', async () => {
    mocks.request.mockResolvedValueOnce({ required: true })
    const manager = useManager()
    await manager.initialize()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/auth/bootstrap')
    expect(manager.bootstrapRequired.value).toBe(true)
    expect(manager.initialized.value).toBe(true)
    expect(manager.user.value).toBeNull()
    expect(manager.backendError.value).toBe('')
  })

  it('restores an existing session and refreshes manager state', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/auth/bootstrap') return { required: false }
      if (path === '/api/v1/me') return { id: 1, username: 'admin', enabled: true }
      if (path === '/api/v1/models') return [model()]
      if (path === '/api/v1/models/m1/runtime') return [{ instance_id: 'i1', model_id: 'm1', state: 'READY' }]
      if (path === '/api/v1/llamacpp/profile') return { available: true, profile: { path: '/app/llama-server', version: '1', fingerprint: 'abc', options: [] } }
      throw new Error(`unexpected path ${path}`)
    })
    const manager = useManager()
    await manager.initialize()
    expect(manager.user.value?.username).toBe('admin')
    expect(manager.models.value).toHaveLength(1)
    expect(manager.runtimes.value.m1?.[0]?.state).toBe('READY')
    expect(manager.profile.value?.version).toBe('1')
  })

  it('treats a failed session restore as signed out', async () => {
    mocks.request.mockResolvedValueOnce({ required: false }).mockRejectedValueOnce(new Error('401'))
    const manager = useManager()
    await manager.initialize()
    expect(manager.user.value).toBeNull()
    expect(manager.initialized.value).toBe(true)
  })

  it('surfaces backend initialization errors', async () => {
    mocks.request.mockRejectedValueOnce(new Error('connection refused'))
    const manager = useManager()
    await manager.initialize()
    expect(manager.backendError.value).toBe('connection refused')
    expect(manager.initialized.value).toBe(true)
  })

  it('bootstraps, logs in, and refreshes', async () => {
    const manager = useManager()
    manager.bootstrapRequired.value = true
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/auth/bootstrap') return {}
      if (path === '/api/v1/auth/login') return { id: 1, username: 'admin', enabled: true }
      if (path === '/api/v1/models') return []
      if (path === '/api/v1/llamacpp/profile') throw new Error('not installed')
      throw new Error(`unexpected path ${path}`)
    })
    await manager.authenticate('admin', 'correct-horse-battery')
    expect(manager.bootstrapRequired.value).toBe(false)
    expect(manager.user.value?.username).toBe('admin')
    expect(manager.profile.value).toBeNull()
  })

  it('logs out and clears local state', async () => {
    const manager = useManager()
    manager.user.value = { id: 1, username: 'admin', enabled: true }
    manager.models.value = [model()]
    manager.runtimes.value = { m1: [{ instance_id: 'i', model_id: 'm1', state: 'READY' }] }
    mocks.request.mockResolvedValue(undefined)
    await manager.logout()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/auth/logout', { method: 'POST' })
    expect(manager.user.value).toBeNull()
    expect(manager.models.value).toEqual([])
    expect(manager.runtimes.value).toEqual({})
  })

  it('skips refresh while signed out and tolerates runtime/profile failures', async () => {
    const manager = useManager()
    await manager.refresh()
    expect(mocks.request).not.toHaveBeenCalled()

    manager.user.value = { id: 2, username: 'reader', enabled: true }
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/models') return [model({ model_id: 'm', name: 'Model', gguf_path: 'm.gguf', autoload_enabled: false, priority: 'low' })]
      if (path.includes('/runtime')) throw new Error('runtime unavailable')
      if (path === '/api/v1/llamacpp/profile') throw new Error('profile unavailable')
      throw new Error(path)
    })
    await manager.refresh()
    expect(manager.models.value).toHaveLength(1)
    expect(manager.runtimes.value.m1).toEqual([])
    expect(manager.profile.value).toBeNull()
  })

  it('selects aggregate model state by priority', () => {
    const manager = useManager()
    const item = model({ model_id: 'm', name: 'Model', gguf_path: 'm.gguf' })
    expect(manager.modelState(item)).toBe('UNLOADED')
    manager.runtimes.value.m1 = [{ instance_id: 'i', model_id: 'm1', state: 'FAILED' }]
    expect(manager.modelState(item)).toBe('FAILED')
    manager.runtimes.value.m1.push({ instance_id: 'i2', model_id: 'm1', state: 'STARTING' })
    expect(manager.modelState(item)).toBe('STARTING')
    manager.runtimes.value.m1.push({ instance_id: 'i3', model_id: 'm1', state: 'READY' })
    expect(manager.modelState(item)).toBe('READY')
  })
})
