import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import ModelsPage from '~/pages/models.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

class FakeEventSource {
  static instances: FakeEventSource[] = []
  static throwOnCreate = false
  url: string
  withCredentials: boolean
  closed = false
  onmessage: ((event: MessageEvent) => void) | null = null

  constructor(url: string, init?: EventSourceInit) {
    if (FakeEventSource.throwOnCreate) throw new Error('stream unavailable')
    this.url = url
    this.withCredentials = !!init?.withCredentials
    FakeEventSource.instances.push(this)
  }

  emit(line: string) {
    this.onmessage?.({ data: JSON.stringify(line) } as MessageEvent)
  }

  close() {
    this.closed = true
  }
}

function seedModel() {
  const manager = useManager()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', role: 'admin', enabled: true }
  manager.models.value = [{
    id: 'm1', model_id: 'coder', name: 'Coder', gguf_path: 'coder.gguf', total_bytes: 4,
    enabled: true, autoload_enabled: true, always_on: false, priority: 'normal', routing_policy: 'least_active'
  }]
  manager.runtimes.value = { m1: [{ instance_id: 'instance-12345678', model_id: 'm1', state: 'UNLOADED' }] }
  manager.profile.value = null
  return manager
}

beforeEach(() => {
  mocks.request.mockReset()
  FakeEventSource.instances = []
  FakeEventSource.throwOnCreate = false
  vi.stubGlobal('EventSource', FakeEventSource as any)
  vi.stubGlobal('confirm', vi.fn(() => true))
  seedModel()
})

describe('model diagnostics', () => {
  it('tests a model to READY and live streams worker logs', async () => {
    const manager = seedModel()
    let started = false
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/models/m1/start' && options?.method === 'POST') { started = true; return [] }
      if (path === '/api/v1/models') return manager.models.value
      if (path === '/api/v1/models/m1/runtime') return [{ instance_id: 'instance-12345678', model_id: 'm1', state: started ? 'READY' : 'UNLOADED', pid: started ? 4242 : undefined, port: started ? 31000 : undefined }]
      if (path === '/api/v1/llamacpp/profile') throw new Error('profile unavailable')
      return []
    })

    const wrapper = await mountSuspended(ModelsPage, { route: false })
    await wrapper.findAll('button').find(button => button.text() === 'Test')!.trigger('click')
    await flushPromises()

    expect(mocks.request).toHaveBeenCalledWith('/api/v1/models/m1/start', { method: 'POST' })
    expect(FakeEventSource.instances).toHaveLength(1)
    const source = FakeEventSource.instances[0]!
    expect(source.url).toBe('http://manager.test:8888/api/v1/instances/instance-12345678/logs/stream')
    expect(source.withCredentials).toBe(true)
    source.emit('[stderr] model loaded')
    source.emit('[stdout] server ready')
    await flushPromises()

    expect(wrapper.text()).toContain('PASS · READY · PID 4242 · port 31000')
    expect(wrapper.text()).toContain('LIVE · 2 lines')
    expect(wrapper.text()).toContain('model loaded')
    expect(wrapper.findAll('button').find(button => button.text() === 'Start')?.attributes('disabled')).toBeDefined()
    wrapper.unmount()
    expect(source.closed).toBe(true)
  })

  it('shows test failures and reuses the existing live stream', async () => {
    const manager = seedModel()
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/models/m1/start' && options?.method === 'POST') throw { data: { error: 'worker exploded' } }
      if (path === '/api/v1/models') return manager.models.value
      if (path === '/api/v1/models/m1/runtime') return [{ instance_id: 'instance-12345678', model_id: 'm1', state: 'FAILED', last_error: 'worker exploded' }]
      if (path === '/api/v1/llamacpp/profile') throw new Error('profile unavailable')
      return []
    })
    const wrapper = await mountSuspended(ModelsPage, { route: false })
    await wrapper.findAll('button').find(button => button.text() === 'Test')!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('FAIL · worker exploded')
    expect(FakeEventSource.instances).toHaveLength(1)
    await wrapper.findAll('button').find(button => button.text().startsWith('Logs'))!.trigger('click')
    await flushPromises()
    expect(FakeEventSource.instances).toHaveLength(1)
  })

  it('runs direct start and stop actions while keeping logs live', async () => {
    const manager = seedModel()
    let state = 'UNLOADED'
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/models/m1/start' && options?.method === 'POST') { state = 'READY'; return [] }
      if (path === '/api/v1/models/m1/stop' && options?.method === 'POST') { state = 'UNLOADED'; return undefined }
      if (path === '/api/v1/models') return manager.models.value
      if (path === '/api/v1/models/m1/runtime') return [{ instance_id: 'instance-12345678', model_id: 'm1', state, pid: state === 'READY' ? 99 : undefined, port: state === 'READY' ? 32000 : undefined }]
      if (path === '/api/v1/llamacpp/profile') throw new Error('profile unavailable')
      return []
    })
    const wrapper = await mountSuspended(ModelsPage, { route: false })
    await wrapper.findAll('button').find(button => button.text() === 'Start')!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('READY')
    await wrapper.findAll('button').find(button => button.text() === 'Stop')!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('UNLOADED')
    expect(FakeEventSource.instances).toHaveLength(1)
  })

  it('handles models without runtimes and live stream construction failures', async () => {
    const manager = seedModel()
    let returnRuntime = false
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/models') return manager.models.value
      if (path === '/api/v1/models/m1/runtime') return returnRuntime ? [{ instance_id: 'instance-12345678', model_id: 'm1', state: 'UNLOADED' }] : []
      if (path === '/api/v1/llamacpp/profile') throw new Error('profile unavailable')
      return []
    })
    const wrapper = await mountSuspended(ModelsPage, { route: false })
    await wrapper.findAll('button').find(button => button.text() === 'Logs')!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Waiting for worker output…')
    returnRuntime = true
    FakeEventSource.throwOnCreate = true
    await wrapper.findAll('button').find(button => button.text() === 'Logs')!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('stream unavailable')
  })
})
