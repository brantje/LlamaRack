import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import HardwarePlacementEditor from '~/components/HardwarePlacementEditor.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

const hardware = {
  gpus: [
    { id: 'CUDA0', backend: 'cuda', index: 0, name: 'GPU zero', total_bytes: 16, used_bytes: 4, free_bytes: 12, utilization_pct: 10 },
    { id: 'CUDA1', backend: 'cuda', index: 1, name: 'GPU one', total_bytes: 16, used_bytes: 2, free_bytes: 14, utilization_pct: 5 }
  ]
}

beforeEach(() => {
  mocks.request.mockReset()
  mocks.request.mockResolvedValue(hardware)

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

  it('shows memory, context and offload guidance for the selected model', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/hardware') return hardware
      if (path === '/api/v1/models/model-1/recommendation') return {
        context_length: 8192,
        context_assumed: false,
        confidence: 'high',
        quantization: { name: 'Q4_K_M', summary: 'Balanced quantization.', tradeoff: 'Good general-purpose choice.' },
        memory: { weights_bytes: 4, kv_cache_bytes: 2, runtime_overhead_bytes: 1, cpu_only_ram_bytes: 7, full_offload_vram_bytes: 7 },
        current_fit: true,
        total_hardware_fit: true,
        cpu_fit: true,
        offload: { mode: 'full', gpu_layers: 32, devices: ['CUDA1'], reason: 'Fits one GPU.' }
      }
      throw new Error(`unexpected request ${path}`)
    })

    const wrapper = await mountSuspended(HardwarePlacementEditor, {
      route: false,
      props: { gpuMode: 'auto', gpuDevices: [], tensorSplit: '', modelId: 'model-1' }
    })
    await flushPromises()

    const recommendation = wrapper.get('[data-testid="hardware-recommendation"]')
    expect(recommendation.text()).toContain('Fits current resources')
    expect(recommendation.text()).toContain('high confidence')
    expect(recommendation.text()).toContain('8,192 tokens')
    expect(recommendation.text()).toContain('Recommended GPU layers: 32')
    expect(recommendation.text()).toContain('Q4_K_M')
  })
})
