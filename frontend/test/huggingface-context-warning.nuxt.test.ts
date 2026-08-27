import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import InstancesPage from '~/pages/instances/index.vue'
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
  manager.models.value = [{ id: 'm-import', name: 'HF Model', gguf_path: 'huggingface/acme/demo/model.gguf', total_bytes: 10, context_length: 0 }]
  manager.instances.value = [{ id: 'hf-instance', model_id: 'm-import', name: 'HF Instance', enabled: true, autoload_enabled: true, always_on: false, priority: 'normal', eviction_enabled: true, idle_unload_seconds: 0, gpu_mode: 'auto' }]
  manager.runtimes.value = { 'm-import': [{ instance_id: 'hf-instance', model_id: 'm-import', state: 'UNLOADED' }] }
  manager.profile.value = null
}

beforeEach(() => {
  mocks.request.mockReset()
  seedManager()
})

describe('Hugging Face context metadata warning', () => {
  it('shows a completed import warning even after the Instance is enabled', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/imports') {
        return [{
          id: 'import-1', job_id: 'job-1', model_id: 'm-import', instance_id: 'hf-instance', state: 'COMPLETED', start_when_ready: false,
          error: 'Context capability could not be detected automatically from GGUF metadata. Enter it manually on the Model edit page.'
        }]
      }
      return []
    })

    const wrapper = await mountSuspended(InstancesPage, { route: '/instances' })
    await flushPromises()

    expect(mocks.request).toHaveBeenCalledWith('/api/v1/imports')
    expect(wrapper.get('[data-testid="import-metadata-warning"]').text()).toContain('Import warning')
    expect(wrapper.get('[data-testid="import-metadata-warning"]').text()).toContain('Context capability could not be detected automatically')
    wrapper.unmount()
  })
})
