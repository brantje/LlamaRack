import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import SystemLogsPage from '~/pages/admin/logs.vue'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

const at = '2026-08-30T00:12:34Z'
function log(level: string, source: string, message: string) {
  return { timestamp: at, level, source, message }
}

function button(wrapper: any, text: string) {
  const found = wrapper.findAll('button').find((candidate: any) => candidate.text().trim() === text)
  if (!found) throw new Error(`Missing button: ${text}`)
  return found
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

  close() { this.closed = true }

  emit(type: string, data: string) {
    const event = new MessageEvent(type, { data })
    for (const listener of this.listeners.get(type) || []) listener(event)
  }
}

beforeEach(() => {
  mocks.request.mockReset()
  FakeEventSource.instances = []
  vi.unstubAllGlobals()
})

afterEach(() => vi.unstubAllGlobals())

describe('System diagnostics logs', () => {
  it('loads the snapshot fallback and composes route source, level and grep filters', async () => {
    vi.stubGlobal('EventSource', undefined)
    mocks.request.mockResolvedValue({ entries: [
      log('INFO', 'manager', 'reconcile: 1 Always On Instance satisfied'),
      log('WARN', 'worker-a', 'three failures'),
      log('ERROR', 'worker-a', 'worker exited status 2'),
      log('DEBUG', 'telemetry', 'mapped host PID'),
      log('INFO', 'gateway', 'POST /v1/chat/completions model=worker-a 200 in 431ms'),
      log('BOGUS', 'manager', 'ignored'),
      { timestamp: 'bad', level: 'INFO', source: 'manager', message: 'ignored' }
    ] })

    const wrapper = await mountSuspended(SystemLogsPage, { route: '/admin/logs?source=worker-a' })
    await flushPromises()

    expect(mocks.request).toHaveBeenCalledWith('/api/v1/logs?scope=system&limit=4000')
    expect(wrapper.text()).toContain('DIAGNOSTICS')
    expect(wrapper.text()).toContain('Logs')
    const workerChip = button(wrapper, 'worker-a')
    expect(workerChip.attributes('aria-pressed')).toBe('true')
    expect(wrapper.findAll('[data-testid="system-log-row"]')).toHaveLength(2)

    await button(wrapper, 'WARN').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('three failures')
    expect(wrapper.text()).toContain('worker exited status 2')

    await wrapper.find('input[aria-label="grep log messages"]').setValue('status 2')
    await flushPromises()
    expect(wrapper.findAll('[data-testid="system-log-row"]')).toHaveLength(1)
    expect(wrapper.text()).toContain('worker exited status 2')

    await wrapper.find('input[aria-label="grep log messages"]').setValue('missing')
    await flushPromises()
    expect(wrapper.text()).toContain('No log lines match this filter.')

    await button(wrapper, 'All sources').trigger('click')
    await button(wrapper, 'DEBUG').trigger('click')
    await wrapper.find('input[aria-label="grep log messages"]').setValue('mapped')
    await flushPromises()
    expect(wrapper.text()).toContain('mapped host PID')
    wrapper.unmount()
  })

  it('tails authenticated SSE, accepts valid frames, ignores malformed frames and handles disconnects', async () => {
    vi.stubGlobal('EventSource', FakeEventSource as any)
    mocks.request.mockResolvedValue({ ticket: 'diag-ticket' })

    const wrapper = await mountSuspended(SystemLogsPage, { route: '/admin/logs?source=worker-b' })
    await flushPromises()

    expect(mocks.request).toHaveBeenCalledWith('/api/v1/auth/ws-ticket', { method: 'POST' })
    expect(FakeEventSource.instances).toHaveLength(1)
    const stream = FakeEventSource.instances[0]!
    expect(stream.url).toBe('http://manager.test:8888/api/v1/logs/stream?scope=system&limit=4000&ticket=diag-ticket')
    expect(button(wrapper, 'worker-b').attributes('aria-pressed')).toBe('true')

    stream.onopen?.(new Event('open'))
    stream.emit('log', JSON.stringify(log('INFO', 'worker-b', 'worker online')))
    stream.emit('log', '{bad json')
    stream.emit('log', JSON.stringify({ ...log('INFO', 'worker-b', 'bad time'), timestamp: 'not-a-date' }))
    stream.emit('log', JSON.stringify(log('INFO', '', 'bad source')))
    await flushPromises()
    expect(wrapper.text()).toContain('worker online')
    expect(wrapper.text()).not.toContain('bad time')

    stream.onerror?.(new Event('error'))
    await flushPromises()
    expect(stream.closed).toBe(true)
    expect(wrapper.text()).toContain('Live log stream disconnected. Reconnect to continue tailing.')

    await button(wrapper, 'Reconnect').trigger('click')
    await flushPromises()
    expect(FakeEventSource.instances).toHaveLength(2)
    wrapper.unmount()
    expect(FakeEventSource.instances[1]!.closed).toBe(true)
  })

  it('covers stream authentication errors, empty tickets, follow scrolling and snapshot errors', async () => {
    vi.stubGlobal('EventSource', FakeEventSource as any)
    mocks.request.mockRejectedValueOnce({ data: { error: 'ticket denied' } })
    const denied = await mountSuspended(SystemLogsPage, { route: '/admin/logs' })
    await flushPromises()
    expect(denied.text()).toContain('ticket denied')
    expect(FakeEventSource.instances).toHaveLength(0)
    denied.unmount()

    mocks.request.mockReset()
    mocks.request.mockResolvedValueOnce({ ticket: '' })
    const empty = await mountSuspended(SystemLogsPage, { route: '/admin/logs' })
    await flushPromises()
    expect(empty.text()).toContain('Unable to authenticate live log stream')
    empty.unmount()

    vi.stubGlobal('EventSource', undefined)
    mocks.request.mockReset()
    mocks.request.mockRejectedValueOnce(new Error('snapshot unavailable'))
    const failed = await mountSuspended(SystemLogsPage, { route: '/admin/logs' })
    await flushPromises()
    expect(failed.text()).toContain('snapshot unavailable')
    failed.unmount()

    mocks.request.mockReset()
    mocks.request.mockResolvedValueOnce({ entries: [log('INFO', 'manager', 'ready')] })
    const scrolling = await mountSuspended(SystemLogsPage, { route: '/admin/logs' })
    await flushPromises()
    const output = scrolling.get('[data-testid="system-log-output"]').element as HTMLElement
    Object.defineProperty(output, 'scrollHeight', { configurable: true, value: 1000 })
    Object.defineProperty(output, 'clientHeight', { configurable: true, value: 200 })
    output.scrollTop = 100
    await scrolling.get('[data-testid="system-log-output"]').trigger('scroll')
    await flushPromises()
    const follow = scrolling.findComponent({ name: 'UCheckbox' })
    expect((follow.props() as any).modelValue).toBe(false)
    follow.vm.$emit('update:modelValue', true)
    await flushPromises()
    expect((follow.props() as any).modelValue).toBe(true)
    scrolling.unmount()
  })

  it('treats a malformed snapshot collection as an empty diagnostic view', async () => {
    vi.stubGlobal('EventSource', undefined)
    mocks.request.mockResolvedValueOnce({ entries: null })

    const wrapper = await mountSuspended(SystemLogsPage, { route: '/admin/logs' })
    await flushPromises()

    expect(wrapper.findAll('[data-testid="system-log-row"]')).toHaveLength(0)
    expect(wrapper.text()).toContain('No log lines match this filter.')
    wrapper.unmount()
  })
})
