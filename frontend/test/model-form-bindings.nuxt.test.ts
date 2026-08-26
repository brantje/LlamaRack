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

function inputNumberComponents(wrapper: any) {
  const components = wrapper.findAllComponents({ name: 'InputNumber' })
  return components.length ? components : wrapper.findAllComponents({ name: 'UInputNumber' })
}

describe('model creation form', () => {
  it('lists relative GGUF paths and exposes lifecycle and eviction settings', async () => {
    const wrapper = await mountSuspended(NewModelPage, { route: '/models/new' })
    await flushPromises()

    expect(mocks.request).toHaveBeenCalledWith('/api/v1/models/available')
    expect(wrapper.text()).toContain('Eviction')
    expect(wrapper.text()).toContain('Idle unload timeout')
    expect(wrapper.text()).toContain('Allow resource-pressure eviction')

    const selects = selectComponents(wrapper)
    expect(selects).toHaveLength(3)
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

    selects[1]!.vm.$emit('update:modelValue', 'round_robin')
    selects[2]!.vm.$emit('update:modelValue', 'high')
    const numberInputs = inputNumberComponents(wrapper)
    expect(numberInputs).toHaveLength(1)
    numberInputs[0]!.vm.$emit('update:modelValue', 120)
    const checkboxes = checkboxComponents(wrapper)
    expect(checkboxes).toHaveLength(3)
    checkboxes[0]!.vm.$emit('update:modelValue', false)
    checkboxes[1]!.vm.$emit('update:modelValue', false)
    checkboxes[2]!.vm.$emit('update:modelValue', true)
    await flushPromises()

    expect(selects[1]!.props('modelValue')).toBe('round_robin')
    expect(selects[2]!.props('modelValue')).toBe('high')
    expect(numberInputs[0]!.props('modelValue')).toBe(120)
    expect(checkboxes[0]!.props('modelValue')).toBe(false)
    expect(checkboxes[1]!.props('modelValue')).toBe(false)
    expect(checkboxes[2]!.props('modelValue')).toBe(true)
  })

  it('creates a model with eviction options and the selected relative GGUF path', async () => {
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
    selects[2]!.vm.$emit('update:modelValue', 'low')
    inputNumberComponents(wrapper)[0]!.vm.$emit('update:modelValue', 45)
    checkboxComponents(wrapper)[0]!.vm.$emit('update:modelValue', false)
    await flushPromises()
    await wrapper.get('[data-testid="model-name"]').setValue('Qwen Coder')
    await flushPromises()
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/models', {
      method: 'POST',
      body: expect.objectContaining({
        gguf_path: 'Qwen/coder/qwen-Q4_K_M.gguf',
        name: 'Qwen Coder',
        model_id: 'qwen-coder',
        priority: 'low',
        eviction_enabled: false,
        idle_unload_seconds: 45
      })
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
