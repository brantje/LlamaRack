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
  manager.user.value = { id: 1, username: 'admin', role: 'admin', enabled: true }
  manager.models.value = []
  manager.runtimes.value = {}
  manager.profile.value = null
})

function selectComponents(wrapper: any) {
  const components = wrapper.findAllComponents({ name: 'Select' })
  return components.length ? components : wrapper.findAllComponents({ name: 'USelect' })
}

function checkboxComponents(wrapper: any) {
  const components = wrapper.findAllComponents({ name: 'Checkbox' })
  return components.length ? components : wrapper.findAllComponents({ name: 'UCheckbox' })
}

describe('model creation form', () => {
  it('lists relative GGUF paths and autofills a safe public ID', async () => {
    const wrapper = await mountSuspended(NewModelPage, { route: '/models/new' })
    await flushPromises()

    expect(mocks.request).toHaveBeenCalledWith('/api/v1/models/available')
    const selects = selectComponents(wrapper)
    expect(selects[0]!.props('items')).toEqual(expect.arrayContaining([
      expect.objectContaining({ label: 'Qwen/coder/qwen-Q4_K_M.gguf · Q4_K_M', value: 'Qwen/coder/qwen-Q4_K_M.gguf' })
    ]))
    expect(JSON.stringify(selects[0]!.props('items'))).not.toContain('/models/')
    selects[0]!.vm.$emit('update:modelValue', 'Qwen/coder/qwen-Q4_K_M.gguf')
    await flushPromises()

    const nameInput = wrapper.get('[data-testid="model-name"]')
    const idInput = wrapper.get('[data-testid="model-id"]')
    await nameInput.setValue('Qwén Coder / 32B!')
    await flushPromises()
    expect((idInput.element as HTMLInputElement).value).toBe('qwen-coder-32b')

    await idInput.setValue('custom-id')
    await nameInput.setValue('Changed name')
    await flushPromises()
    expect((idInput.element as HTMLInputElement).value).toBe('custom-id')

    selects[1]!.vm.$emit('update:modelValue', 'high')
    selects[2]!.vm.$emit('update:modelValue', 'round_robin')
    const checkboxes = checkboxComponents(wrapper)
    checkboxes[0]!.vm.$emit('update:modelValue', false)
    checkboxes[1]!.vm.$emit('update:modelValue', true)
    await flushPromises()
    expect(selects[1]!.props('modelValue')).toBe('high')
    expect(selects[2]!.props('modelValue')).toBe('round_robin')
    expect(checkboxes[0]!.props('modelValue')).toBe(false)
    expect(checkboxes[1]!.props('modelValue')).toBe(true)
  })

  it('creates a model with the selected relative GGUF path and handles errors', async () => {
    const manager = useManager()
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/models/available') return discovered
      if (path === '/api/v1/models' && options?.method === 'POST') return { id: 'm1' }
      if (path === '/api/v1/models') return []
      if (path === '/api/v1/llamacpp/profile') throw new Error('no profile')
      return []
    })
    let wrapper = await mountSuspended(NewModelPage, { route: '/models/new' })
    await flushPromises()
    const selects = selectComponents(wrapper)
    selects[0]!.vm.$emit('update:modelValue', 'Qwen/coder/qwen-Q4_K_M.gguf')
    await flushPromises()
    await wrapper.get('[data-testid="model-name"]').setValue('Qwen Coder')
    await flushPromises()
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/models', {
      method: 'POST',
      body: expect.objectContaining({ gguf_path: 'Qwen/coder/qwen-Q4_K_M.gguf', name: 'Qwen Coder', model_id: 'qwen-coder' })
    })
    expect(manager.models.value).toEqual([])
    wrapper.unmount()

    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/models/available') return discovered
      if (path === '/api/v1/models' && options?.method === 'POST') throw new Error('GGUF missing')
      return []
    })
    wrapper = await mountSuspended(NewModelPage, { route: '/models/new' })
    await flushPromises()
    selectComponents(wrapper)[0]!.vm.$emit('update:modelValue', 'deep/other.gguf')
    await flushPromises()
    await wrapper.get('[data-testid="model-name"]').setValue('Missing')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(wrapper.text()).toContain('GGUF missing')
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

  it('hides creation controls from readonly users', async () => {
    const manager = useManager()
    manager.user.value = { id: 3, username: 'viewer', role: 'readonly', enabled: true }
    const wrapper = await mountSuspended(NewModelPage, { route: '/models/new' })
    await flushPromises()
    expect(wrapper.find('form').exists()).toBe(false)
    expect(wrapper.text()).toContain('cannot create models')
    expect(mocks.request).not.toHaveBeenCalledWith('/api/v1/models/available')
  })
})
