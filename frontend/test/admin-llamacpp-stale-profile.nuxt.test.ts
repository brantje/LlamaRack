import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import AdminLlamaCppPage from '~/pages/admin/llamacpp.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

beforeEach(() => {
  mocks.request.mockReset()
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.profile.value = {
    path: '/usr/local/bin/old-llama-server',
    version: 'old',
    fingerprint: 'stale-profile',
    options: [{ key: 'ctx-size', description: 'Context size' }]
  }
})

describe('Administration llama.cpp profile freshness', () => {
  it('clears a previously discovered profile when the current config reports no binary', async () => {
    mocks.request.mockResolvedValue({ effective: { global: {} } })
    const wrapper = await mountSuspended(AdminLlamaCppPage, { route: false })
    await flushPromises()
    expect(useManager().profile.value).toBeNull()
    expect(wrapper.get('[data-testid="llamacpp-unavailable-warning"]').text()).toContain('llama-server could not be discovered')
    expect(wrapper.text()).not.toContain('/usr/local/bin/old-llama-server')
    wrapper.unmount()
  })
})
