import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import InstanceLogViewer from '~/components/InstanceLogViewer.vue'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

const timestamp = '2026-08-28T12:34:56.000Z'

function entry(source: string, text: string) {
  return { source, timestamp, text }
}

function component(wrapper: any, names: string[]) {
  for (const name of names) {
    const found = wrapper.findAllComponents({ name })[0]
    if (found) return found
  }
  throw new Error(`Missing component: ${names.join(', ')}`)
}

function button(wrapper: any, text: string) {
  const found = wrapper.findAll('button').find((candidate: any) => candidate.text().trim() === text)
  if (!found) throw new Error(`Missing button: ${text}`)
  return found
}

class FakeEventSource {
  static instances: FakeEventSource[] = []
  url: string
  withCredentials: boolean
  onopen: ((event: Event) => void) | null = null
  onerror: ((event: Event) => void) | null = null
  closed = false
  listeners = new Map<string, any[]>()

  constructor(url: string, init?: any) {
    this.url = url
    this.withCredentials = Boolean(init?.withCredentials)
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
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('InstanceLogViewer', () => {
  it('loads a timestamped snapshot fallback, filters sources/search and resets the browser view', async () => {
    vi.stubGlobal('EventSource', undefined)
    mocks.request.mockResolvedValue({
      instance_id: 'gemma/4',
      entries: [
        entry('stdout', 'server booted'),
        entry('stderr', 'CUDA warning'),
        entry('manager', 'worker ready'),
        entry('bogus', 'ignore me'),
        { source: 'stdout', text: 'untimestamped line' },
        { source: 'stdout', timestamp, text: 42 }
      ]
    })

    const wrapper = await mountSuspended(InstanceLogViewer, { props: { instanceId: 'gemma/4' }, route: false })
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/logs?instance_id=gemma%2F4&limit=2000')
    expect(wrapper.get('[data-testid="instance-log-output"]').text()).toContain('server booted')
    expect(wrapper.get('[data-testid="instance-log-output"]').text()).toContain('CUDA warning')
    expect(wrapper.get('[data-testid="instance-log-output"]').text()).toContain('2026-08-28 12:34:56.000 UTC')
    expect(wrapper.text()).not.toContain('ignore me')
    expect(wrapper.text()).not.toContain('untimestamped line')

    const select = component(wrapper, ['SelectMenu', 'USelectMenu'])
    select.vm.$emit('update:modelValue', 'stderr')
    await flushPromises()
    expect(wrapper.get('[data-testid="instance-log-output"]').text()).toContain('CUDA warning')
    expect(wrapper.get('[data-testid="instance-log-output"]').text()).not.toContain('server booted')

    select.vm.$emit('update:modelValue', 'all')
    await wrapper.find('input[aria-label="Search logs"]').setValue('ready')
    await flushPromises()
    expect(wrapper.get('[data-testid="instance-log-output"]').text()).toContain('worker ready')
    expect(wrapper.get('[data-testid="instance-log-output"]').text()).not.toContain('CUDA warning')

    await wrapper.find('input[aria-label="Search logs"]').setValue('does-not-exist')
    await flushPromises()
    expect(wrapper.text()).toContain('No logs match the current filters')

    await wrapper.find('input[aria-label="Search logs"]').setValue('')
    await button(wrapper, 'Clear view').trigger('click')
    expect(wrapper.text()).toContain('No logs in the current view')
    expect(wrapper.text()).toContain('does not stop live tailing')

    await button(wrapper, 'Reconnect').trigger('click')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledTimes(2)
    expect(wrapper.get('[data-testid="instance-log-output"]').text()).toContain('server booted')

    mocks.request.mockRejectedValueOnce({ data: { error: 'log access denied' } })
    await button(wrapper, 'Reconnect').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('log access denied')
    wrapper.unmount()
  })

  it('tails timestamped SSE log events with a one-time auth ticket and reconnects cleanly', async () => {
    vi.stubGlobal('EventSource', FakeEventSource as any)
    let ticketCounter = 0
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/auth/ws-ticket') return { ticket: `ticket-${++ticketCounter}` }
      return {}
    })

    const wrapper = await mountSuspended(InstanceLogViewer, { props: { instanceId: 'coder' }, route: false })
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/auth/ws-ticket', { method: 'POST' })
    expect(FakeEventSource.instances).toHaveLength(1)
    const first = FakeEventSource.instances[0]!
    expect(first.url).toBe('http://manager.test:8888/api/v1/logs/stream?instance_id=coder&limit=2000&ticket=ticket-1')
    expect(first.withCredentials).toBe(false)

    first.onopen?.(new Event('open'))
    first.emit('log', JSON.stringify(entry('stdout', 'ready on port 9000')))
    first.emit('log', JSON.stringify(entry('stderr', 'recoverable warning')))
    first.emit('log', JSON.stringify(entry('manager', 'launch command: "llama-server" "--ctx-size" "8192"')))
    first.emit('log', JSON.stringify(entry('manager', 'autoload triggered by inference request')))
    first.emit('log', '{bad json')
    first.emit('log', JSON.stringify({ source: 'stdout', text: 'missing timestamp' }))
    first.emit('log', JSON.stringify(entry('unknown', 'ignore')))
    await flushPromises()
    expect(wrapper.text()).toContain('Live')
    const output = wrapper.get('[data-testid="instance-log-output"]').text()
    expect(output).toContain('2026-08-28 12:34:56.000 UTC')
    expect(output).toContain('ready on port 9000')
    expect(output).toContain('recoverable warning')
    expect(output).toContain('launch command: "llama-server" "--ctx-size" "8192"')
    expect(output).toContain('autoload triggered by inference request')
    expect(output).not.toContain('missing timestamp')
    expect(output).not.toContain('ignore')

    await button(wrapper, 'Clear view').trigger('click')
    expect(wrapper.text()).toContain('No logs in the current view')
    first.emit('log', JSON.stringify(entry('manager', 'future event')))
    await flushPromises()
    expect(wrapper.get('[data-testid="instance-log-output"]').text()).toContain('future event')

    first.onerror?.(new Event('error'))
    await flushPromises()
    expect(first.closed).toBe(true)
    expect(wrapper.text()).toContain('Live log stream disconnected')
    first.emit('log', JSON.stringify(entry('stdout', 'stale event')))
    await flushPromises()
    expect(wrapper.text()).not.toContain('stale event')

    await button(wrapper, 'Reconnect').trigger('click')
    await flushPromises()
    expect(FakeEventSource.instances).toHaveLength(2)
    const second = FakeEventSource.instances[1]!
    expect(second.url).toContain('ticket=ticket-2')
    second.onopen?.(new Event('open'))
    await wrapper.setProps({ instanceId: 'coder-v2' })
    await flushPromises()
    expect(second.closed).toBe(true)
    expect(FakeEventSource.instances).toHaveLength(3)
    expect(FakeEventSource.instances[2]!.url).toContain('instance_id=coder-v2')
    expect(FakeEventSource.instances[2]!.url).toContain('ticket=ticket-3')
    wrapper.unmount()
    expect(FakeEventSource.instances[2]!.closed).toBe(true)
  })

  it('does not open an EventSource when the stream ticket cannot be issued', async () => {
    vi.stubGlobal('EventSource', FakeEventSource as any)
    mocks.request.mockRejectedValueOnce({ data: { error: 'authentication required' } })

    const wrapper = await mountSuspended(InstanceLogViewer, { props: { instanceId: 'coder' }, route: false })
    await flushPromises()

    expect(FakeEventSource.instances).toHaveLength(0)
    expect(wrapper.text()).toContain('authentication required')
    wrapper.unmount()
  })

  it('rejects an empty stream ticket without creating an EventSource', async () => {
    vi.stubGlobal('EventSource', FakeEventSource as any)
    mocks.request.mockResolvedValueOnce({ ticket: '' })

    const wrapper = await mountSuspended(InstanceLogViewer, { props: { instanceId: 'coder' }, route: false })
    await flushPromises()

    expect(FakeEventSource.instances).toHaveLength(0)
    expect(wrapper.text()).toContain('Unable to authenticate live log stream')
    wrapper.unmount()
  })

  it('ignores a stale ticket response after the Instance changes', async () => {
    vi.stubGlobal('EventSource', FakeEventSource as any)
    let resolveFirst!: (value: { ticket: string }) => void
    const firstTicket = new Promise<{ ticket: string }>((resolve) => { resolveFirst = resolve })
    mocks.request
      .mockReturnValueOnce(firstTicket)
      .mockResolvedValueOnce({ ticket: 'fresh-ticket' })

    const wrapper = await mountSuspended(InstanceLogViewer, { props: { instanceId: 'coder' }, route: false })
    await flushPromises()
    expect(FakeEventSource.instances).toHaveLength(0)

    await wrapper.setProps({ instanceId: 'coder-v2' })
    await flushPromises()
    expect(FakeEventSource.instances).toHaveLength(1)
    expect(FakeEventSource.instances[0]!.url).toContain('instance_id=coder-v2')
    expect(FakeEventSource.instances[0]!.url).toContain('ticket=fresh-ticket')

    resolveFirst({ ticket: 'stale-ticket' })
    await flushPromises()
    expect(FakeEventSource.instances).toHaveLength(1)
    expect(FakeEventSource.instances[0]!.url).not.toContain('stale-ticket')
    wrapper.unmount()
  })
})
