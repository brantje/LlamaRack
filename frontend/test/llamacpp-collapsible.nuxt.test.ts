import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import LlamaCppOptionsEditor from '~/components/LlamaCppOptionsEditor.vue'
import NewInstancePage from '~/pages/instances/new.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

const config = {
  profile: { options: [{ key: 'ctx-size', value_hint: 'N', kind: 'integer' }] },
  effective: {
    global: { 'ctx-size': '4096' }, model: {}, instance: {}, values: {}, sources: {}
  }
}

beforeEach(() => {
  mocks.request.mockReset()
  mocks.request.mockImplementation(async (path: string) => {
    if (path.startsWith('/api/v1/llamacpp/config')) return config
    return []
  })
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.models.value = [{ id: 'm1', name: 'Coder', gguf_path: 'coder.gguf', total_bytes: 1, context_length: 0 }]
  manager.instances.value = []
  manager.runtimes.value = {}
  manager.profile.value = null
})

describe('llama.cpp override editors', () => {
  it('keeps the legacy editor compact until the user expands it', async () => {
    const wrapper = await mountSuspended(LlamaCppOptionsEditor, {
      route: false,
      props: {
        modelValue: { 'ctx-size': '8192' },
        scope: 'instance',
        modelId: 'm1',
        defaultOpen: false
      }
    })
    await flushPromises()

    expect(wrapper.text()).toContain('Instance llama.cpp configuration')
    expect(wrapper.text()).toContain('1 override configured · remaining values inherited')
    expect(wrapper.text()).not.toContain('--ctx-size')

    await wrapper.findAll('button').find(button => button.text().includes('Instance llama.cpp configuration'))!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('--ctx-size')

    await wrapper.setProps({ modelValue: {} })
    await flushPromises()
    expect(wrapper.text()).toContain('No overrides configured · inheriting all values')
  })

  it('uses the structured llama.cpp editor on the shared New Instance form', async () => {
    const wrapper = await mountSuspended(NewInstancePage, { route: '/instances/new' })
    await flushPromises()
    const editor = wrapper.findComponent(LlamaCppOptionsEditor)
    expect(editor.exists()).toBe(true)
    expect(editor.props('scope')).toBe('instance')
    expect(wrapper.text()).toContain('Instance llama.cpp overrides')
    expect(wrapper.text()).toContain('Applied over the Model defaults, which are applied over the global defaults.')
    expect(wrapper.get('[data-testid="llamacpp-mode-basic"]').text()).toBe('Basic')
    expect(wrapper.get('[data-testid="llamacpp-mode-advanced"]').text()).toBe('Advanced')
  })

  it('hides excluded companion keys from both views while keeping them in the stored map', async () => {
    const wrapper = await mountSuspended(LlamaCppOptionsEditor, {
      route: false,
      props: {
        modelValue: { 'ctx-size': '8192', mmproj: '/models/mmproj.gguf' },
        scope: 'model',
        excludeKeys: ['mmproj']
      }
    })
    await flushPromises()

    expect(wrapper.text()).toContain('--ctx-size')
    expect(wrapper.text()).not.toContain('--mmproj')
    expect(wrapper.text()).toContain('1 override configured · remaining values inherited')

    await wrapper.get('[data-testid="llamacpp-mode-advanced"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('--ctx-size')
    expect(wrapper.text()).not.toContain('--mmproj')
  })
})
