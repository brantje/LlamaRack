import { computed } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import APIPage from '~/pages/api.vue'
import AppButton from '~/components/AppButton.vue'
import StatusTag from '~/components/StatusTag.vue'
import { useManager, type APIKey } from '~/composables/useManager'
import { API_KEY_TYPE_ITEMS } from '~/utils/apiKeys'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: computed(() => 'http://manager.test:8888') }))

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
    instance_ids: [],
    missing_instance_ids: [],
    created_at: 1_700_000_000,
    last_used_at: 1_700_003_600,
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
  manager.instances.value = [{ id: 'coder', model_id: 'm1', name: 'Coder', enabled: true, autoload_enabled: true, always_on: false, priority: 'normal', eviction_enabled: true, idle_unload_seconds: 0, gpu_mode: 'auto' }]
  manager.runtimes.value = {}
  manager.profile.value = null
  return manager
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
  resetAuthenticated()
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: vi.fn() } })
})

describe('API redesign', () => {
  it('renders the unified endpoint and owner-bound key registry without revoke', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      if (path === '/api/v1/api-keys') return [
        sampleKey(),
        sampleKey({ id: 'disabled', name: 'SDK', prefix: 'sk-off12345', enabled: false, last_used_at: Number.POSITIVE_INFINITY }),
        sampleKey({ id: 'expired', name: 'Old client', prefix: 'sk-old12345', expires_on: '1999-01-01', last_used_at: 1_700_003_600 }),
        sampleKey({ id: 'owner-off', name: 'Bot', prefix: 'sk-bot12345', owner_name: 'ci', owner_enabled: false, owner_kind: 'service_account', owner_id: 'sa-1' })
      ]
      return []
    })

    const wrapper = await mountSuspended(APIPage, { route: false })
    await flushPromises()

    const base = wrapper.get('[data-testid="api-base-url"]')
    expect(base.text()).toContain('BASE URL')
    expect(base.text()).toContain('Unified endpoint')
    expect(base.text()).toContain('http://manager.test:8888/v1')
    expect(base.text()).toContain('Supported routes: models, chat completions, completions, Responses and embeddings.')
    expect(base.get('code').classes()).toContain('font-mono')

    const table = wrapper.get('[data-testid="api-keys-table"]')
    expect(table.findAll('th').map(cell => cell.text())).toEqual(['Name', 'Owner', 'Type', 'Prefix', 'Status', 'Expires', 'Last used', 'Actions'])
    expect(table.text()).toContain('sk-abcd1234…')
    expect(table.text()).toContain('admin')
    expect(table.text()).toContain('Inference')
    expect(table.text()).toContain('1999-01-01')
    expect(table.text()).not.toContain('Revoke')
    expect(wrapper.findAll('button').some(button => button.text() === 'Revoke')).toBe(false)

    const statuses = wrapper.findAllComponents(StatusTag)
    expect(statuses.map(tag => [tag.text(), tag.props('variant')])).toEqual([
      ['Enabled', 'ready'],
      ['Disabled', 'neutral'],
      ['Expired', 'failed'],
      ['Owner disabled', 'pending']
    ])

    const rowActions = wrapper.findAllComponents(AppButton).filter(button => ['Edit', 'Disable', 'Enable', 'Rotate'].includes(button.text()))
    expect(rowActions.every(button => button.props('intent') === 'ghost')).toBe(true)
  })

  it('opens a create modal with defaults, type descriptions and a one-time secret', async () => {
    let created = false
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/api-keys' && options?.method === 'POST') {
        created = true
        return { key: sampleKey({ id: 'new', name: options.body.name, prefix: 'sk-new12345' }), secret: 'sk-secret-once' }
      }
      if (path === '/api/v1/api-keys') return created ? [sampleKey({ id: 'new', name: 'default', prefix: 'sk-new12345' })] : []
      if (path === '/api/v1/users') return [{ id: 1, username: 'admin', enabled: true }, { id: 2, username: 'operator', enabled: true }, { id: 3, username: 'disabled', enabled: false }]
      if (path === '/api/v1/admin/service-accounts') return [{ id: 'sa-1', name: 'CI bot', enabled: true }, { id: 'sa-2', name: 'retired', enabled: false }]
      return undefined
    })

    const wrapper = await mountSuspended(APIPage, { route: false })
    await flushPromises()
    expect(wrapper.get('[data-testid="api-keys-empty"]').text()).toContain('No API keys created yet.')
    expect(wrapper.text()).toContain('Keys are owned by a user or service account')
    expect(wrapper.findComponent('[data-testid="create-key"]').props('intent')).toBe('primary')

    await wrapper.get('[data-testid="create-key"]').trigger('click')
    await flushPromises()
    expect((document.body.querySelector('[data-testid="key-name"]') as HTMLInputElement | null)?.value || document.body.querySelector('[data-testid="key-name"] input')?.value).toBe('default')
    const typeMenu = menus(wrapper).find((menu: any) => API_KEY_TYPE_ITEMS.every(item => JSON.stringify(menu.props('items')).includes(item.description)))
    expect(typeMenu).toBeTruthy()
    expect(JSON.stringify(typeMenu!.props('items'))).toContain('OpenAI-compatible /v1/* only')
    expect(JSON.stringify(typeMenu!.props('items'))).toContain('cannot call /v1/*')
    expect(JSON.stringify(typeMenu!.props('items'))).toContain('Both /v1/* and /api/v1/* except session and Playground. Can manage service accounts.')
    expect(JSON.stringify(typeMenu!.props('items'))).not.toMatch(/user-owned/i)
    expect(JSON.stringify(typeMenu!.props('items'))).not.toMatch(/signed-in browser session/)
    expect(document.body.textContent).toContain('Full Access can call both planes except session and Playground, and can manage service accounts')
    const ownerMenu = menus(wrapper).find((menu: any) => JSON.stringify(menu.props('items')).includes('Service accounts'))
    expect(ownerMenu).toBeTruthy()
    expect(JSON.stringify(ownerMenu!.props('items'))).toContain('Users')
    expect(JSON.stringify(ownerMenu!.props('items'))).toContain('CI bot')
    expect(JSON.stringify(ownerMenu!.props('items'))).not.toContain('retired')
    expect(JSON.stringify(ownerMenu!.props('items'))).not.toContain('disabled')
    expect(document.body.textContent).toContain('Leave empty to allow all instances.')

    typeMenu!.vm.$emit('update:modelValue', 'management')
    await flushPromises()
    expect(document.body.querySelector('[data-testid="api-key-instances"]')).toBeNull()

    typeMenu!.vm.$emit('update:modelValue', 'inference')
    await flushPromises()
    menus(wrapper).find((menu: any) => menu.props('multiple'))?.vm.$emit('update:modelValue', ['coder'])
    await click('api-key-save')

    expect(mocks.request).toHaveBeenCalledWith('/api/v1/api-keys', {
      method: 'POST',
      body: { name: 'default', key_type: 'inference', owner_user_id: 1, instance_ids: ['coder'] }
    })
    expect(document.body.textContent).toContain('Copy this key now. It will not be shown again.')
    expect(document.body.textContent).toContain('sk-secret-once')
    expect(document.body.querySelector('[data-testid="copy-key"]')).toBeTruthy()
  })
})
