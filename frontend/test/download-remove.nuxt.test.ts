import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import DownloadsPage from '~/pages/downloads.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

beforeEach(() => {
  vi.useFakeTimers()
  vi.stubGlobal('WebSocket', undefined)
  mocks.request.mockReset()
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
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('cancelled download removal', () => {
  it('removes a cancelled download from history', async () => {
    const cancelled = {
      id: 'cancelled', provider: 'huggingface', repo_id: 'acme/demo', revision: 'r', artifact_id: 'a',
      name: 'cancelled.gguf', state: 'CANCELLED', total_bytes: 100, downloaded_bytes: 25, speed_bps: 0,
      created_at: 1, updated_at: 1, files: []
    }
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/downloads' && !options) return [cancelled]
      if (path === '/api/v1/downloads/cancelled' && options?.method === 'DELETE') return undefined
      return []
    })

    const wrapper = await mountSuspended(DownloadsPage, { route: false })
    await flushPromises()
    expect(wrapper.text()).toContain('cancelled.gguf')
    const remove = wrapper.findAll('button').find(button => button.text().trim() === 'Remove')
    expect(remove).toBeTruthy()
    await remove!.trigger('click')
    await flushPromises()

    expect(mocks.request).toHaveBeenCalledWith('/api/v1/downloads/cancelled', { method: 'DELETE' })
    expect(wrapper.text()).not.toContain('cancelled.gguf')
    wrapper.unmount()
  })
})
