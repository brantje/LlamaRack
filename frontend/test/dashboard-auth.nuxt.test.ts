import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import DashboardPage from '~/pages/index.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

function resetManager() {
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = null
  manager.models.value = []
  manager.instances.value = []
  manager.runtimes.value = {}
  manager.runtimeTelemetry.value = {}
  manager.observabilityLive.value = null
  return manager
}

beforeEach(() => {
  mocks.request.mockReset()
  resetManager()
})

describe('Dashboard authentication lifecycle', () => {
  it('waits for an authenticated user before requesting protected observability data', async () => {
    const manager = resetManager()
    mocks.request.mockImplementation(async (path: string) => {
      if (path.startsWith('/api/v1/observability/summary')) {
        return {
          since: Date.now() - 900_000,
          requests: 0,
          successes: 0,
          errors: 0,
          active: 0,
          queued: 0,
          active_api_keys: 0,
          prompt_tokens: 0,
          generated_tokens: 0,
          total_tokens: 0,
          lifecycle: { autoloads: 0, failed_starts: 0, load_duration_ms_total: 0 },
          hardware: {
            hardware: { ram_total_bytes: 0, ram_available_bytes: 0, gpus: [], processes: [], collected_at: '' },
            telemetry: []
          }
        }
      }
      if (path.startsWith('/api/v1/observability/requests')) return { items: [] }
      if (path === '/api/v1/settings/general') return { idle_unload_seconds: { value: 0, source: 'default', editable: true } }
      return {}
    })

    const wrapper = await mountSuspended(DashboardPage, { route: '/' })
    await flushPromises()
    expect(mocks.request).not.toHaveBeenCalled()

    const refresh = wrapper.findAll('button').find(candidate => candidate.text().trim() === 'Refresh')
    expect(refresh).toBeTruthy()
    manager.initialized.value = false
    await refresh!.trigger('click')
    await flushPromises()
    expect(mocks.request).not.toHaveBeenCalled()

    manager.initialized.value = true
    await refresh!.trigger('click')
    await flushPromises()
    expect(mocks.request).not.toHaveBeenCalled()

    manager.user.value = { id: 1, username: 'admin', enabled: true }
    await flushPromises()

    expect(mocks.request).toHaveBeenCalledTimes(3)
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/observability/summary?window_seconds=900')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/settings/general')
    expect(wrapper.text()).not.toContain('Observability data unavailable')
    wrapper.unmount()
  })
})
