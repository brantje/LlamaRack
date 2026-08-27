import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import DownloadsPage from '~/pages/downloads.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

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

function job(overrides: Record<string, any> = {}) {
  return {
    id: 'job', provider: 'huggingface', repo_id: 'acme/demo', revision: 'main', artifact_id: 'artifact',
    name: 'demo.gguf', state: 'DOWNLOADING', total_bytes: 100, downloaded_bytes: 25, speed_bps: 5,
    created_at: 1, updated_at: 1, files: [], ...overrides
  }
}

function button(wrapper: any, label: string) {
  const found = wrapper.findAll('button').find((candidate: any) => candidate.text().trim() === label)
  if (!found) throw new Error(`Missing ${label} button`)
  return found
}

beforeEach(() => {
  vi.useFakeTimers()
  vi.stubGlobal('WebSocket', undefined)
  mocks.request.mockReset()
  resetManager()
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('download branch coverage', () => {
  it('renders state, progress, ETA, quantization and file variants', async () => {
    const jobs = [
      job({ id: 'seconds', name: 'seconds.gguf', quantization: 'Q4_K_M', downloaded_bytes: 50, speed_bps: 10, total_bytes: 100, files: [{ path: 'part.gguf', local_path: '/models/part.gguf', size: 1024, downloaded_bytes: 512, state: 'DOWNLOADING' }] }),
      job({ id: 'minutes', name: 'minutes.gguf', downloaded_bytes: 0, speed_bps: 1, total_bytes: 120, state: 'VERIFYING' }),
      job({ id: 'hours', name: 'hours.gguf', downloaded_bytes: 0, speed_bps: 1, total_bytes: 7200, state: 'FAILED', error: 'checksum mismatch' }),
      job({ id: 'cancelled', name: 'cancelled.gguf', state: 'CANCELLED', speed_bps: 0, total_bytes: 0, downloaded_bytes: 0 }),
      job({ id: 'completed', name: 'completed.gguf', state: 'COMPLETED', total_bytes: 1024 * 1024 * 1024, downloaded_bytes: 1024 * 1024 * 1024, speed_bps: 0 })
    ]
    mocks.request.mockResolvedValue(jobs)

    const wrapper = await mountSuspended(DownloadsPage, { route: false })
    await flushPromises()
    expect(wrapper.text()).toContain('Q4_K_M')
    expect(wrapper.text()).toContain('5s remaining')
    expect(wrapper.text()).toContain('2m remaining')
    expect(wrapper.text()).toContain('2.0h remaining')
    expect(wrapper.text()).toContain('checksum mismatch')
    expect(wrapper.text()).toContain('0 B / 0 B')
    expect(wrapper.text()).toContain('512 B / 1 KB')
    expect(wrapper.text()).toContain('/models/part.gguf')
    expect(wrapper.text()).not.toContain('completed.gguf')
    wrapper.unmount()
  })

  it('cancels and retries jobs, including retry responses without an id', async () => {
    let current = job({ id: 'a/b', name: 'active.gguf' })
    let retryWithID = false
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/downloads' && !options?.method) return [current]
      if (path === '/api/v1/downloads/a%2Fb/cancel' && options?.method === 'POST') return undefined
      if (path === '/api/v1/downloads/a%2Fb/retry' && options?.method === 'POST') {
        return retryWithID ? job({ id: 'a/b', name: 'active.gguf', state: 'QUEUED', downloaded_bytes: 0, speed_bps: 0 }) : {}
      }
      return undefined
    })

    const wrapper = await mountSuspended(DownloadsPage, { route: false })
    await flushPromises()
    await button(wrapper, 'Cancel').trigger('click')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/downloads/a%2Fb/cancel', { method: 'POST' })
    expect(wrapper.text()).toContain('CANCELLED')

    await button(wrapper, 'Retry').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('CANCELLED')

    retryWithID = true
    await button(wrapper, 'Retry').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('QUEUED')
    wrapper.unmount()
  })

  it('covers load, cancel, retry and remove error fallbacks', async () => {
    for (const [failure, message] of [
      [{ data: { error: 'load denied' } }, 'load denied'],
      [new Error('load exploded'), 'load exploded'],
      [{}, 'Unable to load downloads']
    ] as const) {
      mocks.request.mockReset()
      mocks.request.mockRejectedValue(failure)
      const wrapper = await mountSuspended(DownloadsPage, { route: false })
      await flushPromises()
      expect(wrapper.text()).toContain(message)
      wrapper.unmount()
    }

    for (const [action, path, failure, message] of [
      ['Cancel', '/api/v1/downloads/job/cancel', { data: { error: 'cancel denied' } }, 'cancel denied'],
      ['Cancel', '/api/v1/downloads/job/cancel', {}, 'Unable to cancel download'],
      ['Retry', '/api/v1/downloads/job/retry', new Error('retry exploded'), 'retry exploded'],
      ['Retry', '/api/v1/downloads/job/retry', {}, 'Unable to retry download'],
      ['Remove', '/api/v1/downloads/job', { data: { error: 'remove denied' } }, 'remove denied'],
      ['Remove', '/api/v1/downloads/job', {}, 'Unable to remove download']
    ] as const) {
      resetManager()
      mocks.request.mockReset()
      const state = action === 'Cancel' ? 'DOWNLOADING' : action === 'Retry' ? 'FAILED' : 'CANCELLED'
      mocks.request.mockImplementation(async (requestPath: string, options?: any) => {
        if (requestPath === '/api/v1/downloads' && !options?.method) return [job({ state })]
        if (requestPath === path) throw failure
        return undefined
      })
      const wrapper = await mountSuspended(DownloadsPage, { route: false })
      await flushPromises()
      await button(wrapper, action).trigger('click')
      await flushPromises()
      expect(wrapper.text()).toContain(message)
      wrapper.unmount()
    }
  })

  it('polls active downloads only while live updates are unavailable', async () => {
    let calls = 0
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/downloads') {
        calls++
        return [job()]
      }
      return []
    })
    const wrapper = await mountSuspended(DownloadsPage, { route: false })
    await flushPromises()
    expect(calls).toBe(1)

    await vi.advanceTimersByTimeAsync(1500)
    await flushPromises()
    expect(calls).toBe(2)
    wrapper.unmount()
  })
})
