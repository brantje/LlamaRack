import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import { useState } from '#imports'
import App from '~/app.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

class FakeIntersectionObserver {
  constructor(private readonly callback: IntersectionObserverCallback) {}
  observe() {}
  unobserve() {}
  disconnect() {}
  takeRecords() { return [] }
  readonly root = null
  readonly rootMargin = '0px'
  readonly thresholds = [0]
}

function resetState() {
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
  vi.stubGlobal('IntersectionObserver', FakeIntersectionObserver as any)
  mocks.request.mockReset()
  resetState()
})

describe('discover route transitions', () => {
  it('renders repository details after clicking a discovery card', async () => {
    const model = {
      id: 'zerodigest/Qwen3.8-27B-Uncensored-YMQ-MTP-GGUF',
      downloads: 12,
      likes: 3,
      private: false,
      gated: false
    }

    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/huggingface/search?')) {
        return { items: [model], next_cursor: '' }
      }
      if (path === '/api/v1/huggingface/model?repo=zerodigest%2FQwen3.8-27B-Uncensored-YMQ-MTP-GGUF') {
        return { ...model, revision: 'r1', artifacts: [] }
      }
      if (path === '/api/v1/hardware') return { gpus: [] }
      return []
    })

    const wrapper = await mountSuspended(App, { route: '/models/discover' })
    await flushPromises()

    const searchForm = wrapper.findAll('form').find(form => form.text().includes('Search'))
    expect(searchForm).toBeTruthy()
    await searchForm!.trigger('submit')
    await flushPromises()

    const card = wrapper.findAll('[class*="cursor-pointer"]').find(node => node.text().includes(model.id))
    expect(card).toBeTruthy()
    await card!.trigger('click')
    await flushPromises()

    expect(wrapper.vm.$route.path).toBe('/models/discover/zerodigest/Qwen3.8-27B-Uncensored-YMQ-MTP-GGUF')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/huggingface/model?repo=zerodigest%2FQwen3.8-27B-Uncensored-YMQ-MTP-GGUF')
    expect(wrapper.text()).toContain('REPOSITORY')
    expect(wrapper.text()).toContain('Available quantizations')
    expect(wrapper.text()).toContain(model.id)

    wrapper.unmount()
  })
})
