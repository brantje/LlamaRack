import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import ModelsPage from '~/pages/models/index.vue'
import { useManager, type Model } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

function model(overrides: Partial<Model> = {}): Model {
  return {
    id: 'm1',
    name: 'Coder',
    gguf_path: 'models/coder.gguf',
    total_bytes: 4 * 1024 * 1024,
    context_length: 8192,
    ...overrides
  }
}

function resetState() {
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.models.value = [model()]
  manager.instances.value = []
  manager.runtimes.value = {}
  manager.profile.value = null
  return manager
}

function modalCheckbox() {
  const checkbox = document.body.querySelector<HTMLElement>('[data-testid="model-delete-files"] [role="checkbox"]')
  if (!checkbox) throw new Error('Missing model file deletion checkbox')
  return checkbox
}

function checkboxChecked() {
  return modalCheckbox().getAttribute('aria-checked') === 'true'
}

async function clickModal(kind: 'confirm' | 'cancel') {
  await flushPromises()
  const buttons = [...document.body.querySelectorAll<HTMLButtonElement>(`[data-testid="confirmation-${kind}"]`)]
  const button = buttons.at(-1)
  if (!button) throw new Error(`Missing confirmation ${kind} button`)
  button.click()
  await flushPromises()
}

beforeEach(() => {
  mocks.request.mockReset()
  mocks.request.mockImplementation(async (path: string) => {
    if (path.endsWith('/models') || path.endsWith('/instances')) return []
    if (path.endsWith('/llamacpp/profile')) throw new Error('unavailable')
    return {}
  })
  resetState()
})

describe('model file deletion', () => {
  it('keeps file deletion off by default and omits the destructive flag', async () => {
    const wrapper = await mountSuspended(ModelsPage, { route: false })
    await wrapper.findAll('button').find(button => button.text() === 'Delete')!.trigger('click')
    await flushPromises()

    expect(checkboxChecked()).toBe(false)
    expect(document.body.textContent).toContain('model file will be preserved')
    expect(document.body.querySelector('[data-testid="model-delete-warning"]')).toBeNull()

    await clickModal('confirm')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/models/m1', { method: 'DELETE' })
    expect(mocks.request).not.toHaveBeenCalledWith('/api/v1/models/m1?delete_files=true', expect.anything())
  })

  it('sends the destructive flag only after explicit opt-in and resets the checkbox', async () => {
    const manager = resetState()
    const wrapper = await mountSuspended(ModelsPage, { route: false })
    const deleteButton = () => wrapper.findAll('button').find(button => button.text() === 'Delete')!

    await deleteButton().trigger('click')
    await flushPromises()
    modalCheckbox().click()
    await flushPromises()

    expect(checkboxChecked()).toBe(true)
    expect(document.body.textContent).toContain('permanently removed from disk')
    expect(document.body.textContent).toContain('models/coder.gguf')
    expect(document.body.textContent).toContain('4 MiB')

    await clickModal('confirm')
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/models/m1?delete_files=true', { method: 'DELETE' })

    manager.models.value = [model()]
    await wrapper.vm.$nextTick()
    await deleteButton().trigger('click')
    await flushPromises()
    expect(checkboxChecked()).toBe(false)
    await clickModal('cancel')
  })
})
