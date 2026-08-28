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

describe('Hugging Face GGUF hardware fit', () => {
  it('keeps cached HF GGUF metadata visible and classifies single-GPU, multi-GPU and CPU-split weight fits', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/huggingface/search?')) return { items: [{
        id: 'acme/demo', downloads: 1, likes: 2, parameter_count: 27_000_000_000, private: false, gated: false, tags: ['gguf']
      }] }
      if (path.startsWith('/api/v1/huggingface/model?repo=')) return {
        id: 'acme/demo', downloads: 1, likes: 2, private: false, gated: false, revision: 'r1', artifacts
      }
      if (path === '/api/v1/hardware') return {
        ram_available_bytes: 64 * gib,
        gpus: [
          { id: 'CUDA0', name: 'GPU 0', total_bytes: 16 * gib, free_bytes: 8 * gib },
          { id: 'CUDA1', name: 'GPU 1', total_bytes: 16 * gib, free_bytes: 8 * gib }
        ]
      }
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
    expect(wrapper.text()).toContain('GPU-only weight fit')
    expect(wrapper.text()).toContain('GPU-only weights · multi-GPU')
    expect(wrapper.text()).toContain('GPU + CPU split likely')
    expect(wrapper.text()).toContain('Hardware fit unavailable')
    expect(wrapper.text()).toContain('Context/KV is checked at Launch')
    expect(wrapper.findAll('[data-testid="artifact-hardware-fit"]')).toHaveLength(4)
    wrapper.unmount()
  })

  it('shows CPU-only or unavailable guidance when GPUs or hardware data are unavailable', async () => {
    let hardwareMode: 'cpu' | 'error' = 'cpu'
    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/huggingface/model?repo=')) return { id: 'acme/demo', downloads: 1, likes: 2, private: false, gated: false, revision: 'r1', artifacts: [artifacts[0]] }
      if (path === '/api/v1/hardware') {
        if (hardwareMode === 'error') throw new Error('probe failed')
        return { ram_available_bytes: 32 * gib, gpus: [] }
      }
      return []
    })

    const cpu = await mountSuspended(ModelsDiscover, { props: { repoId: 'acme/demo' }, route: false })
    await flushPromises()
    expect(cpu.get('[data-testid="artifact-hardware-fit"]').text()).toContain('CPU only')
    expect(cpu.text()).toContain('No NVIDIA/ROCm GPU is currently detected')
    cpu.unmount()

    hardwareMode = 'error'
    const unavailable = await mountSuspended(ModelsDiscover, { props: { repoId: 'acme/demo' }, route: false })
    await flushPromises()
    expect(unavailable.get('[data-testid="artifact-hardware-fit"]').text()).toContain('Hardware fit unavailable')
    unavailable.unmount()
  })
})
