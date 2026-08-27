import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import NewModelPage from '~/pages/models/new.vue'
import ModelDetailsPage from '~/pages/models/[id]/details.vue'
import ModelsPage from '~/pages/models/index.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

const model = { id: 'm1', name: 'Qwen Model', gguf_path: 'qwen-Q4_K_M.gguf', total_bytes: 4 * 1024 ** 3, quantization: 'Q4_K_M', context_length: 32768 }
const available = [{ path: model.gguf_path, name: model.gguf_path, total_bytes: model.total_bytes, quantization: model.quantization }]

function resetManager() {
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.models.value = [model]
  manager.instances.value = []
  manager.runtimes.value = {}
  manager.profile.value = null
  return manager
}

function selects(wrapper: any) {
  const found = wrapper.findAllComponents({ name: 'SelectMenu' })
  return found.length ? found : wrapper.findAllComponents({ name: 'USelectMenu' })
}

function numbers(wrapper: any) {
  const found = wrapper.findAllComponents({ name: 'InputNumber' })
  return found.length ? found : wrapper.findAllComponents({ name: 'UInputNumber' })
}

beforeEach(() => {
  mocks.request.mockReset()
  resetManager()
})

describe('Model GGUF metadata', () => {
  it('auto-fills Context capability from selected local GGUF metadata and leaves it editable', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/models/available') return available
      if (path === '/api/v1/models/inspect') return { architecture: 'qwen2', context_length: 32768, gguf_version: 3, metadata_count: 22 }
      return []
    })
    const wrapper = await mountSuspended(NewModelPage, { route: '/models/new' })
    await flushPromises()
    selects(wrapper)[0]!.vm.$emit('update:modelValue', model.gguf_path)
    await flushPromises()

    expect(mocks.request).toHaveBeenCalledWith('/api/v1/models/inspect', { method: 'POST', body: { gguf_path: model.gguf_path } })
    const context = numbers(wrapper).find((item: any) => item.attributes('data-testid') === 'context-capability') || numbers(wrapper)[0]!
    expect(context.props('modelValue')).toBe(32768)
    expect(wrapper.text()).toContain('Detected from qwen2 GGUF metadata.')

    context.vm.$emit('update:modelValue', 8192)
    await flushPromises()
    expect(context.props('modelValue')).toBe(8192)
  })

  it('shows metadata detection failures without blocking the Model form', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/models/available') return available
      if (path === '/api/v1/models/inspect') return { context_length: 0, warning: 'GGUF metadata unavailable: invalid magic' }
      return []
    })
    const wrapper = await mountSuspended(NewModelPage, { route: '/models/new' })
    await flushPromises()
    selects(wrapper)[0]!.vm.$emit('update:modelValue', model.gguf_path)
    await flushPromises()
    expect(wrapper.get('[data-testid="metadata-warning"]').text()).toContain('invalid magic')
    expect(numbers(wrapper)[0]!.props('modelValue')).toBe(0)
  })

  it('shows a generic searchable Key / Type / Value metadata view', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (!path.startsWith('/api/v1/models/m1/details?')) return {}
      const filtered = path.includes('q=vendor')
      return {
        model,
        gguf_version: 3,
        tensor_count: 291,
        metadata_count: 3,
        metadata_total: filtered ? 1 : 3,
        offset: 0,
        limit: 100,
        architecture: 'qwen2',
        detected_context_length: 32768,
        warnings: [],
        metadata: filtered
          ? [{ key: 'vendor.future.key', type: 'string', value: 'still visible' }]
          : [
              { key: 'general.architecture', type: 'string', value: 'qwen2' },
              { key: 'qwen2.context_length', type: 'uint32', value: '32768' },
              { key: 'vendor.future.key', type: 'string', value: 'still visible' }
            ]
      }
    })

    const wrapper = await mountSuspended(ModelDetailsPage, { route: '/models/m1/details' })
    await flushPromises()
    expect(wrapper.get('[data-testid="model-details-summary"]').text()).toContain('qwen2')
    expect(wrapper.get('[data-testid="metadata-table"]').text()).toContain('general.architecture')
    expect(wrapper.get('[data-testid="metadata-table"]').text()).toContain('vendor.future.key')
    expect(wrapper.text()).toContain('Key / Type / Value')

    await wrapper.get('[data-testid="metadata-search"]').setValue('vendor')
    await wrapper.get('[data-testid="metadata-search-button"]').trigger('click')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith(expect.stringContaining('q=vendor'))
    expect(wrapper.get('[data-testid="metadata-table"]').text()).toContain('still visible')
    expect(wrapper.get('[data-testid="metadata-table"]').text()).not.toContain('general.architecture')
  })

  it('links registered Models to their read-only details page', async () => {
    const wrapper = await mountSuspended(ModelsPage, { route: '/models' })
    await flushPromises()
    const detailsLink = wrapper.findAll('a').find(link => link.text().includes('Details'))
    expect(detailsLink?.attributes('href')).toBe('/models/m1/details')
  })
})
