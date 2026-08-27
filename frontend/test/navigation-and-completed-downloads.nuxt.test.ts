import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import { existsSync, readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import DownloadsPage from '~/pages/downloads.vue'
import ModelsPage from '~/pages/models/index.vue'
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
  return manager
}

beforeEach(() => {
  mocks.request.mockReset()
  mocks.request.mockResolvedValue([])
  resetManager()
})

describe('model discovery navigation', () => {
  it('uses only /models/discover and removes Discover from the sidebar', async () => {
    const manager = resetManager()
    manager.models.value = [{ id: 'm1', name: 'Demo', gguf_path: 'demo.gguf', total_bytes: 1, context_length: 0 }]
    const wrapper = await mountSuspended(ModelsPage, { route: false })
    const discover = wrapper.findAll('a').find(link => link.text().trim() === 'Discover')
    expect(discover).toBeTruthy()
    expect(discover!.attributes('href')).toBe('/models/discover')

    const layout = readFileSync(resolve(process.cwd(), 'app/layouts/default.vue'), 'utf8')
    expect(layout).not.toContain("{ label: 'Discover'")
    expect(existsSync(resolve(process.cwd(), 'app/pages/discover.vue'))).toBe(false)
    expect(readFileSync(resolve(process.cwd(), 'app/pages/models/discover.vue'), 'utf8')).toContain('ModelsDiscover')
  })
})

describe('completed download visibility', () => {
  it('hides completed downloads by default and toggles them on demand', async () => {
    const completed = {
      id: 'done', provider: 'huggingface', repo_id: 'acme/done', revision: 'r', artifact_id: 'done', name: 'done.gguf',
      state: 'COMPLETED', total_bytes: 1024, downloaded_bytes: 1024, speed_bps: 0, created_at: 1, updated_at: 1, files: []
    }
    mocks.request.mockImplementation(async (path: string) => path === '/api/v1/downloads' ? [completed] : [])

    const wrapper = await mountSuspended(DownloadsPage, { route: false })
    await flushPromises()

    expect(wrapper.text()).toContain('No active downloads')
    expect(wrapper.text()).not.toContain('done.gguf')

    const toggle = wrapper.find('[data-testid="toggle-completed-downloads"]')
    expect(toggle.text()).toBe('Show completed (1)')
    await toggle.trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('done.gguf')
    expect(toggle.text()).toBe('Hide completed (1)')

    await toggle.trigger('click')
    await flushPromises()
    expect(wrapper.text()).not.toContain('done.gguf')
    expect(wrapper.text()).toContain('Completed downloads are hidden by default.')
    wrapper.unmount()
  })
})
