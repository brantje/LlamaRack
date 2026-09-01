import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport } from '@nuxt/test-utils/runtime'
import { useManager, type Instance, type Model } from '~/composables/useManager'
import { clearManagementToken, readManagementToken, storeManagementToken } from '~/composables/useManagerApi'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

class FakeWebSocket {
  static instances: FakeWebSocket[] = []
  static throwOnCreate = false
  url: string
  closed = false
  onopen: ((event: Event) => void) | null = null
  onmessage: ((event: MessageEvent) => void) | null = null
  onclose: ((event: CloseEvent) => void) | null = null
  constructor(url: string) {
    if (FakeWebSocket.throwOnCreate) throw new Error('invalid websocket')
    this.url = url
    FakeWebSocket.instances.push(this)
  }
  open() { this.onopen?.({} as Event) }
  emit(payload: unknown) { this.onmessage?.({ data: JSON.stringify(payload) } as MessageEvent) }
  emitRaw(data: string) { this.onmessage?.({ data } as MessageEvent) }
  disconnect() { this.onclose?.({} as CloseEvent) }
  close() { this.closed = true; this.onclose?.({} as CloseEvent) }
}

function model(overrides: Partial<Model> = {}): Model {
  return { id: 'm1', name: 'Coder', gguf_path: 'coder.gguf', total_bytes: 4, context_length: 8192, ...overrides }
}
function instance(overrides: Partial<Instance> = {}): Instance {
  return {
    id: 'coder', model_id: 'm1', name: 'Coder', enabled: true, autoload_enabled: true,
    always_on: false, priority: 'normal', eviction_enabled: true, idle_unload_seconds: 0,
    gpu_mode: 'auto', gpu_devices: [], ...overrides
  }
}
function resetManager() {
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.user.value = null
  manager.initialized.value = false
  manager.bootstrapRequired.value = false
  manager.localLoginEnabled.value = true
  manager.authProviders.value = []
  manager.models.value = []
  manager.instances.value = []
  manager.runtimes.value = {}
  manager.profile.value = null
  manager.backendError.value = ''
  manager.runtimeEventsConnected.value = false
  return manager
}

function standardRequest(path: string) {
  if (path === '/api/v1/models') return [model()]
  if (path === '/api/v1/instances') return [instance()]
  if (path === '/api/v1/instances/coder/runtime') return { instance_id: 'coder', model_id: 'm1', state: 'READY' }
  if (path === '/api/v1/llamacpp/profile') return { available: true, profile: { path: '/app/llama-server', version: '1', fingerprint: 'abc', options: [] } }
  throw new Error(`unexpected path ${path}`)
}

function authProviders() {
  return { local_login_enabled: true, providers: [] }
}

describe('useManager', () => {
  beforeEach(() => {
    vi.useRealTimers()
    mocks.request.mockReset()
    FakeWebSocket.instances = []
    FakeWebSocket.throwOnCreate = false
    vi.stubGlobal('WebSocket', FakeWebSocket as any)
    clearManagementToken()
    resetManager()
  })

  it('initializes a first-run manager', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/auth/bootstrap') return { required: true }
      if (path === '/api/v1/auth/providers') return authProviders()
      throw new Error(`unexpected path ${path}`)
    })
    const manager = useManager()
    await manager.initialize()
    expect(manager.bootstrapRequired.value).toBe(true)
    expect(manager.initialized.value).toBe(true)
    expect(manager.user.value).toBeNull()
    expect(FakeWebSocket.instances).toHaveLength(0)
  })

  it('restores a stored bearer session and refreshes models, instances and exact runtime state', async () => {
    storeManagementToken('stored-jwt', false)
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/auth/bootstrap') return { required: false }
      if (path === '/api/v1/auth/providers') return authProviders()
      if (path === '/api/v1/me') return { id: 1, username: 'admin', enabled: true }
      if (path === '/api/v1/auth/ws-ticket') return { ticket: 'ticket-one' }
      return standardRequest(path)
    })
    const manager = useManager()
    await manager.initialize()
    await flushPromises()
    expect(manager.user.value?.username).toBe('admin')
    expect(manager.models.value).toHaveLength(1)
    expect(manager.instances.value[0]?.id).toBe('coder')
    expect(manager.runtimeForInstance(instance()).state).toBe('READY')
    expect(manager.profile.value?.version).toBe('1')
    expect(FakeWebSocket.instances[0]?.url).toBe('ws://manager.test:8888/api/v1/ws?ticket=ticket-one')
  })

  it('clears an invalid stored bearer token and surfaces backend initialization errors', async () => {
    storeManagementToken('expired-jwt', false)
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/auth/bootstrap') return { required: false }
      if (path === '/api/v1/auth/providers') return authProviders()
      if (path === '/api/v1/me') throw new Error('401')
      throw new Error(path)
    })
    const manager = useManager()
    await manager.initialize()
    expect(manager.user.value).toBeNull()
    expect(readManagementToken()).toBe('')

    resetManager()
    mocks.request.mockReset().mockRejectedValue(new Error('connection refused'))
    await manager.initialize()
    expect(manager.backendError.value).toBe('connection refused')
    expect(manager.initialized.value).toBe(true)
  })

  it('bootstraps, logs in with a bearer result, refreshes and connects runtime events', async () => {
    const manager = useManager()
    manager.bootstrapRequired.value = true
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/auth/bootstrap') return {}
      if (path === '/api/v1/auth/login') return {
        access_token: 'login-jwt', token_type: 'Bearer', expires_at: 99,
        user: { id: 1, username: 'admin', enabled: true }
      }
      if (path === '/api/v1/models' || path === '/api/v1/instances') return []
      if (path === '/api/v1/llamacpp/profile') throw new Error('not installed')
      if (path === '/api/v1/auth/ws-ticket') return { ticket: 'login-ticket' }
      throw new Error(`unexpected path ${path}`)
    })
    await manager.authenticate('admin', 'correct-horse-battery')
    await flushPromises()
    expect(manager.bootstrapRequired.value).toBe(false)
    expect(manager.user.value?.username).toBe('admin')
    expect(readManagementToken()).toBe('login-jwt')
    expect(manager.profile.value).toBeNull()
    expect(FakeWebSocket.instances[0]?.url).toContain('ticket=login-ticket')
  })

  it('applies websocket snapshots/events and reconnects with a fresh ticket after disconnect', async () => {
    storeManagementToken('stored-jwt', false)
    let ticket = 0
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/auth/bootstrap') return { required: false }
      if (path === '/api/v1/auth/providers') return authProviders()
      if (path === '/api/v1/me') return { id: 1, username: 'admin', enabled: true }
      if (path === '/api/v1/models') return [model()]
      if (path === '/api/v1/instances') return [instance()]
      if (path === '/api/v1/instances/coder/runtime') return { instance_id: 'coder', model_id: 'm1', state: 'UNLOADED' }
      if (path === '/api/v1/llamacpp/profile') throw new Error('profile unavailable')
      if (path === '/api/v1/auth/ws-ticket') return { ticket: `ticket-${++ticket}` }
      throw new Error(path)
    })
    const manager = useManager()
    await manager.initialize()
    await flushPromises()
    const socket = FakeWebSocket.instances[0]!
    socket.open()
    expect(manager.runtimeEventsConnected.value).toBe(true)
    expect(socket.url).toContain('ticket=ticket-1')

    manager.runtimes.value.m1!.push({ instance_id: 'stale', model_id: 'm1', state: 'READY' })
    socket.emit({ type: 'runtime_snapshot', runtimes: [{ instance_id: 'coder', model_id: 'm1', state: 'UNLOADED' }] })
    expect(manager.runtimes.value.m1).toEqual([{ instance_id: 'coder', model_id: 'm1', state: 'UNLOADED' }])
    mocks.request.mockClear()
    await manager.refresh()
    expect(mocks.request.mock.calls.some(([path]) => String(path).includes('/runtime'))).toBe(false)

    socket.emit({ type: 'runtime', runtime: { instance_id: 'coder', model_id: 'm1', state: 'STARTING', pid: 41 } })
    socket.emit({ type: 'runtime', runtime: { instance_id: 'coder-2', model_id: 'm1', state: 'READY', pid: 42 } })
    expect(manager.runtimes.value.m1).toHaveLength(2)
    expect(manager.modelState(model())).toBe('READY')
    socket.emitRaw('{not json')
    socket.emit({ type: 'other', runtime: { instance_id: 'coder', model_id: 'm1', state: 'FAILED' } })
    expect(manager.runtimes.value.m1?.[0]?.state).toBe('STARTING')

    vi.useFakeTimers()
    socket.disconnect()
    expect(manager.runtimeEventsConnected.value).toBe(false)
    await vi.advanceTimersByTimeAsync(1000)
    await flushPromises()
    expect(FakeWebSocket.instances).toHaveLength(2)
    expect(FakeWebSocket.instances[1]?.url).toContain('ticket=ticket-2')
    manager.disconnectRuntimeEvents()
  })

  it('requires a websocket ticket and handles websocket construction failure', async () => {
    const manager = useManager()
    manager.user.value = { id: 1, username: 'admin', enabled: true }
    storeManagementToken('stored-jwt', false)
    let ticket = 0
    mocks.request.mockImplementation(async (path: string) => path === '/api/v1/auth/ws-ticket' ? { ticket: `ticket-${++ticket}` } : [])

    FakeWebSocket.throwOnCreate = true
    await manager.connectRuntimeEvents()
    expect(FakeWebSocket.instances).toHaveLength(0)
    FakeWebSocket.throwOnCreate = false
    await manager.connectRuntimeEvents()
    const socket = FakeWebSocket.instances[0]!
    socket.open()
    socket.emit({ type: 'runtime_snapshot', runtimes: [{ instance_id: '', model_id: 'm1', state: 'READY' }, { instance_id: 'i1', model_id: '', state: 'READY' }] })
    socket.emit({ type: 'runtime', runtime: { instance_id: '', model_id: 'm1', state: 'READY' } })
    socket.emit({ type: 'runtime', runtime: { instance_id: 'i1', model_id: '', state: 'READY' } })
    expect(manager.runtimes.value).toEqual({})
  })

  it('logs out the bearer session, closes runtime events, clears token and local state', async () => {
    const manager = useManager()
    manager.user.value = { id: 1, username: 'admin', enabled: true }
    manager.models.value = [model()]
    manager.instances.value = [instance()]
    manager.runtimes.value = { m1: [{ instance_id: 'coder', model_id: 'm1', state: 'READY' }] }
    storeManagementToken('stored-jwt', false)
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/auth/ws-ticket') return { ticket: 'logout-ticket' }
      if (path === '/api/v1/auth/logout') return undefined
      return []
    })
    await manager.connectRuntimeEvents()
    const socket = FakeWebSocket.instances[0]!
    socket.open()
    await manager.logout()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/auth/logout', { method: 'POST' })
    expect(socket.closed).toBe(true)
    expect(readManagementToken()).toBe('')
    expect(manager.user.value).toBeNull()
    expect(manager.models.value).toEqual([])
    expect(manager.instances.value).toEqual([])
    expect(manager.runtimes.value).toEqual({})
  })

  it('skips refresh while signed out and falls back when runtime/profile requests fail', async () => {
    const manager = useManager()
    await manager.refresh()
    expect(mocks.request).not.toHaveBeenCalled()
    manager.user.value = { id: 2, username: 'reader', enabled: true }
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/models') return [model()]
      if (path === '/api/v1/instances') return [instance({ id: 'offline' })]
      if (path === '/api/v1/instances/offline/runtime') throw new Error('runtime unavailable')
      if (path === '/api/v1/llamacpp/profile') throw new Error('profile unavailable')
      throw new Error(path)
    })
    await manager.refresh()
    expect(manager.instances.value).toHaveLength(1)
    expect(manager.runtimeForInstance(instance({ id: 'offline' })).state).toBe('UNLOADED')
    expect(manager.profile.value).toBeNull()
  })

  it('selects aggregate model and instance state by priority including STOPPING', () => {
    const manager = useManager()
    const item = model()
    expect(manager.modelState(item)).toBe('UNLOADED')
    manager.runtimes.value.m1 = [{ instance_id: 'coder', model_id: 'm1', state: 'FAILED' }]
    expect(manager.modelState(item)).toBe('FAILED')
    manager.runtimes.value.m1.push({ instance_id: 'drain', model_id: 'm1', state: 'DRAINING' })
    expect(manager.modelState(item)).toBe('DRAINING')
    manager.runtimes.value.m1.push({ instance_id: 'stop', model_id: 'm1', state: 'STOPPING' })
    expect(manager.modelState(item)).toBe('STOPPING')
    manager.runtimes.value.m1.push({ instance_id: 'start', model_id: 'm1', state: 'STARTING' })
    expect(manager.modelState(item)).toBe('STARTING')
    manager.runtimes.value.m1.push({ instance_id: 'ready', model_id: 'm1', state: 'READY' })
    expect(manager.modelState(item)).toBe('READY')
    expect(manager.instanceState(instance())).toBe('FAILED')
  })
})
