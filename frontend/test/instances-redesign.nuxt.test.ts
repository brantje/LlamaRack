import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import InstancesPage from '~/pages/instances/index.vue'
import { useManager, type Instance, type RuntimeTelemetry } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

const gib = 1024 ** 3

function instance(id: string, overrides: Partial<Instance> = {}): Instance {
  return { id, model_id: 'm1', name: id.replaceAll('-', ' '), enabled: true, autoload_enabled: true, always_on: false, priority: 'normal', eviction_enabled: true, idle_unload_seconds: 0, gpu_mode: 'auto', gpu_devices: [], request_log_mode: 'metadata', ...overrides }
}

function telemetry(id: string, overrides: Partial<RuntimeTelemetry> = {}): RuntimeTelemetry {
  return { instance_id: id, pid: 42, gpu_devices: ['CUDA0'], gpus: [{ device_id: 'CUDA0', vram_used_bytes: 8 * gib }], vram_used_bytes: 8 * gib, gpu_utilization_pct: 62, cpu_percent: 35, memory_used_bytes: 3 * gib, collected_at: '2026-08-29T20:00:00Z', ...overrides }
}

function seed() {
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.models.value = [{ id: 'm1', name: 'Coder Model', gguf_path: 'coder.gguf', total_bytes: 1, context_length: 8192 }]
  manager.instances.value = [
    instance('ready', { always_on: true, idle_unload_seconds: 600, gpu_devices: ['CUDA0'] }),
    instance('stopped'),
    instance('failed', { autoload_enabled: false }),
    instance('downloading')
  ]
  manager.runtimes.value = { m1: [
    { instance_id: 'ready', model_id: 'm1', state: 'READY', pid: 42, port: 9010 },
    { instance_id: 'stopped', model_id: 'm1', state: 'UNLOADED' },
    { instance_id: 'failed', model_id: 'm1', state: 'FAILED', last_error: 'CUDA allocation failed' },
    { instance_id: 'downloading', model_id: 'm1', state: 'UNLOADED' }
  ] }
  manager.runtimeTelemetry.value = { ready: telemetry('ready') }
  manager.observabilityLive.value = {
    collected_at: '2026-08-29T20:00:00Z',
    hardware: { ram_total_bytes: 32 * gib, ram_available_bytes: 16 * gib, collected_at: '2026-08-29T20:00:00Z', processes: [], gpus: [{ id: 'CUDA0', backend: 'cuda', index: 0, name: 'GPU', total_bytes: 16 * gib, used_bytes: 9 * gib, free_bytes: 7 * gib, utilization_pct: 70 }] },
    telemetry: [manager.runtimeTelemetry.value.ready!],
    gateway: { since: 0, requests: 0, successes: 0, errors: 0, active: 0, queued: 0, active_api_keys: 0, prompt_tokens: 0, generated_tokens: 0, total_tokens: 0, latency_ms: {}, ttft_ms: {} },
    requests: []
  }
  return manager
}

beforeEach(() => {
  mocks.request.mockReset()
  sessionStorage.clear()
  seed()
  mocks.request.mockImplementation(async (path: string) => {
    if (path === '/api/v1/imports') return [{ id: 'imp-1', job_id: 'job-1', model_id: 'm1', instance_id: 'downloading', state: 'DOWNLOADING', start_when_ready: true }]
    if (path === '/api/v1/settings/general') return { idle_unload_seconds: { value: 300 } }
    return []
  })
})

describe('Instances redesign', () => {
  it('defaults to the framed table with filters, lifecycle and telemetry fallbacks', async () => {
    const wrapper = await mountSuspended(InstancesPage, { route: '/instances' })
    await flushPromises()

    expect(wrapper.get('[data-testid="instances-table-view"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="instances-card-view"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('Durable llama-server definitions. Instance slugs are the model IDs used by OpenAI-compatible clients.')
    expect(wrapper.get('[data-testid="instances-telemetry-snapshot"]').text()).toContain('Telemetry snapshot')

    const readyRow = wrapper.get('tr[data-instance-state="READY"]')
    expect(readyRow.text()).toContain('CUDA0')
    expect(readyRow.text()).toContain('62%')
    expect(readyRow.text()).toContain('8.0 GiB')
    expect(readyRow.text()).toContain('global')
    expect(readyRow.text()).toContain('Always On · idle 10 min')
    expect(readyRow.text()).toContain('42')
    expect(readyRow.text()).toContain('9010')
    expect(readyRow.find('[data-testid="instance-table-more"]').exists()).toBe(true)
    expect(readyRow.text()).not.toContain('Kill')
    expect(readyRow.text()).not.toContain('Duplicate')

    const stoppedRow = wrapper.get('tr[data-instance-state="UNLOADED"]')
    expect(stoppedRow.text()).toContain('On demand')
    expect(stoppedRow.text().match(/—/g)?.length).toBeGreaterThanOrEqual(6)
    expect(stoppedRow.find('[data-testid="instance-table-more"]').exists()).toBe(true)

    const downloadingRow = wrapper.get('tr[data-instance-state="DOWNLOADING"]')
    expect(downloadingRow.find('[data-testid="instance-table-more"]').exists()).toBe(false)
    expect(downloadingRow.text()).toContain('Delete')

    await wrapper.get('[data-testid="instances-filter-ready"]').trigger('click')
    expect(wrapper.findAll('tbody tr')).toHaveLength(1)
    await wrapper.get('[data-testid="instances-filter-problems"]').trigger('click')
    expect(wrapper.findAll('tbody tr')).toHaveLength(1)
    expect(wrapper.text()).toContain('CUDA allocation failed')
  })

  it('persists card view and renders required card states', async () => {
    const wrapper = await mountSuspended(InstancesPage, { route: '/instances' })
    await flushPromises()
    await wrapper.get('[data-testid="instances-view-cards"]').trigger('click')
    await flushPromises()

    expect(sessionStorage.getItem('llamarack.instances.view')).toBe('cards')
    expect(wrapper.findAll('[data-testid="instance-card"]')).toHaveLength(4)
    const cards = wrapper.findAll('[data-testid="instance-card"]')
    const readyCard = cards.find(card => card.text().includes('ready'))!
    expect(readyCard.text()).toContain('Priority: normal')
    expect(readyCard.text()).toContain('Always On')
    expect(readyCard.text()).toContain('Autoload')
    expect(readyCard.text()).toContain('Resource-pressure eviction allowed')
    expect(readyCard.text()).toContain('Global GPU usage')
    expect(readyCard.text()).toContain('8.0 GiB')
    expect(readyCard.find('[data-testid="instance-id"]').text()).toBe('ready')
    expect(readyCard.find('[data-testid="instance-card-more"]').exists()).toBe(true)
    expect(readyCard.text()).not.toContain('Kill')
    expect(readyCard.text()).not.toContain('Duplicate')

    const stoppedCard = cards.find(card => card.text().includes('stopped'))!
    expect(stoppedCard.text()).toContain('Unloaded after 300 s without inference activity.')
    const failedCard = cards.find(card => card.text().includes('failed'))!
    expect(failedCard.text()).toContain('CUDA allocation failed')
    const downloadingCard = cards.find(card => card.text().includes('downloading'))!
    expect(downloadingCard.text()).toContain('Model is downloading')
    expect(downloadingCard.text()).toContain('launch automatically')
    expect(downloadingCard.text()).toContain('View download')
    expect(downloadingCard.text()).not.toContain('Restart')
    expect(downloadingCard.text()).not.toContain('Kill')
    expect(downloadingCard.text()).not.toContain('Duplicate')

    expect(wrapper.text()).toContain("GPU telemetry degrades per metric: when process-level utilization cannot be attributed, the assigned device's utilization is shown and labelled Global GPU usage. CPU and RAM stay process-scoped.")
    wrapper.unmount()

    const second = await mountSuspended(InstancesPage, { route: '/instances' })
    await flushPromises()
    expect(second.get('[data-testid="instances-card-view"]').exists()).toBe(true)
    second.unmount()
  })

  it('covers card telemetry aggregation and non-running message edge states', async () => {
    const manager = seed()
    manager.observabilityLive.value = null
    manager.models.value = []
    manager.instances.value = [
      instance('aggregate', { model_id: 'missing', gpu_devices: ['CUDA9'], idle_unload_seconds: 90, eviction_enabled: false }),
      instance('manual', { autoload_enabled: false }),
      instance('runtime-failed'),
      instance('paused'),
      instance('cancelled'),
      instance('downloading', { autoload_enabled: false })
    ]
    manager.runtimes.value = {
      missing: [{ instance_id: 'aggregate', model_id: 'missing', state: 'READY', pid: 42 }],
      m1: [
        { instance_id: 'runtime-failed', model_id: 'm1', state: 'FAILED' },
        { instance_id: 'paused', model_id: 'm1', state: 'PAUSED' }
      ]
    }
    manager.runtimeTelemetry.value = {
      aggregate: telemetry('aggregate', {
        gpu_devices: [],
        gpus: [{ device_id: 'CUDA9', vram_used_bytes: 512 * 1024 ** 2, utilization_pct: 5.5 }],
        vram_used_bytes: undefined,
        gpu_utilization_pct: undefined,
        cpu_percent: -1,
        memory_used_bytes: 0,
        collected_at: 'not-a-date'
      })
    }
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/imports') return [
        { id: 'imp-c', job_id: 'job-c', model_id: 'm1', instance_id: 'cancelled', state: 'CANCELLED', start_when_ready: false },
        { id: 'imp-d', job_id: 'job-d', model_id: 'm1', instance_id: 'downloading', state: 'DOWNLOADING', start_when_ready: false }
      ]
      if (path === '/api/v1/settings/general') return { idle_unload_seconds: { value: -5 } }
      return []
    })
    sessionStorage.setItem('llamarack.instances.view', 'cards')

    const wrapper = await mountSuspended(InstancesPage, { route: '/instances' })
    await flushPromises()
    expect(sessionStorage.getItem('llamarack.instances.view')).toBe('cards')
    const text = wrapper.text()
    expect(wrapper.get('[data-testid="instances-telemetry-snapshot"]').text()).toContain('not-a-date')
    expect(text).toContain('missing')
    expect(text).toContain('5.5%')
    expect(text).toContain('512 MiB')
    expect(text).toContain('0 B')
    expect(text).toContain('CUDA9')
    expect(text).toContain('Never launched since the last manager restart. Launch it manually when needed.')
    expect(text).toContain('llama-server exited unexpectedly.')
    expect(text).toContain('PAUSED — runtime telemetry is not available.')
    expect(text).toContain('Open Downloads to retry or inspect this import.')
    expect(text).toContain('Model is downloading. The Instance will become launchable when the verified GGUF download completes.')

    await wrapper.get('[data-testid="instances-view-table"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('tr[data-instance-state="READY"]').text()).toContain('On demand · idle 1.5 min')
  })

  it('covers empty filters, failed optional reads and direct launch without eviction confirmation', async () => {
    const manager = seed()
    manager.instances.value = [instance('manual-launch', { eviction_enabled: false })]
    manager.runtimes.value = { m1: [] }
    manager.observabilityLive.value = null
    manager.runtimeTelemetry.value = {}
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/imports') throw new Error('imports unavailable')
      if (path === '/api/v1/settings/general') throw new Error('settings unavailable')
      if (path === '/api/v1/instances/manual-launch/start' && options?.method === 'POST') return {}
      if (path === '/api/v1/models') return manager.models.value
      if (path === '/api/v1/instances') return manager.instances.value
      if (path === '/api/v1/instances/manual-launch/runtime') return { instance_id: 'manual-launch', model_id: 'm1', state: 'UNLOADED' }
      if (path === '/api/v1/llamacpp/profile') throw new Error('profile unavailable')
      return []
    })

    const wrapper = await mountSuspended(InstancesPage, { route: '/instances' })
    await flushPromises()
    expect(wrapper.get('[data-testid="instances-telemetry-snapshot"]').text()).toContain('awaiting data')
    await wrapper.get('[data-testid="instances-filter-ready"]').trigger('click')
    expect(wrapper.text()).toContain('No Instances match this filter')
    await wrapper.get('[data-testid="instances-filter-all"]').trigger('click')
    const launch = wrapper.findAll('button').find(button => button.text() === 'Launch')!
    await launch.trigger('click')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/instances/manual-launch/start', { method: 'POST' })
  })
})
