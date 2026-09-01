import { describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import { reactive } from 'vue'
import InstanceForm from '~/components/InstanceForm.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

describe('Instance form empty identity branches', () => {
  it('clears auto and manually owned identity fields without retaining stale values', async () => {
    const manager = useManager()
    manager.disconnectRuntimeEvents()
    manager.initialized.value = true
    manager.bootstrapRequired.value = false
    manager.backendError.value = ''
    manager.user.value = { id: 1, username: 'admin', enabled: true }
    manager.models.value = [{ id: 'm1', name: 'Coder', gguf_path: 'coder.gguf', total_bytes: 1024, context_length: 4096 }]
    manager.instances.value = []
    manager.runtimes.value = {}
    manager.observabilityLive.value = null
    manager.profile.value = { path: '/app/llama-server', version: 'test', fingerprint: 'abc', options: [] }

    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/settings/general') return { idle_unload_seconds: { value: 300 } }
      if (path === '/api/v1/hardware') return { gpus: [] }
      if (path.startsWith('/api/v1/llamacpp/config')) return { effective: { values: {}, sources: {} } }
      return {}
    })

    const state = reactive({
      model_id: 'm1', name: 'Coder', slug: 'coder', enabled: true, always_on: false,
      autoload_enabled: true, priority: 'normal', eviction_enabled: true,
      idle_unload_seconds: 0, max_pending_requests: 0, gpu_mode: 'auto', gpu_devices: [] as string[], tensor_split: '',
      request_log_mode: 'metadata', options: {} as Record<string, string>
    })
    const wrapper = await mountSuspended(InstanceForm, {
      props: { form: state, title: 'Edit Instance', submitLabel: 'Save' }
    })
    await flushPromises()

    await wrapper.get('[data-testid="instance-name"]').setValue('')
    expect(state.name).toBe('')
    expect(state.slug).toBe('')

    await wrapper.get('[data-testid="instance-slug"]').setValue('manual-id')
    await wrapper.get('[data-testid="instance-name"]').setValue('')
    expect(state.slug).toBe('manual-id')

    await wrapper.get('[data-testid="instance-slug"]').setValue('')
    expect(state.slug).toBe('')
  })
})
