import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import HardwarePlacementEditor from '~/components/HardwarePlacementEditor.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

beforeEach(() => {
  mocks.request.mockReset()
  mocks.request.mockResolvedValue({
    gpus: [
      { id: 'CUDA0', backend: 'cuda', index: 0, name: 'GPU zero', total_bytes: 16, used_bytes: 4, free_bytes: 12, utilization_pct: 10 },
      { id: 'CUDA1', backend: 'cuda', index: 1, name: 'GPU one', total_bytes: 16, used_bytes: 2, free_bytes: 14, utilization_pct: 5 }
    ]
  })

  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.models.value = []
  manager.instances.value = []
  manager.runtimes.value = {}
})

describe('GPU placement cards', () => {
  it('switches to manual placement and selects exactly the clicked GPU', async () => {
    const wrapper = await mountSuspended(HardwarePlacementEditor, {
      route: false,
      props: { gpuMode: 'auto', gpuDevices: [], tensorSplit: '3,1' }
    })
    await flushPromises()

    await wrapper.get('[data-testid="gpu-card-CUDA1"]').trigger('click')
    await flushPromises()

    expect(wrapper.emitted('update:gpuMode')?.some(args => args[0] === 'manual')).toBe(true)
    expect(wrapper.emitted('update:gpuDevices')?.some(args => JSON.stringify(args[0]) === JSON.stringify(['CUDA1']))).toBe(true)
    expect(wrapper.emitted('update:tensorSplit')?.some(args => args[0] === '')).toBe(true)

    await wrapper.setProps({ gpuMode: 'manual', gpuDevices: ['CUDA1'], tensorSplit: '' })
    await flushPromises()
    expect(wrapper.get('[data-testid="gpu-card-CUDA1"]').attributes('aria-pressed')).toBe('true')
    expect(wrapper.get('[data-testid="gpu-card-CUDA1"]').text()).toContain('Selected')
    expect(wrapper.get('[data-testid="gpu-card-CUDA0"]').attributes('aria-pressed')).toBe('false')

    await wrapper.get('[data-testid="gpu-card-CUDA0"]').trigger('keydown', { key: 'Enter' })
    await flushPromises()
    expect(wrapper.emitted('update:gpuDevices')?.some(args => JSON.stringify(args[0]) === JSON.stringify(['CUDA0']))).toBe(true)
  })
})
