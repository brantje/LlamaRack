import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import ModelsDiscover from '~/components/ModelsDiscover.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

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

    const list = await mountSuspended(ModelsDiscover, { route: false })
    await list.find('form').trigger('submit')
    await flushPromises()
    expect(list.text()).toContain('Model size 27B params')
    list.unmount()

    const wrapper = await mountSuspended(ModelsDiscover, { props: { repoId: 'acme/demo' }, route: false })
    await flushPromises()

    expect(wrapper.text()).toContain('Hugging Face GGUF metadata: 27B params')
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

    const wrapper = await mountSuspended(ModelsDiscover, { props: { repoId: 'acme/demo' }, route: false })
    await flushPromises()
    expect(wrapper.text()).toContain('Hardware-aware recommendation unavailable')
    expect(wrapper.get('[data-testid="artifact-hardware-fit"]').text()).toContain('Fit unknown')
    wrapper.unmount()
  })
})
