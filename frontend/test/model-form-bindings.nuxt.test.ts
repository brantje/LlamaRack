import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import NewModelPage from '~/pages/models/new.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

const discovered = [
  { path: 'Qwen/coder/qwen-Q4_K_M.gguf', name: 'qwen-Q4_K_M.gguf', total_bytes: 1234, quantization: 'Q4_K_M' },
  { path: 'deep/other.gguf', name: 'other.gguf', total_bytes: 5678 }
]

beforeEach(() => {
  mocks.request.mockReset()
  mocks.request.mockImplementation(async (path: string) => path === '/api/v1/models/available' ? discovered : [])
  const manager = useManager()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.models.value = []
  manager.instances.value = []
  manager.runtimes.value = {}
  manager.profile.value = null
})

function selectComponents(wrapper: any) {
  const components = wrapper.findAllComponents({ name: 'Select' })
  return components.length ? components : wrapper.findAllComponents({ name: 'USelect' })
}

describe('model creation form', () => {
  it('lists relative GGUF paths and only exposes the small first-instance bootstrap set', async () => {
    const wrapper = await mountSuspended(NewModelPage, { route: '/models/new' })
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/models/available')
    expect(wrapper.text()).toContain('Create a first Instance')
    expect(wrapper.text()).toContain('Always on')
    expect(wrapper.text()).toContain('Autoload on request')
    expect(wrapper.text()).toContain('Allow resource-pressure eviction')
    expect(wrapper.text()).toContain('Launch this Instance after creation')
    expect(wrapper.text()).not.toContain('Priority')
    expect(wrapper.text()).not.toContain('GPU placement')

    const select = selectComponents(wrapper)[0]!
    expect(select.props('items')).toEqual(expect.arrayContaining([
      expect.objectContaining({ label: 'Qwen/coder/qwen-Q4_K_M.gguf · Q4_K_M', value: 'Qwen/coder/qwen-Q4_K_M.gguf' })
    ]))
    expect(JSON.stringify(select.props('items'))).not.toContain('/models/')
  })

  it('creates a registry model with the optional first instance payload', async () => {
    const manager = useManager()
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/models/available') return discovered
      if (path === '/api/v1/models' && options?.method === 'POST') return { model: { id: 'm1' }, instance: { id: 'qwen-coder' } }
      if (path === '/api/v1/models' || path === '/api/v1/instances') return []
      if (path === '/api/v1/llamacpp/profile') throw new Error('no profile')
      return []
    })
    const wrapper = await mountSuspended(NewModelPage, { route: '/models/new' })
    await flushPromises()
    selectComponents(wrapper)[0]!.vm.$emit('update:modelValue', 'Qwen/coder/qwen-Q4_K_M.gguf')
    await wrapper.get('[data-testid="model-name"]').setValue('Qwen Coder')
    await flushPromises()
    expect((wrapper.get('[data-testid="instance-name"]').element as HTMLInputElement).value).toBe('Qwen Coder')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/models', {
      method: 'POST',
      body: {
        gguf_path: 'Qwen/coder/qwen-Q4_K_M.gguf',
        name: 'Qwen Coder',
        context_length: 0,
        first_instance: {
          name: 'Qwen Coder', always_on: false, autoload_enabled: true, eviction_enabled: true, start: false
        }
      }
    })
    expect(manager.models.value).toEqual([])
  })

  it('preserves durable records when launch fails and supports registry-only creation', async () => {
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/models/available') return discovered
      if (path === '/api/v1/models' && options?.method === 'POST') return { model: { id: 'm1' }, instance: { id: 'coder' }, start_error: 'worker exploded' }
      if (path === '/api/v1/models' || path === '/api/v1/instances') return []
      return []
    })
    const wrapper = await mountSuspended(NewModelPage, { route: '/models/new' })
    await flushPromises()
    selectComponents(wrapper)[0]!.vm.$emit('update:modelValue', 'deep/other.gguf')
    await wrapper.get('[data-testid="model-name"]').setValue('Coder')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(wrapper.text()).toContain('Model and Instance were created')
    expect(wrapper.text()).toContain('worker exploded')

    const toggle = wrapper.getComponent('[data-testid="create-first-instance"]')
    toggle.vm.$emit('update:modelValue', false)
    await flushPromises()
    mocks.request.mockClear()
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/models', expect.objectContaining({
      method: 'POST', body: expect.objectContaining({ first_instance: undefined })
    }))
  })

  it('shows scan failures and can rescan the model folder', async () => {
    mocks.request.mockRejectedValueOnce(new Error('scan failed'))
    const wrapper = await mountSuspended(NewModelPage, { route: '/models/new' })
    await flushPromises()
    expect(wrapper.text()).toContain('scan failed')
    expect(wrapper.text()).toContain('No unregistered GGUF files found')
    mocks.request.mockResolvedValueOnce(discovered)
    await wrapper.findAll('button').find(button => button.text().includes('Rescan'))!.trigger('click')
    await flushPromises()
    expect(JSON.stringify(selectComponents(wrapper)[0]!.props('items'))).toContain('deep/other.gguf')
  })
})
