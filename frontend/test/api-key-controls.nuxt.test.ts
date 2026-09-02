import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import APIPage from '~/pages/api.vue'
import { useManager, type APIKey } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

function sampleKey(overrides: Partial<APIKey> = {}): APIKey {
  return {
    id: 'k1',
    name: 'LiteLLM',
    prefix: 'sk-abcd1234',
    enabled: true,
    key_type: 'inference',
    owner_kind: 'user',
    owner_id: 1,
    owner_name: 'admin',
    owner_enabled: true,
    created_at: 1,
    ...overrides
  }
}

function resetAuthenticated() {
  const manager = useManager()
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

async function clickConfirmation(kind: 'confirm' | 'cancel') {
  await flushPromises()
  const buttons = [...document.body.querySelectorAll<HTMLButtonElement>(`[data-testid="confirmation-${kind}"]`)]
  const button = buttons.at(-1)
  if (!button) throw new Error(`Missing confirmation ${kind} button`)
  button.click()
  await flushPromises()
}

async function click(testId: string) {
  const button = [...document.body.querySelectorAll<HTMLElement>(`[data-testid="${testId}"]`)].at(-1)
  if (!button) throw new Error(`Missing ${testId}`)
  button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  await flushPromises()
}

function menus(wrapper: any) {
  return [...wrapper.findAllComponents({ name: 'SelectMenu' }), ...wrapper.findAllComponents({ name: 'USelectMenu' })]
}

beforeEach(() => {
  mocks.request.mockReset()
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: vi.fn() } })
  resetAuthenticated()
})

describe('API key controls', () => {
  it('disables, enables, edits and rotates in place without revoke', async () => {
    let enabled = true
    let name = 'LiteLLM'
    let prefix = 'sk-abcd1234'
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/api-keys' && !options?.method) return [sampleKey({ enabled, name, prefix })]
      if (path === '/api/v1/api-keys/k1' && options?.method === 'PATCH') {
        if (typeof options.body.enabled === 'boolean') enabled = options.body.enabled
        if (options.body.name) name = options.body.name
        return undefined
      }
      if (path === '/api/v1/api-keys/k1/rotate' && options?.method === 'POST') {
        prefix = 'sk-new12345'
        return { key: sampleKey({ prefix, enabled, name }), secret: 'sk-replacement' }
      }
      if (path === '/api/v1/users') return [{ id: 1, username: 'admin', enabled: true }]
      if (path === '/api/v1/admin/service-accounts') return []
      return []
    })

    const wrapper = await mountSuspended(APIPage, { route: false })
    await flushPromises()
    expect(wrapper.text()).toContain('Enabled')
    expect(wrapper.findAll('button').some(button => button.text() === 'Revoke')).toBe(false)

    await wrapper.findAll('button').find(button => button.text() === 'Disable')!.trigger('click')
    expect(document.body.textContent).toContain('Clients using this key will fail until it is enabled again.')
    await clickConfirmation('confirm')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/api-keys/k1', { method: 'PATCH', body: { enabled: false } })
    expect(wrapper.text()).toContain('Disabled')

    await wrapper.findAll('button').find(button => button.text() === 'Enable')!.trigger('click')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/api-keys/k1', { method: 'PATCH', body: { enabled: true } })

    await wrapper.findAll('button').find(button => button.text() === 'Edit')!.trigger('click')
    await flushPromises()
    const nameInput = [...document.body.querySelectorAll<HTMLInputElement>('[data-testid="key-name"]')].at(-1)!
    nameInput.value = 'Gateway'
    nameInput.dispatchEvent(new Event('input', { bubbles: true }))
    await click('api-key-save')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/api-keys/k1', {
      method: 'PATCH',
      body: { name: 'Gateway', owner_user_id: 1, owner_service_account_id: null, expires_on: null, instance_ids: [] }
    })

    await wrapper.findAll('button').find(button => button.text() === 'Rotate')!.trigger('click')
    expect(document.body.textContent).toContain('The current secret will stop working immediately')
    await clickConfirmation('confirm')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/api-keys/k1/rotate', { method: 'POST' })
    expect(document.body.textContent).toContain('sk-replacement')
    expect(wrapper.text()).toContain('sk-new12345')
    expect(wrapper.text()).not.toContain('Revoked')
  })

  it('shows the managed LiteLLM key without rotate and keeps name and owner read-only', async () => {
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/api-keys' && !options?.method) {
        return [sampleKey({
          name: 'LiteLLM',
          owner_kind: 'service_account',
          owner_id: 'sa-hidden',
          owner_name: 'LiteLLM',
          managed: true,
          instance_ids: ['coder']
        })]
      }
      if (path === '/api/v1/users') return [{ id: 1, username: 'admin', enabled: true }]
      if (path === '/api/v1/admin/service-accounts') return []
      return []
    })

    const wrapper = await mountSuspended(APIPage, { route: false })
    await flushPromises()
    expect(wrapper.text()).toContain('LiteLLM')
    expect(wrapper.text()).toContain('sk-abcd1234')
    expect(wrapper.findAll('button').some(button => button.text() === 'Rotate')).toBe(false)

    await wrapper.findAll('button').find(button => button.text() === 'Edit')!.trigger('click')
    await flushPromises()
    const nameInput = [...document.body.querySelectorAll<HTMLInputElement>('[data-testid="key-name"]')].at(-1)!
    expect(nameInput.disabled).toBe(true)
    expect(document.body.querySelector('[data-testid="api-key-owner-readonly"]')).not.toBeNull()
    expect(document.body.querySelector('[data-testid="api-key-instances"]')).not.toBeNull()
    await click('api-key-save')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/api-keys/k1', {
      method: 'PATCH',
      body: { expires_on: null, instance_ids: ['coder'] }
    })
  })

  it('cancels disable and surfaces mutation errors', async () => {
    let mode: 'toggle-error' | 'ok' = 'toggle-error'
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/api-keys' && !options?.method) return [sampleKey({ name: 'SDK' })]
      if (path === '/api/v1/api-keys/k1' && options?.method === 'PATCH' && mode === 'toggle-error') throw { data: { error: 'toggle failed' } }
      return undefined
    })

    const wrapper = await mountSuspended(APIPage, { route: false })
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === 'Disable')!.trigger('click')
    await clickConfirmation('cancel')
    expect(mocks.request).not.toHaveBeenCalledWith('/api/v1/api-keys/k1', expect.objectContaining({ method: 'PATCH' }))

    await wrapper.findAll('button').find(button => button.text() === 'Disable')!.trigger('click')
    await clickConfirmation('confirm')
    expect(wrapper.text()).toContain('toggle failed')

    mode = 'ok'
    await wrapper.findAll('button').find(button => button.text() === 'Edit')!.trigger('click')
    await flushPromises()
    expect(menus(wrapper).length).toBeGreaterThan(0)
  })
})
