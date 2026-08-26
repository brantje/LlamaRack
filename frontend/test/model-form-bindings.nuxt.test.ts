import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import ModelsPage from '~/pages/models.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

beforeEach(() => {
  mocks.request.mockReset()
  mocks.request.mockResolvedValue([])
  const manager = useManager()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', role: 'admin', enabled: true }
  manager.models.value = []
  manager.artifacts.value = [{ id: 'a1', display_name: 'Artifact', local_path: 'artifact.gguf', total_bytes: 4 }]
  manager.runtimes.value = {}
  manager.profile.value = null
})

describe('model form bindings', () => {
  it('updates optional fields and model settings', async () => {
    const wrapper = await mountSuspended(ModelsPage, { route: false })
    const forms = wrapper.findAll('form')

    const artifactInputs = forms[0]!.findAll('input')
    await artifactInputs[1]!.setValue('Diagnostic artifact')
    expect((artifactInputs[1]!.element as HTMLInputElement).value).toBe('Diagnostic artifact')

    const modelInputs = forms[1]!.findAll('input')
    await modelInputs[1]!.setValue('Diagnostic model')
    expect((modelInputs[1]!.element as HTMLInputElement).value).toBe('Diagnostic model')

    const selects = forms[1]!.findAll('select')
    await selects[1]!.setValue('high')
    await selects[2]!.setValue('round_robin')
    expect((selects[1]!.element as HTMLSelectElement).value).toBe('high')
    expect((selects[2]!.element as HTMLSelectElement).value).toBe('round_robin')

    const checkboxes = forms[1]!.findAll('input[type="checkbox"]')
    await checkboxes[0]!.setValue(false)
    await checkboxes[1]!.setValue(true)
    expect((checkboxes[0]!.element as HTMLInputElement).checked).toBe(false)
    expect((checkboxes[1]!.element as HTMLInputElement).checked).toBe(true)
  })
})
