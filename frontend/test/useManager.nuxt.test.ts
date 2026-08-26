import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mockNuxtImport } from '@nuxt/test-utils/runtime'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({
  request: vi.fn()
}))

mockNuxtImport('useManagerApi', () => () => ({
  request: mocks.request,
  apiBase: { value: 'http://manager.test:8888' }
}))

function resetManager() {
  const manager = useManager()
  manager.user.value = null
  manager.initialized.value = false
  manager.bootstrapRequired.value = false
  manager.models.value = []
  manager.artifacts.value = []
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
      if (path === '/api/v1/me') return { id: 1, username: 'admin', role: 'admin', enabled: true }
      if (path === '/api/v1/models') return [{ id: 'm1', model_id: 'coder', artifact_id: 'a1', artifact_path: 'coder.gguf', enabled: true, autoload_enabled: true, always_on: false, priority: 'normal', routing_policy: 'least_active' }]
      if (path === '/api/v1/artifacts') return [{ id: 'a1', display_name: 'Coder', local_path: 'coder.gguf', total_bytes: 4 }]
      if (path === '/api/v1/models/m1/runtime') return [{ instance_id: 'i1', model_id: 'm1', state: 'READY' }]
      if (path === '/api/v1/llamacpp/profile') return { available: true, profile: { path: '/app/llama-server', version: '1', fingerprint: 'abc', options: [] } }
      throw new Error(`unexpected path ${path}`)
    })
    const manager = useManager()
    await manager.initialize()
    expect(manager.user.value?.username).toBe('admin')
    expect(manager.models.value).toHaveLength(1)
    expect(manager.artifacts.value).toHaveLength(1)
    expect(manager.runtimes.value.m1?.[0]?.state).toBe('READY')
    expect(manager.profile.value?.version).toBe('1')
    expect(manager.canOperate.value).toBe(true)
    expect(manager.isAdmin.value).toBe(true)
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
      if (path === '/api/v1/auth/login') return { id: 1, username: 'admin', role: 'operator', enabled: true }
      if (path === '/api/v1/models' || path === '/api/v1/artifacts') return []
      if (path === '/api/v1/llamacpp/profile') throw new Error('not installed')
      throw new Error(`unexpected path ${path}`)
    })
    await manager.authenticate('admin', 'correct-horse-battery')
    expect(manager.bootstrapRequired.value).toBe(false)
    expect(manager.user.value?.role).toBe('operator')
    expect(manager.canOperate.value).toBe(true)
    expect(manager.isAdmin.value).toBe(false)
    expect(manager.profile.value).toBeNull()
  })

  it('logs out and clears local state', async () => {
    const manager = useManager()
    manager.user.value = { id: 1, username: 'admin', role: 'admin', enabled: true }
    manager.models.value = [{ id: 'm1', model_id: 'm', artifact_id: 'a', artifact_path: 'x', enabled: true, autoload_enabled: true, always_on: false, priority: 'normal', routing_policy: 'least_active' }]
    manager.artifacts.value = [{ id: 'a', display_name: 'a', local_path: 'a.gguf', total_bytes: 1 }]
    manager.runtimes.value = { m1: [{ instance_id: 'i', model_id: 'm1', state: 'READY' }] }
    mocks.request.mockResolvedValue(undefined)
    await manager.logout()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/auth/logout', { method: 'POST' })
    expect(manager.user.value).toBeNull()
    expect(manager.models.value).toEqual([])
    expect(manager.artifacts.value).toEqual([])
    expect(manager.runtimes.value).toEqual({})
  })

  it('skips refresh while signed out and tolerates runtime/profile failures', async () => {
    const manager = useManager()
    await manager.refresh()
    expect(mocks.request).not.toHaveBeenCalled()

    manager.user.value = { id: 2, username: 'reader', role: 'readonly', enabled: true }
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/models') return [{ id: 'm1', model_id: 'm', artifact_id: 'a', artifact_path: 'm.gguf', enabled: true, autoload_enabled: false, always_on: false, priority: 'low', routing_policy: 'least_active' }]
      if (path === '/api/v1/artifacts') return null
      if (path.includes('/runtime')) throw new Error('runtime unavailable')
      if (path === '/api/v1/llamacpp/profile') throw new Error('profile unavailable')
      throw new Error(path)
    })
    await manager.refresh()
    expect(manager.models.value).toHaveLength(1)
    expect(manager.artifacts.value).toEqual([])
    expect(manager.runtimes.value.m1).toEqual([])
    expect(manager.profile.value).toBeNull()
    expect(manager.canOperate.value).toBe(false)
    expect(manager.isAdmin.value).toBe(false)
  })

  it('selects aggregate model state by priority', () => {
    const manager = useManager()
    const model = { id: 'm1', model_id: 'm', artifact_id: 'a', artifact_path: 'm.gguf', enabled: true, autoload_enabled: true, always_on: false, priority: 'normal', routing_policy: 'least_active' }
    expect(manager.modelState(model)).toBe('UNLOADED')
    manager.runtimes.value.m1 = [{ instance_id: 'i', model_id: 'm1', state: 'FAILED' }]
    expect(manager.modelState(model)).toBe('FAILED')
    manager.runtimes.value.m1.push({ instance_id: 'i2', model_id: 'm1', state: 'STARTING' })
    expect(manager.modelState(model)).toBe('STARTING')
    manager.runtimes.value.m1.push({ instance_id: 'i3', model_id: 'm1', state: 'READY' })
    expect(manager.modelState(model)).toBe('READY')
  })
})
