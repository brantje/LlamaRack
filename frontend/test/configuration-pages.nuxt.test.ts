import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import NewInstancePage from '~/pages/instances/new.vue'
import EditInstancePage from '~/pages/instances/[id]/edit.vue'
import EditModelPage from '~/pages/models/[id]/edit.vue'
import { useManager, type Instance, type Model } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

function model(overrides: Partial<Model> = {}): Model {
  return { id: 'm1', name: 'Coder Model', gguf_path: 'coder.gguf', total_bytes: 4, context_length: 8192, ...overrides }
}

function instance(overrides: Partial<Instance> = {}): Instance {
  return {
    id: 'primary-coder', model_id: 'm1', name: 'Primary Coder', enabled: true,
    autoload_enabled: true, always_on: false, priority: 'normal', eviction_enabled: true,
    idle_unload_seconds: 0, gpu_mode: 'auto', gpu_devices: [], tensor_split: '', ...overrides
  }
}

function resetManager() {
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.models.value = [model()]
  manager.instances.value = []
  manager.runtimes.value = {}
  manager.profile.value = null
  return manager
}

function components(wrapper: any, name: string, fallback: string) {
  const found = wrapper.findAllComponents({ name })
  return found.length ? found : wrapper.findAllComponents({ name: fallback })
}
function selects(wrapper: any) { return components(wrapper, 'Select', 'USelect') }
function inputs(wrapper: any) { return components(wrapper, 'Input', 'UInput') }
function numbers(wrapper: any) { return components(wrapper, 'InputNumber', 'UInputNumber') }
function textareas(wrapper: any) { return components(wrapper, 'Textarea', 'UTextarea') }
function checkboxes(wrapper: any) { return components(wrapper, 'Checkbox', 'UCheckbox') }

beforeEach(() => {
  mocks.request.mockReset()
  vi.stubGlobal('confirm', vi.fn(() => true))
  resetManager()
})

describe('Instance configuration pages', () => {
  it('creates a fully configured Instance and launches the exact created ID', async () => {
    const manager = resetManager()
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/instances' && options?.method === 'POST') return { id: 'primary-coder' }
      if (path === '/api/v1/instances/primary-coder/start') return { state: 'READY' }
      if (path === '/api/v1/models') return manager.models.value
      if (path === '/api/v1/instances') return []
      if (path === '/api/v1/llamacpp/profile') throw new Error('profile unavailable')
      return {}
    })

    const wrapper = await mountSuspended(NewInstancePage, { route: '/instances/new' })
    const select = selects(wrapper)
    select[0]!.vm.$emit('update:modelValue', 'm1')
    select[1]!.vm.$emit('update:modelValue', 'high')
    select[2]!.vm.$emit('update:modelValue', 'manual')
    await flushPromises()

    const input = inputs(wrapper)
    input[0]!.vm.$emit('update:modelValue', 'Primary Coder')
    input[1]!.vm.$emit('update:modelValue', '0, 1')
    input[2]!.vm.$emit('update:modelValue', '1,1')
    numbers(wrapper)[0]!.vm.$emit('update:modelValue', 90)
    textareas(wrapper)[0]!.vm.$emit('update:modelValue', 'ctx-size=8192\nthreads=4')
    const checks = checkboxes(wrapper)
    checks[1]!.vm.$emit('update:modelValue', true)
    checks[4]!.vm.$emit('update:modelValue', true)
    await flushPromises()

    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(globalThis.confirm).toHaveBeenCalled()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/instances', {
      method: 'POST',
      body: expect.objectContaining({
        model_id: 'm1', name: 'Primary Coder', priority: 'high', always_on: true,
        idle_unload_seconds: 90, gpu_mode: 'manual', gpu_devices: ['0', '1'], tensor_split: '1,1',
        options: { 'ctx-size': '8192', threads: '4' }
      })
    })
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/instances/primary-coder/start', { method: 'POST' })
  })

  it('reports invalid overrides and preserves a created Instance when launch confirmation is cancelled', async () => {
    const manager = resetManager()
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/instances' && options?.method === 'POST') return { id: 'cancelled-launch' }
      if (path === '/api/v1/models') return manager.models.value
      if (path === '/api/v1/instances') return []
      if (path === '/api/v1/llamacpp/profile') throw new Error('profile unavailable')
      return {}
    })
    const wrapper = await mountSuspended(NewInstancePage, { route: '/instances/new' })
    selects(wrapper)[0]!.vm.$emit('update:modelValue', 'm1')
    inputs(wrapper)[0]!.vm.$emit('update:modelValue', 'Cancelled Launch')
    textareas(wrapper)[0]!.vm.$emit('update:modelValue', 'invalid-option')
    await flushPromises()
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(wrapper.text()).toContain('use key=value')
    expect(mocks.request).not.toHaveBeenCalledWith('/api/v1/instances', expect.objectContaining({ method: 'POST' }))

    textareas(wrapper)[0]!.vm.$emit('update:modelValue', 'ctx-size=4096')
    checkboxes(wrapper)[4]!.vm.$emit('update:modelValue', true)
    vi.mocked(globalThis.confirm).mockReturnValue(false)
    await flushPromises()
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/instances', expect.objectContaining({ method: 'POST' }))
    expect(mocks.request).not.toHaveBeenCalledWith('/api/v1/instances/cancelled-launch/start', expect.anything())
  })

  it('loads and applies a confirmed running rename with restart semantics', async () => {
    const manager = resetManager()
    const current = instance({ gpu_mode: 'manual', gpu_devices: ['0', '1'], tensor_split: '1,1' })
    manager.instances.value = [current]
    manager.runtimes.value = { m1: [{ instance_id: current.id, model_id: 'm1', state: 'READY' }] }
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/instances/primary-coder') {
        if (options?.method === 'PUT') return { id: 'renamed-coder' }
        return current
      }
      if (path === '/api/v1/instances/primary-coder/options') return { 'ctx-size': '8192' }
      if (path === '/api/v1/models') return manager.models.value
      if (path === '/api/v1/instances') return [instance({ id: 'renamed-coder', name: 'Renamed Coder' })]
      if (path === '/api/v1/llamacpp/profile') throw new Error('profile unavailable')
      return {}
    })

    const wrapper = await mountSuspended(EditInstancePage, { route: '/instances/primary-coder/edit' })
    await flushPromises()
    expect(wrapper.text()).toContain('Edit Instance')
    expect(wrapper.text()).toContain('ctx-size=8192')
    inputs(wrapper)[0]!.vm.$emit('update:modelValue', 'Renamed Coder')
    textareas(wrapper)[0]!.vm.$emit('update:modelValue', 'ctx-size=16384\nthreads=6')
    await flushPromises()
    expect(wrapper.text()).toContain('API-breaking rename')

    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(globalThis.confirm).toHaveBeenCalledTimes(2)
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/instances/primary-coder', {
      method: 'PUT',
      body: expect.objectContaining({
        name: 'Renamed Coder', gpu_mode: 'manual', gpu_devices: ['0', '1'], tensor_split: '1,1',
        options: { 'ctx-size': '16384', threads: '6' }, restart_running: true, confirm_model_id_change: true
      })
    })
  })

  it('handles edit load failures, cancelled renames and invalid overrides', async () => {
    resetManager()
    mocks.request.mockRejectedValueOnce({ data: { error: 'instance load failed' } }).mockResolvedValueOnce({})
    let wrapper = await mountSuspended(EditInstancePage, { route: '/instances/primary-coder/edit' })
    await flushPromises()
    expect(wrapper.text()).toContain('instance load failed')
    wrapper.unmount()

    const manager = resetManager()
    const current = instance()
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/instances/primary-coder' && !options?.method) return current
      if (path === '/api/v1/instances/primary-coder/options') return {}
      if (path === '/api/v1/models') return manager.models.value
      if (path === '/api/v1/instances') return [current]
      return {}
    })
    wrapper = await mountSuspended(EditInstancePage, { route: '/instances/primary-coder/edit' })
    await flushPromises()
    inputs(wrapper)[0]!.vm.$emit('update:modelValue', 'Breaking Rename')
    vi.mocked(globalThis.confirm).mockReturnValue(false)
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(mocks.request).not.toHaveBeenCalledWith('/api/v1/instances/primary-coder', expect.objectContaining({ method: 'PUT' }))

    inputs(wrapper)[0]!.vm.$emit('update:modelValue', 'Primary Coder')
    textareas(wrapper)[0]!.vm.$emit('update:modelValue', 'not-valid')
    vi.mocked(globalThis.confirm).mockReturnValue(true)
    await flushPromises()
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(wrapper.text()).toContain('use key=value')
  })
})

describe('Model edit page', () => {
  it('loads reusable defaults and saves edited metadata/options', async () => {
    const manager = resetManager()
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/models/m1') {
        if (options?.method === 'PUT') return model({ name: 'Updated Model', context_length: 32768 })
        return model()
      }
      if (path === '/api/v1/models/m1/options') return { 'ctx-size': '8192', threads: '4' }
      if (path === '/api/v1/models') return manager.models.value
      if (path === '/api/v1/instances') return []
      if (path === '/api/v1/llamacpp/profile') throw new Error('profile unavailable')
      return {}
    })
    const wrapper = await mountSuspended(EditModelPage, { route: '/models/m1/edit' })
    await flushPromises()
    expect(wrapper.text()).toContain('ctx-size=8192')
    inputs(wrapper)[0]!.vm.$emit('update:modelValue', 'Updated Model')
    numbers(wrapper)[0]!.vm.$emit('update:modelValue', 32768)
    textareas(wrapper)[0]!.vm.$emit('update:modelValue', 'ctx-size=32768\nthreads=8')
    await flushPromises()
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/models/m1', {
      method: 'PUT',
      body: { name: 'Updated Model', context_length: 32768, options: { 'ctx-size': '32768', threads: '8' } }
    })
  })

  it('renders load, validation and save errors', async () => {
    resetManager()
    mocks.request.mockRejectedValueOnce(new Error('model load failed')).mockResolvedValueOnce({})
    let wrapper = await mountSuspended(EditModelPage, { route: '/models/m1/edit' })
    await flushPromises()
    expect(wrapper.text()).toContain('model load failed')
    wrapper.unmount()

    const manager = resetManager()
    let failSave = false
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/models/m1' && options?.method === 'PUT') {
        if (failSave) throw { data: { error: 'save failed' } }
        return {}
      }
      if (path === '/api/v1/models/m1') return model()
      if (path === '/api/v1/models/m1/options') return {}
      if (path === '/api/v1/models') return manager.models.value
      if (path === '/api/v1/instances') return []
      return {}
    })
    wrapper = await mountSuspended(EditModelPage, { route: '/models/m1/edit' })
    await flushPromises()
    textareas(wrapper)[0]!.vm.$emit('update:modelValue', 'invalid')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(wrapper.text()).toContain('use key=value')

    textareas(wrapper)[0]!.vm.$emit('update:modelValue', 'threads=4')
    failSave = true
    await flushPromises()
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(wrapper.text()).toContain('save failed')
  })
})
