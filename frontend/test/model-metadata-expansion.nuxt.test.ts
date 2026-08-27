import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import ModelDetailsPage from '~/pages/models/[id]/details.vue'
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
  manager.models.value = [{ id: 'm1', name: 'Demo', gguf_path: 'demo.gguf', total_bytes: 10, context_length: 4096 }]
  manager.instances.value = []
  manager.runtimes.value = {}
  manager.profile.value = null
}

beforeEach(() => {
  mocks.request.mockReset()
  seedManager()
})

describe('Model metadata lazy expansion', () => {
  it('loads a bounded metadata value page only after Expand is requested', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/models/m1/details/value?')) {
        return {
          key: 'tokenizer.ggml.tokens',
          type: 'array<string>',
          items: ['"alpha"', '"beta"'],
          offset: 0,
          limit: 100,
          total: 200,
          has_more: true
        }
      }
      if (path.startsWith('/api/v1/models/m1/details?')) {
        return {
          model: { id: 'm1', name: 'Demo', gguf_path: 'demo.gguf', total_bytes: 10, context_length: 4096 },
          gguf_version: 3,
          tensor_count: 1,
          metadata_count: 1,
          metadata_total: 1,
          offset: 0,
          limit: 100,
          warnings: [],
          metadata: [{ key: 'tokenizer.ggml.tokens', type: 'array<string>', value: '["alpha", … 199 more]', truncated: true, array_length: 200 }]
        }
      }
      return {}
    })

    const wrapper = await mountSuspended(ModelDetailsPage, { route: '/models/m1/details' })
    await flushPromises()
    expect(mocks.request.mock.calls.some(call => String(call[0]).includes('/details/value?'))).toBe(false)

    await wrapper.get('[data-testid="metadata-expand"]').trigger('click')
    await flushPromises()

    expect(mocks.request).toHaveBeenCalledWith(expect.stringContaining('/api/v1/models/m1/details/value?key=tokenizer.ggml.tokens&offset=0'))
    expect(wrapper.get('[data-testid="metadata-expanded-items"]').text()).toContain('alpha')
    expect(wrapper.get('[data-testid="metadata-expanded-items"]').text()).toContain('beta')
    expect(wrapper.text()).toContain('200')
    wrapper.unmount()
  })
})
