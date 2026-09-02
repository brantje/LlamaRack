import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import AdminSidebar from '~/components/navigation/AdminSidebar.vue'
import AdminSystemPage from '~/pages/admin/system.vue'
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
}

function systemResponse() {
  return {
    manager: {
      uptime_seconds: 42,
      runtime: {
        data_dir: '/a/very/long/configuration/path/that/must/remain-readable',
        models_dir: '/models',
        database_path: '/config/manager.db',
        listen_addr: ':8888',
        llama_server_path: '/app/llama-server'
      }
    },
    network: {
      effective_scheme: 'https',
      secure_cookie: true,
      allowed_origins: { value: ['https://manager.test', 'https://admin.test'], source: 'database', editable: true },
      trusted_proxies: { value: [{ cidr: '10.0.0.0/8', name: 'internal' }, '192.168.1.1'], source: 'database', editable: true },
      external_url: { value: 'https://manager.test' }
    },
    llamacpp: { available: true, path: '/app/llama-server', version: 'b9999', options: 123 }
  }
}

beforeEach(() => {
  mocks.request.mockReset()
  resetManager()
})

describe('Administration System UX feedback', () => {
  it('uses a compact accessible disclosure below the desktop breakpoint', async () => {
    const wrapper = await mountSuspended(AdminSidebar, { route: '/admin/system' })

    const mobile = wrapper.get('[data-testid="admin-mobile-navigation"]')
    expect(mobile.classes()).toContain('lg:hidden')
    expect(mobile.get('summary').text()).toContain('Administration · System')
    expect(mobile.findAll('a')).toHaveLength(9)
    expect(mobile.get('a[aria-current="page"]').text()).toContain('System')

    const desktop = wrapper.get('[data-testid="admin-desktop-navigation"]')
    expect(desktop.classes()).toContain('hidden')
    expect(desktop.classes()).toContain('lg:block')
    expect(desktop.findAll('a')).toHaveLength(9)
    expect(desktop.get('a[aria-current="page"]').text()).toContain('System')
    wrapper.unmount()
  })

  it('formats diagnostic collections without object stringification and exposes freshness', async () => {
    mocks.request.mockResolvedValue(systemResponse())
    const wrapper = await mountSuspended(AdminSystemPage, { route: '/admin/system' })
    await flushPromises()

    expect(wrapper.text()).not.toContain('[object Object]')
    expect(wrapper.text()).toContain('Data directory')
    expect(wrapper.text()).toContain('Models directory')
    expect(wrapper.text()).toContain('Listen address')
    expect(wrapper.findAll('[data-testid="allowed-origin-value"]')).toHaveLength(2)
    expect(wrapper.findAll('[data-testid="trusted-proxy-value"]')).toHaveLength(3)
    expect(wrapper.text()).toContain('42s')
    expect(wrapper.text()).toContain('42 s')
    expect(wrapper.text()).toContain('CIDR: 10.0.0.0/8')
    expect(wrapper.text()).toContain('Name: internal')
    expect(wrapper.text()).toContain('192.168.1.1')
    expect(wrapper.text()).toContain('Enabled')
    expect(wrapper.get('[data-testid="system-freshness"]').text()).toMatch(/^Updated \d{2}:\d{2}:\d{2}$/)
    wrapper.unmount()
  })

  it('shows a stable pending refresh state and a clear empty proxy state', async () => {
    const initial: any = systemResponse()
    initial.network.trusted_proxies = { value: '' }
    mocks.request.mockResolvedValueOnce(initial)

    let resolveRefresh!: (value: ReturnType<typeof systemResponse>) => void
    const pending = new Promise<ReturnType<typeof systemResponse>>(resolve => { resolveRefresh = resolve })
    mocks.request.mockImplementationOnce(() => pending)

    const wrapper = await mountSuspended(AdminSystemPage, { route: '/admin/system' })
    await flushPromises()
    expect(wrapper.text()).toContain('None configured')

    const refresh = wrapper.findAll('button').find(candidate => candidate.text().trim() === 'Refresh')
    expect(refresh).toBeTruthy()
    await refresh!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Refreshing diagnostics…')
    expect(wrapper.findAll('button').some(candidate => candidate.text().trim() === 'Refreshing…')).toBe(true)

    resolveRefresh(systemResponse())
    await flushPromises()
    expect(wrapper.text()).not.toContain('Refreshing diagnostics…')
    expect(wrapper.text()).toContain('CIDR: 10.0.0.0/8')
    wrapper.unmount()
  })
})
