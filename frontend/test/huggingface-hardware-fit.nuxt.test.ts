import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import ModelsDiscover from '~/components/ModelsDiscover.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

const TooltipStub = { props: ['text'], template: '<span><slot /></span>' }
const mountDiscover = (options: Record<string, any> = {}) => mountSuspended(ModelsDiscover, {
  route: false,
  ...options,
  global: { ...(options.global || {}), stubs: { ...(options.global?.stubs || {}), UTooltip: TooltipStub } }
})

function component(wrapper: any, names: string[]) {
  for (const name of names) {
    const found = wrapper.findAllComponents({ name })[0]
    if (found) return found
  }
  return undefined
}

const gib = 1024 ** 3
const artifacts = [
  { id: 'single', name: 'single-Q4_K_M.gguf', quantization: 'Q4_K_M', model_bytes: 4 * gib, total_bytes: 4 * gib, shard_count: 1, expected_shards: 1, complete: true, files: [{ path: 'single-Q4_K_M.gguf', size: 4 * gib }] },
  { id: 'multi', name: 'multi-Q5_K_M.gguf', quantization: 'Q5_K_M', model_bytes: 10 * gib, total_bytes: 10 * gib, shard_count: 1, expected_shards: 1, complete: true, files: [{ path: 'multi-Q5_K_M.gguf', size: 10 * gib }] },
  { id: 'split', name: 'split-Q8_0.gguf', quantization: 'Q8_0', model_bytes: 20 * gib, total_bytes: 20 * gib, shard_count: 1, expected_shards: 1, complete: true, files: [{ path: 'split-Q8_0.gguf', size: 20 * gib }] },
  { id: 'unknown', name: 'unknown.gguf', model_bytes: 0, total_bytes: 0, shard_count: 1, expected_shards: 1, complete: true, files: [{ path: 'unknown.gguf', size: 0 }] }
]

function advice(id: string, quantization: any, fit: string, fitLabel: string, recommended = false) {
  return {
    artifact_id: id,
    quantization,
    recommended,
    runnable: fit !== 'unknown' && fit !== 'no_fit',
    fit,
    fit_label: fitLabel,
    reason: `${fitLabel} because of the current context and available memory.`,
    memory: { weights_bytes: 4 * gib, kv_cache_bytes: gib, runtime_overhead_bytes: 256 * 1024 ** 2, cpu_only_ram_bytes: 6 * gib, full_offload_vram_bytes: 6 * gib },
    offload: { mode: fit === 'gpu' ? 'full' : fit === 'multi_gpu' ? 'multi_gpu' : fit === 'hybrid' ? 'hybrid' : fit === 'cpu' ? 'cpu' : '', kv_on_gpu: fit === 'gpu' || fit === 'multi_gpu', reason: fitLabel },
    confidence: fit === 'unknown' ? 'low' : 'high',
    warnings: quantization.warning ? [quantization.warning] : []
  }
}

const guides = {
  q4: { name: 'Q4_K_M', tier: 'Balanced', quality: 'Balanced', memory: 'Moderate', speed: 'Good general-purpose balance', summary: 'Balanced quantization.', tradeoff: 'General purpose.', known: true },
  q5: { name: 'Q5_K_M', tier: 'High quality', quality: 'High', memory: 'Moderate-high', speed: 'Hardware-dependent', summary: 'Higher fidelity.', tradeoff: 'Uses more memory.', known: true },
  q8: { name: 'Q8_0', tier: 'Maximum quality', quality: 'Maximum', memory: 'Very high', speed: 'Hardware-dependent', summary: 'Large quantization.', tradeoff: 'Uses substantially more memory.', warning: 'Q8 usually offers a small quality gain over Q6 for substantially more memory.', known: true },
  unknown: { tier: 'Unknown profile', quality: 'Unknown', memory: 'Unknown', speed: 'Hardware-dependent', summary: 'Quantization is unknown.', tradeoff: 'Quality and speed vary.', known: false }
}

function recommendationResponse() {
  return {
    context_length: 4096,
    context_capability: 131072,
    context_assumed: true,
    metadata: { architecture: 'llama', context_length: 131072, block_count: 32, embedding_length: 4096, head_count: 32, kv_head_count: 8 },
    hardware_available: true,
    hybrid_recommendations_enabled: true,
    artifacts: [
      advice('single', guides.q4, 'gpu', 'Fits on GPU', true),
      advice('multi', guides.q5, 'multi_gpu', 'Fits across GPUs'),
      advice('split', guides.q8, 'hybrid', 'GPU + CPU'),
      advice('unknown', guides.unknown, 'unknown', 'Fit unknown')
    ]
  }
}

function seedManager() {
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.models.value = []
  manager.instances.value = []
  manager.runtimes.value = {}
}

beforeEach(() => {
  mocks.request.mockReset()
  seedManager()
})

describe('Hugging Face GGUF hardware recommendations', () => {
  it('keeps raw labels visible while explaining quality, memory, speed and hardware fit', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/huggingface/search?')) return { items: [{ id: 'acme/demo', downloads: 1, likes: 2, parameter_count: 27_000_000_000, private: false, gated: false, tags: ['gguf'] }] }
      if (path.startsWith('/api/v1/huggingface/model?repo=')) return { id: 'acme/demo', downloads: 1, likes: 2, private: false, gated: false, revision: 'r1', artifacts }
      if (path.startsWith('/api/v1/huggingface/recommendations?')) return recommendationResponse()
      return []
    })

    const list = await mountDiscover()
    await list.find('form').trigger('submit')
    await flushPromises()
    expect(list.text()).toContain('Model size 27B params')
    list.unmount()

    const wrapper = await mountDiscover({ props: { repoId: 'acme/demo' } })
    await flushPromises()

    const repositoryMetadata = wrapper.get('[data-testid="repository-metadata"]')
    expect(repositoryMetadata.text()).toContain('Model size')
    expect(repositoryMetadata.text()).toContain('27B params')
    expect(repositoryMetadata.text()).toContain('Architecture')
    expect(repositoryMetadata.text()).toContain('llama')
    expect(repositoryMetadata.text()).toContain('Context capability')
    expect(repositoryMetadata.text()).toContain('128K')
    expect(wrapper.text()).toContain('Recommended')
    expect(wrapper.text()).toContain('Balanced')
    expect(wrapper.text()).toContain('High quality')
    expect(wrapper.text()).toContain('Maximum quality')
    expect(wrapper.text()).toContain('Q4_K_M')
    expect(wrapper.text()).toContain('Q8_0')
    expect(wrapper.text()).toContain('Fits on GPU')
    expect(wrapper.text()).toContain('Fits across GPUs')
    expect(wrapper.text()).toContain('GPU + CPU')
    expect(wrapper.text()).toContain('Fit unknown')
    expect(wrapper.text()).toContain('Temporary context assumption')
    expect(wrapper.text()).toContain('Detected capability: 128K')
    expect(wrapper.text()).toContain('Q8 usually offers a small quality gain')
    expect(wrapper.findAll('[data-testid="artifact-hardware-fit"]')).toHaveLength(4)
    expect(wrapper.findAll('[data-testid^="artifact-"]').filter(node => node.text().includes('Recommended'))).toHaveLength(1)

    const recommendedArtifact = wrapper.get('[data-testid="artifact-single"]')
    expect(recommendedArtifact.attributes('data-recommended')).toBe('true')
    expect(recommendedArtifact.classes()).not.toContain('border-primary')
    expect(recommendedArtifact.classes()).not.toContain('border-l-2')
    expect(recommendedArtifact.classes()).toContain('bg-primary/5')
    expect(wrapper.get('[data-testid="recommended-badge"]').text()).toBe('Recommended')
    expect(wrapper.get('[data-testid="artifact-multi"]').attributes('data-recommended')).toBeUndefined()
    wrapper.unmount()
  })

  it('shows generic guidance when hardware telemetry is unavailable', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/huggingface/model?repo=')) return { id: 'acme/demo', downloads: 1, likes: 2, private: false, gated: false, revision: 'r1', artifacts: [artifacts[0]] }
      if (path.startsWith('/api/v1/huggingface/recommendations?')) return {
        ...recommendationResponse(), hardware_available: false, hardware_warning: 'probe failed', artifacts: [advice('single', guides.q4, 'unknown', 'Fit unknown')]
      }
      return []
    })

    const wrapper = await mountDiscover({ props: { repoId: 'acme/demo' } })
    await flushPromises()
    expect(wrapper.text()).toContain('Hardware-aware recommendation unavailable')
    expect(wrapper.get('[data-testid="artifact-hardware-fit"]').text()).toContain('Fit unknown')
    wrapper.unmount()
  })

  it('renders placement, artifact and metadata edge states and recalculates context', async () => {
    const edgeArtifacts = [
      { id: 'gpu', name: 'gpu-Q4.gguf', quantization: 'Q4_K_M', model_bytes: 4 * gib, total_bytes: 4 * gib, shard_count: 2, expected_shards: 2, complete: true, files: [{ path: 'gpu-1.gguf', size: 2 * gib }, { path: 'gpu-2.gguf', size: 2 * gib }] },
      { id: 'multi-edge', name: 'multi-Q5.gguf', quantization: 'Q5_K_M', model_bytes: 6 * gib, total_bytes: 6 * gib, shard_count: 1, expected_shards: 1, complete: true, files: [{ path: 'multi-Q5.gguf', size: 6 * gib }] },
      { id: 'hybrid-edge', name: 'hybrid-Q8.gguf', quantization: 'Q8_0', model_bytes: 8 * gib, total_bytes: 8 * gib, shard_count: 1, expected_shards: 1, complete: true, files: [{ path: 'hybrid-Q8.gguf', size: 8 * gib }] },
      { id: 'cpu-edge', name: 'cpu-Q4.gguf', quantization: 'Q4_K_M', model_bytes: 4 * gib, total_bytes: 4 * gib + 1024, shard_count: 1, expected_shards: 1, complete: true, files: [{ path: 'cpu-Q4.gguf', size: 4 * gib }], dependencies: [{ kind: 'adapter', name: 'helper.gguf', total_bytes: 1024, files: [{ path: 'helper.gguf', size: 1024 }] }] },
      { id: 'no-fit-edge', name: 'oversized.gguf', model_bytes: 30 * gib, total_bytes: 30 * gib, shard_count: 1, expected_shards: 1, complete: true, files: [{ path: 'oversized.gguf', size: 30 * gib }] },
      { id: 'no-advice', name: 'broken.gguf', model_bytes: 0, total_bytes: 0, shard_count: 1, expected_shards: 2, complete: false, files: [{ path: 'broken.gguf', size: 0 }] }
    ]

    const responseFor = (contextLength = 5000) => {
      const gpu = advice('gpu', guides.q4, 'gpu', 'Fits on GPU', true)
      gpu.offload = { ...gpu.offload, gpu_layers: 32, devices: ['CUDA0'], tensor_split: '1.0' }
      const multi = advice('multi-edge', guides.q5, 'multi_gpu', 'Fits across GPUs')
      multi.offload = { ...multi.offload, gpu_layers: 32, devices: ['CUDA0', 'CUDA1'], tensor_split: '0.5,0.5' }
      const hybrid = advice('hybrid-edge', guides.q8, 'hybrid', 'GPU + CPU')
      hybrid.offload = { ...hybrid.offload, gpu_layers: 20, devices: ['CUDA0'], tensor_split: '' }
      const cpu = advice('cpu-edge', guides.q4, 'cpu', 'CPU only')
      const noFit = advice('no-fit-edge', guides.unknown, 'no_fit', "Doesn't fit")
      return {
        context_length: contextLength,
        context_capability: 100000,
        context_assumed: false,
        metadata: { architecture: 'llama', context_length: 100000, block_count: 32, embedding_length: 4096, head_count: 32, kv_head_count: 8 },
        metadata_warning: 'Some optional GGUF metadata is unavailable.',
        hardware_available: true,
        hybrid_recommendations_enabled: true,
        artifacts: [gpu, multi, hybrid, cpu, noFit]
      }
    }

    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/huggingface/model?repo=')) return {
        id: 'acme/edge', downloads: 3, likes: 4, parameter_count: 999, private: true, gated: true,
        description: 'Edge-state repository', revision: 'r2', artifacts: edgeArtifacts
      }
      if (path.startsWith('/api/v1/huggingface/recommendations?')) {
        const parsed = new URL(`http://manager.test${path}`)
        return responseFor(Number(parsed.searchParams.get('context_length')) || 5000)
      }
      if (path === '/api/v1/downloads') return { id: 'download' }
      return []
    })

    const wrapper = await mountDiscover({ props: { repoId: 'acme/edge' } })
    await flushPromises()

    expect(wrapper.text()).toContain('Access may require approval')
    expect(wrapper.text()).toContain('Edge-state repository')
    expect(wrapper.text()).toContain('Some optional GGUF metadata is unavailable.')
    expect(wrapper.text()).toContain('Detected capability: 98K')
    expect(wrapper.text()).toContain('2 shards')
    expect(wrapper.text()).toContain('Incomplete split')
    expect(wrapper.text()).toContain('Quantization details unavailable')
    expect(wrapper.text()).toContain('Unknown size')
    expect(wrapper.text()).toContain('CPU only')
    expect(wrapper.text()).toContain("Doesn't fit")
    expect(wrapper.text()).toContain('adapter')
    expect(wrapper.findAll('[data-testid="artifact-hardware-fit"]')).toHaveLength(6)

    const advanced = wrapper.findAll('button').filter(button => button.text().trim() === 'Advanced details')
    expect(advanced.length).toBeGreaterThan(0)
    await advanced[0].trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('CUDA0')

    const slider = component(wrapper, ['Slider', 'USlider'])
    expect(slider).toBeTruthy()
    slider.vm.$emit('update:modelValue', [2])
    await new Promise(resolve => setTimeout(resolve, 300))
    await flushPromises()
    expect(mocks.request.mock.calls.some(([path]) => String(path).includes('context_length=8192'))).toBe(true)

    slider.vm.$emit('update:modelValue', 3)
    await new Promise(resolve => setTimeout(resolve, 300))
    await flushPromises()
    expect(mocks.request.mock.calls.some(([path]) => String(path).includes('context_length=32768'))).toBe(true)
    expect(wrapper.text()).not.toContain('Temporary context assumption')
    wrapper.unmount()
  })

  it('surfaces recommendation and detail failures and handles an empty repository', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/huggingface/model?repo=')) return { id: 'acme/empty', downloads: 0, likes: 0, private: false, gated: false, revision: 'r1', artifacts: [] }
      if (path.startsWith('/api/v1/huggingface/recommendations?')) return []
      return []
    })
    const invalid = await mountDiscover({ props: { repoId: 'acme/empty' } })
    await flushPromises()
    expect(invalid.text()).toContain('Context-aware recommendation data is unavailable')
    expect(invalid.text()).toContain('No GGUF model files found')
    invalid.unmount()

    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/huggingface/model?repo=')) return { id: 'acme/rejected', downloads: 0, likes: 0, private: false, gated: false, revision: 'r1', artifacts: [artifacts[0]] }
      if (path.startsWith('/api/v1/huggingface/recommendations?')) throw { data: { error: 'recommendations denied' } }
      return []
    })
    const rejected = await mountDiscover({ props: { repoId: 'acme/rejected' } })
    await flushPromises()
    expect(rejected.text()).toContain('recommendations denied')
    expect(rejected.get('[data-testid="artifact-hardware-fit"]').text()).toContain('Fit unknown')
    rejected.unmount()

    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/huggingface/model?repo=')) throw new Error('detail exploded')
      return []
    })
    const detailFailure = await mountDiscover({ props: { repoId: 'acme/failure' } })
    await flushPromises()
    expect(detailFailure.text()).toContain('detail exploded')
    detailFailure.unmount()
  })
})