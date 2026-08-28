import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountSuspended } from '@nuxt/test-utils/runtime'
import { mockNuxtImport } from '@nuxt/test-utils/runtime'
import InstanceDetailPage from '~/pages/instances/[id]/detail.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

const gib = 1024 ** 3

function seed() {
  const manager = useManager()
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.models.value = [{ id: 'm1', name: 'Gemma', gguf_path: 'gemma.gguf', total_bytes: 1, context_length: 32768 }]
  manager.instances.value = [{ id: 'gemma-4', model_id: 'm1', name: 'Gemma 4', enabled: true, autoload_enabled: true, always_on: false, priority: 'normal', eviction_enabled: true, idle_unload_seconds: 0, gpu_mode: 'auto', gpu_devices: [], request_log_mode: 'metadata' }]
  manager.runtimes.value = { m1: [{ instance_id: 'gemma-4', model_id: 'm1', state: 'READY', pid: 308, port: 12001 }] }
  manager.runtimeTelemetry.value = {
    'gemma-4': {
      instance_id: 'gemma-4', pid: 308, gpu_devices: ['CUDA0'],
      gpus: [{ device_id: 'CUDA0', vram_used_bytes: 14 * gib }],
      vram_used_bytes: 14 * gib, gpu_utilization_pct: 97, cpu_percent: 125, memory_used_bytes: 2.5 * gib,
      collected_at: '2026-08-27T18:30:00Z',
      llama_metrics: {
        prompt_tokens_total: 120, prompt_seconds_total: 4, prompt_tokens_per_second: 30,
        predicted_tokens_total: 210, predicted_seconds_total: 7, predicted_tokens_per_second: 52.9,
        requests_processing: 2, requests_deferred: 1, context_tokens_max: 8192,
        decode_total: 70, busy_slots_per_decode: 1.5,
        spec_draft_tokens_total: 100, spec_accepted_tokens_total: 75, spec_drafts_total: 20,
        spec_accepted_tokens_per_position: { '0': 20, '1': 18 }, spec_acceptance_rate_pct: 75
      }
    } as any
  }
  return manager
}

beforeEach(() => {
  mocks.request.mockReset()
  mocks.request.mockImplementation(async (path: string) => {
    if (path.startsWith('/api/v1/logs?')) return { instance_id: 'gemma-4', entries: [] }
    return []
  })
  vi.stubGlobal('EventSource', undefined)
  seed()
})

describe('Instance detail page', () => {
  it('renders runtime resources and every llama.cpp metrics snapshot field', async () => {
    const wrapper = await mountSuspended(InstanceDetailPage, { route: '/instances/gemma-4/detail' })
    await flushPromises()
    const text = wrapper.text()
    expect(text).toContain('Gemma 4')
    expect(text).toContain('READY')
    expect(text).toContain('CUDA0')
    expect(text).toContain('Global GPU usage')
    expect(text).toContain('97.0%')
    expect(text).toContain('14 GiB')
    expect(text).toContain('52.9 tok/s')
    expect(text).toContain('30.0 tok/s')
    expect(text).toContain('Active requests')
    expect(text).toContain('Queued requests')
    expect(text).toContain('8,192 / 32,768')
    expect(text).toContain('1.50')
    expect(text).toContain('Prompt tokens')
    expect(text).toContain('120')
    expect(text).toContain('4.00 s')
    expect(text).toContain('Generated tokens')
    expect(text).toContain('210')
    expect(text).toContain('7.00 s')
    expect(text).toContain('llama_decode() calls')
    expect(text).toContain('Draft tokens')
    expect(text).toContain('Accepted draft tokens')
    expect(text).toContain('Verification steps')
    expect(text).toContain('75.0%')
    expect(text).toContain('Current-session logs')
    expect(wrapper.get('[data-testid="instance-detail-spec-positions"]').text()).toContain('Position 1: 18')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/logs?instance_id=gemma-4&limit=2000')
  })

  it('shows stopped-state guidance when no llama metrics snapshot exists', async () => {
    const manager = seed()
    manager.runtimes.value = { m1: [{ instance_id: 'gemma-4', model_id: 'm1', state: 'UNLOADED' }] }
    manager.runtimeTelemetry.value = {}
    const wrapper = await mountSuspended(InstanceDetailPage, { route: '/instances/gemma-4/detail' })
    await flushPromises()
    expect(wrapper.text()).toContain('llama.cpp metrics unavailable while stopped')
    expect(wrapper.text()).toContain('Start the Instance')
  })
})
