import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import NewModelPage from '~/pages/models/new.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

const available = [{ path: 'qwen-Q4_K_M.gguf', name: 'qwen-Q4_K_M.gguf', total_bytes: 1234, quantization: 'Q4_K_M' }]

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
  manager.profile.value = null
}

beforeEach(() => {
  mocks.request.mockReset()
  resetManager()
})

describe('detected model name prefill', () => {
  it('prefills general.name with an uppercase first letter and updates the first Instance defaults', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/models/available') return available
      if (path === '/api/v1/models/inspect') {
        return {
          id: 'qwen-Q4_K_M.gguf',
          name: 'qwen-Q4_K_M.gguf',
          model_name: 'qwen coder 32b',
          model_bytes: 1234,
          total_bytes: 1234,
          shard_count: 1,
          expected_shards: 1,
          complete: true,
          files: [{ path: 'qwen-Q4_K_M.gguf', size: 1234 }]
        }
      }
      return []
    })

    const wrapper = await mountSuspended(NewModelPage, { route: '/models/new' })
    await flushPromises()
    await wrapper.get('input[type="radio"][name="gguf_path"][value="qwen-Q4_K_M.gguf"]').setValue()
    await flushPromises()

    expect((wrapper.get('[data-testid="model-name"]').element as HTMLInputElement).value).toBe('Qwen coder 32b')
    expect((wrapper.get('[data-testid="instance-name"]').element as HTMLInputElement).value).toBe('Qwen coder 32b')
    expect((wrapper.get('[data-testid="instance-slug"]').element as HTMLInputElement).value).toBe('qwen-coder-32b')
  })
})
