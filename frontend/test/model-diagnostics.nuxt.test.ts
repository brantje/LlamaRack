import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import ModelsPage from '~/pages/models.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

function seedModel() {
  const manager = useManager()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', role: 'admin', enabled: true }
  manager.models.value = [{
    id: 'm1',
    model_id: 'coder',
    display_name: 'Coder',
    artifact_id: 'a1',
    artifact_path: 'coder.gguf',
    enabled: true,
    autoload_enabled: true,
    always_on: false,
    priority: 'normal',
    routing_policy: 'least_active'
  }]
  manager.artifacts.value = []
  manager.runtimes.value = { m1: [{ instance_id: 'instance-12345678', model_id: 'm1', state: 'UNLOADED' }] }
  manager.profile.value = null
  return manager
}

beforeEach(() => {
  mocks.request.mockReset()
  vi.stubGlobal('confirm', vi.fn(() => true))
  seedModel()
})

describe('model diagnostics', () => {
  it('tests a model to READY and displays worker logs', async () => {
    const manager = seedModel()
    let started = false
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/models/m1/start' && options?.method === 'POST') {
        started = true
        return []
      }
      if (path === '/api/v1/models') return manager.models.value
      if (path === '/api/v1/artifacts') return []
      if (path === '/api/v1/models/m1/runtime') {
        return [{
          instance_id: 'instance-12345678',
          model_id: 'm1',
          state: started ? 'READY' : 'UNLOADED',
          pid: started ? 4242 : undefined,
          port: started ? 31000 : undefined
        }]
      }
      if (path === '/api/v1/instances/instance-12345678/logs') {
        return { lines: ['[stderr] model loaded', '[stdout] server ready'] }
      }
      if (path === '/api/v1/llamacpp/profile') throw new Error('profile unavailable')
      return []
    })

    const wrapper = await mountSuspended(ModelsPage, { route: false })
    const testButton = wrapper.findAll('button').find(button => button.text() === 'Test')
    expect(testButton).toBeTruthy()
    await testButton!.trigger('click')
    await flushPromises()

    expect(mocks.request).toHaveBeenCalledWith('/api/v1/models/m1/start', { method: 'POST' })
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/instances/instance-12345678/logs')
    expect(wrapper.text()).toContain('PASS · READY · PID 4242 · port 31000')
    expect(wrapper.text()).toContain('Worker logs')
    expect(wrapper.text()).toContain('model loaded')
    expect(wrapper.text()).toContain('server ready')
    expect(wrapper.findAll('button').find(button => button.text() === 'Start')?.attributes('disabled')).toBeDefined()
  })

  it('shows test failures and supports manually loading empty logs', async () => {
    const manager = seedModel()
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/models/m1/start' && options?.method === 'POST') throw { data: { error: 'worker exploded' } }
      if (path === '/api/v1/models') return manager.models.value
      if (path === '/api/v1/artifacts') return []
      if (path === '/api/v1/models/m1/runtime') return [{ instance_id: 'instance-12345678', model_id: 'm1', state: 'FAILED', last_error: 'worker exploded' }]
      if (path === '/api/v1/instances/instance-12345678/logs') return { lines: [] }
      if (path === '/api/v1/llamacpp/profile') throw new Error('profile unavailable')
      return []
    })

    const wrapper = await mountSuspended(ModelsPage, { route: false })
    await wrapper.findAll('button').find(button => button.text() === 'Test')!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('FAIL · worker exploded')
    expect(wrapper.text()).toContain('No worker output captured yet.')

    await wrapper.findAll('button').find(button => button.text() === 'Logs')!.trigger('click')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/instances/instance-12345678/logs')
  })
})
