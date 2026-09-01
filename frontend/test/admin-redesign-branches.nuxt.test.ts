import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import AdminGeneralPage from '~/pages/admin/general.vue'
import AdminSystemPage from '~/pages/admin/system.vue'
import AdminShell from '~/components/AdminShell.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

function resetManager(user = true) {
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = user ? { id: 1, username: 'admin', enabled: true } : null
  manager.models.value = []
  manager.instances.value = []
  manager.runtimes.value = {}
  manager.runtimeTelemetry.value = {}
  manager.profile.value = null
  return manager
}

function setting(value: any, source = 'default', editable = true) {
  return { value, source, editable }
}

function generalSettings(overrides: Record<string, any> = {}) {
  return {
    session_lifetime_seconds: setting(3600),
    login_protection_enabled: setting(true),
    login_failure_threshold: setting(5),
    login_lockout_seconds: setting(60),
    trusted_proxies: setting(''),
    allowed_origins: setting(''),
    external_url: setting(''),
    startup_timeout_seconds: setting(120),
    idle_unload_seconds: setting(300),
    always_on_reconcile_seconds: setting(15),
    max_pending_requests_per_instance: setting(32),
    max_pending_requests_global: setting(128),
    runtime: { data_dir: '/data', models_dir: '/models', database_path: '/data/db', listen_addr: ':8000', llama_server_path: '/llama-server' },
    ...overrides
  }
}

function button(wrapper: any, text: string) {
  const found = wrapper.findAll('button').find((candidate: any) => candidate.text().trim() === text)
  if (!found) throw new Error(`Missing button: ${text}`)
  return found
}

beforeEach(() => {
  mocks.request.mockReset()
  resetManager()
})

describe('Administration redesign branches', () => {
  it('renders the shared shell without an action slot', async () => {
    const wrapper = await mountSuspended(AdminShell, {
      route: '/admin/general',
      props: { title: 'General', description: 'Description' },
      slots: { default: '<p>Body</p>' }
    })
    expect(wrapper.text()).toContain('ADMINISTRATION')
    expect(wrapper.text()).toContain('Body')
    wrapper.unmount()
  })

  it('covers optional General settings, a locked Discover policy and post-save network fallback', async () => {
    let general = generalSettings({
      observability_retention_days: setting(14, 'environment', false),
      prometheus_auth_token: setting('metrics-secret', 'database', true)
    })
    let discover: any = { hybrid_recommendations_enabled: setting(false, 'environment', false) }
    let system: any = { network: { effective_scheme: 'https', secure_cookie: true } }

    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/settings/general' && options?.method === 'PUT') {
        expect(options.body.observability_retention_days).toBeUndefined()
        expect(options.body.prometheus_auth_token).toBe('metrics-secret-2')
        return general
      }
      if (path === '/api/v1/settings/discover' && options?.method === 'PUT') {
        expect(options.body).toEqual({ hybrid_recommendations_enabled: false })
        discover = {}
        return discover
      }
      if (path === '/api/v1/settings/general') return general
      if (path === '/api/v1/settings/discover') return discover
      if (path === '/api/v1/system') return system
      return []
    })

    const wrapper = await mountSuspended(AdminGeneralPage, { route: false })
    await flushPromises()
    expect(wrapper.find('[data-testid="observability-settings"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('History retention (days)')
    expect(wrapper.text()).toContain('Prometheus Bearer token')
    expect(wrapper.text()).toContain('environment')
    expect(wrapper.text()).toContain('database')
    expect(wrapper.text()).toContain('https')
    expect(wrapper.text()).toContain('Enabled')

    system = {}
    await wrapper.find('input[type="password"]').setValue('metrics-secret-2')
    await flushPromises()
    await button(wrapper, 'Save changes').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Manager settings saved')
    expect(wrapper.text()).toContain('unknown')
    expect(wrapper.text()).toContain('Disabled')
    wrapper.unmount()
  })

  it('covers General message and generic load errors plus structured and generic save errors', async () => {
    for (const [failure, message] of [
      [new Error('general exploded'), 'general exploded'],
      [{}, 'Unable to load manager settings']
    ] as const) {
      resetManager()
      mocks.request.mockReset()
      mocks.request.mockRejectedValue(failure)
      const wrapper = await mountSuspended(AdminGeneralPage, { route: false })
      await flushPromises()
      expect(wrapper.text()).toContain(message)
      wrapper.unmount()
    }

    for (const [failure, message] of [
      [{ data: { error: 'save denied' } }, 'save denied'],
      [{}, 'Unable to save manager settings']
    ] as const) {
      resetManager()
      mocks.request.mockReset()
      mocks.request.mockImplementation(async (path: string, options?: any) => {
        if (path === '/api/v1/settings/general' && options?.method === 'PUT') throw failure
        if (path === '/api/v1/settings/general') return generalSettings()
        if (path === '/api/v1/settings/discover') return { hybrid_recommendations_enabled: setting(true) }
        if (path === '/api/v1/system') return { network: { effective_scheme: 'http', secure_cookie: false } }
        return []
      })
      const wrapper = await mountSuspended(AdminGeneralPage, { route: false })
      await flushPromises()
      await wrapper.find('input[placeholder="https://manager.example.com"]').setValue('https://changed.test')
      await flushPromises()
      await button(wrapper, 'Save changes').trigger('click')
      await flushPromises()
      expect(wrapper.text()).toContain(message)
      wrapper.unmount()
    }
  })

  it('covers System message and generic load errors', async () => {
    for (const [failure, message] of [
      [new Error('system exploded'), 'system exploded'],
      [{}, 'Unable to load system diagnostics']
    ] as const) {
      resetManager()
      mocks.request.mockReset()
      mocks.request.mockRejectedValue(failure)
      const wrapper = await mountSuspended(AdminSystemPage, { route: false })
      await flushPromises()
      expect(wrapper.text()).toContain(message)
      wrapper.unmount()
    }
  })
})
