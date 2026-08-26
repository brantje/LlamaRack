import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import NewModelPage from '~/pages/models/new.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

beforeEach(() => {
  mocks.request.mockReset()
  const manager = useManager()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', role: 'admin', enabled: true }
  manager.models.value = []
  manager.runtimes.value = {}
  manager.profile.value = null
})

describe('model creation form', () => {
  it('autofills a safe public ID and allows overriding model settings', async () => {
    const wrapper = await mountSuspended(NewModelPage, { route: '/models/new' })
    const inputs = wrapper.findAll('input')
    await inputs[1]!.setValue('Qwén Coder / 32B!')
    await flushPromises()
    expect((inputs[2]!.element as HTMLInputElement).value).toBe('qwen-coder-32b')

    await inputs[2]!.setValue('custom-id')
    await inputs[1]!.setValue('Changed name')
    await flushPromises()
    expect((inputs[2]!.element as HTMLInputElement).value).toBe('custom-id')

    const selects = wrapper.findAll('select')
    await selects[0]!.setValue('high')
    await selects[1]!.setValue('round_robin')
    const checkboxes = wrapper.findAll('input[type="checkbox"]')
    await checkboxes[0]!.setValue(false)
    await checkboxes[1]!.setValue(true)
    expect((selects[0]!.element as HTMLSelectElement).value).toBe('high')
    expect((selects[1]!.element as HTMLSelectElement).value).toBe('round_robin')
    expect((checkboxes[0]!.element as HTMLInputElement).checked).toBe(false)
    expect((checkboxes[1]!.element as HTMLInputElement).checked).toBe(true)
  })

  it('creates a model in one request and handles errors', async () => {
    const manager = useManager()
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/models' && options?.method === 'POST') return { id: 'm1' }
      if (path === '/api/v1/models') return []
      if (path === '/api/v1/llamacpp/profile') throw new Error('no profile')
      return []
    })
    let wrapper = await mountSuspended(NewModelPage, { route: '/models/new' })
    const inputs = wrapper.findAll('input')
    await inputs[0]!.setValue('/models/qwen.gguf')
    await inputs[1]!.setValue('Qwen Coder')
    await flushPromises()
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/models', {
      method: 'POST',
      body: expect.objectContaining({ gguf_path: '/models/qwen.gguf', name: 'Qwen Coder', model_id: 'qwen-coder' })
    })
    expect(manager.models.value).toEqual([])
    wrapper.unmount()

    mocks.request.mockReset()
    mocks.request.mockRejectedValueOnce(new Error('GGUF missing'))
    wrapper = await mountSuspended(NewModelPage, { route: '/models/new' })
    const errorInputs = wrapper.findAll('input')
    await errorInputs[0]!.setValue('/models/missing.gguf')
    await errorInputs[1]!.setValue('Missing')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(wrapper.text()).toContain('GGUF missing')
  })

  it('hides creation controls from readonly users', async () => {
    const manager = useManager()
    manager.user.value = { id: 3, username: 'viewer', role: 'readonly', enabled: true }
    const wrapper = await mountSuspended(NewModelPage, { route: '/models/new' })
    expect(wrapper.find('form').exists()).toBe(false)
    expect(wrapper.text()).toContain('cannot create models')
  })
})
