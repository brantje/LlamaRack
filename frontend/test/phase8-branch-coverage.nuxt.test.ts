import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import DiscoverPage from '~/pages/discover.vue'
import DownloadsPage from '~/pages/downloads.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

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
  manager.profile.value = null
  return manager
}

beforeEach(() => {
  mocks.request.mockReset()
  seedManager()
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('Discover formatting and URL branches', () => {
  it('formats every parameter scale and relative update range', async () => {
    const now = Date.now()
    const old = new Date(now - 40 * 86400_000)
    const results = [
      { id: 'acme/plain', downloads: 0, likes: 0, parameter_count: 500, last_modified: new Date(now - 20_000).toISOString(), private: false, gated: false },
      { id: 'acme/kilo', downloads: 1, likes: 1, parameter_count: 1_500, last_modified: new Date(now - 5 * 60_000).toISOString(), private: false, gated: false },
      { id: 'acme/million', downloads: 1, likes: 1, parameter_count: 1_500_000, last_modified: new Date(now - 2 * 3600_000).toISOString(), private: false, gated: false },
      { id: 'acme/billion', downloads: 1, likes: 1, parameter_count: 2_000_000_000, last_modified: new Date(now - 2 * 86400_000).toISOString(), private: false, gated: false },
      { id: 'acme/trillion', downloads: 1, likes: 1, parameter_count: 1_500_000_000_000, last_modified: old.toISOString(), private: false, gated: false },
      { id: 'acme/unknown', downloads: 1, likes: 1, parameter_count: 0, last_modified: 'not-a-date', private: false, gated: false }
    ]
    mocks.request.mockImplementation(async (path: string) => path.startsWith('/api/v1/huggingface/search?') ? results : [])
    const wrapper = await mountSuspended(DiscoverPage, { route: false })
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    const text = wrapper.text()
    expect(text).toContain('Model size 500 params')
    expect(text).toContain('Model size 1.5K params')
    expect(text).toContain('Model size 1.5M params')
    expect(text).toContain('Model size 2B params')
    expect(text).toContain('Model size 1.5T params')
    expect(text).toContain('Updated just now')
    expect(text).toContain('Updated 5m ago')
    expect(text).toContain('Updated 2h ago')
    expect(text).toContain('Updated 2d ago')
    expect(text).toContain(`Updated ${old.toLocaleDateString()}`)
    wrapper.unmount()
  })

  it('exercises supported and rejected Hugging Face URL shapes', async () => {
    mocks.request.mockImplementation(async (path: string) => path.startsWith('/api/v1/huggingface/search?') ? [] : [])
    const wrapper = await mountSuspended(DiscoverPage, { route: false })
    const input = wrapper.find('input[placeholder="Qwen, Llama, Gemma… or Hugging Face URL"]')

    for (const value of [
      'plain search',
      'https://example.com/acme/demo',
      'https://huggingface.co/one',
      'https://huggingface.co/datasets/acme/demo',
      'https://huggingface.co/spaces/acme/demo',
      'http://%',
      ''
    ]) {
      await input.setValue(value)
      await flushPromises()
    }
    await input.setValue('www.huggingface.co/models/acme/demo/tree/main')
    await flushPromises()
    expect((input.element as HTMLInputElement).value).toBe('acme/demo')
    wrapper.unmount()
  })

  it('renders dependency labels including unknown helpers and plural helper feedback', async () => {
    const detail = {
      id: 'acme/demo', downloads: 1, likes: 1, private: false, gated: false, revision: 'r',
      artifacts: [{
        id: 'a', name: 'a.gguf', model_bytes: 10, total_bytes: 13, shard_count: 1, expected_shards: 1, complete: true,
        files: [{ path: 'a.gguf', size: 10 }],
        dependencies: [
          { kind: 'mmproj', name: 'mmproj.gguf', total_bytes: 1, files: [{ path: 'mmproj.gguf', size: 1 }] },
          { kind: 'mtp', name: 'mtp.gguf', total_bytes: 1, files: [{ path: 'mtp.gguf', size: 1 }] },
          { kind: 'future', name: 'future.gguf', total_bytes: 1, files: [{ path: 'future.gguf', size: 1 }] }
        ]
      }]
    }
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path.startsWith('/api/v1/huggingface/search?')) return [{ id: 'acme/demo', downloads: 1, likes: 1, private: false, gated: false }]
      if (path.startsWith('/api/v1/huggingface/model?')) return detail
      if (path === '/api/v1/downloads' && options?.method === 'POST') return { id: 'job' }
      return []
    })
    const wrapper = await mountSuspended(DiscoverPage, { route: false })
    await wrapper.find('form').trigger('submit'); await flushPromises()
    await wrapper.find('[class*="cursor-pointer"]').trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('Vision projector')
    expect(wrapper.text()).toContain('MTP draft model')
    expect(wrapper.text()).toContain('future')
    await wrapper.findAll('button').find(button => button.text() === 'Download')!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('3 detected helper artifacts were added')
    wrapper.unmount()
  })
})

describe('Downloads live-event and formatting branches', () => {
  class FakeWebSocket {
    static instances: FakeWebSocket[] = []
    onopen: (() => void) | null = null
    onmessage: ((event: { data: string }) => void) | null = null
    onclose: (() => void) | null = null
    closed = false
    constructor(public url: string) { FakeWebSocket.instances.push(this) }
    close() { this.closed = true }
    open() { this.onopen?.() }
    message(value: unknown) { this.onmessage?.({ data: typeof value === 'string' ? value : JSON.stringify(value) }) }
    disconnect() { this.onclose?.() }
  }

  function job(id: string, state: string, extras: Record<string, any> = {}) {
    return {
      id, provider: 'huggingface', repo_id: `acme/${id}`, revision: 'r', artifact_id: id, name: `${id}.gguf`,
      state, total_bytes: 100, downloaded_bytes: 10, speed_bps: 0, created_at: 1, updated_at: 1, files: [], ...extras
    }
  }

  it('handles websocket snapshots, updates, inserts, deletes, malformed messages and reconnects', async () => {
    vi.useFakeTimers()
    FakeWebSocket.instances = []
    vi.stubGlobal('WebSocket', FakeWebSocket as any)
    mocks.request.mockResolvedValue([job('initial', 'QUEUED')])
    const wrapper = await mountSuspended(DownloadsPage, { route: false })
    await flushPromises()
    const socket = FakeWebSocket.instances[0]!
    expect(socket.url).toContain('/api/v1/downloads/ws')
    socket.open(); await flushPromises()
    expect(wrapper.text()).toContain('Live updates')
    socket.message('{bad json'); await flushPromises()
    socket.message({ type: 'download_snapshot', downloads: [job('snap', 'VERIFYING')] }); await flushPromises()
    expect(wrapper.text()).toContain('snap.gguf')
    socket.message({ type: 'download', job: job('snap', 'COMPLETED', { downloaded_bytes: 100 }) }); await flushPromises()
    expect(wrapper.text()).toContain('COMPLETED')
    socket.message({ type: 'download', job: job('new', 'DOWNLOADING') }); await flushPromises()
    expect(wrapper.text()).toContain('new.gguf')
    socket.message({ type: 'download_deleted', id: 'snap' }); await flushPromises()
    expect(wrapper.text()).not.toContain('snap.gguf')
    socket.message({ type: 'unknown' }); await flushPromises()
    socket.disconnect(); await flushPromises()
    expect(wrapper.text()).toContain('Reconnecting')
    await vi.advanceTimersByTimeAsync(1000)
    expect(FakeWebSocket.instances.length).toBeGreaterThan(1)
    wrapper.unmount()
  })

  it('covers progress, ETA, size and state display edge cases', async () => {
    vi.stubGlobal('WebSocket', undefined as any)
    const jobs = [
      job('zero', 'CANCELLED', { total_bytes: 0, downloaded_bytes: 0, speed_bps: 0, files: [{ path: 'zero.gguf', size: 0, downloaded_bytes: 0, state: 'CANCELLED' }] }),
      job('seconds', 'RESOLVING', { total_bytes: 100, downloaded_bytes: 50, speed_bps: 10 }),
      job('minutes', 'DOWNLOADING', { total_bytes: 7200, downloaded_bytes: 3600, speed_bps: 60 }),
      job('hours', 'FAILED', { total_bytes: 72_000, downloaded_bytes: 0, speed_bps: 10, error: 'boom' }),
      job('over', 'COMPLETED', { total_bytes: 10, downloaded_bytes: 20, speed_bps: 10, quantization: 'Q4', files: [{ path: 'provider.gguf', local_path: 'local/model.gguf', size: 10, downloaded_bytes: 10, state: 'COMPLETED' }] }),
      job('cancelled', 'CANCELLED'),
      job('unknown-state', 'SOMETHING')
    ]
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/downloads' && !options) return jobs
      if (path.endsWith('/retry')) return {}
      if (options?.method === 'DELETE') return undefined
      return {}
    })
    const wrapper = await mountSuspended(DownloadsPage, { route: false })
    await flushPromises()
    const text = wrapper.text()
    expect(text).toContain('5s remaining')
    expect(text).toContain('1m remaining')
    expect(text).toContain('2.0h remaining')
    expect(text).toContain('100%')
    expect(text).toContain('0 B / 0 B')
    expect(text).toContain('local/model.gguf')
    expect(text).toContain('boom')
    expect(text).toContain('Q4')
    const cancelledCards = wrapper.findAllComponents({ name: 'UCard' }).filter(card => card.text().includes('cancelled.gguf'))
    const card = cancelledCards[0] || wrapper.findAllComponents({ name: 'Card' }).find(card => card.text().includes('cancelled.gguf'))!
    const retry = card.findAll('button').find(button => button.text() === 'Retry')!
    await retry.trigger('click'); await flushPromises()
    const remove = card.findAll('button').find(button => button.text() === 'Remove')!
    await remove.trigger('click'); await flushPromises()
    expect(wrapper.text()).not.toContain('cancelled.gguf')
    wrapper.unmount()
  })

  it('survives WebSocket constructor failure', async () => {
    class ThrowingSocket { constructor() { throw new Error('blocked') } }
    vi.stubGlobal('WebSocket', ThrowingSocket as any)
    mocks.request.mockResolvedValue([])
    const wrapper = await mountSuspended(DownloadsPage, { route: false })
    await flushPromises()
    expect(wrapper.text()).toContain('Reconnecting')
    wrapper.unmount()
  })
})
