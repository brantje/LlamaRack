import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import ModelsPage from '~/pages/models/index.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

beforeEach(() => {
  mocks.request.mockReset()
  mocks.request.mockResolvedValue([])
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.instances.value = []
  manager.runtimes.value = {}
  manager.profile.value = null
})

describe('model registry metadata formatting', () => {
  it('formats unknown capability and zero or multi-kibibyte artifact sizes', async () => {
    const manager = useManager()
    manager.models.value = [
      { id: 'empty', name: 'Unknown Artifact', gguf_path: 'empty.gguf', total_bytes: 0, context_length: 0 },
      { id: 'large', name: 'Larger Artifact', gguf_path: 'large.gguf', total_bytes: 2048, context_length: 8192 }
    ]

    const wrapper = await mountSuspended(ModelsPage, { route: false })
    expect(wrapper.text()).toContain('Unknown')
    expect(wrapper.text()).toContain('—')
    expect(wrapper.text()).toContain('2.0 KiB')
    expect(wrapper.text()).toContain('8,192')
  })
})
