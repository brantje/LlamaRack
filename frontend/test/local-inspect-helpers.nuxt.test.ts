import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import NewModelPage from '~/pages/models/new.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

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

async function selectGGUF(wrapper: any, path: string) {
  const input = wrapper.findAll('input[type="radio"][name="gguf_path"]').find((item: any) => item.attributes('value') === path)
  if (!input) throw new Error(`Missing GGUF option ${path}`)
  await input.setValue()
  await flushPromises()
}

beforeEach(() => {
  mocks.request.mockReset()
  resetManager()
})

describe('local GGUF inspection helpers', () => {
  it('uses /inspect artifact dependencies and applies their model defaults', async () => {
    const ggufPath = 'local/model-Q4_K_M.gguf'
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/models/available') {
        return [{ path: ggufPath, name: 'model-Q4_K_M.gguf', total_bytes: 100, quantization: 'Q4_K_M' }]
      }
      if (path === '/api/v1/models/inspect') {
        return {
          id: ggufPath,
          name: 'model-Q4_K_M.gguf',
          quantization: 'Q4_K_M',
          model_bytes: 100,
          total_bytes: 130,
          shard_count: 1,
          expected_shards: 1,
          complete: true,
          files: [
            { path: ggufPath, size: 100 },
            { path: 'local/vision-F16.gguf', size: 20 },
            { path: 'local/draft-Q4_0.gguf', size: 10 }
          ],
          dependencies: [
            { kind: 'mmproj', name: 'vision-F16.gguf', quantization: 'F16', total_bytes: 20, files: [{ path: 'local/vision-F16.gguf', size: 20 }] },
            { kind: 'mtp', name: 'draft-Q4_0.gguf', quantization: 'Q4_0', total_bytes: 10, files: [{ path: 'local/draft-Q4_0.gguf', size: 10 }] }
          ],
          architecture: 'qwen2',
          context_length: 32768,
          gguf_version: 3,
          metadata_count: 20,
          suggested_options: {
            mmproj: '/models/local/vision-F16.gguf',
            'spec-draft-model': '/models/local/draft-Q4_0.gguf',
            'spec-type': 'draft-mtp',
            'spec-draft-n-max': '16',
            'spec-draft-p-min': '0.8'
          }
        }
      }
      return []
    })

    const wrapper = await mountSuspended(NewModelPage, { route: '/models/new' })
    await flushPromises()
    await selectGGUF(wrapper, ggufPath)

    expect(mocks.request).toHaveBeenCalledWith('/api/v1/models/inspect', { method: 'POST', body: { gguf_path: ggufPath } })
    expect(wrapper.get('[data-testid="detected-gguf-helpers"]').text()).toContain('Vision projector: vision-F16.gguf')
    expect(wrapper.get('[data-testid="detected-gguf-helpers"]').text()).toContain('MTP draft model: draft-Q4_0.gguf')

    const options = wrapper.findComponent({ name: 'ModelOverridesEditor' })
    expect(options.exists()).toBe(true)
    expect(options.props('modelValue')).toMatchObject({
      mmproj: '/models/local/vision-F16.gguf',
      'spec-draft-model': '/models/local/draft-Q4_0.gguf',
      'spec-type': 'draft-mtp'
    })
  })
})
