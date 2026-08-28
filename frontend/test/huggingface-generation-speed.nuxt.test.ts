import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import ModelsDiscover from '~/components/ModelsDiscover.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

const TooltipStub = { props: ['text'], template: '<span><slot /></span>' }
const gib = 1024 ** 3
const artifact = {
  id: 'ridge',
  name: 'Qwen3.8-27B-Ridge-3.7bpw.gguf',
  quantization: '3.69BPW',
  model_bytes: 12 * gib,
  total_bytes: 12 * gib,
  shard_count: 1,
  expected_shards: 1,
  complete: true,
  files: [{ path: 'Qwen3.8-27B-Ridge-3.7bpw.gguf', size: 12 * gib }]
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

function recommendation(context = 4096, label = '~10–16 tok/s') {
  return {
    context_length: context,
    context_capability: 262144,
    context_assumed: false,
    metadata: {
      architecture: 'qwen35', context_length: 262144, block_count: 65,
      embedding_length: 5120, head_count: 24, kv_head_count: 4
    },
    hardware_available: true,
    hybrid_recommendations_enabled: true,
    artifacts: [{
      artifact_id: 'ridge',
      quantization: {
        name: '3.69BPW', tier: 'Mixed quantization', quality: 'Recipe-dependent', memory: 'Low',
        speed: 'See generation estimate', summary: 'Mixed quantization averaging 3.69 bits per weight.',
        tradeoff: 'BPW is a size signal.', known: true
      },
      recommended: true,
      runnable: true,
      fit: 'gpu',
      fit_label: 'Fits on GPU',
      reason: 'The model fits on CUDA0.',
      memory: {
        weights_bytes: 12 * gib, kv_cache_bytes: context * 1024,
        runtime_overhead_bytes: 600 * 1024 ** 2,
        cpu_only_ram_bytes: 14 * gib, full_offload_vram_bytes: 14 * gib
      },
      offload: { mode: 'full', gpu_layers: 65, devices: ['CUDA0'], kv_on_gpu: true, reason: 'Fits.' },
      estimated_generation_speed: {
        estimated: true,
        min_tokens_per_second: 10,
        max_tokens_per_second: 16,
        label,
        reason: 'Bandwidth-limited generation/decode estimate using CUDA0 288 GB/s theoretical VRAM bandwidth.'
      },
      confidence: 'high'
    }]
  }
}

async function mountDiscover() {
  return mountSuspended(ModelsDiscover, {
    route: false,
    props: { repoId: 'empero-ai/Qwen3.8-27B-Ridge-GGUF' },
    global: { stubs: { UTooltip: TooltipStub } }
  })
}

beforeEach(() => {
  mocks.request.mockReset()
  seedManager()
})

describe('Hugging Face estimated generation speed', () => {
  it('renders backend tok/s guidance instead of a generic hardware-dependent label', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/huggingface/model?repo=')) {
        return {
          id: 'empero-ai/Qwen3.8-27B-Ridge-GGUF', downloads: 1, likes: 1,
          private: false, gated: false, revision: 'ridge', parameter_count: 27_320_697_856,
          artifacts: [artifact]
        }
      }
      if (path.startsWith('/api/v1/huggingface/recommendations?')) return recommendation()
      return []
    })

    const wrapper = await mountDiscover()
    await flushPromises()

    expect(wrapper.get('[data-testid="artifact-generation-speed"]').text()).toBe('~10–16 tok/s')
    expect(wrapper.text()).toContain('Estimated Generation')
    expect(wrapper.text()).not.toContain('Hardware-dependent')

    const advanced = wrapper.findAll('button').find(button => button.text().trim() === 'Advanced details')
    expect(advanced).toBeTruthy()
    await advanced!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Estimated Generation')
    expect(wrapper.text()).toContain('Generation estimate basis')
    wrapper.unmount()
  })

  it('updates the displayed generation estimate when context is recalculated', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/huggingface/model?repo=')) {
        return {
          id: 'empero-ai/Qwen3.8-27B-Ridge-GGUF', downloads: 1, likes: 1,
          private: false, gated: false, revision: 'ridge', artifacts: [artifact]
        }
      }
      if (path.startsWith('/api/v1/huggingface/recommendations?')) {
        const url = new URL(`http://manager.test${path}`)
        const context = Number(url.searchParams.get('context_length')) || 4096
        return context > 4096 ? recommendation(context, '~7–11 tok/s') : recommendation()
      }
      return []
    })

    const wrapper = await mountDiscover()
    await flushPromises()
    expect(wrapper.get('[data-testid="artifact-generation-speed"]').text()).toBe('~10–16 tok/s')

    const slider = wrapper.findAllComponents({ name: 'Slider' })[0] || wrapper.findAllComponents({ name: 'USlider' })[0]
    expect(slider).toBeTruthy()
    slider!.vm.$emit('update:modelValue', 1)
    await new Promise(resolve => setTimeout(resolve, 300))
    await flushPromises()

    expect(mocks.request.mock.calls.some(([path]) => String(path).includes('context_length=8192'))).toBe(true)
    expect(wrapper.get('[data-testid="artifact-generation-speed"]').text()).toBe('~7–11 tok/s')
    wrapper.unmount()
  })

  it('shows an honest unavailable state when bandwidth telemetry is missing', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/huggingface/model?repo=')) {
        return {
          id: 'empero-ai/Qwen3.8-27B-Ridge-GGUF', downloads: 1, likes: 1,
          private: false, gated: false, revision: 'ridge', artifacts: [artifact]
        }
      }
      if (path.startsWith('/api/v1/huggingface/recommendations?')) {
        const value = recommendation()
        value.artifacts[0].estimated_generation_speed = {
          estimated: false,
          label: 'Estimate unavailable',
          reason: 'GPU memory-bandwidth telemetry is unavailable for CUDA0.'
        } as any
        return value
      }
      return []
    })

    const wrapper = await mountDiscover()
    await flushPromises()
    expect(wrapper.get('[data-testid="artifact-generation-speed"]').text()).toBe('Estimate unavailable')
    wrapper.unmount()
  })
})
