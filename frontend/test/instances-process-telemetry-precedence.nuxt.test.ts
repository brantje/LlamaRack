import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import InstancesPage from '~/pages/instances/index.vue'
import { useManager, type Instance, type RuntimeTelemetry } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

const gib = 1024 ** 3

function instance(): Instance {
  return {
    id: 'ready',
    model_id: 'm1',
    name: 'ready',
    enabled: true,
    autoload_enabled: true,
    always_on: false,
    priority: 'normal',
    eviction_enabled: true,
    idle_unload_seconds: 0,
    gpu_mode: 'auto',
    gpu_devices: ['CUDA0', 'CUDA1'],
    request_log_mode: 'metadata'
  }
}

function telemetry(): RuntimeTelemetry {
  return {
    instance_id: 'ready',
    pid: 42,
    gpu_devices: ['CUDA0', 'CUDA1'],
    gpus: [
      { device_id: 'CUDA0', utilization_pct: 20, vram_used_bytes: gib },
      { device_id: 'CUDA1', utilization_pct: 40, vram_used_bytes: 2 * gib }
    ],
    gpu_utilization_pct: 91,
    vram_used_bytes: 12 * gib,
    cpu_percent: 35,
    memory_used_bytes: 3 * gib,
    collected_at: '2026-08-31T08:00:00Z'
  }
}

beforeEach(() => {
  mocks.request.mockReset()
  mocks.request.mockImplementation(async (path: string) => {
    if (path === '/api/v1/imports') return []
    if (path === '/api/v1/settings/general') return { idle_unload_seconds: { value: 300 } }
    return []
  })
  sessionStorage.clear()

  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.models.value = [{ id: 'm1', name: 'Coder Model', gguf_path: 'coder.gguf', total_bytes: 1, context_length: 8192 }]
  manager.instances.value = [instance()]
  manager.runtimes.value = { m1: [{ instance_id: 'ready', model_id: 'm1', state: 'READY', pid: 42, port: 9010 }] }
  manager.runtimeTelemetry.value = { ready: telemetry() }
  manager.observabilityLive.value = {
    collected_at: '2026-08-31T08:00:00Z',
    hardware: {
      ram_total_bytes: 32 * gib,
      ram_available_bytes: 16 * gib,
      collected_at: '2026-08-31T08:00:00Z',
      processes: [],
      gpus: [
        { id: 'CUDA0', backend: 'cuda', index: 0, name: 'GPU 0', total_bytes: 16 * gib, used_bytes: 9 * gib, free_bytes: 7 * gib, utilization_pct: 70 },
        { id: 'CUDA1', backend: 'cuda', index: 1, name: 'GPU 1', total_bytes: 16 * gib, used_bytes: 8 * gib, free_bytes: 8 * gib, utilization_pct: 65 }
      ]
    },
    telemetry: [manager.runtimeTelemetry.value.ready!],
    gateway: { since: 0, requests: 0, successes: 0, errors: 0, active: 0, queued: 0, active_api_keys: 0, prompt_tokens: 0, generated_tokens: 0, total_tokens: 0, latency_ms: {}, ttft_ms: {} },
    requests: []
  }
})

describe('Instances process telemetry precedence', () => {
  it('displays aggregated process GPU metrics instead of conflicting global fallbacks', async () => {
    const wrapper = await mountSuspended(InstancesPage, { route: '/instances' })
    await flushPromises()

    const readyRow = wrapper.get('tr[data-instance-state="READY"]')
    expect(readyRow.text()).toContain('30%')
    expect(readyRow.text()).toContain('3.0 GiB')
    expect(readyRow.text()).not.toContain('91%')
    expect(readyRow.text()).not.toContain('12 GiB')
    expect(readyRow.text()).not.toContain('12.0 GiB')
    expect(readyRow.text()).not.toContain('global')
  })
})
