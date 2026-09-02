import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import AdminLiteLLMPage from '~/pages/admin/litellm.vue'
import AdminSidebar from '~/components/navigation/AdminSidebar.vue'
import AdminIndexPage from '~/pages/admin/index.vue'
import APIPage from '~/pages/api.vue'
import AdminServiceAccountsPage from '~/pages/admin/service-accounts.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

type LiteLLMState = {
  proxy_url: string
  api_base: string
  default_api_base: string
  proxy_key: { configured: boolean; prefix?: string }
  generated_key: { prefix?: string; name?: string }
  last_sync?: { at?: number; ok?: boolean; error?: string; counts?: { published?: number; unpublished?: number } }
}

function emptyStatus(): LiteLLMState {
  return {
    proxy_url: '',
    api_base: '',
    default_api_base: 'http://manager.test:8888/v1',
    proxy_key: { configured: false },
    generated_key: {}
  }
}

function configuredStatus(overrides: Partial<LiteLLMState> = {}): LiteLLMState {
  return {
    proxy_url: 'https://litellm.example.com',
    api_base: 'http://manager.test:8888/v1',
    default_api_base: 'http://manager.test:8888/v1',
    proxy_key: { configured: true, prefix: 'sk-proxy' },
    generated_key: { prefix: 'sk-gen', name: 'LiteLLM' },
    last_sync: { at: 1_700_000_000, ok: true, counts: { published: 2, unpublished: 0 } },
    ...overrides
  }
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
  return manager
}

function button(wrapper: any, text: string) {
  const found = wrapper.findAll('button').find((item: any) => item.text().trim() === text)
  if (!found) throw new Error(`Missing button ${text}`)
  return found
}

async function clickTestId(testId: string) {
  await flushPromises()
  const target = [...document.body.querySelectorAll<HTMLElement>(`[data-testid="${testId}"]`)].at(-1)
  if (!target) throw new Error(`Missing ${testId}`)
  target.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  await flushPromises()
}

async function toggleDisconnectUnpublish() {
  await flushPromises()
  const checkbox = document.body.querySelector<HTMLElement>('[data-testid="litellm-disconnect-unpublish"] [role="checkbox"]')
  if (!checkbox) throw new Error('Missing unpublish checkbox')
  checkbox.click()
  await flushPromises()
}

async function setWrapperInput(wrapper: any, selector: string, value: string) {
  const input = wrapper.find(selector)
  if (!input.exists()) throw new Error(`Missing input ${selector}`)
  await input.setValue(value)
}

beforeEach(() => {
  mocks.request.mockReset()
  resetManager()
})

describe('LiteLLM administration', () => {
  it('loads, saves, tests, syncs, rotates and disconnects without echoing secrets', async () => {
    let current = emptyStatus()
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/litellm' && options?.method === 'PUT') {
        current = configuredStatus({
          proxy_url: options.body.proxy_url,
          api_base: options.body.api_base,
          proxy_key: { configured: true, prefix: 'sk-new' }
        })
        return current
      }
      if (path === '/api/v1/litellm' && options?.method === 'DELETE') {
        expect(options.body).toEqual({ unpublish: true })
        current = emptyStatus()
        return undefined
      }
      if (path === '/api/v1/litellm/test' && options?.method === 'POST') return { ok: true }
      if (path === '/api/v1/litellm/sync' && options?.method === 'POST') {
        current = configuredStatus({ last_sync: { at: 1_700_000_000, ok: true, counts: { published: 3, unpublished: 1 } } })
        return { at: '2023-11-15T10:13:20Z', ok: true, published: 3, unpublished: 1 }
      }
      if (path === '/api/v1/litellm/rotate' && options?.method === 'POST') {
        current = configuredStatus({ generated_key: { prefix: 'sk-rotated', name: 'LiteLLM' } })
        return current
      }
      if (path === '/api/v1/litellm') return current
      return []
    })

    const wrapper = await mountSuspended(AdminLiteLLMPage, { route: '/admin/litellm' })
    await flushPromises()
    expect(wrapper.text()).toContain('Not configured')
    expect(wrapper.text()).toContain('Never synced')

    await setWrapperInput(wrapper, 'input[placeholder="https://litellm.example.com"]', 'https://litellm.example.com')
    await setWrapperInput(wrapper, 'input[placeholder="sk-…"]', 'sk-secret-proxy')
    await setWrapperInput(wrapper, 'input[placeholder="http://manager.test:8888/v1"]', 'http://manager.test:8888/v1')
    await button(wrapper, 'Save').trigger('click')
    await flushPromises()

    expect(mocks.request).toHaveBeenCalledWith('/api/v1/litellm', {
      method: 'PUT',
      body: {
        proxy_url: 'https://litellm.example.com',
        api_base: 'http://manager.test:8888/v1',
        proxy_key: 'sk-secret-proxy'
      }
    })
    expect(wrapper.text()).toContain('LiteLLM settings saved')
    expect(wrapper.text()).toContain('Configured')
    expect(wrapper.text()).toContain('sk-gen…')
    expect(wrapper.text()).not.toContain('sk-secret-proxy')

    await button(wrapper, 'Test connection').trigger('click')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/litellm/test', { method: 'POST' })
    expect(wrapper.get('[data-testid="litellm-test-success"]').text()).toContain('connection succeeded')

    await button(wrapper, 'Sync now').trigger('click')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/litellm/sync', { method: 'POST' })
    const statusLoads = mocks.request.mock.calls.filter(([path, options]) => path === '/api/v1/litellm' && !options?.method)
    expect(statusLoads.length).toBeGreaterThanOrEqual(2)
    expect(wrapper.get('[data-testid="litellm-sync-success"]').text()).toContain('Catalog sync completed')
    expect(wrapper.text()).toContain('3 published')

    await button(wrapper, 'Rotate LlamaRack key').trigger('click')
    await flushPromises()
    await clickTestId('litellm-rotate-confirm')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/litellm/rotate', { method: 'POST' })
    expect(wrapper.text()).toContain('sk-rotated…')
    expect(wrapper.text()).not.toContain('sk-rotated-secret')

    await button(wrapper, 'Disconnect').trigger('click')
    await flushPromises()
    await toggleDisconnectUnpublish()
    await clickTestId('litellm-disconnect-confirm')
    expect(wrapper.text()).toContain('Not configured')
    wrapper.unmount()
  })

  it('omits proxy_key on save when the field is left blank', async () => {
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/litellm' && options?.method === 'PUT') {
        expect(options.body).toEqual({
          proxy_url: 'https://litellm.example.com',
          api_base: 'http://manager.test:8888/v1'
        })
        return configuredStatus()
      }
      if (path === '/api/v1/litellm') return configuredStatus()
      return []
    })

    const wrapper = await mountSuspended(AdminLiteLLMPage, { route: '/admin/litellm' })
    await flushPromises()
    await setWrapperInput(wrapper, 'input[placeholder="https://litellm.example.com"]', 'https://litellm.example.com')
    await button(wrapper, 'Save').trigger('click')
    await flushPromises()
    wrapper.unmount()
  })

  it('surfaces load, save, test, sync, rotate and disconnect error variants', async () => {
    let mode: 'load-data' | 'load-message' | 'load-fallback' | 'save-data' | 'save-message' | 'save-fallback' | 'test-data' | 'test-message' | 'test-fallback' | 'sync-data' | 'sync-message' | 'sync-fallback' | 'rotate-data' | 'rotate-message' | 'rotate-fallback' | 'disconnect-data' | 'disconnect-message' | 'disconnect-fallback' | 'ok' = 'load-data'
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/litellm' && options?.method === 'PUT') {
        if (mode === 'save-data') throw { data: { error: 'save denied' } }
        if (mode === 'save-message') throw new Error('save exploded')
        if (mode === 'save-fallback') throw {}
        return configuredStatus()
      }
      if (path === '/api/v1/litellm' && options?.method === 'DELETE') {
        if (mode === 'disconnect-data') throw { data: { error: 'disconnect denied' } }
        if (mode === 'disconnect-message') throw new Error('disconnect exploded')
        if (mode === 'disconnect-fallback') throw {}
        return undefined
      }
      if (path === '/api/v1/litellm/test' && options?.method === 'POST') {
        if (mode === 'test-data') throw { data: { error: 'test denied' } }
        if (mode === 'test-message') throw new Error('test exploded')
        if (mode === 'test-fallback') throw {}
        return { ok: true }
      }
      if (path === '/api/v1/litellm/sync' && options?.method === 'POST') {
        if (mode === 'sync-data') throw { data: { error: 'sync denied' } }
        if (mode === 'sync-message') throw new Error('sync exploded')
        if (mode === 'sync-fallback') throw {}
        return configuredStatus()
      }
      if (path === '/api/v1/litellm/rotate' && options?.method === 'POST') {
        if (mode === 'rotate-data') throw { data: { error: 'rotate denied' } }
        if (mode === 'rotate-message') throw new Error('rotate exploded')
        if (mode === 'rotate-fallback') throw {}
        return configuredStatus()
      }
      if (path === '/api/v1/litellm') {
        if (mode === 'load-data') throw { data: { error: 'load denied' } }
        if (mode === 'load-message') throw new Error('load exploded')
        if (mode === 'load-fallback') throw {}
        return configuredStatus(mode === 'ok' ? {} : { last_sync: { ok: false, error: 'STORE_MODEL_IN_DB required' } })
      }
      return []
    })

    const wrapper = await mountSuspended(AdminLiteLLMPage, { route: '/admin/litellm' })
    await flushPromises()
    expect(wrapper.text()).toContain('load denied')

    mode = 'ok'
    await wrapper.unmount()

    for (const [next, expected] of [
      ['save-data', 'save denied'],
      ['save-message', 'save exploded'],
      ['save-fallback', 'Unable to save LiteLLM settings']
    ] as const) {
      mode = 'ok'
      const candidate = await mountSuspended(AdminLiteLLMPage, { route: '/admin/litellm' })
      await flushPromises()
      mode = next
      await setWrapperInput(candidate, 'input[placeholder="https://litellm.example.com"]', 'https://litellm.example.com')
      await setWrapperInput(candidate, 'input[placeholder="sk-…"]', 'sk-test')
      await button(candidate, 'Save').trigger('click')
      await flushPromises()
      expect(candidate.text()).toContain(expected)
      candidate.unmount()
    }

    for (const [next, expected] of [
      ['test-data', 'test denied'],
      ['test-message', 'test exploded'],
      ['test-fallback', 'Unable to test LiteLLM connection']
    ] as const) {
      mode = 'ok'
      const candidate = await mountSuspended(AdminLiteLLMPage, { route: '/admin/litellm' })
      await flushPromises()
      mode = next
      await button(candidate, 'Test connection').trigger('click')
      await flushPromises()
      expect(candidate.get('[data-testid="litellm-test-error"]').text()).toContain(expected)
      candidate.unmount()
    }

    for (const [next, expected] of [
      ['sync-data', 'sync denied'],
      ['sync-message', 'sync exploded'],
      ['sync-fallback', 'Unable to sync LiteLLM catalog']
    ] as const) {
      mode = 'ok'
      const candidate = await mountSuspended(AdminLiteLLMPage, { route: '/admin/litellm' })
      await flushPromises()
      mode = next
      await button(candidate, 'Sync now').trigger('click')
      await flushPromises()
      expect(candidate.get('[data-testid="litellm-sync-error"]').text()).toContain(expected)
      candidate.unmount()
    }

    for (const [next, expected] of [
      ['rotate-data', 'rotate denied'],
      ['rotate-message', 'rotate exploded'],
      ['rotate-fallback', 'Unable to rotate LiteLLM key']
    ] as const) {
      mode = 'ok'
      const candidate = await mountSuspended(AdminLiteLLMPage, { route: '/admin/litellm' })
      await flushPromises()
      mode = next
      await button(candidate, 'Rotate LlamaRack key').trigger('click')
      await flushPromises()
      await clickTestId('litellm-rotate-confirm')
      await flushPromises()
      expect(candidate.text()).toContain(expected)
      candidate.unmount()
    }

    for (const [next, expected] of [
      ['disconnect-data', 'disconnect denied'],
      ['disconnect-message', 'disconnect exploded'],
      ['disconnect-fallback', 'Unable to disconnect LiteLLM']
    ] as const) {
      mode = 'ok'
      const candidate = await mountSuspended(AdminLiteLLMPage, { route: '/admin/litellm' })
      await flushPromises()
      mode = next
      await button(candidate, 'Disconnect').trigger('click')
      await flushPromises()
      await clickTestId('litellm-disconnect-confirm')
      await flushPromises()
      expect(candidate.text()).toContain(expected)
      candidate.unmount()
    }
  })

  it('places LiteLLM after Hugging Face in the administration sidebar', async () => {
    const wrapper = await mountSuspended(AdminSidebar, { route: '/admin/litellm' })
    const labels = wrapper.findAll('[data-testid="admin-desktop-navigation"] a').map(link => link.text())
    const huggingFace = labels.findIndex(text => text.includes('Hugging Face'))
    const litellm = labels.findIndex(text => text.includes('LiteLLM'))
    expect(litellm).toBe(huggingFace + 1)
    expect(wrapper.get('[data-testid="admin-nav-litellm"]').attributes('href')).toBe('/admin/litellm')
    wrapper.unmount()
  })

  it('renders the dashboard LiteLLM summary card', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/admin/summary') {
        return {
          users: { total: 2, enabled: 2 },
          huggingface: { configured: false },
          litellm: { configured: true, last_sync_ok: false },
          llamacpp: { available: true, version: 'b1' }
        }
      }
      return []
    })
    const wrapper = await mountSuspended(AdminIndexPage, { route: '/admin' })
    await flushPromises()
    expect(wrapper.text()).toContain('LiteLLM')
    expect(wrapper.text()).toContain('Last sync failed')
    wrapper.unmount()
  })

  it('renders never synced when the summary omits last_sync_ok', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/admin/summary') {
        return {
          users: { total: 2, enabled: 2 },
          huggingface: { configured: false },
          litellm: { configured: true },
          llamacpp: { available: true, version: 'b1' }
        }
      }
      return []
    })
    const wrapper = await mountSuspended(AdminIndexPage, { route: '/admin' })
    await flushPromises()
    expect(wrapper.text()).toContain('Never synced')
    wrapper.unmount()
  })

  it('lists the generated LiteLLM key on API keys and keeps the hidden service account off Service accounts', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/api-keys') return [{
        id: 'k-user',
        name: 'Operator',
        prefix: 'sk-user',
        enabled: true,
        key_type: 'management',
        owner_kind: 'user',
        owner_id: 1,
        owner_name: 'admin',
        owner_enabled: true,
        created_at: 1
      }, {
        id: 'k-litellm',
        name: 'LiteLLM',
        prefix: 'sk-gen',
        enabled: true,
        key_type: 'inference',
        owner_kind: 'service_account',
        owner_id: 'sa-hidden',
        owner_name: 'LiteLLM',
        owner_enabled: true,
        managed: true,
        created_at: 2
      }]
      if (path === '/api/v1/users') return [{ id: 1, username: 'admin', enabled: true }]
      if (path === '/api/v1/admin/service-accounts') return [{
        id: 'sa-visible',
        name: 'CI bot',
        enabled: true,
        created_at: 1
      }]
      return []
    })

    const apiPage = await mountSuspended(APIPage, { route: '/api' })
    await flushPromises()
    expect(apiPage.text()).toContain('Operator')
    expect(apiPage.findAll('tr').some((row: any) => row.text().includes('LiteLLM'))).toBe(true)
    const litellmRow = apiPage.findAll('tr').find((row: any) => row.text().includes('LiteLLM'))
    expect(litellmRow?.text()).toContain('sk-gen')
    expect(litellmRow?.findAll('button').some((item: any) => item.text().trim() === 'Rotate')).toBe(false)
    apiPage.unmount()

    const accountsPage = await mountSuspended(AdminServiceAccountsPage, { route: '/admin/service-accounts' })
    await flushPromises()
    expect(accountsPage.text()).toContain('CI bot')
    expect(accountsPage.findAll('tr').some((row: any) => row.text().includes('LiteLLM'))).toBe(false)
    accountsPage.unmount()
  })
})
