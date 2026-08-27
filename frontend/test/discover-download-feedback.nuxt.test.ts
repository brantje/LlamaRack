import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import DiscoverPage from '~/components/ModelsDiscover.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

const searchResult = { id: 'acme/demo', downloads: 10, likes: 2, private: false, gated: false, tags: ['gguf'] }
const detail = {
  ...searchResult,
  revision: 'rev1',
  artifacts: [{
    id: 'q4', name: 'demo-Q4_K_M.gguf', quantization: 'Q4_K_M', model_bytes: 1024, total_bytes: 1024,
    shard_count: 1, expected_shards: 1, complete: true, files: [{ path: 'demo-Q4_K_M.gguf', size: 1024 }]
  }]
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
  manager.profile.value = { path: '/app/llama-server', version: 'test', fingerprint: 'abcdefghijklmnopqrstuvwxyz', options: [] }
}

function buttons(wrapper: any) {
  const found = wrapper.findAllComponents({ name: 'Button' })
  return found.length ? found : wrapper.findAllComponents({ name: 'UButton' })
}

function downloadButton(wrapper: any) {
  const found = buttons(wrapper).find((component: any) => component.text().trim() === 'Download')
  if (!found) throw new Error('missing Download button')
  return found
}

async function openArtifact(wrapper: any) {
  await wrapper.find('form').trigger('submit')
  await flushPromises()
  const card = wrapper.findAllComponents({ name: 'Card' }).find((component: any) => component.text().includes('acme/demo'))
    || wrapper.findAllComponents({ name: 'UCard' }).find((component: any) => component.text().includes('acme/demo'))
  if (!card) throw new Error('missing model card')
  await card.trigger('click')
  await flushPromises()
}

beforeEach(() => {
  mocks.request.mockReset()
  resetManager()
})

describe('Discover download feedback', () => {
  it('keeps a successfully queued artifact spinning and disabled', async () => {
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path.startsWith('/api/v1/huggingface/search?')) return [searchResult]
      if (path.startsWith('/api/v1/huggingface/model?')) return detail
      if (path === '/api/v1/downloads' && options?.method === 'POST') return { id: 'job1' }
      return []
    })

    const wrapper = await mountSuspended(DiscoverPage, { route: false })
    await openArtifact(wrapper)

    const button = downloadButton(wrapper)
    expect(button.props('loading')).toBeFalsy()
    expect(button.props('disabled')).toBeFalsy()

    await button.trigger('click')
    await flushPromises()

    const queued = downloadButton(wrapper)
    expect(queued.props('loading')).toBe(true)
    expect(queued.props('disabled')).toBe(true)
    expect(wrapper.text()).toContain('was added to Downloads')

    await queued.trigger('click')
    await flushPromises()
    expect(mocks.request.mock.calls.filter(([path, options]) => path === '/api/v1/downloads' && options?.method === 'POST')).toHaveLength(1)
  })

  it('re-enables the button when queueing the download fails', async () => {
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path.startsWith('/api/v1/huggingface/search?')) return [searchResult]
      if (path.startsWith('/api/v1/huggingface/model?')) return detail
      if (path === '/api/v1/downloads' && options?.method === 'POST') throw new Error('queue failed')
      return []
    })

    const wrapper = await mountSuspended(DiscoverPage, { route: false })
    await openArtifact(wrapper)
    await downloadButton(wrapper).trigger('click')
    await flushPromises()

    const retryable = downloadButton(wrapper)
    expect(retryable.props('loading')).toBeFalsy()
    expect(retryable.props('disabled')).toBeFalsy()
    expect(wrapper.text()).toContain('queue failed')
  })
})
