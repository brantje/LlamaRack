import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import DiscoverPage from '~/pages/discover.vue'
import DownloadsPage from '~/pages/downloads.vue'
import SettingsPage from '~/pages/settings.vue'
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
  manager.profile.value = {
    path: '/app/llama-server', version: 'phase8', fingerprint: 'abcdefghijklmnopqrstuvwxyz', options: []
  }
  return manager
}

function components(wrapper: any, names: string[]) {
  const out: any[] = []
  const seen = new Set<Element>()
  for (const name of names) {
    for (const component of wrapper.findAllComponents({ name })) {
      if (component.element && !seen.has(component.element)) {
        seen.add(component.element)
        out.push(component)
      }
    }
  }
  return out
}

function button(wrapper: any, text: string) {
  const found = wrapper.findAll('button').find((item: any) => item.text().trim() === text)
  if (!found) throw new Error(`Missing button ${text}`)
  return found
}

beforeEach(() => {
  mocks.request.mockReset()
  mocks.request.mockResolvedValue([])
  resetManager()
})

afterEach(() => {
  vi.useRealTimers()
})

describe('Phase 8 Discover', () => {
  it('searches GGUF repositories, opens grouped artifacts and queues a download', async () => {
    const searchResults = [
      { id: 'acme/demo', downloads: 12345, likes: 22, private: true, gated: true, tags: ['gguf', 'text-generation', 'transformers', 'tag4', 'tag5', 'hidden'] },
      { id: 'other/model', downloads: 2, likes: 1, private: false, gated: false, tags: [] }
    ]
    const detail = {
      id: 'acme/demo', author: 'acme', downloads: 12345, likes: 22, private: false, gated: true, description: 'A demo model', revision: 'rev1', tags: ['gguf'],
      artifacts: [
        { id: 'split', name: 'demo-Q4_K_M.gguf', quantization: 'Q4_K_M', total_bytes: 5 * 1024 ** 3, shard_count: 2, expected_shards: 2, complete: true, files: [{ path: 'demo-00001-of-00002.gguf', size: 3 * 1024 ** 3 }, { path: 'demo-00002-of-00002.gguf', size: 2 * 1024 ** 3 }] },
        { id: 'tiny', name: 'tiny.gguf', total_bytes: 999, shard_count: 1, expected_shards: 1, complete: true, files: [{ path: 'tiny.gguf', size: 999 }] },
        { id: 'incomplete', name: 'broken-Q8_0.gguf', quantization: 'Q8_0', total_bytes: 1024, shard_count: 1, expected_shards: 2, complete: false, files: [{ path: 'broken-00001-of-00002.gguf', size: 1024 }] }
      ]
    }
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path.startsWith('/api/v1/huggingface/search?')) return searchResults
      if (path === '/api/v1/huggingface/model?repo=acme%2Fdemo') return detail
      if (path === '/api/v1/downloads' && options?.method === 'POST') return { id: 'job1' }
      return []
    })

    const wrapper = await mountSuspended(DiscoverPage, { route: false })
    expect(wrapper.text()).toContain('Search Hugging Face')
    await wrapper.find('input[placeholder="Qwen, Llama, Gemma… or Hugging Face URL"]').setValue('demo')
    await wrapper.find('input[placeholder="Optional"]').setValue('acme')
    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(mocks.request.mock.calls[0][0]).toContain('/api/v1/huggingface/search?')
    expect(mocks.request.mock.calls[0][0]).toContain('q=demo')
    expect(mocks.request.mock.calls[0][0]).toContain('author=acme')
    expect(wrapper.text()).toContain('acme/demo')
    expect(wrapper.text()).toContain('12,345')
    expect(wrapper.text()).toContain('Private')
    expect(wrapper.text()).toContain('Gated')
    expect(wrapper.text()).not.toContain('hidden')

    const cards = components(wrapper, ['Card', 'UCard'])
    const modelCard = cards.find(card => card.text().includes('acme/demo'))
    expect(modelCard).toBeTruthy()
    await modelCard.trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('A demo model')
    expect(wrapper.text()).toContain('Q4_K_M')
    expect(wrapper.text()).toContain('5.0 GB')
    expect(wrapper.text()).toContain('999 B')
    expect(wrapper.text()).toContain('2 shards')
    expect(wrapper.text()).toContain('1/2 shards')
    expect(wrapper.text()).toContain('Access may require approval')

    const downloadButtons = wrapper.findAll('button').filter(item => item.text().trim() === 'Download')
    expect(downloadButtons).toHaveLength(3)
    expect(downloadButtons[2].attributes('disabled')).toBeDefined()
    await downloadButtons[0].trigger('click')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/downloads', { method: 'POST', body: { repo_id: 'acme/demo', artifact_id: 'split' } })
    expect(wrapper.text()).toContain('was added to Downloads')

    await button(wrapper, 'Back to results').trigger('click')
    expect(wrapper.text()).toContain('other/model')
  })

  it('renders search, detail and download failure fallbacks', async () => {
    let mode: 'search-data' | 'search-message' | 'search-fallback' | 'detail' | 'download' = 'search-data'
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path.startsWith('/api/v1/huggingface/search?')) {
        if (mode === 'search-data') throw { data: { error: 'search denied' } }
        if (mode === 'search-message') throw new Error('search exploded')
        if (mode === 'search-fallback') throw {}
        return [{ id: 'acme/demo', downloads: 1, likes: 1, private: false, gated: false, tags: [] }]
      }
      if (path.startsWith('/api/v1/huggingface/model?')) {
        if (mode === 'detail') throw { data: { error: 'detail denied' } }
        return { id: 'acme/demo', downloads: 1, likes: 1, private: false, gated: false, revision: 'r', artifacts: [{ id: 'a', name: 'a.gguf', total_bytes: 2048, shard_count: 1, expected_shards: 1, complete: true, files: [{ path: 'a.gguf', size: 2048 }] }] }
      }
      if (path === '/api/v1/downloads' && options?.method === 'POST') throw new Error('download exploded')
      return []
    })

    const wrapper = await mountSuspended(DiscoverPage, { route: false })
    const submit = async () => { await wrapper.find('form').trigger('submit'); await flushPromises() }
    await submit(); expect(wrapper.text()).toContain('search denied')
    mode = 'search-message'; await submit(); expect(wrapper.text()).toContain('search exploded')
    mode = 'search-fallback'; await submit(); expect(wrapper.text()).toContain('Unable to search Hugging Face')

    mode = 'detail'
    await submit()
    const modelCard = components(wrapper, ['Card', 'UCard']).find(card => card.text().includes('acme/demo'))!
    await modelCard.trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('detail denied')

    mode = 'download'
    await modelCard.trigger('click'); await flushPromises()
    await button(wrapper, 'Download').trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('download exploded')
  })
})

describe('Phase 8 Downloads', () => {
  it('renders progress, speed and ETA across states and runs cancel/retry actions', async () => {
    vi.useFakeTimers()
    const jobs = [
      { id: 'active', provider: 'huggingface', repo_id: 'acme/active', revision: 'r', artifact_id: 'a', name: 'active.gguf', state: 'DOWNLOADING', total_bytes: 7200, downloaded_bytes: 3600, speed_bps: 60, created_at: 1, updated_at: 1, files: [{ path: 'active.gguf', size: 7200, state: 'DOWNLOADING', downloaded_bytes: 3600 }] },
      { id: 'verify', provider: 'huggingface', repo_id: 'acme/verify', revision: 'r', artifact_id: 'v', name: 'verify.gguf', state: 'VERIFYING', total_bytes: 100, downloaded_bytes: 100, speed_bps: 0, created_at: 1, updated_at: 1, files: [] },
      { id: 'done', provider: 'huggingface', repo_id: 'acme/done', revision: 'r', artifact_id: 'd', name: 'done.gguf', state: 'COMPLETED', total_bytes: 0, downloaded_bytes: 0, speed_bps: 0, created_at: 1, updated_at: 1, files: [] },
      { id: 'failed', provider: 'huggingface', repo_id: 'acme/failed', revision: 'r', artifact_id: 'f', name: 'failed.gguf', quantization: 'Q8_0', state: 'FAILED', total_bytes: 1024 ** 3, downloaded_bytes: 1, speed_bps: 1, error: 'disk full', created_at: 1, updated_at: 1, files: [{ path: 'failed.gguf', local_path: 'huggingface/acme/failed/failed.gguf', size: 1024 ** 3, state: 'FAILED', downloaded_bytes: 1 }] },
      { id: 'cancelled', provider: 'huggingface', repo_id: 'acme/cancelled', revision: 'r', artifact_id: 'c', name: 'cancelled.gguf', state: 'CANCELLED', total_bytes: 100, downloaded_bytes: 5, speed_bps: 0, created_at: 1, updated_at: 1, files: [] }
    ]
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/downloads') return jobs
      if (options?.method === 'POST') return {}
      return []
    })
    const wrapper = await mountSuspended(DownloadsPage, { route: false })
    await flushPromises()
    expect(wrapper.text()).toContain('active.gguf')
    expect(wrapper.text()).toContain('50%')
    expect(wrapper.text()).toContain('60 B/s')
    expect(wrapper.text()).toContain('1m remaining')
    expect(wrapper.text()).toContain('disk full')
    expect(wrapper.text()).toContain('1.0 GB')
    expect(wrapper.text()).toContain('Q8_0')

    await button(wrapper, 'Cancel').trigger('click'); await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/downloads/active/cancel', { method: 'POST' })
    const failedCard = components(wrapper, ['Card', 'UCard']).find(card => card.text().includes('failed.gguf'))
    expect(failedCard).toBeTruthy()
    const failedRetry = failedCard.findAll('button').find((item: any) => item.text().trim() === 'Retry')
    expect(failedRetry).toBeTruthy()
    await failedRetry.trigger('click'); await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/downloads/failed/retry', { method: 'POST' })

    const callsBeforeTimer = mocks.request.mock.calls.length
    await vi.advanceTimersByTimeAsync(1500)
    await flushPromises()
    expect(mocks.request.mock.calls.length).toBeGreaterThan(callsBeforeTimer)
    wrapper.unmount()
  })

  it('covers empty state, refresh and action error fallbacks', async () => {
    mocks.request.mockResolvedValue([])
    let wrapper = await mountSuspended(DownloadsPage, { route: false }); await flushPromises()
    expect(wrapper.text()).toContain('No downloads yet')
    expect(wrapper.text()).toContain('Open Discover')
    await button(wrapper, 'Refresh').trigger('click'); await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/downloads')
    wrapper.unmount()

    let action: 'cancel-data' | 'retry-message' | 'refresh-fallback' = 'cancel-data'
    const jobs = [
      { id: 'a', provider: 'huggingface', repo_id: 'a/b', revision: 'r', artifact_id: 'a', name: 'a.gguf', state: 'DOWNLOADING', total_bytes: 10, downloaded_bytes: 1, speed_bps: 20, created_at: 1, updated_at: 1, files: [] },
      { id: 'f', provider: 'huggingface', repo_id: 'a/f', revision: 'r', artifact_id: 'f', name: 'f.gguf', state: 'FAILED', total_bytes: 10, downloaded_bytes: 1, speed_bps: 0, created_at: 1, updated_at: 1, files: [] }
    ]
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/downloads' && !options) {
        if (action === 'refresh-fallback') throw {}
        return jobs
      }
      if (path.endsWith('/cancel')) throw { data: { error: 'cancel denied' } }
      if (path.endsWith('/retry')) throw new Error('retry exploded')
      return []
    })
    wrapper = await mountSuspended(DownloadsPage, { route: false }); await flushPromises()
    await button(wrapper, 'Cancel').trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('cancel denied')
    action = 'retry-message'
    await button(wrapper, 'Retry').trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('retry exploded')
    action = 'refresh-fallback'
    await button(wrapper, 'Refresh').trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('Unable to load downloads')
  })
})

describe('Phase 8 Hugging Face settings', () => {
  it('loads, saves, replaces and removes the encrypted provider token', async () => {
    let configured = false
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/huggingface/token' && options?.method === 'PUT') {
        configured = true
        return { configured: true, prefix: 'hf_abc' }
      }
      if (path === '/api/v1/huggingface/token' && options?.method === 'DELETE') {
        configured = false
        return undefined
      }
      if (path === '/api/v1/huggingface/token') return configured ? { configured: true, prefix: 'hf_abc' } : { configured: false }
      if (path === '/api/v1/llamacpp/config') return { effective: { global: {} } }
      if (path === '/api/v1/models' || path === '/api/v1/instances') return []
      if (path === '/api/v1/llamacpp/profile') return { available: true, profile: resetManager().profile.value }
      return []
    })
    const wrapper = await mountSuspended(SettingsPage, { route: false }); await flushPromises()
    expect(wrapper.text()).toContain('Not configured')
    const token = wrapper.find('input[placeholder="hf_…"]')
    await token.setValue('hf_secret')
    await button(wrapper, 'Save token').trigger('click'); await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/huggingface/token', { method: 'PUT', body: { token: 'hf_secret' } })
    expect(wrapper.text()).toContain('Hugging Face token saved')
    expect(wrapper.text()).toContain('Configured')
    await wrapper.find('input[placeholder="hf_…"]').setValue('hf_replacement')
    await button(wrapper, 'Replace').trigger('click'); await flushPromises()
    await button(wrapper, 'Remove').trigger('click'); await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/huggingface/token', { method: 'DELETE' })
    expect(wrapper.text()).toContain('Not configured')
    await button(wrapper, 'Refresh').trigger('click'); await flushPromises()
  })

  it('shows token load/save/remove error variants', async () => {
    let mode: 'load-data' | 'load-message' | 'load-fallback' | 'save-data' | 'save-message' | 'save-fallback' | 'remove-data' | 'remove-message' | 'remove-fallback' = 'load-data'
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/llamacpp/config') return { effective: { global: {} } }
      if (path === '/api/v1/huggingface/token' && options?.method === 'PUT') {
        if (mode === 'save-data') throw { data: { error: 'token save denied' } }
        if (mode === 'save-message') throw new Error('token save exploded')
        if (mode === 'save-fallback') throw {}
        return { configured: true, prefix: 'hf_abc' }
      }
      if (path === '/api/v1/huggingface/token' && options?.method === 'DELETE') {
        if (mode === 'remove-data') throw { data: { error: 'token remove denied' } }
        if (mode === 'remove-message') throw new Error('token remove exploded')
        if (mode === 'remove-fallback') throw {}
        return undefined
      }
      if (path === '/api/v1/huggingface/token') {
        if (mode === 'load-data') throw { data: { error: 'token load denied' } }
        if (mode === 'load-message') throw new Error('token load exploded')
        if (mode === 'load-fallback') throw {}
        return { configured: true, prefix: 'hf_abc' }
      }
      if (path === '/api/v1/models' || path === '/api/v1/instances') return []
      if (path === '/api/v1/llamacpp/profile') throw new Error('none')
      return []
    })
    const wrapper = await mountSuspended(SettingsPage, { route: false }); await flushPromises()
    expect(wrapper.text()).toContain('token load denied')
    mode = 'load-message'; await button(wrapper, 'Refresh').trigger('click'); await flushPromises(); expect(wrapper.text()).toContain('token load exploded')
    mode = 'load-fallback'; await button(wrapper, 'Refresh').trigger('click'); await flushPromises(); expect(wrapper.text()).toContain('Unable to load Hugging Face token status')

    for (const [next, expected] of [['save-data', 'token save denied'], ['save-message', 'token save exploded'], ['save-fallback', 'Unable to save Hugging Face token']] as const) {
      mode = next
      await wrapper.find('input[placeholder="hf_…"]').setValue('hf_test')
      await button(wrapper, 'Save token').trigger('click'); await flushPromises()
      expect(wrapper.text()).toContain(expected)
    }

    mode = 'load-data'
    mocks.request.mockImplementationOnce(async () => ({ effective: { global: {} } }))
    mocks.request.mockImplementationOnce(async () => ({ configured: true, prefix: 'hf_abc' }))
    await button(wrapper, 'Refresh').trigger('click'); await flushPromises()

    for (const [next, expected] of [['remove-data', 'token remove denied'], ['remove-message', 'token remove exploded'], ['remove-fallback', 'Unable to remove Hugging Face token']] as const) {
      mode = next
      await button(wrapper, 'Remove').trigger('click'); await flushPromises()
      expect(wrapper.text()).toContain(expected)
    }
  })
})
