import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import LlamaCppOptionsEditor from '~/components/LlamaCppOptionsEditor.vue'
import HardwarePlacementEditor from '~/components/HardwarePlacementEditor.vue'
import AdminLlamaCppPage from '~/pages/admin/llamacpp.vue'
import { useManager, type Profile } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

const editorProfile = {
  path: '/app/llama-server',
  version: 'option-editor-test',
  fingerprint: 'option-editor',
  options: [
    { key: 'ctx-size', value_hint: 'N', description: 'Context size', kind: 'integer' },
    { key: 'flash-attn', description: 'Flash attention', kind: 'boolean' },
    { key: 'cache-type-k', value_hint: '<f16|q8_0>', kind: 'enum', choices: ['f16', 'q8_0'] },
    { key: 'spec-type', value_hint: '<none|draft>', kind: 'enum' },
    { key: 'reranking', value_hint: 'VALUE', kind: 'enum' },
    { key: 'threads', value_hint: 'N', description: 'CPU threads' },
    { key: 'batch-size', value_hint: 'N' },
    { key: 'jinja', description: 'Enable Jinja templates' },
    { key: 'parallel', value_hint: 'N', kind: 'integer' },
    { key: 'embeddings', kind: 'boolean' },
    { key: 'temperature', value_hint: 'FLOAT', description: 'Sampling temperature', kind: 'number' },
    { key: 'device', value_hint: 'DEVICE', kind: 'string' }
  ]
} satisfies Profile

function configResponse() {
  return {
    profile: { options: editorProfile.options },
    effective: {
      global: { 'ctx-size': '4096', threads: '8', embeddings: 'true' },
      model: { 'ctx-size': '8192', parallel: '4' },
      instance: {},
      values: {},
      sources: {}
    }
  }
}

function resetManager() {
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.models.value = []
  manager.instances.value = []
  manager.runtimes.value = {}
  manager.profile.value = editorProfile
  return manager
}

function components(wrapper: any, names: string[]) {
  const out: any[] = []
  const seen = new Set<Element>()
  for (const name of names) {
    for (const component of wrapper.findAllComponents({ name })) {
      if (component.element && !seen.has(component.element)) {
        seen.add(component.element)
        out.push(component)
      }
    }
  }
  return out
}

function optionRoot(wrapper: any, key: string): HTMLElement {
  const code = [...wrapper.element.querySelectorAll('code')].find((element: Element) => element.textContent?.trim() === `--${key}`)
  if (!code) throw new Error(`Missing option ${key}`)
  let current = code.parentElement
  while (current && current !== wrapper.element.parentElement) {
    const labels = [...current.querySelectorAll('button')].map(button => button.textContent?.trim())
    if (labels.includes('Override here') || labels.includes('Remove override')) return current
    current = current.parentElement
  }
  throw new Error(`Missing option root ${key}`)
}

function optionComponent(wrapper: any, key: string, names: string[]) {
  const root = optionRoot(wrapper, key)
  const component = components(wrapper, names).find(candidate => root.contains(candidate.element))
  if (!component) throw new Error(`Missing component for ${key}`)
  return component
}

async function clickOptionAction(wrapper: any, key: string, label: string) {
  const root = optionRoot(wrapper, key)
  const button = [...root.querySelectorAll('button')].find(element => element.textContent?.trim() === label)
  if (!button) throw new Error(`Missing ${label} for ${key}`)
  button.click()
  await flushPromises()
}

beforeEach(() => {
  mocks.request.mockReset()
  mocks.request.mockResolvedValue(configResponse())
  resetManager()
})

describe('llama.cpp option editor', () => {
  it('covers inheritance, protected values, search and every editable option shape', async () => {
    const base = {
      'ctx-size': '2048',
      'flash-attn': 'false',
      'cache-type-k': 'f16',
      'old-flag': 'legacy'
    }
    const wrapper = await mountSuspended(LlamaCppOptionsEditor, {
      route: false,
      props: { modelValue: base, scope: 'instance', modelId: 'm1', instanceId: 'i1' }
    })
    await flushPromises()

    expect(mocks.request).toHaveBeenCalledWith('/api/v1/llamacpp/config?model_id=m1&instance_id=i1')
    expect(wrapper.text()).toContain('Instance llama.cpp configuration')
    expect(wrapper.text()).toContain('Effective inherited value: 8')
    expect(wrapper.text()).toContain('Effective inherited value: 4')

    optionComponent(wrapper, 'ctx-size', ['Input', 'UInput']).vm.$emit('update:modelValue', '3072')
    optionComponent(wrapper, 'flash-attn', ['Checkbox', 'UCheckbox']).vm.$emit('update:modelValue', true)
    optionComponent(wrapper, 'cache-type-k', ['SelectMenu', 'USelectMenu']).vm.$emit('update:modelValue', 'q8_0')
    await flushPromises()

    await clickOptionAction(wrapper, 'threads', 'Override here')
    await clickOptionAction(wrapper, 'jinja', 'Override here')
    await clickOptionAction(wrapper, 'spec-type', 'Override here')
    await clickOptionAction(wrapper, 'reranking', 'Override here')
    await clickOptionAction(wrapper, 'batch-size', 'Override here')

    const emitted = (wrapper.emitted('update:modelValue') || []).map((args: any[]) => args[0] as Record<string, string>)
    expect(emitted.some(value => value['ctx-size'] === '3072')).toBe(true)
    expect(emitted.some(value => value['flash-attn'] === 'true')).toBe(true)
    expect(emitted.some(value => value['cache-type-k'] === 'q8_0')).toBe(true)
    expect(emitted.some(value => value.threads === '8')).toBe(true)
    expect(emitted.some(value => value.jinja === 'true')).toBe(true)
    expect(emitted.some(value => value['spec-type'] === 'none')).toBe(true)
    expect(emitted.some(value => value.reranking === '')).toBe(true)
    expect(emitted.some(value => value['batch-size'] === '')).toBe(true)

    await wrapper.findAll('button').find(button => button.text() === 'Advanced')!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('--temperature')
    expect(wrapper.text()).toContain('--device')
    expect(wrapper.text()).toContain('Manager controlled')
    expect(wrapper.text()).toContain('--old-flag')
    expect(wrapper.text()).toContain('Unsupported · retained')

    const search = wrapper.find('input[placeholder="Search all detected llama-server options"]')
    await search.setValue('sampling')
    expect(wrapper.text()).toContain('--temperature')
    await search.setValue('temperature')
    expect(wrapper.text()).toContain('--temperature')
    await search.setValue('does-not-exist')
    expect(wrapper.text()).toContain('No options match this view')
    await search.setValue('')

    const unsupported = optionComponent(wrapper, 'old-flag', ['Input', 'UInput'])
    expect(unsupported.props('disabled')).toBe(true)
    await clickOptionAction(wrapper, 'old-flag', 'Remove override')
    const afterRemove = (wrapper.emitted('update:modelValue') || []).map((args: any[]) => args[0] as Record<string, string>)
    expect(afterRemove.some(value => !Object.prototype.hasOwnProperty.call(value, 'old-flag'))).toBe(true)
  })

  it('covers global/model scope fallbacks, invalid responses and all load error messages', async () => {
    const manager = resetManager()
    mocks.request.mockRejectedValue({ data: { error: 'schema denied' } })
    const globalWrapper = await mountSuspended(LlamaCppOptionsEditor, {
      route: false,
      props: { modelValue: {}, scope: 'global' }
    })
    await flushPromises()
    expect(globalWrapper.text()).toContain('Global llama.cpp configuration')
    expect(globalWrapper.text()).toContain('schema denied')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/llamacpp/config')

    mocks.request.mockRejectedValue(new Error('schema exploded'))
    await globalWrapper.setProps({ modelId: 'm2' })
    await flushPromises()
    expect(globalWrapper.text()).toContain('schema exploded')

    mocks.request.mockRejectedValue({})
    await globalWrapper.setProps({ instanceId: 'i2' })
    await flushPromises()
    expect(globalWrapper.text()).toContain('Unable to load llama.cpp configuration')

    mocks.request.mockResolvedValue([])
    await globalWrapper.setProps({ modelId: 'm3' })
    await flushPromises()
    expect(globalWrapper.text()).toContain('--ctx-size')
    globalWrapper.unmount()

    manager.profile.value = null
    mocks.request.mockResolvedValue({
      profile: { options: [{ key: 'ctx-size', value_hint: 'N' }] },
      effective: { global: { 'ctx-size': '4096' }, model: {}, instance: {}, values: {}, sources: {} }
    })
    const modelWrapper = await mountSuspended(LlamaCppOptionsEditor, {
      route: false,
      props: { modelValue: {}, scope: 'model', modelId: 'm1' }
    })
    await flushPromises()
    expect(modelWrapper.text()).toContain('Model llama.cpp configuration')
    expect(modelWrapper.text()).toContain('Effective inherited value: 4096')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/llamacpp/config?model_id=m1')
  })
})

describe('Hardware placement editor', () => {
  it('covers hardware rendering, manual edits, auto reset and refresh failures', async () => {
    const snapshot = {
      gpus: [
        { id: 'CUDA0', backend: 'cuda', index: 0, name: 'Tiny', total_bytes: 512, used_bytes: 1536, free_bytes: 0, utilization_pct: 2 },
        { id: 'CUDA1', backend: 'cuda', index: 1, name: 'Large', total_bytes: 12 * 1024 ** 3, used_bytes: 12 * 1024 ** 3, free_bytes: 5.5 * 1024 ** 3, utilization_pct: 50 },
        { id: 'ROCm2', backend: 'rocm', index: 2, name: 'Huge', total_bytes: 2 * 1024 ** 4, used_bytes: 0, free_bytes: Number.NaN, utilization_pct: 0 }
      ]
    }
    mocks.request.mockResolvedValue(snapshot)
    const wrapper = await mountSuspended(HardwarePlacementEditor, {
      route: false,
      props: { gpuMode: 'manual', gpuDevices: ['CUDA0', 'CUDA1'], tensorSplit: '3,1' }
    })
    await flushPromises()

    expect(mocks.request).toHaveBeenCalledWith('/api/v1/hardware')
    expect(wrapper.text()).toContain('CUDA0')
    expect(wrapper.text()).toContain('0 B')
    expect(wrapper.text()).toContain('512 B')
    expect(wrapper.text()).toContain('1.5 KiB')
    expect(wrapper.text()).toContain('12 GiB')
    expect(wrapper.text()).toContain('2.0 TiB')

    const selects = components(wrapper, ['SelectMenu', 'USelectMenu'])
    const modeSelect = selects.find(component => !component.props('multiple'))!
    const deviceSelect = selects.find(component => component.props('multiple'))!
    modeSelect.vm.$emit('update:modelValue', null)
    deviceSelect.vm.$emit('update:modelValue', ['CUDA1', 2])
    deviceSelect.vm.$emit('update:modelValue', 'CUDA1')

    const tensor = components(wrapper, ['Input', 'UInput']).find(component => component.props('placeholder') === '3,1')!
    tensor.vm.$emit('update:modelValue', '2,1')
    tensor.vm.$emit('update:modelValue', null)
    await flushPromises()

    expect(wrapper.emitted('update:gpuMode')?.some(args => args[0] === 'auto')).toBe(true)
    expect(wrapper.emitted('update:gpuDevices')?.some(args => JSON.stringify(args[0]) === JSON.stringify(['CUDA1', '2']))).toBe(true)
    expect(wrapper.emitted('update:gpuDevices')?.some(args => Array.isArray(args[0]) && args[0].length === 0)).toBe(true)
    expect(wrapper.emitted('update:tensorSplit')?.some(args => args[0] === '2,1')).toBe(true)
    expect(wrapper.emitted('update:tensorSplit')?.some(args => args[0] === '')).toBe(true)

    await wrapper.setProps({ gpuMode: 'auto' })
    await flushPromises()
    expect(wrapper.text()).toContain('Single-GPU first')
    await wrapper.setProps({ gpuMode: 'manual' })
    await flushPromises()

    const refresh = () => wrapper.findAll('button').find(button => button.text().includes('Refresh hardware'))!
    mocks.request.mockRejectedValueOnce({ data: { error: 'gpu denied' } })
    await refresh().trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('gpu denied')

    mocks.request.mockRejectedValueOnce(new Error('gpu exploded'))
    await refresh().trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('gpu exploded')

    mocks.request.mockRejectedValueOnce({})
    await refresh().trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Unable to read hardware state')

    mocks.request.mockResolvedValueOnce([])
    await refresh().trigger('click')
    await flushPromises()
    expect(wrapper.text()).not.toContain('gpu denied')

    mocks.request.mockResolvedValueOnce({ gpus: [] })
    await refresh().trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('No NVIDIA or ROCm GPUs were detected')
  })
})

describe('Global llama.cpp administration', () => {
  it('covers saving defaults plus load/save error fallbacks', async () => {
    const manager = resetManager()
    manager.profile.value = { ...editorProfile, version: undefined }
    let saveMode: 'ok' | 'data' | 'message' | 'fallback' = 'ok'
    let loadMode: 'ok' | 'data' | 'message' | 'fallback' = 'ok'
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/llamacpp/config' && options?.method === 'PUT') {
        if (saveMode === 'data') throw { data: { error: 'save denied' } }
        if (saveMode === 'message') throw new Error('save exploded')
        if (saveMode === 'fallback') throw {}
        return {}
      }
      if (path === '/api/v1/llamacpp/config') {
        if (loadMode === 'data') throw { data: { error: 'load denied' } }
        if (loadMode === 'message') throw new Error('load exploded')
        if (loadMode === 'fallback') throw {}
        return {
          profile: { options: editorProfile.options },
          effective: { global: { 'ctx-size': '4096' }, model: {}, instance: {}, values: {}, sources: {} }
        }
      }
      if (path === '/api/v1/models' || path === '/api/v1/instances') return []
      if (path === '/api/v1/llamacpp/profile') return { available: true, profile: editorProfile }
      return []
    })

    const wrapper = await mountSuspended(AdminLlamaCppPage, { route: false })
    await flushPromises()
    expect(wrapper.text()).toContain('unknown')
    expect(wrapper.findComponent(LlamaCppOptionsEditor).exists()).toBe(true)
    expect(wrapper.get('[data-testid="llamacpp-mode-basic"]').attributes('aria-pressed')).toBe('true')

    const save = () => wrapper.findAll('button').find(button => button.text().includes('Save defaults'))!
    expect((save().element as HTMLButtonElement).disabled).toBe(true)
    wrapper.findComponent(LlamaCppOptionsEditor).vm.$emit('update:modelValue', { 'ctx-size': '8192' })
    await flushPromises()
    expect((save().element as HTMLButtonElement).disabled).toBe(false)
    await save().trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Global llama.cpp defaults saved')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/llamacpp/config', { method: 'PUT', body: { options: { 'ctx-size': '8192' } } })

    saveMode = 'data'
    wrapper.findComponent(LlamaCppOptionsEditor).vm.$emit('update:modelValue', { 'ctx-size': '16384' })
    await flushPromises()
    await save().trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('save denied')
    saveMode = 'message'
    await save().trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('save exploded')
    saveMode = 'fallback'
    await save().trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Unable to save llama.cpp defaults')

    saveMode = 'ok'
    const refresh = () => wrapper.findAll('button').find(button => button.text() === 'Refresh')!
    loadMode = 'data'
    await refresh().trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('load denied')
    loadMode = 'message'
    await refresh().trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('load exploded')
    loadMode = 'fallback'
    await refresh().trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Unable to load llama.cpp configuration')
  })
})
