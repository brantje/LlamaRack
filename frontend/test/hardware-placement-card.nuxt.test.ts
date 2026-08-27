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

function recommendation(overrides: Record<string, any> = {}) {
  return {
    context_length: 8192,
    context_capability: 262144,
    context_assumed: false,
    confidence: 'high',
    metadata: { context_length: 262144 },
    quantization: { name: 'Q4_K_M', summary: 'Balanced quantization.', tradeoff: 'Good general-purpose choice.' },
    memory: { weights_bytes: 4, kv_cache_bytes: 2, runtime_overhead_bytes: 1, cpu_only_ram_bytes: 7, full_offload_vram_bytes: 7 },
    current_fit: true,
    total_hardware_fit: true,
    cpu_fit: true,
    offload: { mode: 'full', gpu_layers: 32, devices: ['CUDA1'], kv_on_gpu: true, reason: 'Fits one GPU.' },
    ...overrides
  }
}

function isConfig(path: string) {
  return path.startsWith('/api/v1/llamacpp/config?')
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

  it('uses inherited context and shows a GPU-only fit with memory guidance', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/hardware') return hardware
      if (isConfig(path)) return { effective: { values: { 'ctx-size': '8192' } } }
      if (path === '/api/v1/models/model-1/recommendation?context_length=8192') return recommendation()
      throw new Error(`unexpected request ${path}`)
    })

    const wrapper = await mountSuspended(HardwarePlacementEditor, {
      route: false,
      props: { gpuMode: 'auto', gpuDevices: [], tensorSplit: '', modelId: 'model-1', llamaOptions: {} }
    })
    await flushPromises()

    const fit = wrapper.get('[data-testid="execution-fit"]')
    const panel = wrapper.get('[data-testid="hardware-recommendation"]')
    expect(wrapper.text()).toContain('inherited llama.cpp config')
    expect(wrapper.text()).toContain('Model capability: 262,144 tokens')
    expect(fit.text()).toContain('GPU only')
    expect(fit.text()).toContain('No CPU model split is needed')
    expect(panel.text()).toContain('Fits current resources')
    expect(panel.text()).toContain('high confidence')
    expect(panel.text()).toContain('8,192 tokens')
    expect(panel.text()).toContain('Recommended GPU layers: 32')
    expect(panel.text()).toContain('KV cache: GPU')
    expect(panel.text()).toContain('Q4_K_M')
  })

  it('recalculates when context changes, persists ctx-size and shows a GPU + CPU split', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/hardware') return hardware
      if (isConfig(path)) return { effective: { values: {} } }
      if (path === '/api/v1/models/model-1/recommendation?context_length=4096') return recommendation({ context_length: 4096 })
      if (path === '/api/v1/models/model-1/recommendation?context_length=65536') return recommendation({
        context_length: 65536,
        memory: { weights_bytes: 4, kv_cache_bytes: 64, runtime_overhead_bytes: 1, cpu_only_ram_bytes: 69, full_offload_vram_bytes: 69 },
        offload: { mode: 'hybrid', gpu_layers: 24, devices: ['CUDA1'], kv_on_gpu: false, reason: 'Keep KV in RAM.' }
      })
      throw new Error(`unexpected request ${path}`)
    })

    const wrapper = await mountSuspended(HardwarePlacementEditor, {
      route: false,
      props: { gpuMode: 'auto', gpuDevices: [], tensorSplit: '', modelId: 'model-1', llamaOptions: {} }
    })
    await flushPromises()
    expect(wrapper.text()).toContain('Estimate: 4,096 tokens')

    const input = wrapper.findComponent('[data-testid="context-input"]')
    expect(input.exists()).toBe(true)
    await input.setValue(65536)
    await flushPromises()

    expect(wrapper.emitted('update:contextSize')?.some(args => args[0] === '65536')).toBe(true)
    await vi.waitFor(() => {
      expect(mocks.request).toHaveBeenCalledWith('/api/v1/models/model-1/recommendation?context_length=65536')
    })
    await flushPromises()
    expect(wrapper.get('[data-testid="execution-fit"]').text()).toContain('GPU + CPU split needed')
    expect(wrapper.get('[data-testid="hardware-recommendation"]').text()).toContain('hybrid')
    expect(wrapper.get('[data-testid="hardware-recommendation"]').text()).toContain('KV cache: system RAM')
  })

  it('prefers an unsaved Instance ctx-size override over inherited config', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/hardware') return hardware
      if (path === '/api/v1/models/model-1/recommendation?context_length=32768') return recommendation({ context_length: 32768 })
      if (isConfig(path)) throw new Error('config should not be needed for a local override')
      throw new Error(`unexpected request ${path}`)
    })

    const wrapper = await mountSuspended(HardwarePlacementEditor, {
      route: false,
      props: { gpuMode: 'auto', gpuDevices: [], tensorSplit: '', modelId: 'model-1', llamaOptions: { 'ctx-size': '32768' } }
    })
    await flushPromises()
    expect(wrapper.text()).toContain('Instance override')
    expect(wrapper.text()).toContain('Estimate: 32,768 tokens')
  })

  it('renders multi-GPU, partial, CPU and pressure fit states', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/hardware') return hardware
      if (isConfig(path)) return { effective: { values: {} } }
      if (path.includes('/model-2/')) return recommendation({
        context_assumed: true,
        current_fit: false,
        total_hardware_fit: true,
        cpu_fit: false,
        metadata_warning: 'Metadata fallback active',
        hardware_warning: 'GPU probe degraded',
        quantization: { summary: 'Unknown quantization.', tradeoff: 'Use actual file size.' },
        offload: { mode: 'multi_gpu', gpu_layers: 40, devices: ['CUDA0', 'CUDA1'], tensor_split: '1,1', kv_on_gpu: true, reason: 'Needs both GPUs.' }
      })
      if (path.includes('/model-3/')) return recommendation({
        current_fit: false,
        total_hardware_fit: false,
        cpu_fit: true,
        offload: { mode: 'cpu', kv_on_gpu: false, reason: 'CPU fallback.' }
      })
      if (path.includes('/model-4/')) return recommendation({
        current_fit: false,
        total_hardware_fit: false,
        cpu_fit: false,
        offload: { mode: 'cpu', kv_on_gpu: false, reason: 'Insufficient resources.' }
      })
      if (path.includes('/model-5/')) return recommendation({
        current_fit: true,
        total_hardware_fit: true,
        cpu_fit: true,
        offload: { mode: 'partial', gpu_layers: 20, devices: ['CUDA1'], kv_on_gpu: true, reason: 'Partial offload.' }
      })
      throw new Error(`unexpected request ${path}`)
    })

    const wrapper = await mountSuspended(HardwarePlacementEditor, {
      route: false,
      props: { gpuMode: 'manual', gpuDevices: ['CUDA0', 'CUDA1'], tensorSplit: '', modelId: 'model-2', llamaOptions: {} }
    })
    await flushPromises()

    let panel = wrapper.get('[data-testid="hardware-recommendation"]')
    expect(wrapper.get('[data-testid="execution-fit"]').text()).toContain('GPU only · multi-GPU')
    expect(panel.text()).toContain('Fits installed hardware after freeing resources')
    expect(panel.text()).toContain('Tensor split: 1,1')
    expect(panel.text()).toContain('Metadata fallback active')
    expect(panel.text()).toContain('GPU probe degraded')

    await wrapper.setProps({ modelId: 'model-5' })
    await flushPromises()
    expect(wrapper.get('[data-testid="execution-fit"]').text()).toContain('GPU + CPU split needed')
    expect(wrapper.get('[data-testid="execution-fit"]').text()).toContain('remaining weights in system RAM')

    await wrapper.setProps({ modelId: 'model-3' })
    await flushPromises()
    panel = wrapper.get('[data-testid="hardware-recommendation"]')
    expect(wrapper.get('[data-testid="execution-fit"]').text()).toContain('CPU only')
    expect(panel.text()).toContain('CPU fallback fits current RAM')

    await wrapper.setProps({ modelId: 'model-4' })
    await flushPromises()
    panel = wrapper.get('[data-testid="hardware-recommendation"]')
    expect(wrapper.get('[data-testid="execution-fit"]').text()).toContain('CPU only')
    expect(panel.text()).toContain('Resource pressure expected')
  })

  it('ignores malformed recommendation payloads and surfaces request failures', async () => {
    const invalid: Record<string, any> = {
      primitive: null,
      context: {},
      confidence: { context_length: 1 },
      fit: { context_length: 1, confidence: 'low' },
      quantization: { context_length: 1, confidence: 'low', current_fit: false },
      quantSummary: { context_length: 1, confidence: 'low', current_fit: false, quantization: {} },
      quantTradeoff: { context_length: 1, confidence: 'low', current_fit: false, quantization: { summary: 'x' } },
      memory: { context_length: 1, confidence: 'low', current_fit: false, quantization: { summary: 'x', tradeoff: 'y' } },
      memoryVRAM: { context_length: 1, confidence: 'low', current_fit: false, quantization: { summary: 'x', tradeoff: 'y' }, memory: {} },
      memoryRAM: { context_length: 1, confidence: 'low', current_fit: false, quantization: { summary: 'x', tradeoff: 'y' }, memory: { full_offload_vram_bytes: 1 } },
      memoryKV: { context_length: 1, confidence: 'low', current_fit: false, quantization: { summary: 'x', tradeoff: 'y' }, memory: { full_offload_vram_bytes: 1, cpu_only_ram_bytes: 1 } },
      offload: { context_length: 1, confidence: 'low', current_fit: false, quantization: { summary: 'x', tradeoff: 'y' }, memory: { full_offload_vram_bytes: 1, cpu_only_ram_bytes: 1, kv_cache_bytes: 1 } },
      offloadMode: { context_length: 1, confidence: 'low', current_fit: false, quantization: { summary: 'x', tradeoff: 'y' }, memory: { full_offload_vram_bytes: 1, cpu_only_ram_bytes: 1, kv_cache_bytes: 1 }, offload: {} },
      offloadReason: { context_length: 1, confidence: 'low', current_fit: false, quantization: { summary: 'x', tradeoff: 'y' }, memory: { full_offload_vram_bytes: 1, cpu_only_ram_bytes: 1, kv_cache_bytes: 1 }, offload: { mode: 'cpu' } }
    }
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/hardware') return { gpus: [] }
      if (isConfig(path)) return { effective: { values: {} } }
      const id = path.split('/')[4]
      if (id === 'request-error') throw new Error('recommendation unavailable')
      return invalid[id!]
    })

    const wrapper = await mountSuspended(HardwarePlacementEditor, {
      route: false,
      props: { gpuMode: 'auto', gpuDevices: [], tensorSplit: '', modelId: 'primitive', llamaOptions: {} }
    })
    await flushPromises()
    expect(wrapper.find('[data-testid="hardware-recommendation"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="execution-fit"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('No NVIDIA or ROCm GPUs were detected')

    for (const id of Object.keys(invalid).slice(1)) {
      await wrapper.setProps({ modelId: id })
      await flushPromises()
      expect(wrapper.find('[data-testid="hardware-recommendation"]').exists()).toBe(false)
      expect(wrapper.find('[data-testid="execution-fit"]').exists()).toBe(false)
    }

    await wrapper.setProps({ modelId: 'request-error' })
    await flushPromises()
    expect(wrapper.text()).toContain('recommendation unavailable')
  })

  it('falls back to 4K when inherited config fails and surfaces hardware failures without blocking recommendations', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/hardware') throw { data: { error: 'hardware unavailable' } }
      if (isConfig(path)) throw new Error('config unavailable')
      if (path === '/api/v1/models/model-1/recommendation?context_length=4096') return recommendation({ context_length: 4096 })
      throw new Error('unexpected request')
    })
    const wrapper = await mountSuspended(HardwarePlacementEditor, {
      route: false,
      props: { gpuMode: 'auto', gpuDevices: [], tensorSplit: '', modelId: 'model-1', llamaOptions: {} }
    })
    await flushPromises()
    expect(wrapper.text()).toContain('hardware unavailable')
    expect(wrapper.text()).toContain('Estimate: 4,096 tokens')
    expect(wrapper.find('[data-testid="hardware-recommendation"]').exists()).toBe(true)
  })
})
