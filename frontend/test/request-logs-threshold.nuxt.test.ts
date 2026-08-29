import { afterEach, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import LogsPage from '~/pages/logs/index.vue'
import { useManager } from '~/composables/useManager'

enableAutoUnmount(afterEach)

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

it('covers named API keys and unfinished requests with an empty result', async () => {
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.models.value = []
  manager.instances.value = []
  manager.observabilityLive.value = null
  manager.runtimeEventsConnected.value = true
  mocks.request.mockResolvedValue({ items: [], has_more: false })

  const wrapper = await mountSuspended(LogsPage, { route: '/logs' })
  await flushPromises()
  const vm = wrapper.vm as any

  expect(vm.requestKeyAlias({ api_key: { id: 'key-id', name: 'Home Assistant', prefix: 'ha_' } })).toBe('Home Assistant')
  expect(vm.isPending({ finished_at: 1, result: '' })).toBe(true)
})
