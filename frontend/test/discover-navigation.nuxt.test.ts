import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import { useState } from '#imports'
import DiscoverPage from '~/components/ModelsDiscover.vue'
import DiscoverDetailPage from '~/pages/models/discover/[owner]/[repo].vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

class FakeIntersectionObserver {
  static instances: FakeIntersectionObserver[] = []
  private callback: IntersectionObserverCallback

  constructor(callback: IntersectionObserverCallback) {
    this.callback = callback
    FakeIntersectionObserver.instances.push(this)
  }

  observe() {}
  unobserve() {}
  disconnect() {}
  takeRecords() { return [] }
  readonly root = null
  readonly rootMargin = '0px'
  readonly thresholds = [0]

  trigger() {
    this.callback([{ isIntersecting: true } as IntersectionObserverEntry], this as unknown as IntersectionObserver)
  }
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
  manager.profile.value = null

  useState<string>('models-discover-query').value = ''
  useState<string>('models-discover-author').value = ''
  useState<string>('models-discover-sort').value = 'trending_score'
  useState<any[]>('models-discover-results').value = []
  useState<string>('models-discover-next-cursor').value = ''
  useState<boolean>('models-discover-has-searched').value = false
  useState<number>('models-discover-scroll-position').value = 0
}

beforeEach(() => {
  mocks.request.mockReset()
  FakeIntersectionObserver.instances = []
  vi.stubGlobal('IntersectionObserver', FakeIntersectionObserver as any)
  resetManager()
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('Discover URL navigation and endless scrolling', () => {
  it('loads cursor pages and restores cached items and scroll position after detail navigation', async () => {
    const first = { id: 'acme/one', downloads: 1, likes: 1, private: false, gated: false }
    const second = { id: 'acme/two', downloads: 2, likes: 2, private: false, gated: false }
    const third = { id: 'acme/three', downloads: 3, likes: 3, private: false, gated: false }
    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/huggingface/search?') && path.includes('cursor=cursor-2')) {
        return { items: [third], next_cursor: '' }
      }
      if (path.startsWith('/api/v1/huggingface/search?')) {
        return { items: [first, second], next_cursor: 'cursor-2' }
      }
      if (path.startsWith('/api/v1/huggingface/model?')) {
        return { ...first, revision: 'r1', artifacts: [] }
      }
      return []
    })

    const list = await mountSuspended(DiscoverPage, { route: '/models/discover' })
    await list.find('form').trigger('submit')
    await flushPromises()
    expect(list.text()).toContain('acme/one')
    expect(list.text()).toContain('acme/two')
    expect(mocks.request.mock.calls[0][0]).toContain('/api/v1/huggingface/search?')
    expect(mocks.request.mock.calls[0][0]).not.toContain('cursor=')

    expect(FakeIntersectionObserver.instances).toHaveLength(1)
    FakeIntersectionObserver.instances[0]!.trigger()
    await flushPromises()
    expect(list.text()).toContain('acme/three')
    expect(mocks.request.mock.calls.some(([path]) => String(path).includes('cursor=cursor-2'))).toBe(true)

    const scrollY = vi.spyOn(window, 'scrollY', 'get').mockReturnValue(777)
    const firstCard = list.findAll('[class*="cursor-pointer"]').find(node => node.text().includes('acme/one'))
    expect(firstCard).toBeTruthy()
    await firstCard!.trigger('click')
    await flushPromises()
    expect(list.vm.$route.path).toBe('/models/discover/acme/one')
    expect(useState<number>('models-discover-scroll-position').value).toBe(777)
    scrollY.mockRestore()
    list.unmount()

    const detail = await mountSuspended(DiscoverPage, { props: { repoId: 'acme/one' }, route: false })
    await flushPromises()
    expect(detail.text()).toContain('acme/one')
    detail.unmount()

    const scrollTo = vi.fn()
    vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => { callback(0); return 1 })
    Object.defineProperty(window, 'scrollTo', { configurable: true, value: scrollTo })
    const callsBeforeReturn = mocks.request.mock.calls.filter(([path]) => String(path).startsWith('/api/v1/huggingface/search?')).length
    const restored = await mountSuspended(DiscoverPage, { route: '/models/discover' })
    await flushPromises()
    expect(restored.text()).toContain('acme/one')
    expect(restored.text()).toContain('acme/two')
    expect(restored.text()).toContain('acme/three')
    expect(scrollTo).toHaveBeenCalledWith(0, 777)
    expect(mocks.request.mock.calls.filter(([path]) => String(path).startsWith('/api/v1/huggingface/search?'))).toHaveLength(callsBeforeReturn)
    restored.unmount()
  })

  it('loads a model directly from /models/discover/:owner/:repo', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/huggingface/model?repo=Qwen%2FQwen3.8-Flash-Next') {
        return { id: 'Qwen/Qwen3.8-Flash-Next', downloads: 1, likes: 2, private: false, gated: false, revision: 'r1', artifacts: [] }
      }
      return []
    })

    const wrapper = await mountSuspended(DiscoverDetailPage, { route: '/models/discover/Qwen/Qwen3.8-Flash-Next' })
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/huggingface/model?repo=Qwen%2FQwen3.8-Flash-Next')
    expect(wrapper.text()).toContain('Qwen/Qwen3.8-Flash-Next')
    wrapper.unmount()
  })
})
