import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import NewModelPage from '~/pages/models/new.vue'
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
  manager.profile.value = null
  return manager
}

async function chooseGGUF(wrapper: any, path: string) {
  const input = wrapper.findAll('input[type="radio"][name="gguf_path"]').find((item: any) => item.attributes('value') === path)
  if (!input) throw new Error(`Missing GGUF option ${path}`)
  await input.setValue()
  await flushPromises()
}

beforeEach(() => {
  mocks.request.mockReset()
  resetManager()
})

describe('Add model edge branches', () => {
  it('formats missing, invalid, fresh, day-old and dated artifact timestamps without filename inference', async () => {
    const now = Date.now()
    const oldTimestamp = new Date(now - 40 * 24 * 60 * 60 * 1000)
    const available = [
      { path: 'zero.gguf', name: 'zero.gguf', total_bytes: 0, suggested_options: {} },
      { path: 'invalid.gguf', name: 'invalid.gguf', total_bytes: 1, modified_at: 'not-a-date', suggested_options: {} },
      { path: 'fresh.gguf', name: 'fresh.gguf', total_bytes: 1, modified_at: new Date(now - 10_000).toISOString(), suggested_options: {} },
      { path: 'days.gguf', name: 'days.gguf', total_bytes: 1, modified_at: new Date(now - 2 * 24 * 60 * 60 * 1000).toISOString(), suggested_options: {} },
      { path: 'old.gguf', name: 'old.gguf', total_bytes: 1, modified_at: oldTimestamp.toISOString(), suggested_options: {} }
    ]
    mocks.request.mockImplementation(async (path: string) => path === '/api/v1/models/available' ? available : {})

    const wrapper = await mountSuspended(NewModelPage, { route: '/models/new' })
    await flushPromises()
    const rows = wrapper.findAll('[data-testid="gguf-option"]')
    expect(rows).toHaveLength(5)
    expect(rows[0]!.text()).toContain('Unknown size · modified Unknown')
    expect(rows[1]!.text()).toContain('modified Unknown')
    expect(rows[2]!.text()).toContain('modified just now')
    expect(rows[3]!.text()).toContain('modified 2d ago')
    expect(rows[4]!.text()).toContain(`modified ${oldTimestamp.toLocaleDateString()}`)
    expect(wrapper.get('[data-testid="companion-mmproj"]').text()).toContain('None found')
    expect(wrapper.get('[data-testid="companion-mtp"]').text()).toContain('None found')
    expect(wrapper.get('[data-testid="companion-empty-mmproj"]').classes()).toContain('text-[var(--neutral-800)]')
    expect(wrapper.get('[data-testid="companion-empty-mtp"]').classes()).toContain('text-[var(--neutral-800)]')
  })

  it('surfaces scan and metadata inspection API errors through their prescribed states', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/models/available') throw { data: { error: 'scan exploded' } }
      return {}
    })
    let wrapper = await mountSuspended(NewModelPage, { route: '/models/new' })
    await flushPromises()
    expect(wrapper.text()).toContain('scan exploded')
    expect(wrapper.text()).toContain('No unregistered GGUF files found')
    wrapper.unmount()

    resetManager()
    mocks.request.mockReset()
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/models/available') return [{ path: 'broken.gguf', name: 'broken.gguf', total_bytes: 1024, suggested_options: {} }]
      if (path === '/api/v1/models/inspect') throw { data: { error: 'inspect exploded' } }
      return {}
    })
    wrapper = await mountSuspended(NewModelPage, { route: '/models/new' })
    await flushPromises()
    await chooseGGUF(wrapper, 'broken.gguf')
    const warning = wrapper.get('[data-testid="metadata-warning"]')
    expect(warning.text()).toContain('Metadata warning')
    expect(warning.text()).toContain('inspect exploded')
    expect(wrapper.text()).not.toContain('Reading GGUF metadata…')
  })

  it('covers missing, incomplete and unquantized remote artifacts without inventing helper state', async () => {
    mocks.request.mockResolvedValue({ id: 'acme/demo', revision: 'rev', artifacts: [] })
    let wrapper = await mountSuspended(NewModelPage, { route: '/models/new?repo=acme%2Fdemo&artifact=q4' })
    await flushPromises()
    expect(wrapper.text()).toContain('Selected Hugging Face artifact is no longer available')
    wrapper.unmount()

    resetManager()
    mocks.request.mockReset()
    mocks.request.mockResolvedValue({
      id: 'acme/demo', revision: 'rev', artifacts: [{
        id: 'q4', name: 'partial.gguf', model_bytes: 10, total_bytes: 10,
        shard_count: 1, expected_shards: 2, complete: false, files: [{ path: 'partial.gguf', size: 10 }]
      }]
    })
    wrapper = await mountSuspended(NewModelPage, { route: '/models/new?repo=acme%2Fdemo&artifact=q4' })
    await flushPromises()
    expect(wrapper.text()).toContain('Selected Hugging Face split GGUF is incomplete')
    wrapper.unmount()

    resetManager()
    mocks.request.mockReset()
    mocks.request.mockResolvedValue({
      id: 'acme/demo', revision: 'rev', artifacts: [{
        id: 'q4', name: 'model.gguf', model_bytes: 10, total_bytes: 10,
        shard_count: 1, expected_shards: 1, complete: true, files: [{ path: 'model.gguf', size: 10 }]
      }]
    })
    wrapper = await mountSuspended(NewModelPage, { route: '/models/new?repo=acme%2Fdemo&artifact=q4' })
    await flushPromises()
    expect((wrapper.get('[data-testid="model-name"]').element as HTMLInputElement).value).toBe('demo')
    expect(wrapper.get('[data-testid="remote-artifact-summary"]').text()).not.toContain('including detected helpers')
    expect(wrapper.get('[data-testid="companion-mmproj"]').text()).toContain('None found')
    expect(wrapper.get('[data-testid="companion-mtp"]').text()).toContain('None found')
  })
})
