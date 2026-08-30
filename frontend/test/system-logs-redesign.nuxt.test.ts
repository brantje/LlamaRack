import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import SystemLogsPage from '~/pages/admin/system-logs.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

const timestamp = '2026-08-30T12:04:58Z'

function log(level: string, source: string, message: string) {
  return { timestamp, level, source, message }
}

function button(wrapper: any, text: string) {
  const found = wrapper.findAll('button').find((candidate: any) => candidate.text().trim() === text)
  if (!found) throw new Error(`Missing button: ${text}`)
  return found
}

function component(wrapper: any, names: string[]) {
  for (const name of names) {
    const found = wrapper.findAllComponents({ name })[0]
    if (found) return found
  }
  throw new Error(`Missing component: ${names.join(', ')}`)
}

function resetManager() {
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.models.value = []
  manager.instances.value = []
  manager.runtimes.value = {}
  manager.runtimeTelemetry.value = {}
  manager.profile.value = null
}

class FakeEventSource {
  static instances: FakeEventSource[] = []
  url: string
  onopen: ((event: Event) => void) | null = null
  onerror: ((event: Event) => void) | null = null
  closed = false
  listeners = new Map<string, any[]>()

  constructor(url: string) {
    this.url = url
    FakeEventSource.instances.push(this)
  }

  addEventListener(type: string, listener: any) {
    this.listeners.set(type, [...(this.listeners.get(type) || []), listener])
  }

  close() {
    this.closed = true
  }

  emit(type: string, data: string) {
    const event = new MessageEvent(type, { data })
    for (const listener of this.listeners.get(type) || []) listener(event)
  }
}

beforeEach(() => {
  mocks.request.mockReset()
  FakeEventSource.instances = []
  vi.unstubAllGlobals()
  resetManager()
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('Administration system logs', () => {
  it('renders diagnostics, composes level/source/grep filters and disables Follow after manual scroll', async () => {
    vi.stubGlobal('EventSource', undefined)
    mocks.request.mockResolvedValue({ entries: [
      log('INFO', 'manager', 'reconcile: 1 Always On Instance satisfied'),
      log('WARN', 'telemetry', 'nvidia-smi pmon -s u returned no process rows, retrying plain pmon'),
      log('INFO', 'gateway', 'POST /v1/embeddings model=embeddings 200 in 41ms'),
      log('ERROR', 'qwen-coder-ci', 'exit status 1: invalid device CUDA1'),
      log('DEBUG', 'qwen-coder-ci', 'worker debug detail'),
      log('TRACE', 'manager', 'invalid level'),
      { timestamp: 'not-a-time', level: 'INFO', source: 'manager', message: 'invalid timestamp' }
    ] })

    const wrapper = await mountSuspended(SystemLogsPage, { route: '/admin/system-logs?source=qwen-coder-ci' })
    await flushPromises()

    expect(mocks.request).toHaveBeenCalledWith('/api/v1/logs?scope=system&limit=4000')
    expect(wrapper.text()).toContain('DIAGNOSTICS')
    expect(wrapper.text()).toContain('Logs')
    expect(wrapper.get('[data-testid="system-log-header-controls"]').text()).toContain('All')
    expect(wrapper.get('[data-testid="system-log-header-controls"]').text()).toContain('INFO')
    expect(wrapper.get('[data-testid="system-log-header-controls"]').text()).toContain('WARN+')
    expect(wrapper.get('[data-testid="system-log-header-controls"]').text()).toContain('DEBUG')
    expect(wrapper.text()).toContain('All sources')
    expect(wrapper.text()).toContain('manager')
    expect(wrapper.text()).toContain('gateway')
    expect(wrapper.text()).toContain('telemetry')
    expect(wrapper.text()).toContain('qwen-coder-ci')
    expect(wrapper.findAll('[data-testid="system-log-row"]')).toHaveLength(2)
    expect(wrapper.text()).not.toContain('invalid level')
    expect(wrapper.text()).not.toContain('invalid timestamp')

    await button(wrapper, 'All sources').trigger('click')
    await flushPromises()
    expect(wrapper.findAll('[data-testid="system-log-row"]')).toHaveLength(5)

    const warnButton = button(wrapper, 'WARN+')
    expect(warnButton.attributes('title')).toBe('WARN + ERROR')
    await warnButton.trigger('click')
    await flushPromises()
    expect(wrapper.findAll('[data-testid="system-log-row"]')).toHaveLength(2)
    expect(wrapper.text()).toContain('pmon')
    expect(wrapper.text()).toContain('invalid device CUDA1')

    await wrapper.find('input[aria-label="grep log messages"]').setValue('invalid device')
    await flushPromises()
    expect(wrapper.findAll('[data-testid="system-log-row"]')).toHaveLength(1)
    expect(wrapper.text()).not.toContain('retrying plain pmon')

    await button(wrapper, 'DEBUG').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('No log lines match this filter.')
    await wrapper.find('input[aria-label="grep log messages"]').setValue('')
    await flushPromises()
    expect(wrapper.text()).toContain('worker debug detail')

    const follow = component(wrapper, ['Checkbox', 'UCheckbox'])
    expect(follow.props('modelValue')).toBe(true)
    const output = wrapper.get('[data-testid="system-log-output"]')
    Object.defineProperty(output.element, 'scrollHeight', { configurable: true, value: 1000 })
    Object.defineProperty(output.element, 'clientHeight', { configurable: true, value: 100 })
    Object.defineProperty(output.element, 'scrollTop', { configurable: true, writable: true, value: 100 })
    await output.trigger('scroll')
    await flushPromises()
    expect(follow.props('modelValue')).toBe(false)
    follow.vm.$emit('update:modelValue', true)
    await flushPromises()
    expect(follow.props('modelValue')).toBe(true)
    wrapper.unmount()
  })

  it('tails authenticated structured SSE events and reconnects after a disconnect', async () => {
    vi.stubGlobal('EventSource', FakeEventSource as any)
    let ticket = 0
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/auth/ws-ticket') return { ticket: `ticket-${++ticket}` }
      return {}
    })

    const wrapper = await mountSuspended(SystemLogsPage, { route: '/admin/system-logs' })
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/auth/ws-ticket', { method: 'POST' })
    expect(FakeEventSource.instances).toHaveLength(1)
    const first = FakeEventSource.instances[0]!
    expect(first.url).toBe('http://manager.test:8888/api/v1/logs/stream?scope=system&limit=4000&ticket=ticket-1')

    first.onopen?.(new Event('open'))
    first.emit('log', JSON.stringify(log('INFO', 'manager', 'manager ready')))
    first.emit('log', JSON.stringify(log('INFO', 'llama-70b-long', 'load_tensors: offloading 41/81 layers to GPU')))
    first.emit('log', '{bad json')
    first.emit('log', JSON.stringify(log('TRACE', 'manager', 'ignore invalid level')))
    await flushPromises()
    expect(wrapper.findAll('[data-testid="system-log-row"]')).toHaveLength(2)
    expect(wrapper.text()).toContain('llama-70b-long')
    expect(wrapper.text()).not.toContain('ignore invalid level')

    first.onerror?.(new Event('error'))
    await flushPromises()
    expect(first.closed).toBe(true)
    expect(wrapper.text()).toContain('Live log stream disconnected')
    first.emit('log', JSON.stringify(log('ERROR', 'stale', 'stale event')))
    await flushPromises()
    expect(wrapper.text()).not.toContain('stale event')

    await button(wrapper, 'Reconnect').trigger('click')
    await flushPromises()
    expect(FakeEventSource.instances).toHaveLength(2)
    expect(FakeEventSource.instances[1]!.url).toContain('ticket=ticket-2')
    wrapper.unmount()
    expect(FakeEventSource.instances[1]!.closed).toBe(true)
  })

  it('surfaces snapshot error fallbacks and keeps unknown Instance source selections visible', async () => {
    vi.stubGlobal('EventSource', undefined)
    const failures = [
      { value: { data: { error: 'Snapshot denied' } }, text: 'Snapshot denied' },
      { value: new Error('Snapshot message'), text: 'Snapshot message' },
      { value: {}, text: 'Unable to load manager logs' }
    ]
    for (const failure of failures) {
      mocks.request.mockRejectedValueOnce(failure.value)
      const wrapper = await mountSuspended(SystemLogsPage, { route: '/admin/system-logs?source=not-yet-seen' })
      await flushPromises()
      expect(wrapper.text()).toContain(failure.text)
      expect(wrapper.text()).toContain('not-yet-seen')
      wrapper.unmount()
    }

    mocks.request.mockResolvedValueOnce({ entries: null })
    const wrapper = await mountSuspended(SystemLogsPage, { route: '/admin/system-logs?source=not-yet-seen' })
    await flushPromises()
    expect(wrapper.findAll('[data-testid="system-log-row"]')).toHaveLength(0)
    expect(wrapper.text()).toContain('No log lines match this filter.')
    wrapper.unmount()
  })

  it('handles live authentication errors and missing tickets without opening EventSource', async () => {
    vi.stubGlobal('EventSource', FakeEventSource as any)
    const failures = [
      { value: { data: { error: 'Ticket denied' } }, text: 'Ticket denied' },
      { value: new Error('Ticket message'), text: 'Ticket message' },
      { value: {}, text: 'Unable to authenticate live log stream' }
    ]
    for (const failure of failures) {
      mocks.request.mockRejectedValueOnce(failure.value)
      const wrapper = await mountSuspended(SystemLogsPage, { route: '/admin/system-logs' })
      await flushPromises()
      expect(wrapper.text()).toContain(failure.text)
      wrapper.unmount()
    }

    mocks.request.mockResolvedValueOnce({ ticket: '' })
    const wrapper = await mountSuspended(SystemLogsPage, { route: '/admin/system-logs' })
    await flushPromises()
    expect(wrapper.text()).toContain('Unable to authenticate live log stream')
    expect(FakeEventSource.instances).toHaveLength(0)
    wrapper.unmount()
  })
})
