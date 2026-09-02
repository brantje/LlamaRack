import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import HardwarePlacementEditor from '~/components/HardwarePlacementEditor.vue'
import LlamaCppOptionsEditor from '~/components/LlamaCppOptionsEditor.vue'
import ModelsDiscover from '~/components/ModelsDiscover.vue'
import { useManager, type Profile } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

const TooltipStub = { props: ['text'], template: '<span><slot /></span>' }
const gib = 1024 ** 3

function seedManager(profile: Profile | null = null) {
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.models.value = []
  manager.instances.value = []
  manager.runtimes.value = {}
  manager.profile.value = profile
}

function moeRecommendation() {
  return {
    model_id: 'm1',
    context_length: 4096,
    context_capability: 32768,
    context_assumed: false,
    confidence: 'high',
    metadata: { architecture: 'qwen3moe', context_length: 32768, block_count: 48, embedding_length: 4096, head_count: 32, kv_head_count: 8, expert_count: 128 },
    quantization: { name: 'Q4_K_M', summary: 'Balanced quantization.', tradeoff: 'Balanced memory use.' },
    memory: { weights_bytes: 30 * gib, kv_cache_bytes: gib, runtime_overhead_bytes: 1536 * 1024 ** 2, cpu_only_ram_bytes: 33 * gib, full_offload_vram_bytes: 32 * gib },
    current_fit: true,
    total_hardware_fit: true,
    cpu_fit: true,
    offload: {
      mode: 'moe', gpu_layers: 48, n_cpu_moe: 17, devices: ['CUDA0', 'CUDA1'], tensor_split: '31,29', kv_on_gpu: true,
      reason: 'Full GPU offload does not fit; routed experts use system RAM while attention and KV stay on GPU.'
    },
    placement_ranges: {
      available: true,
      minimum_context: 512,
      maximum_context: 32768,
      context_step: 512,
      zones: [{
        start_context: 512, end_context: 32768, kind: 'moe', offload_mode: 'moe', gpu_count: 2,
        devices: ['CUDA0', 'CUDA1'], kv_on_gpu: true, gpu_layers: 48, n_cpu_moe: 17, tensor_split: '31,29', current_fit: true, total_hardware_fit: true
      }]
    }
  }
}

async function clickButtonContaining(wrapper: any, label: string) {
  const button = wrapper.findAll('button').find((item: any) => item.text().includes(label))
  expect(button, `button containing ${label}`).toBeTruthy()
  await button!.trigger('click')
  await flushPromises()
}

beforeEach(() => {
  mocks.request.mockReset()
  localStorage.clear()
  seedManager()
})

describe('MoE placement UI', () => {
  it('explains automatic all-free-GPU expert spill and exposes n-cpu-moe', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/hardware') return {
        gpus: [
          { id: 'CUDA0', backend: 'cuda', index: 0, name: 'GPU 0', total_bytes: 16 * gib, used_bytes: 2 * gib, free_bytes: 14 * gib, utilization_pct: 10 },
          { id: 'CUDA1', backend: 'cuda', index: 1, name: 'GPU 1', total_bytes: 16 * gib, used_bytes: 3 * gib, free_bytes: 13 * gib, utilization_pct: 20 }
        ]
      }
      if (path === '/api/v1/llamacpp/config?model_id=m1') return { effective: { values: { 'ctx-size': '4096' } } }
      if (path.startsWith('/api/v1/models/m1/recommendation?')) return moeRecommendation()
      return []
    })

    const wrapper = await mountSuspended(HardwarePlacementEditor, {
      route: false,
      props: { gpuMode: 'auto', gpuDevices: [], tensorSplit: '', modelId: 'm1', llamaOptions: {} }
    })
    await flushPromises()

    expect(wrapper.text()).toContain('every currently free GPU')
    expect(wrapper.text()).toContain('routed experts in system RAM')
    expect(wrapper.text()).toContain('2 GPUs + experts in RAM')
    expect(wrapper.text()).toContain('Context cache: GPU')
    expect(wrapper.text()).not.toContain('chooses the smallest GPU set that safely fits the model')

    await clickButtonContaining(wrapper, 'Technical details')
    expect(wrapper.text()).toContain('CPU expert blocks')
    expect(wrapper.text()).toContain('17')
  })

  it('renders Discover MoE as a warning placement with expert details', async () => {
    const artifact = {
      id: 'q4', name: 'model-Q4_K_M.gguf', quantization: 'Q4_K_M', model_bytes: 30 * gib, total_bytes: 30 * gib,
      shard_count: 1, expected_shards: 1, complete: true, files: [{ path: 'model-Q4_K_M.gguf', size: 30 * gib }]
    }
    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/huggingface/model?repo=')) return { id: 'acme/moe', downloads: 1, likes: 2, private: false, gated: false, revision: 'main', artifacts: [artifact] }
      if (path.startsWith('/api/v1/huggingface/recommendations?')) return {
        context_length: 4096,
        context_capability: 32768,
        context_assumed: false,
        metadata: { architecture: 'qwen3moe', context_length: 32768, block_count: 48, embedding_length: 4096, head_count: 32, kv_head_count: 8, expert_count: 128 },
        hardware_available: true,
        hybrid_recommendations_enabled: true,
        artifacts: [{
          artifact_id: 'q4',
          quantization: { name: 'Q4_K_M', tier: 'Balanced', quality: 'Balanced', memory: 'Moderate', speed: 'See generation estimate', summary: 'Balanced quantization.', tradeoff: 'Balanced memory use.', known: true },
          recommended: true,
          runnable: true,
          fit: 'moe',
          fit_label: '2 GPUs + experts in RAM',
          reason: 'Routed experts use system RAM while attention and KV remain on GPU.',
          memory: { weights_bytes: 30 * gib, kv_cache_bytes: gib, runtime_overhead_bytes: 1536 * 1024 ** 2, cpu_only_ram_bytes: 33 * gib, full_offload_vram_bytes: 32 * gib },
          offload: { mode: 'moe', gpu_layers: 48, n_cpu_moe: 17, devices: ['CUDA0', 'CUDA1'], tensor_split: '31,29', kv_on_gpu: true, reason: 'Routed experts use system RAM.' },
          estimated_generation_speed: { estimated: true, min_tokens_per_second: 10, max_tokens_per_second: 15, label: '~10–15 tok/s', reason: 'MoE bandwidth estimate.' },
          confidence: 'high'
        }]
      }
      return []
    })

    const wrapper = await mountSuspended(ModelsDiscover, {
      route: false,
      props: { repoId: 'acme/moe' },
      global: { stubs: { UTooltip: TooltipStub } }
    })
    await flushPromises()

    expect(wrapper.text()).toContain('2 GPUs + experts in RAM')
    const tag = wrapper.findAllComponents({ name: 'StatusTag' }).find(component => component.attributes('data-testid') === 'artifact-hardware-fit')
    expect(tag).toBeTruthy()
    expect(tag!.props('variant')).toBe('pending')

    await clickButtonContaining(wrapper, 'Advanced details')
    expect(wrapper.text()).toContain('CPU expert blocks')
    expect(wrapper.text()).toContain('17')
    expect(wrapper.text()).toContain('CUDA0, CUDA1')
  })

  it('keeps n-cpu-moe and cpu-moe editable in the basic llama.cpp view', async () => {
    const profile = {
      path: '/app/llama-server',
      version: 'moe-test',
      fingerprint: 'moe-test',
      options: [
        { key: 'n-cpu-moe', value_hint: 'N', description: 'CPU MoE blocks', kind: 'integer', manager_owned: false },
        { key: 'cpu-moe', description: 'All routed experts on CPU', kind: 'boolean', manager_owned: false }
      ]
    } satisfies Profile
    seedManager(profile)
    mocks.request.mockResolvedValue({
      profile,
      effective: { global: {}, model: {}, instance: {}, values: {}, sources: {} }
    })

    const wrapper = await mountSuspended(LlamaCppOptionsEditor, {
      route: false,
      props: { modelValue: {}, scope: 'instance', modelId: 'm1', instanceId: 'i1' }
    })
    await flushPromises()

    expect(wrapper.text()).toContain('--n-cpu-moe')
    expect(wrapper.text()).toContain('--cpu-moe')
    expect(wrapper.text()).not.toContain('Manager controlled')
  })
})
