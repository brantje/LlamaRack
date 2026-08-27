import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import InstanceRuntimeTelemetry from '~/components/InstanceRuntimeTelemetry.vue'
import InstancesPage from '~/pages/instances/index.vue'
import { useManager, type Instance, type Model, type RuntimeTelemetry } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

class FakeWebSocket {
  static instances: FakeWebSocket[] = []
  url: string
  onopen: ((event: Event) => void) | null = null
  onmessage: ((event: MessageEvent) => void) | null = null
  onclose: ((event: CloseEvent) => void) | null = null
  constructor(url: string) {
    this.url = url
    FakeWebSocket.instances.push(this)
  }
  open() { this.onopen?.({} as Event) }
  emit(payload: unknown) { this.onmessage?.({ data: JSON.stringify(payload) } as MessageEvent) }
  disconnect() { this.onclose?.({} as CloseEvent) }
  close() {}
}

function model(): Model {
  return { id: 'm1', name: 'Coder', gguf_path: 'coder.gguf', total_bytes: 4, context_length: 8192 }
}

function instance(): Instance {
  return {
    id: 'coder', model_id: 'm1', name: 'Coder', enabled: true, autoload_enabled: true,
    always_on: false, priority: 'normal', eviction_enabled: true, idle_unload_seconds: 0,
    gpu_mode: 'auto', gpu_devices: []
  }
}

function telemetry(overrides: Partial<RuntimeTelemetry> = {}): RuntimeTelemetry {
  return {
    instance_id: 'coder',
    pid: 42,
    gpu_devices: ['CUDA0', 'CUDA1'],
    gpus: [
      { device_id: 'CUDA0', vram_used_bytes: 4 * 1024 ** 3, utilization_pct: 20 },
      { device_id: 'CUDA1', vram_used_bytes: 6 * 1024 ** 3, utilization_pct: 54 }
    ],
    vram_used_bytes: 10 * 1024 ** 3,
    gpu_utilization_pct: 37,
    cpu_percent: 136.4,
    memory_used_bytes: 2.5 * 1024 ** 3,
    collected_at: '2026-08-27T16:00:00Z',
    ...overrides
  }
}

function resetManager() {
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.models.value = [model()]
  manager.instances.value = [instance()]
  manager.runtimes.value = {}
  manager.runtimeTelemetry.value = {}
  manager.runtimeEventsConnected.value = false
  return manager
}

beforeEach(() => {
  mocks.request.mockReset()
  FakeWebSocket.instances = []
  vi.stubGlobal('WebSocket', FakeWebSocket as any)
  resetManager()
})

describe('per-Instance runtime telemetry', () => {
  it('accepts only telemetry matching the active Instance PID and clears stale samples', () => {
    const manager = resetManager()
    manager.connectRuntimeEvents()
    const socket = FakeWebSocket.instances[0]!
    socket.open()
    socket.emit({ type: 'runtime_snapshot', runtimes: [{ instance_id: 'coder', model_id: 'm1', state: 'READY', pid: 42 }] })

    socket.emit({ type: 'runtime_telemetry', telemetry: [
      telemetry(),
      telemetry({ instance_id: '', pid: 42 }),
      telemetry({ instance_id: 'missing', pid: 42 }),
      telemetry({ pid: 99 })
    ] })
    expect(manager.telemetryForInstance(instance())?.gpu_utilization_pct).toBe(37)
    expect(manager.runtimeTelemetry.value.missing).toBeUndefined()

    socket.emit({ type: 'runtime', runtime: { instance_id: 'coder', model_id: 'm1', state: 'UNLOADED', pid: 0 } })
    expect(manager.telemetryForInstance(instance())).toBeUndefined()

    socket.emit({ type: 'runtime', runtime: { instance_id: 'coder', model_id: 'm1', state: 'READY', pid: 77 } })
    socket.emit({ type: 'runtime_telemetry', telemetry: [telemetry({ pid: 42 })] })
    expect(manager.telemetryForInstance(instance())).toBeUndefined()
    socket.emit({ type: 'runtime_telemetry', telemetry: [telemetry({ pid: 77 })] })
    expect(manager.telemetryForInstance(instance())?.pid).toBe(77)

    socket.disconnect()
    expect(manager.runtimeTelemetry.value).toEqual({})
  })

  it('renders process-scoped GPU, VRAM, CPU and RAM values without whole-device wording', async () => {
    const wrapper = await mountSuspended(InstanceRuntimeTelemetry, {
      route: false,
      props: { state: 'READY', telemetry: telemetry() }
    })
    expect(wrapper.text()).toContain('Live Instance resources')
    expect(wrapper.get('[data-testid="instance-gpu-placement"]').text()).toBe('CUDA0, CUDA1')
    expect(wrapper.get('[data-testid="instance-gpu-usage"]').text()).toBe('37%')
    expect(wrapper.get('[data-testid="instance-vram"]').text()).toBe('10 GiB')
    expect(wrapper.get('[data-testid="instance-cpu"]').text()).toBe('136%')
    expect(wrapper.get('[data-testid="instance-memory"]').text()).toBe('2.5 GiB')
    expect(wrapper.get('[data-testid="instance-gpu-details"]').text()).toContain('CUDA0 · 4.0 GiB · 20%')
    expect(wrapper.text()).not.toContain('GPU utilization')

    await wrapper.setProps({ telemetry: telemetry({ gpu_devices: [], gpus: [], vram_used_bytes: 0, gpu_utilization_pct: undefined, cpu_percent: 0, memory_used_bytes: 0 }) })
    expect(wrapper.get('[data-testid="instance-gpu-placement"]').text()).toBe('No GPU allocation detected')
    expect(wrapper.get('[data-testid="instance-gpu-usage"]').text()).toBe('—')
    expect(wrapper.get('[data-testid="instance-vram"]').text()).toBe('0 B')
    expect(wrapper.get('[data-testid="instance-cpu"]').text()).toBe('0.0%')
    expect(wrapper.get('[data-testid="instance-memory"]').text()).toBe('0 B')

    await wrapper.setProps({ telemetry: undefined })
    expect(wrapper.text()).toContain('Collecting…')
    expect(wrapper.get('[data-testid="instance-gpu-placement"]').text()).toBe('—')

    await wrapper.setProps({ state: 'UNLOADED' })
    expect(wrapper.find('[data-testid="instance-telemetry"]').exists()).toBe(false)
  })

  it('integrates live telemetry into the Instance card', async () => {
    const manager = resetManager()
    manager.runtimes.value = { m1: [{ instance_id: 'coder', model_id: 'm1', state: 'READY', pid: 42 }] }
    manager.runtimeTelemetry.value = { coder: telemetry() }
    const wrapper = await mountSuspended(InstancesPage, { route: false })
    await flushPromises()
    const card = wrapper.get('[data-testid="instance-card"]')
    expect(card.find('[data-testid="instance-telemetry"]').exists()).toBe(true)
    expect(card.text()).toContain('Instance GPU usage')
    expect(card.text()).toContain('CUDA0, CUDA1')
    expect(card.text()).toContain('10 GiB')
  })
})
