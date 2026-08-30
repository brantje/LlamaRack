import { computed } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import APIPage from '~/pages/api.vue'
import AppButton from '~/components/AppButton.vue'
import StatusTag from '~/components/StatusTag.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: computed(() => 'http://manager.test:8888') }))

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
  manager.profile.value = null
  return manager
}

beforeEach(() => {
  mocks.request.mockReset()
  resetAuthenticated()
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: vi.fn() } })
})

describe('API redesign', () => {
  it('renders the unified endpoint and six-column client credential registry without user attribution', async () => {
    const created = 1_700_000_000
    const lastUsed = 1_700_003_600
    mocks.request.mockResolvedValue([
      { id: 'enabled', name: 'LiteLLM', prefix: 'live1234', enabled: true, created_at: created, last_used_at: lastUsed, created_by_user_id: 99 },
      { id: 'disabled', name: 'SDK', prefix: 'off12345', enabled: false, created_at: created, last_used_at: Number.POSITIVE_INFINITY, created_by_user_id: 99 },
      { id: 'revoked', name: 'Old client', prefix: 'old12345', enabled: false, created_at: created, revoked_at: lastUsed, created_by_user_id: 99 }
    ])

    const wrapper = await mountSuspended(APIPage, { route: false })
    await flushPromises()

    const base = wrapper.get('[data-testid="api-base-url"]')
    expect(base.text()).toContain('BASE URL')
    expect(base.text()).toContain('Unified endpoint')
    expect(base.text()).toContain('http://manager.test:8888/v1')
    expect(base.text()).toContain('Supported routes: models, chat completions, completions, Responses and embeddings.')
    expect(base.get('code').classes()).toContain('text-[13.5px]')
    expect(base.get('code').classes()).toContain('font-mono')

    const table = wrapper.get('[data-testid="api-keys-table"]')
    expect(table.findAll('th').map(cell => cell.text())).toEqual(['Name', 'Prefix', 'Status', 'Created', 'Last used', 'Actions'])
    expect(table.text()).toContain('live1234…')
    expect(table.text()).toContain(new Date(created * 1000).toLocaleString())
    expect(table.text()).toContain(new Date(lastUsed * 1000).toLocaleString())
    expect(table.text()).toContain('—')
    expect(table.text()).not.toContain('Created by')
    expect(table.text()).not.toContain('admin')

    const statuses = wrapper.findAllComponents(StatusTag)
    expect(statuses.map(tag => [tag.text(), tag.props('variant')])).toEqual([
      ['Enabled', 'ready'],
      ['Disabled', 'neutral'],
      ['Revoked', 'failed']
    ])

    const revokedRow = table.findAll('tr').find(row => row.text().includes('Old client'))!
    expect(revokedRow.text()).toContain('Revoked')
    expect(revokedRow.findAll('button')).toHaveLength(0)
    const rowActions = wrapper.findAllComponents(AppButton).filter(button => ['Disable', 'Enable', 'Rotate', 'Revoke'].includes(button.text()))
    expect(rowActions.every(button => button.props('intent') === 'ghost')).toBe(true)
  })

  it('uses the prescribed credential note, primary create action and one-time secret strip', async () => {
    let created = false
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/api-keys' && options?.method === 'POST') {
        created = true
        return { key: { id: 'new', name: options.body.name, prefix: 'new12345', enabled: true, created_at: 1 }, secret: 'llm-secret-once' }
      }
      if (path === '/api/v1/api-keys') return created ? [{ id: 'new', name: 'client', prefix: 'new12345', enabled: true, created_at: 1 }] : []
      return undefined
    })

    const wrapper = await mountSuspended(APIPage, { route: false })
    await flushPromises()
    expect(wrapper.get('[data-testid="api-keys-empty"]').text()).toContain('No API keys created yet.')
    expect(wrapper.get('[data-testid="api-keys-empty"]').text()).toContain('Create a key to authenticate OpenAI-compatible clients.')
    expect(wrapper.text()).toContain('Keys authenticate clients, not users.')

    const create = wrapper.findComponent('[data-testid="create-key"]')
    expect(create.props('intent')).toBe('primary')
    await wrapper.get('[data-testid="key-name"]').setValue('client')
    await wrapper.get('[data-testid="create-key"]').trigger('click')
    await flushPromises()

    const fresh = wrapper.get('[data-testid="fresh-api-key"]')
    expect(fresh.text()).toContain('Copy this key now. It will not be shown again.')
    expect(fresh.text()).toContain('llm-secret-once')
    expect(fresh.find('[data-testid="copy-key"]').exists()).toBe(true)
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/api-keys', { method: 'POST', body: { name: 'client' } })
  })
})
