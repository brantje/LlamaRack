import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import ModelsPage from '~/pages/models/index.vue'
import AppButton from '~/components/AppButton.vue'
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
  manager.instances.value = []
  manager.runtimes.value = {}
  manager.profile.value = null
  return manager
}

beforeEach(() => {
  mocks.request.mockReset()
  mocks.request.mockResolvedValue([])
  resetManager().models.value = []
})

describe('Models registry redesign', () => {
  it('renders the exact registry columns, linked names and ghost row actions', async () => {
    const manager = resetManager()
    manager.models.value = [{
      id: 'coder',
      name: 'Coder 7B',
      gguf_path: 'nested/models/coder/coder-q4.gguf',
      total_bytes: 1536,
      context_length: 0,
      quantization: 'Q4_K_M'
    }]

    const wrapper = await mountSuspended(ModelsPage, { route: false })
    const table = wrapper.get('[data-testid="models-table"]')
    expect(table.findAll('th').map(cell => cell.text())).toEqual([
      'Name', 'Path', 'Size', 'Quantization', 'Context capability', 'Actions'
    ])
    expect(table.findAll('[data-testid="model-row"]')).toHaveLength(1)
    expect(table.text()).toContain('1.5 KiB')
    expect(table.text()).toContain('Q4_K_M')
    expect(table.text()).toContain('Unknown')

    const nameLink = table.findAll('a').find(link => link.text().includes('Coder 7B'))!
    expect(nameLink.attributes('href')).toBe('/models/coder/details')
    expect(nameLink.classes()).toContain('text-[13.5px]')

    const pathCell = table.findAll('td')[1]!
    expect(pathCell.classes()).toContain('max-w-[340px]')
    expect(pathCell.classes()).toContain('break-all')
    expect(pathCell.classes()).toContain('font-mono')
    expect(pathCell.classes()).toContain('text-[11.5px]')

    const rowActions = wrapper.findAllComponents(AppButton).filter(button => ['Details', 'Edit', 'Delete'].includes(button.text()))
    expect(rowActions.map(button => button.props('intent'))).toEqual(['ghost', 'ghost', 'ghost'])
  })

  it('uses the prescribed empty state and header action hierarchy', async () => {
    const wrapper = await mountSuspended(ModelsPage, { route: false })
    const empty = wrapper.get('[data-testid="models-empty-state"]')
    expect(empty.text()).toContain('No models registered')
    expect(empty.text()).toContain('Register a local GGUF file to get started.')
    expect(wrapper.find('[data-testid="models-table"]').exists()).toBe(false)

    const actions = wrapper.findAllComponents(AppButton)
    const refresh = actions.find(button => button.text() === 'Refresh')!
    const discover = actions.find(button => button.text() === 'Discover')!
    const add = actions.find(button => button.text() === 'Add model')!
    expect(refresh.props('intent')).toBe('secondary')
    expect(discover.props('intent')).toBe('secondary')
    expect(wrapper.findAll('a').find(link => link.text() === 'Discover')?.attributes('href')).toBe('/models/discover')
    expect(add.props('intent')).toBe('primary')
  })
})
