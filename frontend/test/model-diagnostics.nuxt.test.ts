import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import InstancesPage from '~/pages/instances/index.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

function seed() {
  const manager = useManager()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.models.value = [{ id: 'm1', name: 'Coder model', gguf_path: 'coder.gguf', total_bytes: 4, quantization: 'Q4_K_M', context_length: 8192 }]
  manager.instances.value = [{ id: 'coder-primary', model_id: 'm1', name: 'Coder Primary', enabled: true, autoload_enabled: true, always_on: false, priority: 'normal', eviction_enabled: true, idle_unload_seconds: 0, gpu_mode: 'auto', gpu_devices: [] }]
  manager.runtimes.value = { m1: [{ instance_id: 'coder-primary', model_id: 'm1', state: 'UNLOADED' }] }
  return manager
}

beforeEach(() => {
  mocks.request.mockReset()
  vi.stubGlobal('confirm', vi.fn(() => true))
  seed()
})

describe('instance control plane', () => {
  it('renders stopped instances as addressable cards with lifecycle actions', async () => {
    const wrapper = await mountSuspended(InstancesPage, { route: false })
    expect(wrapper.findAll('[data-testid="instance-card"]')).toHaveLength(1)
    expect(wrapper.text()).toContain('Coder Primary')
    expect(wrapper.text()).toContain('model=coder-primary')
    expect(wrapper.text()).toContain('UNLOADED')
    expect(wrapper.text()).toContain('Launch')
    expect(wrapper.text()).toContain('Duplicate')
    expect(wrapper.text()).toContain('Logs')
  })

  it('launches, stops, restarts, duplicates and kills the exact instance', async () => {
    const manager = seed()
    let runtimeState = 'UNLOADED'
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/instances/coder-primary/start' && options?.method === 'POST') runtimeState = 'READY'
      if (path === '/api/v1/instances/coder-primary/stop' && options?.method === 'POST') runtimeState = 'UNLOADED'
      if (path === '/api/v1/instances') return manager.instances.value
      if (path === '/api/v1/models') return manager.models.value
      if (path === '/api/v1/instances/coder-primary/runtime') return { instance_id: 'coder-primary', model_id: 'm1', state: runtimeState }
      if (path === '/api/v1/llamacpp/profile') throw new Error('profile unavailable')
      return {}
    })
    const wrapper = await mountSuspended(InstancesPage, { route: false })
    await wrapper.findAll('button').find(button => button.text() === 'Launch')!.trigger('click')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/instances/coder-primary/start', { method: 'POST' })
    for (const label of ['Restart', 'Duplicate', 'Kill']) {
      await wrapper.findAll('button').find(button => button.text() === label)!.trigger('click')
      await flushPromises()
    }
    const stop = wrapper.findAll('button').find(button => button.text() === 'Stop')
    if (stop) { await stop.trigger('click'); await flushPromises() }
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/instances/coder-primary/restart', { method: 'POST' })
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/instances/coder-primary/duplicate', { method: 'POST' })
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/instances/coder-primary/kill', { method: 'POST' })
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/instances/coder-primary/stop', { method: 'POST' })
  })

  it('shows logs and deletes without deleting the registered model', async () => {
    const manager = seed()
    mocks.request.mockImplementation(async (path: string) => {
      if (path.endsWith('/logs')) return { lines: ['worker ready', 'request complete'] }
      if (path === '/api/v1/instances') return manager.instances.value
      if (path === '/api/v1/models') return manager.models.value
      if (path === '/api/v1/instances/coder-primary/runtime') return { instance_id: 'coder-primary', model_id: 'm1', state: 'UNLOADED' }
      return {}
    })
    const wrapper = await mountSuspended(InstancesPage, { route: false })
    await wrapper.findAll('button').find(button => button.text() === 'Logs')!.trigger('click')
    await flushPromises()
    expect(document.body.textContent).toContain('worker ready')
    await wrapper.findAll('button').find(button => button.text() === 'Delete')!.trigger('click')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/instances/coder-primary', { method: 'DELETE' })
    expect(mocks.request).not.toHaveBeenCalledWith('/api/v1/models/m1', expect.anything())
  })

  it('keeps instances visible on action errors and supports cancelled destructive actions', async () => {
    mocks.request.mockRejectedValue({ data: { error: 'worker failed' } })
    const confirm = vi.mocked(globalThis.confirm)
    confirm.mockReturnValueOnce(false)
    const wrapper = await mountSuspended(InstancesPage, { route: false })
    await wrapper.findAll('button').find(button => button.text() === 'Launch')!.trigger('click')
    expect(mocks.request).not.toHaveBeenCalled()
    confirm.mockReturnValue(true)
    await wrapper.findAll('button').find(button => button.text() === 'Restart')!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('worker failed')
    expect(wrapper.findAll('[data-testid="instance-card"]')).toHaveLength(1)
  })

  it('renders the empty state', async () => {
    const manager = seed()
    manager.instances.value = []
    const wrapper = await mountSuspended(InstancesPage, { route: false })
    expect(wrapper.text()).toContain('No Instances configured')
    expect(wrapper.text()).toContain('New Instance')
  })
})
