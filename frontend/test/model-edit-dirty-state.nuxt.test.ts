import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import ModelEditPage from '~/pages/models/[id]/edit.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

beforeEach(() => {
  mocks.request.mockReset()
  mocks.request.mockImplementation(async (path: string) => {
    if (path === '/api/v1/models/model-1') return { id: 'model-1', name: 'Model One', context_length: 8192 }
    if (path === '/api/v1/models/model-1/options') return { 'ctx-size': '8192' }
    throw new Error(`unexpected request ${path}`)
  })
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.models.value = []
  manager.instances.value = []
  manager.runtimes.value = {}
})

describe('Edit Model dirty state', () => {
  it('disables save until the loaded Model actually changes', async () => {
    const wrapper = await mountSuspended(ModelEditPage, { route: '/models/model-1/edit' })
    await flushPromises()

    const save = wrapper.get('button[type="submit"]')
    expect((save.element as HTMLButtonElement).disabled).toBe(true)
    expect(wrapper.get('[data-testid="model-edit-submit-hint"]').text()).toContain('No changes to save.')

    const name = wrapper.get('input[required]')
    await name.setValue('Model One Updated')
    await flushPromises()
    expect((save.element as HTMLButtonElement).disabled).toBe(false)
    expect(wrapper.get('[data-testid="model-edit-submit-hint"]').text()).toContain('Unsaved changes.')

    await name.setValue('Model One')
    await flushPromises()
    expect((save.element as HTMLButtonElement).disabled).toBe(true)
    expect(wrapper.get('[data-testid="model-edit-submit-hint"]').text()).toContain('No changes to save.')

    await name.setValue('')
    await flushPromises()
    expect((save.element as HTMLButtonElement).disabled).toBe(true)
    expect(wrapper.get('[data-testid="model-edit-submit-hint"]').text()).toContain('Required: Model name.')
  })

  it('does not render an empty edit form when the Model payload is missing', async () => {
    mocks.request.mockImplementation(async () => ({}))
    const wrapper = await mountSuspended(ModelEditPage, { route: '/models/model-1/edit' })
    await flushPromises()
    expect(wrapper.get('[data-testid="model-edit-error"]').text()).toContain('Unable to load Model')
    expect(wrapper.find('[data-testid="model-edit-metadata"]').exists()).toBe(false)
  })
})
