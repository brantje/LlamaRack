import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import HardwarePlacementEditor from '~/components/HardwarePlacementEditor.vue'
import { useManager } from '~/composables/useManager'
import { contextToSliderPosition } from '~/utils/placementPresentation'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

const hardware = {
  gpus: [
    { id: 'CUDA0', backend: 'cuda', index: 0, name: 'RTX 3090', total_bytes: 16, used_bytes: 4, free_bytes: 12, utilization_pct: 10 },
    { id: 'CUDA1', backend: 'cuda', index: 1, name: 'NVIDIA GeForce RTX 4060 Ti', total_bytes: 16, used_bytes: 2, free_bytes: 14, utilization_pct: 5 }
  ]
}

function ranges(overrides: Record<string, any> = {}) {
  return {
    available: true,
    minimum_context: 512,
    maximum_context: 262144,
    context_step: 512,
    gpu_only_max_context: 14336,
    zones: [
      { start_context: 512, end_context: 14336, kind: 'gpu', offload_mode: 'full', gpu_count: 1, devices: ['CUDA1'], kv_on_gpu: true, gpu_layers: 32, current_fit: true, total_hardware_fit: true },
      { start_context: 14848, end_context: 262144, kind: 'hybrid', offload_mode: 'hybrid', gpu_count: 1, devices: ['CUDA1'], kv_on_gpu: false, gpu_layers: 24, current_fit: true, total_hardware_fit: true }
    ],
    ...overrides
  }
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
    placement_ranges: ranges(),
    ...overrides
  }
}

function isConfig(path: string) {
  return path.startsWith('/api/v1/llamacpp/config?')
}

async function openTechnical(wrapper: any) {
  const trigger = wrapper.findAll('button').find((button: any) => button.text().includes('Technical details'))
  if (trigger) {
    await trigger.trigger('click')
    await flushPromises()
  }
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
      props: { gpuMode: 'manual', gpuDevices: [], tensorSplit: '3,1' }
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
    expect(wrapper.text()).toContain('inherited llama.cpp config')
    expect(wrapper.text()).toContain('Model capability: 262,144 tokens')
    expect(wrapper.text()).toContain('How much text the model can keep in memory at once.')
    expect(fit.text()).toContain('Runs on 1 GPU')
    expect(fit.text()).toContain('NVIDIA GeForce RTX 4060 Ti')
    expect(fit.text()).toContain('Model: GPU')
    expect(fit.text()).toContain('Context cache: GPU')
    expect(wrapper.get('[data-testid="placement-ranges"]').text()).toContain('1 GPU')
    await wrapper.get('[data-testid="placement-zone-1"]').trigger('click')
    await flushPromises()
    expect(wrapper.emitted('update:contextSize')?.some(args => args[0] === '14848')).toBe(true)
    expect(wrapper.get('[data-testid="gpu-card-CUDA1"]').text()).toContain('Used by this placement')
    expect(wrapper.get('[data-testid="gpu-card-CUDA0"]').text()).toContain('Not needed for this placement')

    await openTechnical(wrapper)
    const panel = wrapper.get('[data-testid="hardware-recommendation"]')
    expect(panel.text()).toContain('high confidence')
    expect(panel.text()).toContain('8,192 tokens')
    expect(panel.text()).toContain('GPU layers')
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
    expect(wrapper.text()).toContain('Selected: 4,096 tokens')

    const slider = [
      ...wrapper.findAllComponents({ name: 'Slider' }),
      ...wrapper.findAllComponents({ name: 'USlider' })
    ][0]
    expect(slider).toBeTruthy()
    slider!.vm.$emit('update:modelValue', contextToSliderPosition(ranges().zones, 65536))
    await flushPromises()

    expect(wrapper.emitted('update:contextSize')?.some(args => args[0] === '65536')).toBe(true)
    await vi.waitFor(() => {
      expect(mocks.request).toHaveBeenCalledWith('/api/v1/models/model-1/recommendation?context_length=65536')
    })
    await flushPromises()
    expect(wrapper.get('[data-testid="execution-fit"]').text()).toContain('Runs on GPU + system memory')
    expect(wrapper.get('[data-testid="execution-fit"]').text()).toContain('Context cache: system RAM')
    expect(wrapper.get('[data-testid="use-boundary-context"]').text()).toContain('Use 14,336')
    await wrapper.get('[data-testid="use-boundary-context"]').trigger('click')
    await flushPromises()
    expect(wrapper.emitted('update:contextSize')?.some(args => args[0] === '14336')).toBe(true)
    await openTechnical(wrapper)
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
    expect(wrapper.text()).toContain('Selected: 32,768 tokens')
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
        offload: { mode: 'multi_gpu', gpu_layers: 40, devices: ['CUDA0', 'CUDA1'], tensor_split: '1,1', kv_on_gpu: true, reason: 'Needs both GPUs.' },
        placement_ranges: ranges({
          gpu_only_max_context: 20000,
          zones: [
            { start_context: 512, end_context: 8192, kind: 'gpu', offload_mode: 'full', gpu_count: 1, devices: ['CUDA1'], kv_on_gpu: true, current_fit: true, total_hardware_fit: true },
            { start_context: 8704, end_context: 20000, kind: 'gpu', offload_mode: 'multi_gpu', gpu_count: 2, devices: ['CUDA0', 'CUDA1'], kv_on_gpu: true, tensor_split: '1,1', current_fit: false, total_hardware_fit: true },
            { start_context: 20512, end_context: 262144, kind: 'hybrid', offload_mode: 'hybrid', gpu_count: 1, devices: ['CUDA1'], kv_on_gpu: false, current_fit: false, total_hardware_fit: true }
          ]
        })
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

    expect(wrapper.get('[data-testid="execution-fit"]').text()).toContain('Runs fully on 2 GPUs')
    expect(wrapper.get('[data-testid="execution-fit"]').text()).toContain('Fits after freeing GPU memory')
    expect(wrapper.get('[data-testid="gpu-card-CUDA0"]').text()).toContain('RTX 3090')
    await openTechnical(wrapper)
    let panel = wrapper.get('[data-testid="hardware-recommendation"]')
    expect(panel.text()).toContain('Fits installed hardware after freeing resources')
    expect(panel.text()).toContain('Tensor split: 1,1')
    expect(panel.text()).toContain('Metadata fallback active')
    expect(panel.text()).toContain('GPU probe degraded')

    await wrapper.setProps({ modelId: 'model-5' })
    await flushPromises()
    expect(wrapper.get('[data-testid="execution-fit"]').text()).toContain('Runs partly on GPU')
    expect(wrapper.get('[data-testid="execution-fit"]').text()).toContain('remainder use system memory')

    await wrapper.setProps({ modelId: 'model-3' })
    await flushPromises()
    expect(wrapper.get('[data-testid="execution-fit"]').text()).toContain('Runs on CPU')
    await openTechnical(wrapper)
    panel = wrapper.get('[data-testid="hardware-recommendation"]')
    expect(panel.text()).toContain('CPU fallback fits current RAM')

    await wrapper.setProps({ modelId: 'model-4' })
    await flushPromises()
    expect(wrapper.get('[data-testid="execution-fit"]').text()).toContain('Not enough memory')
    await openTechnical(wrapper)
    panel = wrapper.get('[data-testid="hardware-recommendation"]')
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
    expect(wrapper.text()).toContain('Selected: 4,096 tokens')
    expect(wrapper.find('[data-testid="hardware-recommendation"]').exists()).toBe(true)
  })

  it('hides duplicate placement fields when requested by the Instance form', async () => {
    const wrapper = await mountSuspended(HardwarePlacementEditor, {
      route: false,
      props: { gpuMode: 'auto', gpuDevices: [], tensorSplit: '', hidePlacementControls: true }
    })
    await flushPromises()
    expect(wrapper.find('[name="gpu_mode"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('Single-GPU first')
    expect(wrapper.text()).toContain('Automatic')
    wrapper.unmount()
  })

  it('shows unavailable ranges, compact many-GPU transitions, and refresh updates', async () => {
    let rangePayload = ranges({ available: false, unavailable_reason: 'LlamaRack could not determine reliable context boundaries for this Model.', zones: [] })
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/hardware') return {
        gpus: [
          ...hardware.gpus,
          { id: 'CUDA2', backend: 'cuda', index: 2, name: 'RTX 4070', total_bytes: 16, used_bytes: 1, free_bytes: 15, utilization_pct: 3 },
          { id: 'CUDA3', backend: 'cuda', index: 3, name: 'RTX 4080', total_bytes: 16, used_bytes: 1, free_bytes: 15, utilization_pct: 4 },
          { id: 'CUDA4', backend: 'cuda', index: 4, name: 'RTX 4090', total_bytes: 24, used_bytes: 1, free_bytes: 23, utilization_pct: 5 }
        ]
      }
      if (isConfig(path)) return { effective: { values: { 'ctx-size': '20000' } } }
      if (path.includes('/recommendation')) return recommendation({
        context_length: 20000,
        offload: { mode: 'multi_gpu', gpu_layers: 32, devices: ['CUDA1', 'CUDA0', 'CUDA2'], kv_on_gpu: true, reason: 'Needs three GPUs.' },
        placement_ranges: rangePayload
      })
      throw new Error(`unexpected request ${path}`)
    })

    const wrapper = await mountSuspended(HardwarePlacementEditor, {
      route: false,
      props: { gpuMode: 'auto', gpuDevices: [], tensorSplit: '', modelId: 'model-1', llamaOptions: {} }
    })
    await flushPromises()
    expect(wrapper.get('[data-testid="placement-ranges-unavailable"]').text()).toContain('Placement ranges unavailable')

    rangePayload = ranges({
      gpu_only_max_context: 112000,
      zones: [
        { start_context: 512, end_context: 8192, kind: 'gpu', offload_mode: 'full', gpu_count: 1, devices: ['CUDA1'], kv_on_gpu: true, current_fit: true, total_hardware_fit: true },
        { start_context: 8704, end_context: 16384, kind: 'gpu', offload_mode: 'multi_gpu', gpu_count: 2, devices: ['CUDA1', 'CUDA0'], kv_on_gpu: true, current_fit: true, total_hardware_fit: true },
        { start_context: 16896, end_context: 24576, kind: 'gpu', offload_mode: 'multi_gpu', gpu_count: 3, devices: ['CUDA1', 'CUDA0', 'CUDA2'], kv_on_gpu: true, current_fit: true, total_hardware_fit: true },
        { start_context: 25088, end_context: 32768, kind: 'gpu', offload_mode: 'multi_gpu', gpu_count: 4, devices: ['CUDA1', 'CUDA0', 'CUDA2', 'CUDA3'], kv_on_gpu: true, current_fit: true, total_hardware_fit: true },
        { start_context: 33280, end_context: 112000, kind: 'gpu', offload_mode: 'multi_gpu', gpu_count: 5, devices: ['CUDA1', 'CUDA0', 'CUDA2', 'CUDA3', 'CUDA4'], kv_on_gpu: true, current_fit: true, total_hardware_fit: true },
        { start_context: 112512, end_context: 262144, kind: 'hybrid', offload_mode: 'hybrid', gpu_count: 1, devices: ['CUDA1'], kv_on_gpu: false, current_fit: true, total_hardware_fit: true }
      ]
    })
    await wrapper.findAll('button').find((button: any) => button.text().includes('Refresh hardware'))!.trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="placement-range-compact"]').text()).toContain('3 GPUs')
    expect(wrapper.get('[data-testid="placement-range-compact"]').text()).toContain('Next transition')
    await wrapper.findAll('button').find((button: any) => button.text().includes('All placement ranges'))!.trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="placement-range-all"]').text()).toContain('5 GPUs')
    expect(wrapper.get('[data-testid="execution-fit"]').text()).toContain('Runs fully on 3 GPUs')
    expect(wrapper.get('[data-testid="execution-fit"]').text()).toContain('Using 3 of 5 GPUs')
    expect(wrapper.text()).toContain('Why are 3 GPUs being used?')
  })

  it('lets the context slider track freely and keeps ranges visible until commit', async () => {
    const zoneRanges = ranges({
      gpu_only_max_context: 14336,
      zones: [
        { start_context: 512, end_context: 3584, kind: 'gpu', offload_mode: 'full', gpu_count: 1, devices: ['CUDA1'], kv_on_gpu: true, current_fit: true, total_hardware_fit: true },
        { start_context: 4096, end_context: 14336, kind: 'gpu', offload_mode: 'multi_gpu', gpu_count: 2, devices: ['CUDA1', 'CUDA0'], kv_on_gpu: true, current_fit: true, total_hardware_fit: true },
        { start_context: 14848, end_context: 65536, kind: 'hybrid', offload_mode: 'hybrid', gpu_count: 1, devices: ['CUDA1'], kv_on_gpu: false, current_fit: true, total_hardware_fit: true },
        { start_context: 66048, end_context: 262144, kind: 'no_fit', offload_mode: 'none', gpu_count: 0, current_fit: false, total_hardware_fit: false }
      ]
    })
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/hardware') return hardware
      if (isConfig(path)) return { effective: { values: {} } }
      if (path.includes('/recommendation')) {
        const context = Number(new URL(path, 'http://local.test').searchParams.get('context_length'))
        return recommendation({ context_length: context || 4096, placement_ranges: zoneRanges })
      }
      throw new Error(`unexpected request ${path}`)
    })

    const wrapper = await mountSuspended(HardwarePlacementEditor, {
      route: false,
      props: { gpuMode: 'auto', gpuDevices: [], tensorSplit: '', modelId: 'model-1', llamaOptions: {} }
    })
    await flushPromises()
    expect(wrapper.find('[data-testid="placement-ranges"]').exists()).toBe(true)

    const slider = [
      ...wrapper.findAllComponents({ name: 'Slider' }),
      ...wrapper.findAllComponents({ name: 'USlider' })
    ][0]
    expect(slider).toBeTruthy()
    expect(slider!.props('step')).toBe(0.001)
    expect(slider!.props('max')).toBe(4)
    expect(wrapper.text()).toContain('65K')
    slider!.vm.$emit('update:modelValue', 4)
    await flushPromises()
    expect(wrapper.text()).toContain('Selected: 66,560 tokens')
    expect(wrapper.get('[data-testid="placement-zone-3"]').attributes('aria-pressed')).toBe('true')
    expect(slider!.props('size')).toBe('xl')
    expect(String(slider!.props('ui')?.thumb || '')).toContain('size-7')

    slider!.vm.$emit('update:modelValue', 1.5)
    await flushPromises()
    expect(wrapper.get('[data-testid="placement-zone-1"]').attributes('aria-pressed')).toBe('true')
    expect(wrapper.get('[data-testid="gpu-card-CUDA0"]').text()).toContain('Part of 2-GPU placement')
    expect(wrapper.get('[data-testid="gpu-card-CUDA1"]').text()).toContain('Part of 2-GPU placement')

    slider!.vm.$emit('update:modelValue', 1.99)
    await flushPromises()
    expect(wrapper.get('[data-testid="placement-zone-1"]').attributes('aria-pressed')).toBe('true')

    const twoGpuContext = 65000
    slider!.vm.$emit('update:modelValue', contextToSliderPosition(zoneRanges.zones, twoGpuContext))
    await flushPromises()
    expect(wrapper.text()).toContain('Selected: 65,000 tokens')
    expect(wrapper.emitted('update:contextSize')?.some(args => args[0] === '65024')).toBe(true)
    expect(wrapper.get('[data-testid="placement-zone-2"]').attributes('aria-pressed')).toBe('true')
    expect(wrapper.find('[data-testid="placement-ranges"]').exists()).toBe(true)

    await wrapper.setProps({ llamaOptions: { 'ctx-size': '65024' } })
    await flushPromises()
    expect(wrapper.text()).toContain('Selected: 65,000 tokens')

    await vi.waitFor(() => {
      expect(mocks.request).toHaveBeenCalledWith('/api/v1/models/model-1/recommendation?context_length=65024')
    })
    await flushPromises()
    expect(wrapper.text()).toContain('Selected: 65,024 tokens')
    expect(wrapper.find('[data-testid="placement-ranges"]').exists()).toBe(true)
  })

  it('does not treat automatic GPU cards as placement toggles', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/hardware') return hardware
      if (isConfig(path)) return { effective: { values: {} } }
      if (path.includes('/recommendation')) return recommendation()
      return {}
    })
    const wrapper = await mountSuspended(HardwarePlacementEditor, {
      route: false,
      props: { gpuMode: 'auto', gpuDevices: [], tensorSplit: '', modelId: 'model-1', llamaOptions: {} }
    })
    await flushPromises()
    expect(wrapper.get('[data-testid="gpu-card-CUDA1"]').element.tagName.toLowerCase()).toBe('div')
    await wrapper.get('[data-testid="gpu-card-CUDA1"]').trigger('click')
    await flushPromises()
    expect(wrapper.emitted('update:gpuMode') || []).toEqual([])
  })
})
