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
    name: 'Client',
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
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.models.value = []
  manager.instances.value = []
  manager.runtimes.value = {}
  manager.runtimeTelemetry.value = {}
  manager.profile.value = null
}

async function confirm(kind: 'confirm' | 'cancel' = 'confirm') {
  await flushPromises()
  const target = [...document.body.querySelectorAll<HTMLButtonElement>(`[data-testid="confirmation-${kind}"]`)].at(-1)
  if (!target) throw new Error(`Missing confirmation ${kind}`)
  target.click()
  await flushPromises()
}

function action(wrapper: any, label: string) {
  const found = wrapper.findAll('button').find((button: any) => button.text().trim() === label)
  if (!found) throw new Error(`Missing ${label} button`)
  return found
}

async function click(testId: string) {
  const button = [...document.body.querySelectorAll<HTMLElement>(`[data-testid="${testId}"]`)].at(-1)
  if (!button) throw new Error(`Missing ${testId}`)
  button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  await flushPromises()
}

beforeEach(() => {
  mocks.request.mockReset()
  resetAuthenticated()
})

describe('API key branch coverage', () => {
  it('normalizes an empty response and covers every load error fallback', async () => {
    mocks.request.mockResolvedValueOnce(null)
    let wrapper = await mountSuspended(APIPage, { route: false })
    await flushPromises()
    expect(wrapper.text()).toContain('No API keys created yet.')
    wrapper.unmount()

    for (const [failure, message] of [
      [{ data: { error: 'key list denied' } }, 'key list denied'],
      [new Error('key list exploded'), 'key list exploded'],
      [{}, 'Unable to load API keys']
    ] as const) {
      resetAuthenticated()
      mocks.request.mockReset()
      mocks.request.mockRejectedValue(failure)
      wrapper = await mountSuspended(APIPage, { route: false })
      await flushPromises()
      expect(wrapper.text()).toContain(message)
      wrapper.unmount()
      await flushPromises()
    }
  })

  it('creates a key and covers every create error fallback', async () => {
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/api-keys' && options?.method === 'POST') return { key: sampleKey({ id: 'new', name: 'default', prefix: 'sk-new12345' }), secret: 'one-time-secret' }
      if (path === '/api/v1/api-keys') return []
      if (path === '/api/v1/users') return [{ id: 1, username: 'admin', enabled: true }]
      if (path === '/api/v1/admin/service-accounts') return []
      return undefined
    })
    let wrapper = await mountSuspended(APIPage, { route: false })
    await flushPromises()
    await action(wrapper, 'Create key').trigger('click')
    await flushPromises()
    await click('api-key-save')
    expect(document.body.textContent).toContain('one-time-secret')
    wrapper.unmount()

    for (const [failure, message] of [
      [{ data: { error: 'create denied' } }, 'create denied'],
      [new Error('create exploded'), 'create exploded'],
      [{}, 'Unable to create API key']
    ] as const) {
      resetAuthenticated()
      mocks.request.mockReset()
      mocks.request.mockImplementation(async (path: string, options?: any) => {
        if (path === '/api/v1/api-keys' && options?.method === 'POST') throw failure
        if (path === '/api/v1/api-keys') return []
        if (path === '/api/v1/users') return [{ id: 1, username: 'admin', enabled: true }]
        if (path === '/api/v1/admin/service-accounts') return []
        return undefined
      })
      wrapper = await mountSuspended(APIPage, { route: false })
      await flushPromises()
      await action(wrapper, 'Create key').trigger('click')
      await flushPromises()
      await click('api-key-save')
      expect(wrapper.text()).toContain(message)
      wrapper.unmount()
      await flushPromises()
    }
  })

  it('covers toggle, edit and rotate fallback/cancel branches', async () => {
    const key = sampleKey()
    let mutation: 'toggle' | 'edit' | 'rotate' = 'toggle'
    let failure: any = {}
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/api-keys' && !options?.method) return [key]
      if (mutation === 'toggle' && path === '/api/v1/api-keys/k1' && options?.method === 'PATCH') throw failure
      if (mutation === 'edit' && path === '/api/v1/api-keys/k1' && options?.method === 'PATCH') throw failure
      if (mutation === 'rotate' && path === '/api/v1/api-keys/k1/rotate' && options?.method === 'POST') throw failure
      if (path === '/api/v1/users') return [{ id: 1, username: 'admin', enabled: true }]
      if (path === '/api/v1/admin/service-accounts') throw new Error('sa list failed')
      return undefined
    })

    const wrapper = await mountSuspended(APIPage, { route: false })
    await flushPromises()

    await action(wrapper, 'Disable').trigger('click')
    await confirm('cancel')
    expect(mocks.request).not.toHaveBeenCalledWith('/api/v1/api-keys/k1', expect.objectContaining({ method: 'PATCH' }))

    await action(wrapper, 'Disable').trigger('click')
    await confirm()
    expect(wrapper.text()).toContain('Unable to update API key')

    mutation = 'edit'
    failure = { data: { error: 'edit denied' } }
    await action(wrapper, 'Edit').trigger('click')
    await flushPromises()
    await click('api-key-save')
    expect(wrapper.text()).toContain('edit denied')

    mutation = 'rotate'
    await action(wrapper, 'Rotate').trigger('click')
    await confirm('cancel')
    expect(mocks.request).not.toHaveBeenCalledWith('/api/v1/api-keys/k1/rotate', { method: 'POST' })

    failure = { data: { error: 'rotate denied' } }
    await action(wrapper, 'Rotate').trigger('click')
    await confirm()
    expect(wrapper.text()).toContain('rotate denied')

    failure = {}
    await action(wrapper, 'Rotate').trigger('click')
    await confirm()
    expect(wrapper.text()).toContain('Unable to rotate API key')
    wrapper.unmount()
  })

  it('skips loading when signed out and covers secret copy, clear expiry and close', async () => {
    resetAuthenticated()
    useManager().user.value = null
    mocks.request.mockReset()
    let wrapper = await mountSuspended(APIPage, { route: false })
    await flushPromises()
    expect(mocks.request).not.toHaveBeenCalled()
    wrapper.unmount()

    resetAuthenticated()
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/api-keys' && options?.method === 'POST') return { key: sampleKey({ id: 'new', name: 'default' }), secret: 'sk-once' }
      if (path === '/api/v1/api-keys') return [sampleKey({ expires_on: '2027-01-01', missing_instance_ids: ['gone'] })]
      if (path === '/api/v1/users') return [{ id: 1, username: 'admin', enabled: true }]
      if (path === '/api/v1/admin/service-accounts') return []
      return undefined
    })
    wrapper = await mountSuspended(APIPage, { route: false })
    await flushPromises()
    await action(wrapper, 'Create key').trigger('click')
    await flushPromises()
    await click('api-key-save')
    const copy = wrapper.findAllComponents({ name: 'AppCopyButton' }).find((button: any) => button.attributes('data-testid') === 'copy-key' || button.props('text') === 'sk-once')
    copy?.vm.$emit('error', 'Clipboard blocked. Copy manually.')
    await flushPromises()
    expect(document.body.textContent).toContain('Clipboard blocked. Copy manually.')
    copy?.vm.$emit('copied', 'sk-once')
    await flushPromises()
    await click('api-key-secret-done')
    expect(document.body.querySelector('[data-testid="fresh-api-key"]')).toBeNull()

    await action(wrapper, 'Edit').trigger('click')
    await flushPromises()
    expect(document.body.textContent).toContain('gone')
    const date = [...wrapper.findAllComponents({ name: 'InputDate' }), ...wrapper.findAllComponents({ name: 'UInputDate' })][0]
    date?.vm.$emit('update:modelValue', undefined)
    await flushPromises()
    const clear = [...document.body.querySelectorAll<HTMLButtonElement>('[data-testid="clear-api-key-expires"]')].at(-1)
    if (clear) {
      clear.click()
      await flushPromises()
    }
    const cancel = [...document.body.querySelectorAll<HTMLButtonElement>('button')].find(candidate => candidate.textContent?.trim() === 'Cancel')
    cancel?.click()
    await flushPromises()
    wrapper.unmount()
  })
})
