import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mockNuxtImport } from '@nuxt/test-utils/runtime'
import { useManager, type Instance, type Model } from '~/composables/useManager'
import { clearManagementToken } from '~/composables/useManagerApi'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

class BranchWebSocket {
  static instances: BranchWebSocket[] = []
  static throwOnCreate = false
  url: string
  onopen: ((event: Event) => void) | null = null
  onmessage: ((event: MessageEvent) => void) | null = null
  onclose: ((event: CloseEvent) => void) | null = null

  constructor(url: string) {
    if (BranchWebSocket.throwOnCreate) throw new Error('websocket construction failed')
    this.url = url
    BranchWebSocket.instances.push(this)
  }

  emit(payload: unknown) { this.onmessage?.({ data: JSON.stringify(payload) } as MessageEvent) }
  disconnect() { this.onclose?.({} as CloseEvent) }
  close() {}
}

function model(): Model {
  return { id: 'm', name: 'Model', gguf_path: 'model.gguf', total_bytes: 1, context_length: 4096 }
}

function instance(id: string): Instance {
  return {
    id,
    model_id: 'm',
    name: id,
    enabled: true,
    autoload_enabled: true,
    always_on: false,
    priority: 'normal',
    eviction_enabled: true,
    idle_unload_seconds: 0,
    gpu_mode: 'auto'
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
  manager.runtimeTelemetry.value = {}
  manager.observabilityLive.value = null
  manager.runtimeEventsConnected.value = false
  return manager
}

describe('useManager authentication edge branches', () => {
  beforeEach(() => {
    vi.useRealTimers()
    mocks.request.mockReset()
    BranchWebSocket.instances = []
    BranchWebSocket.throwOnCreate = false
    vi.stubGlobal('WebSocket', BranchWebSocket as any)
    clearManagementToken()
    resetManager()
  })

  it('covers provider fallback and websocket ticket guard exits', async () => {
    const manager = useManager()

    await manager.connectRuntimeEvents()
    expect(mocks.request).not.toHaveBeenCalled()

    mocks.request.mockResolvedValueOnce({ local_login_enabled: false })
    await manager.refreshAuthProviders()
    expect(manager.localLoginEnabled.value).toBe(false)
    expect(manager.authProviders.value).toEqual([])

    manager.user.value = { id: 1, username: 'admin', enabled: true }
    mocks.request.mockRejectedValueOnce(new Error('ticket denied'))
    await manager.connectRuntimeEvents()
    expect(BranchWebSocket.instances).toHaveLength(0)

    mocks.request.mockResolvedValueOnce({ ticket: '' })
    await manager.connectRuntimeEvents()
    expect(BranchWebSocket.instances).toHaveLength(0)

    mocks.request.mockImplementationOnce(async () => {
      manager.user.value = null
      return { ticket: 'orphaned-ticket' }
    })
    manager.user.value = { id: 1, username: 'admin', enabled: true }
    await manager.connectRuntimeEvents()
    expect(BranchWebSocket.instances).toHaveLength(0)

    manager.user.value = { id: 1, username: 'admin', enabled: true }
    BranchWebSocket.throwOnCreate = true
    mocks.request.mockResolvedValueOnce({ ticket: 'bad-socket-ticket' })
    await manager.connectRuntimeEvents()
    expect(BranchWebSocket.instances).toHaveLength(0)
  })

  it('filters invalid telemetry, supplies observability defaults and avoids signed-out reconnects', async () => {
    const manager = useManager()
    manager.user.value = { id: 1, username: 'admin', enabled: true }
    manager.models.value = [model()]
    manager.instances.value = [instance('valid'), instance('inactive'), instance('wrong-pid')]
    manager.runtimes.value = {
      m: [
        { instance_id: 'valid', model_id: 'm', state: 'READY', pid: 7 },
        { instance_id: 'inactive', model_id: 'm', state: 'FAILED', pid: 8 },
        { instance_id: 'wrong-pid', model_id: 'm', state: 'READY', pid: 9 }
      ]
    }
    mocks.request.mockResolvedValue({ ticket: 'telemetry-ticket' })

    await manager.connectRuntimeEvents()
    const socket = BranchWebSocket.instances[0]!

    socket.emit({
      type: 'runtime_telemetry',
      telemetry: [
        { instance_id: '', pid: 7 },
        { instance_id: 'valid', pid: Number.NaN },
        { instance_id: 'valid', pid: 0 },
        { instance_id: 'missing', pid: 7 },
        { instance_id: 'inactive', pid: 8 },
        { instance_id: 'wrong-pid', pid: 10 },
        { instance_id: 'valid', pid: 7, gpu_devices: [], gpus: [], collected_at: 'now' }
      ]
    })
    expect(manager.runtimeTelemetry.value.valid?.pid).toBe(7)
    expect(Object.keys(manager.runtimeTelemetry.value)).toEqual(['valid'])

    socket.emit({ type: 'observability', collected_at: 'ignored-without-hardware' })
    expect(manager.observabilityLive.value).toBeNull()

    socket.emit({
      type: 'observability',
      collected_at: '2026-08-28T21:00:00Z',
      hardware: { ram_total_bytes: 1, ram_available_bytes: 1, gpus: [], processes: [], collected_at: 'now' }
    })
    expect(manager.observabilityLive.value?.gateway.requests).toBe(0)
    expect(manager.observabilityLive.value?.telemetry).toEqual([])
    expect(manager.observabilityLive.value?.requests).toEqual([])

    manager.user.value = null
    socket.disconnect()
    expect(manager.runtimeEventsConnected.value).toBe(false)
    expect(BranchWebSocket.instances).toHaveLength(1)
  })
})
