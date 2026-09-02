import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import AdminServiceAccountsPage from '~/pages/admin/service-accounts.vue'
import AdminServiceAccountDetailPage from '~/pages/admin/service-accounts/[id].vue'
import AdminSidebar from '~/components/navigation/AdminSidebar.vue'
import { useManager, type ServiceAccount } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

function sampleAccount(overrides: Partial<ServiceAccount> = {}): ServiceAccount {
  return {
    id: 'sa-1',
    name: 'CI bot',
    enabled: true,
    created_at: 10,
    ...overrides
  }
}

function resetManager(user: { id: number; username: string; enabled: boolean } | null = { id: 1, username: 'admin', enabled: true }) {
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = user
  manager.models.value = []
  manager.instances.value = []
  manager.runtimes.value = {}
  manager.profile.value = null
  return manager
}

function button(wrapper: any, text: string) {
  const found = wrapper.findAll('button').find((candidate: any) => candidate.text().trim() === text)
  if (!found) throw new Error(`Missing button: ${text}`)
  return found
}
function tableRow(wrapper: any, text: string) {
  const found = wrapper.findAll('tr').find((candidate: any) => candidate.text().includes(text))
  if (!found) throw new Error(`Missing table row: ${text}`)
  return found
}
function rowButton(row: any, text: string) {
  const found = row.findAll('button').find((candidate: any) => candidate.text().trim() === text)
  if (!found) throw new Error(`Missing row button: ${text}`)
  return found
}
async function clickConfirmation(kind: 'confirm' | 'cancel') {
  await flushPromises()
  const target = [...document.body.querySelectorAll<HTMLButtonElement>(`[data-testid="confirmation-${kind}"]`)].at(-1)
  if (!target) throw new Error(`Missing confirmation ${kind}`)
  target.click()
  await flushPromises()
}
function setInput(testId: string, value: string) {
  const host = [...document.body.querySelectorAll(`[data-testid="${testId}"]`)].at(-1)
  const input = (host instanceof HTMLInputElement ? host : host?.querySelector('input')) as HTMLInputElement | undefined
  if (!input) throw new Error(`Missing ${testId}`)
  input.value = value
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

beforeEach(() => {
  mocks.request.mockReset()
  resetManager()
})

describe('Service accounts administration', () => {
  it('places Service accounts after Users in the administration sidebar', async () => {
    const wrapper = await mountSuspended(AdminSidebar, { route: '/admin/service-accounts' })
    const labels = wrapper.findAll('[data-testid="admin-desktop-navigation"] a').map(link => link.text())
    const users = labels.findIndex(text => text.includes('Users'))
    const accounts = labels.findIndex(text => text.includes('Service accounts'))
    expect(accounts).toBe(users + 1)
    expect(wrapper.get('[data-testid="admin-nav-service-accounts"]').attributes('href')).toBe('/admin/service-accounts')
    expect(wrapper.get('[data-testid="admin-nav-service-accounts"]').classes()).toContain('border-[var(--color-accent)]')
    wrapper.unmount()
  })

  it('creates, renames, toggles and deletes service accounts', async () => {
    let accounts = [sampleAccount(), sampleAccount({ id: 'sa-2', name: 'Nightly', enabled: false })]
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/admin/service-accounts' && options?.method === 'POST') {
        accounts = [...accounts, sampleAccount({ id: 'sa-3', name: options.body.name, created_at: 30 })]
        return accounts.at(-1)
      }
      if (path === '/api/v1/admin/service-accounts' && !options?.method) return accounts
      if (path === '/api/v1/admin/service-accounts/sa-1' && options?.method === 'PATCH') {
        accounts = accounts.map(item => item.id === 'sa-1' ? { ...item, ...options.body } : item)
        return undefined
      }
      if (path === '/api/v1/admin/service-accounts/sa-2' && options?.method === 'PATCH') {
        accounts = accounts.map(item => item.id === 'sa-2' ? { ...item, enabled: options.body.enabled } : item)
        return undefined
      }
      if (path === '/api/v1/admin/service-accounts/sa-1' && options?.method === 'DELETE') {
        accounts = accounts.filter(item => item.id !== 'sa-1')
        return undefined
      }
      return []
    })

    const wrapper = await mountSuspended(AdminServiceAccountsPage, { route: '/admin/service-accounts' })
    await flushPromises()
    expect(wrapper.get('[data-testid="admin-service-accounts-table"]').findAll('th').map(cell => cell.text())).toEqual(['Name', 'Status', 'Created', 'Actions'])
    expect(wrapper.text()).toContain('CI bot')
    expect(wrapper.get('a[href="/admin/service-accounts/sa-1"]').text()).toBe('CI bot')

    await button(wrapper, 'Add service account').trigger('click')
    await flushPromises()
    setInput('service-account-name', 'Docs')
    await flushPromises()
    const add = [...document.body.querySelectorAll<HTMLButtonElement>('[data-testid="service-account-create"]')].find(candidate => !candidate.disabled)
    add?.click()
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/admin/service-accounts', { method: 'POST', body: { name: 'Docs' } })
    expect(wrapper.text()).toContain('Service account created.')
    expect(wrapper.text()).toContain('Docs')

    await rowButton(tableRow(wrapper, 'CI bot'), 'Rename').trigger('click')
    await flushPromises()
    setInput('service-account-rename', 'CI')
    ;[...document.body.querySelectorAll<HTMLButtonElement>('[data-testid="service-account-save"]')].at(-1)?.click()
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/admin/service-accounts/sa-1', { method: 'PATCH', body: { name: 'CI' } })

    await rowButton(tableRow(wrapper, 'Nightly'), 'Enable').trigger('click')
    await clickConfirmation('cancel')
    expect(mocks.request).not.toHaveBeenCalledWith('/api/v1/admin/service-accounts/sa-2', expect.objectContaining({ method: 'PATCH' }))
    await rowButton(tableRow(wrapper, 'Nightly'), 'Enable').trigger('click')
    await clickConfirmation('confirm')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/admin/service-accounts/sa-2', { method: 'PATCH', body: { enabled: true } })

    await rowButton(tableRow(wrapper, 'CI'), 'Disable').trigger('click')
    await clickConfirmation('confirm')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/admin/service-accounts/sa-1', { method: 'PATCH', body: { enabled: false } })

    await rowButton(tableRow(wrapper, 'CI'), 'Delete').trigger('click')
    expect(document.body.textContent).toContain('API keys owned by this service account will be deleted with the account')
    await clickConfirmation('confirm')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/admin/service-accounts/sa-1', { method: 'DELETE' })
    expect(wrapper.text()).not.toContain('CI bot')
  })

  it('covers empty, signed-out and error branches', async () => {
    mocks.request.mockResolvedValueOnce(null)
    let wrapper = await mountSuspended(AdminServiceAccountsPage, { route: false })
    await flushPromises()
    expect(wrapper.text()).toContain('No service accounts')
    wrapper.unmount()

    resetManager(null)
    mocks.request.mockReset()
    wrapper = await mountSuspended(AdminServiceAccountsPage, { route: false })
    await flushPromises()
    expect(mocks.request).not.toHaveBeenCalled()
    wrapper.unmount()

    resetManager()
    for (const [failure, message] of [
      [{ data: { error: 'sa denied' } }, 'sa denied'],
      [new Error('sa exploded'), 'sa exploded'],
      [{}, 'Unable to load service accounts']
    ] as const) {
      mocks.request.mockReset()
      mocks.request.mockRejectedValue(failure)
      wrapper = await mountSuspended(AdminServiceAccountsPage, { route: false })
      await flushPromises()
      expect(wrapper.text()).toContain(message)
      wrapper.unmount()
    }

    let failCreate: any = {}
    mocks.request.mockReset()
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/admin/service-accounts' && options?.method === 'POST') throw failCreate
      if (path === '/api/v1/admin/service-accounts') return [sampleAccount()]
      if (path.endsWith('/sa-1') && options?.method === 'PATCH') throw { data: { error: 'toggle denied' } }
      if (path.endsWith('/sa-1') && options?.method === 'DELETE') throw new Error('delete exploded')
      return {}
    })
    wrapper = await mountSuspended(AdminServiceAccountsPage, { route: false })
    await flushPromises()
    await button(wrapper, 'Add service account').trigger('click')
    await flushPromises()
    setInput('service-account-name', 'Nope')
    await flushPromises()
    const createDenied = [...document.body.querySelectorAll<HTMLButtonElement>('[data-testid="service-account-create"]')].find(candidate => !candidate.disabled)
    createDenied?.click()
    await flushPromises()
    expect(wrapper.text()).toContain('Unable to create service account')

    failCreate = { data: { error: 'create denied' } }
    createDenied?.click()
    await flushPromises()
    expect(wrapper.text()).toContain('create denied')

    await rowButton(tableRow(wrapper, 'CI bot'), 'Disable').trigger('click')
    await clickConfirmation('confirm')
    expect(wrapper.text()).toContain('toggle denied')

    await rowButton(tableRow(wrapper, 'CI bot'), 'Rename').trigger('click')
    await flushPromises()
    ;[...document.body.querySelectorAll<HTMLButtonElement>('[data-testid="service-account-save"]')].at(-1)?.click()
    await flushPromises()
    expect(wrapper.text()).toContain('toggle denied')

    await rowButton(tableRow(wrapper, 'CI bot'), 'Delete').trigger('click')
    await clickConfirmation('cancel')
    expect(mocks.request).not.toHaveBeenCalledWith('/api/v1/admin/service-accounts/sa-1', { method: 'DELETE' })
    await rowButton(tableRow(wrapper, 'CI bot'), 'Delete').trigger('click')
    await clickConfirmation('confirm')
    expect(wrapper.text()).toContain('delete exploded')
    wrapper.unmount()
  })

  it('shows a list-only keys table on the detail route', async () => {
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/admin/service-accounts/sa-1' && !options?.method) {
        return {
          ...sampleAccount(),
          keys: [{
            id: 'k1', name: 'worker', prefix: 'sk-work1234', enabled: true, key_type: 'full', owner_kind: 'service_account',
            owner_id: 'sa-1', owner_name: 'CI bot', owner_enabled: true, created_at: 1, expires_on: '2027-01-01'
          }]
        }
      }
      if (path === '/api/v1/admin/service-accounts/sa-1' && options?.method === 'PATCH') return undefined
      if (path === '/api/v1/admin/service-accounts/sa-1' && options?.method === 'DELETE') return undefined
      return {}
    })

    const wrapper = await mountSuspended(AdminServiceAccountDetailPage, { route: '/admin/service-accounts/sa-1' })
    await flushPromises()
    expect(wrapper.get('[data-testid="service-account-detail"]').text()).toContain('CI bot')
    expect(wrapper.get('[data-testid="service-account-detail"]').text()).toContain('Enabled')
    const keys = wrapper.get('[data-testid="service-account-keys"]')
    expect(keys.text()).toContain('worker')
    expect(keys.text()).toContain('Full Access')
    expect(keys.text()).toContain('sk-work1234…')
    expect(keys.text()).toContain('2027-01-01')
    expect(keys.text()).toContain('Create, edit and rotate secrets on API')
    expect(keys.findAll('button').some(button => ['Edit', 'Rotate', 'Disable', 'Create key'].includes(button.text()))).toBe(false)

    await button(wrapper, 'Rename').trigger('click')
    await flushPromises()
    const name = [...document.body.querySelectorAll<HTMLInputElement>('input')].find(input => input.value === 'CI bot')!
    name.value = 'CI'
    name.dispatchEvent(new Event('input', { bubbles: true }))
    const save = [...document.body.querySelectorAll<HTMLButtonElement>('button')].find(candidate => candidate.textContent?.trim() === 'Save' && !candidate.disabled)
    save?.click()
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/admin/service-accounts/sa-1', { method: 'PATCH', body: { name: 'CI' } })

    await button(wrapper, 'Disable').trigger('click')
    await clickConfirmation('confirm')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/admin/service-accounts/sa-1', { method: 'PATCH', body: { enabled: false } })

    await button(wrapper, 'Delete').trigger('click')
    expect(document.body.textContent).toContain('will be deleted with the account')
    await clickConfirmation('cancel')
    expect(mocks.request).not.toHaveBeenCalledWith('/api/v1/admin/service-accounts/sa-1', { method: 'DELETE' })
  })

  it('covers detail load, nested payload, empty keys and mutation errors', async () => {
    mocks.request.mockRejectedValueOnce({ data: { error: 'missing account' } })
    let wrapper = await mountSuspended(AdminServiceAccountDetailPage, { route: '/admin/service-accounts/sa-1' })
    await flushPromises()
    expect(wrapper.text()).toContain('missing account')
    wrapper.unmount()

    resetManager(null)
    mocks.request.mockReset()
    wrapper = await mountSuspended(AdminServiceAccountDetailPage, { route: '/admin/service-accounts/sa-1' })
    await flushPromises()
    expect(mocks.request).not.toHaveBeenCalled()
    wrapper.unmount()

    resetManager()
    mocks.request.mockReset()
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/admin/service-accounts/sa-1') return { account: sampleAccount(), keys: [] }
      return {}
    })
    wrapper = await mountSuspended(AdminServiceAccountDetailPage, { route: '/admin/service-accounts/sa-1' })
    await flushPromises()
    expect(wrapper.text()).toContain('No keys for this account')
    expect(wrapper.get('[data-testid="service-account-detail"]').text()).toContain('CI bot')

    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/admin/service-accounts/sa-1' && !options?.method) return sampleAccount()
      if (options?.method === 'PATCH') throw {}
      if (options?.method === 'DELETE') throw { data: { error: 'delete denied' } }
      return {}
    })
    await button(wrapper, 'Disable').trigger('click')
    await clickConfirmation('confirm')
    expect(wrapper.text()).toContain('Unable to disable service account')
    await button(wrapper, 'Rename').trigger('click')
    await flushPromises()
    const save = [...document.body.querySelectorAll<HTMLButtonElement>('button')].find(candidate => candidate.textContent?.trim() === 'Save')
    save?.click()
    await flushPromises()
    expect(wrapper.text()).toContain('Unable to update service account')
    await button(wrapper, 'Delete').trigger('click')
    await clickConfirmation('confirm')
    expect(wrapper.text()).toContain('delete denied')
    wrapper.unmount()

    mocks.request.mockReset()
    mocks.request.mockRejectedValue(new Error('detail exploded'))
    wrapper = await mountSuspended(AdminServiceAccountDetailPage, { route: '/admin/service-accounts/sa-1' })
    await flushPromises()
    expect(wrapper.text()).toContain('detail exploded')
    wrapper.unmount()

    mocks.request.mockReset()
    mocks.request.mockRejectedValue({})
    wrapper = await mountSuspended(AdminServiceAccountDetailPage, { route: '/admin/service-accounts/sa-1' })
    await flushPromises()
    expect(wrapper.text()).toContain('Unable to load service account')
    wrapper.unmount()
  })

  it('discards a stale detail response after the route id changes', async () => {
    let finishSlow: (value: unknown) => void
    const slow = new Promise(resolve => { finishSlow = resolve })
    mocks.request.mockImplementation(async (path: string) => {
      if (path.endsWith('/sa-slow')) return slow
      if (path.endsWith('/sa-2')) return sampleAccount({ id: 'sa-2', name: 'Second' })
      return {}
    })
    const wrapper = await mountSuspended(AdminServiceAccountDetailPage, { route: '/admin/service-accounts/sa-slow' })
    await useRouter().replace('/admin/service-accounts/sa-2')
    await flushPromises()
    finishSlow!(sampleAccount({ id: 'sa-slow', name: 'First' }))
    await flushPromises()
    expect(wrapper.text()).toContain('Second')
    expect(wrapper.text()).not.toContain('First')
    wrapper.unmount()
  })
})
