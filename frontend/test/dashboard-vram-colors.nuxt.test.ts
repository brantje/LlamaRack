import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import DashboardPage from '~/pages/index.vue'
import { useManager, type Instance } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

const gib = 1024 ** 3

function instance(id: string): Instance {
  return { id, model_id: 'm1', name: id, enabled: true, autoload_enabled: true, always_on: false, priority: 'normal', eviction_enabled: true, idle_unload_seconds: 0, gpu_mode: 'auto', gpu_devices: [], tensor_split: '', request_log_mode: 'metadata' }
}

beforeEach(() => {
  mocks.request.mockReset()
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.models.value = [{ id: 'm1', name: 'Model', gguf_path: 'model.gguf', total_bytes: 1, context_length: 8192 }]
  manager.instances.value = [instance('gemma-4-26b-a4b'), instance('gpt-oss-20b')]
  manager.runtimes.value = { m1: [] }
  manager.runtimeTelemetry.value = {}
  manager.observabilityLive.value = {
    collected_at: '2026-08-28T10:00:00Z',
    hardware: {
      ram_total_bytes: 64 * gib,
      ram_available_bytes: 32 * gib,
      collected_at: '2026-08-28T10:00:00Z',
      processes: [],
      gpus: [
        { id: 'CUDA0', backend: 'cuda', index: 0, name: 'RTX 4060 Ti', total_bytes: 16 * gib, used_bytes: 15 * gib, free_bytes: gib, utilization_pct: 0 },
        { id: 'CUDA1', backend: 'cuda', index: 1, name: 'RTX 4060 Ti', total_bytes: 16 * gib, used_bytes: 15 * gib, free_bytes: gib, utilization_pct: 0 }
      ]
    },
    telemetry: [
      { instance_id: 'gemma-4-26b-a4b', pid: 100, gpu_devices: ['CUDA0', 'CUDA1'], gpus: [{ device_id: 'CUDA0', vram_used_bytes: 15 * gib }, { device_id: 'CUDA1', vram_used_bytes: 948 * 1024 ** 2 }], vram_used_bytes: 15 * gib + 948 * 1024 ** 2, collected_at: '2026-08-28T10:00:00Z' },
      { instance_id: 'gpt-oss-20b', pid: 101, gpu_devices: ['CUDA0', 'CUDA1'], gpus: [{ device_id: 'CUDA0', vram_used_bytes: 120 * 1024 ** 2 }, { device_id: 'CUDA1', vram_used_bytes: 14 * gib }], vram_used_bytes: 14 * gib + 120 * 1024 ** 2, collected_at: '2026-08-28T10:00:00Z' }
    ]
  }
})

describe('Dashboard VRAM allocation colors', () => {
  it('keeps Instance colors consistent across GPUs and Free on the neutral scale', async () => {
    const manager = useManager()
    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/observability/summary')) return { requests: 0, active_api_keys: 0, lifecycle: { autoloads: 0, failed_starts: 0, load_duration_ms_total: 0 }, hardware: { hardware: manager.observabilityLive.value!.hardware, telemetry: manager.observabilityLive.value!.telemetry } }
      if (path.startsWith('/api/v1/observability/requests')) return { items: [] }
      if (path === '/api/v1/settings/general') return { idle_unload_seconds: { value: 0, source: 'default', editable: true } }
      return {}
    })

    const wrapper = await mountSuspended(DashboardPage, { route: '/' })
    await flushPromises()

    const gemma0 = wrapper.get('[data-testid="gpu-progress-CUDA0"] [data-vram-label="gemma-4-26b-a4b"]')
    const gemma1 = wrapper.get('[data-testid="gpu-progress-CUDA1"] [data-vram-label="gemma-4-26b-a4b"]')
    const gpt0 = wrapper.get('[data-testid="gpu-progress-CUDA0"] [data-vram-label="gpt-oss-20b"]')
    const gpt1 = wrapper.get('[data-testid="gpu-progress-CUDA1"] [data-vram-label="gpt-oss-20b"]')
    const free0 = wrapper.get('[data-testid="gpu-progress-CUDA0"] [data-vram-label="Free"]')

    expect(gemma0.attributes('data-vram-color')).toBe(gemma1.attributes('data-vram-color'))
    expect(gpt0.attributes('data-vram-color')).toBe(gpt1.attributes('data-vram-color'))
    expect(gemma0.attributes('data-vram-color')).not.toBe(gpt0.attributes('data-vram-color'))
    expect(free0.attributes('data-vram-color')).toBe('neutral-500')
  })
})
