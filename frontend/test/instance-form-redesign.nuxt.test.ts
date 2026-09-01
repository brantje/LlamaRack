import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import { reactive } from 'vue'
import InstanceForm from '~/components/InstanceForm.vue'
import LlamaCppOptionsEditor from '~/components/LlamaCppOptionsEditor.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

function form(overrides: Record<string, any> = {}) {
  return {
    model_id: '', name: '', slug: '', enabled: true, always_on: false,
    autoload_enabled: true, priority: 'normal', eviction_enabled: true,
    idle_unload_seconds: 0, gpu_mode: 'auto', gpu_devices: [], tensor_split: '',
    request_log_mode: 'metadata', options: {}, ...overrides
  }
}

function seedManager() {
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.models.value = [
    { id: 'm1', name: 'Vision Model', gguf_path: 'vision.gguf', total_bytes: 0, context_length: 8192, quantization: 'Q4_K_M' },
    { id: 'm2', name: 'Plain Model', gguf_path: 'plain.gguf', total_bytes: 5 * 1024 ** 3, context_length: 4096 }
  ]
  manager.instances.value = []
  manager.runtimes.value = {}
  manager.observabilityLive.value = null
  manager.profile.value = {
    path: '/app/llama-server', version: 'test', fingerprint: 'abc', options: [
      { key: 'threads', value_hint: 'N', kind: 'integer', description: 'CPU threads' }
    ]
  }
  return manager
}

function controls(wrapper: any, name: string, fallback: string) {
  const found = wrapper.findAllComponents({ name })
  return found.length ? found : wrapper.findAllComponents({ name: fallback })
}

beforeEach(() => {
  mocks.request.mockReset()
  seedManager()
})

describe('shared Instance form redesign', () => {
  it('drives identity, slug ownership, priority and launch state from the flat form', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/settings/general') return { idle_unload_seconds: { value: 45 } }
      if (path === '/api/v1/hardware') return { gpus: [] }
      if (path.startsWith('/api/v1/llamacpp/config')) return { effective: { values: {}, sources: {} } }
      return {}
    })
    const state = reactive(form({ options: { mmproj: '/old/projector.gguf', 'spec-draft-model': '/old/draft.gguf', threads: '4' } }))
    const wrapper = await mountSuspended(InstanceForm, {
      props: { form: state, title: 'New Instance', submitLabel: 'Create Instance', showLaunchAfterCreate: true, launchAfterCreate: false }
    })
    await flushPromises()

    expect((wrapper.get('button[type="submit"]').element as HTMLButtonElement).disabled).toBe(true)
    const sectionNav = wrapper.get('[data-testid="instance-form-section-nav"]')
    expect(sectionNav.attributes('aria-label')).toBe('Instance form sections')
    expect(sectionNav.findAll('a').map(link => link.attributes('href'))).toEqual([
      '#instance-identity', '#instance-companions', '#instance-lifecycle', '#instance-placement', '#instance-overrides', '#instance-observability'
    ])
    expect(wrapper.get('[data-testid="priority-normal"]').attributes('aria-pressed')).toBe('true')
    expect(wrapper.get('[data-testid="placement-mode-auto"]').attributes('aria-pressed')).toBe('true')
    expect(wrapper.find('[name="gpu_mode"]').exists()).toBe(false)
    expect(wrapper.findComponent(LlamaCppOptionsEditor).props('scope')).toBe('instance')
    expect(wrapper.get('[data-testid="llamacpp-mode-basic"]').text()).toBe('Basic')
    expect(wrapper.get('[data-testid="llamacpp-mode-advanced"]').text()).toBe('Advanced')
    expect(wrapper.text()).toContain('Q4_K_M · 0 B · ctx 8,192')
    expect(wrapper.text()).toContain('— · 5.0 GiB · ctx 4,096')

    await wrapper.get('input[type="radio"][value="m1"]').trigger('change')
    await flushPromises()
    expect(state.name).toBe('Vision Model')
    expect(state.slug).toBe('vision-model')
    expect(state.options).toEqual({ threads: '4' })

    await wrapper.get('[data-testid="instance-name"]').setValue('Coder / Primary')
    expect(state.slug).toBe('coder-primary')
    await wrapper.get('[data-testid="instance-slug"]').setValue('api-model-v1')
    await wrapper.get('[data-testid="instance-name"]').setValue('Coder Renamed')
    expect(state.slug).toBe('api-model-v1')

    await wrapper.get('[data-testid="priority-low"]').trigger('click')
    expect(state.priority).toBe('low')
    expect(wrapper.get('[data-testid="priority-low"]').attributes('aria-pressed')).toBe('true')
    await wrapper.get('[data-testid="priority-high"]').trigger('click')
    expect(state.priority).toBe('high')

    const launch = controls(wrapper, 'Checkbox', 'UCheckbox').find((item: any) => item.props('label') === 'Launch after creation')!
    launch.vm.$emit('update:modelValue', true)
    await flushPromises()
    expect(wrapper.emitted('update:launchAfterCreate')?.at(-1)).toEqual([true])
    expect((wrapper.get('button[type="submit"]').element as HTMLButtonElement).disabled).toBe(false)
  })

  it('resolves real companion defaults and makes enable/disable persistent option mutations', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/settings/general') return { idle_unload_seconds: { value: 300 } }
      if (path === '/api/v1/hardware') return { gpus: [] }
      if (path.startsWith('/api/v1/llamacpp/config')) return {
        effective: {
          values: { mmproj: '/models/mmproj.gguf', 'spec-draft-model': '/models/draft.gguf', ignored: '/global.gguf' },
          sources: { mmproj: 'model', 'spec-draft-model': 'detected', ignored: 'global' }
        }
      }
      if (path === '/api/v1/models/inspect') return { dependencies: [{ kind: 'mmproj', total_bytes: 0 }, { kind: 'mtp', total_bytes: 1536 }] }
      return {}
    })
    const state = reactive(form({ model_id: 'm1', name: 'Vision Model', slug: 'vision-model', options: { mmproj: '' } }))
    const wrapper = await mountSuspended(InstanceForm, { props: { form: state, title: 'Edit Instance', submitLabel: 'Save' } })
    await flushPromises()

    const projector = wrapper.get('[data-testid="companion-mmproj"]')
    const draft = wrapper.get('[data-testid="companion-spec-draft-model"]')
    expect(projector.text()).toContain('Ignored')
    expect(projector.text()).toContain('value cleared — the flag is not passed')
    expect(draft.text()).toContain('Auto-detected')
    expect(draft.text()).toContain('1.5 KiB · inherited from the Model defaults')
    expect(wrapper.findComponent(LlamaCppOptionsEditor).props('excludeKeys')).toEqual(['mmproj', 'spec-draft-model'])

    await projector.findAll('button').find(button => button.text() === 'Enable')!.trigger('click')
    await flushPromises()
    expect(state.options.mmproj).toBe('/models/mmproj.gguf')
    expect(projector.text()).toContain('Auto-detected')
    await draft.findAll('button').find(button => button.text() === 'Disable')!.trigger('click')
    await flushPromises()
    expect(state.options['spec-draft-model']).toBe('')
    expect(draft.text()).toContain('Ignored')

    const companionInputs = controls(wrapper, 'Input', 'UInput').filter((item: any) => String(item.props('modelValue') || '').includes('/models/'))
    companionInputs[0]!.vm.$emit('update:modelValue', '/custom/projector.gguf')
    await flushPromises()
    expect(Object.values(state.options)).toContain('/custom/projector.gguf')
  })

  it('keeps unavailable or non-model companion sources neutral, isolates probe failures and tolerates inspection errors', async () => {
    let detected = false
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/settings/general') throw new Error('settings unavailable')
      if (path === '/api/v1/hardware') return { gpus: [{ id: 'CUDA7', backend: 'cuda', index: 7, name: 'Fallback GPU', total_bytes: 4096, used_bytes: 1024, free_bytes: 3072, utilization_pct: 25 }] }
      if (path.startsWith('/api/v1/llamacpp/config')) {
        if (!detected) return { effective: { values: { mmproj: '/global/mmproj.gguf' }, sources: { mmproj: 'global' } } }
        return { effective: { values: { mmproj: '/models/mmproj.gguf' }, sources: { mmproj: 'model' } } }
      }
      if (path === '/api/v1/models/inspect') throw new Error('inspection unavailable')
      return {}
    })
    const state = reactive(form({ model_id: 'm1', name: 'Vision Model', slug: 'vision-model', gpu_mode: 'manual' }))
    const wrapper = await mountSuspended(InstanceForm, { props: { form: state, title: 'Edit Instance', submitLabel: 'Save' } })
    await flushPromises()
    expect(wrapper.get('[data-testid="companion-mmproj"]').text()).toContain('None found')
    expect(wrapper.get('[data-testid="companion-spec-draft-model"]').text()).toContain('None found')
    expect(wrapper.get('[data-testid="manual-placement-controls"]').text()).toContain('CUDA7')

    detected = true
    await wrapper.get('input[type="radio"][value="m2"]').trigger('change')
    await flushPromises()
    await wrapper.get('input[type="radio"][value="m1"]').trigger('change')
    await flushPromises()
    expect(wrapper.get('[data-testid="companion-mmproj"]').text()).toContain('Auto-detected')
    expect(wrapper.get('[data-testid="companion-mmproj"]').text()).toContain('size unavailable')
  })

  it('exposes manual GPU controls, toggles devices, and clears manual-only state in Auto', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/settings/general') return { idle_unload_seconds: { value: -1 } }
      if (path === '/api/v1/hardware') return { gpus: [{ id: 'CUDA0', backend: 'cuda', index: 0, name: 'GPU 0', total_bytes: 8 * 1024 ** 3, used_bytes: 2 * 1024 ** 3, free_bytes: 6 * 1024 ** 3, utilization_pct: 20 }] }
      if (path.startsWith('/api/v1/llamacpp/config')) return { effective: { values: {}, sources: {} } }
      return {}
    })
    const state = reactive(form({ model_id: 'm1', name: 'Vision Model', slug: 'vision-model', gpu_mode: 'manual', gpu_devices: [], tensor_split: '60,40' }))
    const wrapper = await mountSuspended(InstanceForm, { props: { form: state, title: 'Edit Instance', submitLabel: 'Save' } })
    await flushPromises()

    const manual = wrapper.get('[data-testid="manual-placement-controls"]')
    expect(manual.text()).toContain('CUDA0')
    expect(manual.text()).toContain('6.0 GiB free')
    const gpu = manual.get('input[type="checkbox"]')
    await gpu.trigger('change')
    expect(state.gpu_devices).toEqual(['CUDA0'])
    await gpu.trigger('change')
    expect(state.gpu_devices).toEqual([])

    state.gpu_devices = ['CUDA0']
    state.tensor_split = '1'
    await wrapper.get('[data-testid="placement-mode-auto"]').trigger('click')
    await flushPromises()
    expect(state.gpu_mode).toBe('auto')
    expect(state.gpu_devices).toEqual([])
    expect(state.tensor_split).toBe('')
    expect(wrapper.find('[data-testid="manual-placement-controls"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('The scheduler picks devices from fresh VRAM state at launch time.')
    await wrapper.get('[data-testid="placement-mode-manual"]').trigger('click')
    expect(state.gpu_mode).toBe('manual')
    expect(wrapper.get('[data-testid="placement-mode-manual"]').attributes('aria-pressed')).toBe('true')
  })

  it('uses live GPU data and renders the full-content logging warning', async () => {
    const manager = seedManager()
    manager.observabilityLive.value = {
      collected_at: '', telemetry: [], requests: [], gateway: {} as any,
      hardware: { ram_total_bytes: 0, ram_available_bytes: 0, collected_at: '', processes: [], gpus: [{ id: 'CUDA9', backend: 'cuda', index: 9, name: 'Live GPU', total_bytes: 1024, used_bytes: 0, free_bytes: 1024, utilization_pct: 0 }] }
    } as any
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/settings/general') return { idle_unload_seconds: { value: 0 } }
      if (path === '/api/v1/hardware') return { gpus: [] }
      if (path.startsWith('/api/v1/llamacpp/config')) throw new Error('config unavailable')
      return {}
    })
    const state = reactive(form({ model_id: 'm1', name: 'Vision Model', slug: 'vision-model', gpu_mode: 'manual', request_log_mode: 'full' }))
    const wrapper = await mountSuspended(InstanceForm, { props: { form: state, title: 'Edit Instance', submitLabel: 'Save', error: 'save failed' } })
    await flushPromises()
    expect(wrapper.text()).toContain('CUDA9')
    expect(wrapper.text()).toContain('Full content logging enabled')
    expect(wrapper.text()).toContain('save failed')
  })
})
