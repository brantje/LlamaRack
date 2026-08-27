import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import NewInstancePage from '~/pages/instances/new.vue'
import EditInstancePage from '~/pages/instances/[id]/edit.vue'
import EditModelPage from '~/pages/models/[id]/edit.vue'
import LlamaCppOptionsEditor from '~/components/LlamaCppOptionsEditor.vue'
import HardwarePlacementEditor from '~/components/HardwarePlacementEditor.vue'
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
  manager.profile.value = {
    path: '/app/llama-server', version: 'test', fingerprint: 'abc', options: [
      { key: 'ctx-size', value_hint: 'N', kind: 'integer', description: 'Context size' },
      { key: 'threads', value_hint: 'N', kind: 'integer', description: 'CPU threads' },
      { key: 'flash-attn', kind: 'boolean', description: 'Flash attention' }
    ]
  }
  return manager
}

function phase7Response(path: string) {
  if (path === '/api/v1/hardware') {
    return {
      ram_total_bytes: 64 * 1024 ** 3,
      ram_available_bytes: 48 * 1024 ** 3,
      collected_at: '2026-08-27T00:00:00Z',
      gpus: [
        { id: 'CUDA0', backend: 'cuda', index: 0, name: 'GPU 0', total_bytes: 24 * 1024 ** 3, used_bytes: 2 * 1024 ** 3, free_bytes: 22 * 1024 ** 3, utilization_pct: 10 },
        { id: 'CUDA1', backend: 'cuda', index: 1, name: 'GPU 1', total_bytes: 24 * 1024 ** 3, used_bytes: 4 * 1024 ** 3, free_bytes: 20 * 1024 ** 3, utilization_pct: 20 }
      ]
    }
  }
  if (path.startsWith('/api/v1/llamacpp/config')) {
    return {
      profile: useManager().profile.value,
      effective: { global: {}, model: {}, instance: {}, values: {}, sources: {} },
      unsupported: []
    }
  }
  return undefined
}

function components(wrapper: any, name: string, fallback: string) {
  const found = wrapper.findAllComponents({ name })
  return found.length ? found : wrapper.findAllComponents({ name: fallback })
}
function selects(wrapper: any) { return components(wrapper, 'SelectMenu', 'USelectMenu') }
function numbers(wrapper: any) { return components(wrapper, 'InputNumber', 'UInputNumber') }
function checkboxes(wrapper: any) { return components(wrapper, 'Checkbox', 'UCheckbox') }
function checkbox(wrapper: any, label: string) {
  const control = checkboxes(wrapper).find((item: any) => item.props('label') === label)
  if (!control) throw new Error(`Missing checkbox: ${label}`)
  return control
}
function selectWithValue(wrapper: any, value: string) {
  const control = selects(wrapper).find((item: any) => Array.isArray(item.props('items')) && item.props('items').some((entry: any) => entry?.value === value))
  if (!control) throw new Error(`Missing select containing value: ${value}`)
  return control
}
function inputWithValue(wrapper: any, value: string) {
  const control = components(wrapper, 'Input', 'UInput').find((item: any) => item.props('modelValue') === value)
  if (!control) throw new Error(`Missing input containing value: ${value}`)
  return control
}

async function clickConfirmation(kind: 'confirm' | 'cancel') {
  await flushPromises()
  const buttons = [...document.body.querySelectorAll<HTMLButtonElement>(`[data-testid="confirmation-${kind}"]`)]
  const button = buttons.at(-1)
  if (!button) throw new Error(`Missing confirmation ${kind} button`)
  button.click()
  await flushPromises()
}

beforeEach(() => {
  mocks.request.mockReset()
  resetManager()
})

describe('Instance configuration pages', () => {
  it('creates a fully configured Instance with visual placement and structured llama.cpp overrides', async () => {
    const manager = resetManager()
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      const phase7 = phase7Response(path)
      if (phase7 !== undefined) return phase7
      if (path === '/api/v1/instances' && options?.method === 'POST') return { id: 'custom-coder' }
      if (path === '/api/v1/instances/custom-coder/start') return { state: 'READY' }
      if (path === '/api/v1/models') return manager.models.value
      if (path === '/api/v1/instances') return []
      if (path === '/api/v1/llamacpp/profile') throw new Error('profile unavailable')
      return {}
    })

    const wrapper = await mountSuspended(NewInstancePage, { route: '/instances/new' })
    selectWithValue(wrapper, 'm1').vm.$emit('update:modelValue', 'm1')
    selectWithValue(wrapper, 'high').vm.$emit('update:modelValue', 'high')
    await flushPromises()

    expect((wrapper.get('[data-testid="instance-name"]').element as HTMLInputElement).value).toBe('Coder Model')
    expect((wrapper.get('[data-testid="instance-slug"]').element as HTMLInputElement).value).toBe('coder-model')
    await wrapper.get('[data-testid="instance-name"]').setValue('Primary Coder')
    await flushPromises()
    expect((wrapper.get('[data-testid="instance-slug"]').element as HTMLInputElement).value).toBe('primary-coder')
    await wrapper.get('[data-testid="instance-slug"]').setValue('custom-coder')

    const placement = wrapper.findComponent(HardwarePlacementEditor)
    placement.vm.$emit('update:gpuMode', 'manual')
    placement.vm.$emit('update:gpuDevices', ['CUDA0', 'CUDA1'])
    placement.vm.$emit('update:tensorSplit', '1,1')
    wrapper.findComponent(LlamaCppOptionsEditor).vm.$emit('update:modelValue', { 'ctx-size': '8192', threads: '4' })
    numbers(wrapper)[0]!.vm.$emit('update:modelValue', 90)
    checkbox(wrapper, 'Always On').vm.$emit('update:modelValue', true)
    checkbox(wrapper, 'Launch after creation').vm.$emit('update:modelValue', true)
    await flushPromises()

    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(document.body.textContent).toContain('may stop other eligible idle Instances')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/instances', {
      method: 'POST',
      body: expect.objectContaining({
        model_id: 'm1', name: 'Primary Coder', slug: 'custom-coder', priority: 'high', always_on: true,
        idle_unload_seconds: 90, gpu_mode: 'manual', gpu_devices: ['CUDA0', 'CUDA1'], tensor_split: '1,1',
        options: { 'ctx-size': '8192', threads: '4' }
      })
    })
    expect(mocks.request).not.toHaveBeenCalledWith('/api/v1/instances/custom-coder/start', expect.anything())
    await clickConfirmation('confirm')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/instances/custom-coder/start', { method: 'POST' })
  })

  it('keeps a created Instance stopped when launch confirmation is cancelled and surfaces create errors', async () => {
    const manager = resetManager()
    let failCreate = false
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      const phase7 = phase7Response(path)
      if (phase7 !== undefined) return phase7
      if (path === '/api/v1/instances' && options?.method === 'POST') {
        if (failCreate) throw { data: { error: 'create failed' } }
        return { id: 'cancelled-launch' }
      }
      if (path === '/api/v1/models') return manager.models.value
      if (path === '/api/v1/instances') return []
      return {}
    })
    const wrapper = await mountSuspended(NewInstancePage, { route: '/instances/new' })
    selectWithValue(wrapper, 'm1').vm.$emit('update:modelValue', 'm1')
    await wrapper.get('[data-testid="instance-name"]').setValue('Cancelled Launch')
    checkbox(wrapper, 'Launch after creation').vm.$emit('update:modelValue', true)
    wrapper.findComponent(LlamaCppOptionsEditor).vm.$emit('update:modelValue', { 'ctx-size': '4096' })
    await flushPromises()
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(document.body.textContent).toContain('Keep stopped')
    await clickConfirmation('cancel')
    expect(mocks.request).not.toHaveBeenCalledWith('/api/v1/instances/cancelled-launch/start', expect.anything())

    failCreate = true
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(wrapper.text()).toContain('create failed')
  })

  it('loads and applies a confirmed running rename with restart semantics', async () => {
    const manager = resetManager()
    const current = instance({ gpu_mode: 'manual', gpu_devices: ['CUDA0', 'CUDA1'], tensor_split: '1,1' })
    manager.instances.value = [current]
    manager.runtimes.value = { m1: [{ instance_id: current.id, model_id: 'm1', state: 'READY' }] }
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      const phase7 = phase7Response(path)
      if (phase7 !== undefined) return phase7
      if (path === '/api/v1/instances/primary-coder') {
        if (options?.method === 'PUT') return { id: 'renamed-coder' }
        return current
      }
      if (path === '/api/v1/instances/primary-coder/options') return { 'ctx-size': '8192' }
      if (path === '/api/v1/models') return manager.models.value
      if (path === '/api/v1/instances') return [instance({ id: 'renamed-coder', name: 'Renamed Coder' })]
      return {}
    })

    const wrapper = await mountSuspended(EditInstancePage, { route: '/instances/primary-coder/edit' })
    await flushPromises()
    expect(wrapper.text()).toContain('Edit Instance')
    expect(wrapper.findComponent(LlamaCppOptionsEditor).props('modelValue')).toEqual({ 'ctx-size': '8192' })
    inputWithValue(wrapper, 'Primary Coder').vm.$emit('update:modelValue', 'Renamed Coder')
    wrapper.findComponent(LlamaCppOptionsEditor).vm.$emit('update:modelValue', { 'ctx-size': '16384', threads: '6' })
    await flushPromises()
    expect(wrapper.text()).toContain('API-breaking rename')

    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(document.body.textContent).toContain('Existing clients using the old model ID will break')
    await clickConfirmation('confirm')
    expect(document.body.textContent).toContain('temporary unavailability')
    await clickConfirmation('confirm')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/instances/primary-coder', {
      method: 'PUT',
      body: expect.objectContaining({
        name: 'Renamed Coder', gpu_mode: 'manual', gpu_devices: ['CUDA0', 'CUDA1'], tensor_split: '1,1',
        options: { 'ctx-size': '16384', threads: '6' }, restart_running: true, confirm_model_id_change: true
      })
    })
  })

  it('handles edit load failures, cancelled renames and save errors', async () => {
    resetManager()
    mocks.request.mockRejectedValueOnce({ data: { error: 'instance load failed' } }).mockResolvedValue({})
    let wrapper = await mountSuspended(EditInstancePage, { route: '/instances/primary-coder/edit' })
    await flushPromises()
    expect(wrapper.text()).toContain('instance load failed')
    wrapper.unmount()

    const manager = resetManager()
    const current = instance()
    let failSave = false
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      const phase7 = phase7Response(path)
      if (phase7 !== undefined) return phase7
      if (path === '/api/v1/instances/primary-coder' && options?.method === 'PUT') {
        if (failSave) throw { data: { error: 'save failed' } }
        return current
      }
      if (path === '/api/v1/instances/primary-coder' && !options?.method) return current
      if (path === '/api/v1/instances/primary-coder/options') return {}
      if (path === '/api/v1/models') return manager.models.value
      if (path === '/api/v1/instances') return [current]
      return {}
    })
    wrapper = await mountSuspended(EditInstancePage, { route: '/instances/primary-coder/edit' })
    await flushPromises()
    inputWithValue(wrapper, 'Primary Coder').vm.$emit('update:modelValue', 'Breaking Rename')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    await clickConfirmation('cancel')
    expect(mocks.request).not.toHaveBeenCalledWith('/api/v1/instances/primary-coder', expect.objectContaining({ method: 'PUT' }))

    inputWithValue(wrapper, 'Breaking Rename').vm.$emit('update:modelValue', 'Primary Coder')
    failSave = true
    await flushPromises()
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(wrapper.text()).toContain('save failed')
  })
})

describe('Model edit page', () => {
  it('loads reusable defaults and saves edited metadata/options', async () => {
    const manager = resetManager()
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      const phase7 = phase7Response(path)
      if (phase7 !== undefined) return phase7
      if (path === '/api/v1/models/m1') {
        if (options?.method === 'PUT') return model({ name: 'Updated Model', context_length: 32768 })
        return model()
      }
      if (path === '/api/v1/models/m1/options') return { 'ctx-size': '8192', threads: '4' }
      if (path === '/api/v1/models') return manager.models.value
      if (path === '/api/v1/instances') return []
      return {}
    })
    const wrapper = await mountSuspended(EditModelPage, { route: '/models/m1/edit' })
    await flushPromises()
    expect(wrapper.findComponent(LlamaCppOptionsEditor).props('modelValue')).toEqual({ 'ctx-size': '8192', threads: '4' })
    inputWithValue(wrapper, 'Coder Model').vm.$emit('update:modelValue', 'Updated Model')
    numbers(wrapper)[0]!.vm.$emit('update:modelValue', 32768)
    wrapper.findComponent(LlamaCppOptionsEditor).vm.$emit('update:modelValue', { 'ctx-size': '32768', threads: '8' })
    await flushPromises()
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/models/m1', {
      method: 'PUT',
      body: { name: 'Updated Model', context_length: 32768, options: { 'ctx-size': '32768', threads: '8' } }
    })
  })

  it('renders load and save errors', async () => {
    resetManager()
    mocks.request.mockRejectedValueOnce(new Error('model load failed')).mockResolvedValue({})
    let wrapper = await mountSuspended(EditModelPage, { route: '/models/m1/edit' })
    await flushPromises()
    expect(wrapper.text()).toContain('model load failed')
    wrapper.unmount()

    const manager = resetManager()
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      const phase7 = phase7Response(path)
      if (phase7 !== undefined) return phase7
      if (path === '/api/v1/models/m1' && options?.method === 'PUT') throw { data: { error: 'save failed' } }
      if (path === '/api/v1/models/m1') return model()
      if (path === '/api/v1/models/m1/options') return {}
      if (path === '/api/v1/models') return manager.models.value
      if (path === '/api/v1/instances') return []
      return {}
    })
    wrapper = await mountSuspended(EditModelPage, { route: '/models/m1/edit' })
    await flushPromises()
    wrapper.findComponent(LlamaCppOptionsEditor).vm.$emit('update:modelValue', { threads: '4' })
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(wrapper.text()).toContain('save failed')
  })
})
