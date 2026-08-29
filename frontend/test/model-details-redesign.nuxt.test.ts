import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import ModelDetailsPage from '~/pages/models/[id]/details.vue'
import AppButton from '~/components/AppButton.vue'
import Frame from '~/components/Frame.vue'
import StatusTag from '~/components/StatusTag.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

function seedManager() {
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.models.value = [{ id: 'm1', name: 'Demo', gguf_path: 'demo.gguf', total_bytes: 0, context_length: 0 }]
  manager.instances.value = []
  manager.runtimes.value = {}
  manager.profile.value = null
}

function response() {
  return {
    model: { id: 'm1', name: 'Demo', gguf_path: 'nested/demo.gguf', total_bytes: 0, context_length: 0, quantization: '' },
    gguf_version: 0,
    tensor_count: 0,
    metadata_count: 0,
    metadata_total: 2,
    offset: 0,
    limit: 100,
    architecture: '',
    detected_context_length: 0,
    warnings: ['metadata scan was partial'],
    metadata: [
      { key: 'general.name', type: 'string', value: 'Demo', truncated: false },
      { key: 'tokenizer.ggml.tokens', type: 'array<string>', value: '[alpha, …]', truncated: true }
    ]
  }
}

beforeEach(() => {
  mocks.request.mockReset()
  mocks.request.mockImplementation(async (path: string) => {
    if (path.startsWith('/api/v1/models/m1/details/value?')) {
      return { key: 'tokenizer.ggml.tokens', type: 'array<string>', value: 'full value', offset: 0, limit: 100, total: 10, has_more: false }
    }
    if (path.startsWith('/api/v1/models/m1/details?')) return response()
    return {}
  })
  seedManager()
})

describe('Model details redesign', () => {
  it('renders framed summary and warnings with the exact registry-only hierarchy', async () => {
    const wrapper = await mountSuspended(ModelDetailsPage, { route: '/models/m1/details' })
    await flushPromises()

    expect(wrapper.text()).toContain('MODEL REGISTRY')
    expect(wrapper.text()).toContain('General metadata read directly from the registered GGUF. Runtime controls remain on Instances.')

    const summary = wrapper.get('[data-testid="model-details-summary"]')
    for (const label of ['Path', 'Size', 'GGUF version', 'Metadata keys', 'Architecture', 'Quantization', 'Context capability', 'Tensor count']) {
      expect(summary.text()).toContain(label)
    }
    expect(summary.text().match(/Unknown/g)?.length).toBeGreaterThanOrEqual(7)
    expect(summary.findAll('dt').every(item => item.classes().includes('text-[9.5px]'))).toBe(true)
    expect(summary.findAll('dd').every(item => item.classes().includes('font-mono') && item.classes().includes('text-[13px]'))).toBe(true)

    const warning = wrapper.get('[data-testid="model-details-warning"]')
    expect(warning.text()).toContain('metadata scan was partial')
    expect(warning.findComponent(Frame).exists()).toBe(false)
    const warningTag = warning.findComponent(StatusTag)
    expect(warningTag.exists()).toBe(true)
    expect(warningTag.props('variant')).toBe('pending')

    const actions = wrapper.findAllComponents(AppButton)
    expect(actions.find(button => button.text() === 'Back to models')?.props('intent')).toBe('secondary')
    expect(actions.find(button => button.text() === 'Edit')?.props('intent')).toBe('primary')
  })

  it('shows Expand only for truncated metadata and preserves lazy value expansion', async () => {
    const wrapper = await mountSuspended(ModelDetailsPage, { route: '/models/m1/details' })
    await flushPromises()

    const table = wrapper.get('[data-testid="metadata-table"]')
    expect(table.findAll('th').map(item => item.text())).toEqual(['Key', 'Type', 'Value'])
    expect(table.text()).toContain('general.name')
    expect(table.text()).toContain('tokenizer.ggml.tokens')
    expect(wrapper.findAll('[data-testid="metadata-expand"]')).toHaveLength(1)
    expect(wrapper.text()).toContain('Showing 1–2 of 2 matching keys')

    await wrapper.get('[data-testid="metadata-expand"]').trigger('click')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith(expect.stringContaining('/api/v1/models/m1/details/value?key=tokenizer.ggml.tokens&offset=0'))
    expect(document.body.querySelector('[data-testid="metadata-expanded-value"]')?.textContent).toContain('full value')
  })
})
