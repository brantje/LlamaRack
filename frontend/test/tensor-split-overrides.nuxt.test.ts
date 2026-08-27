import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import LlamaCppOptionsEditor from '~/components/LlamaCppOptionsEditor.vue'
import { useManager, type Profile } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

const profile = {
  path: '/app/llama-server', version: 'test', fingerprint: 'tensor-split', options: [
    { key: 'tensor-split', value_hint: 'SPLIT', description: 'Fraction of model to offload to each GPU', kind: 'string' },
    { key: 'device', value_hint: 'DEVICE', kind: 'string' }
  ]
} satisfies Profile

beforeEach(() => {
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.profile.value = profile
  mocks.request.mockReset()
  mocks.request.mockResolvedValue({
    profile,
    effective: { global: {}, model: {}, instance: {}, values: {}, sources: {} }
  })
})

describe('tensor split llama.cpp overrides', () => {
  it('keeps tensor-split editable while device remains manager controlled', async () => {
    const wrapper = await mountSuspended(LlamaCppOptionsEditor, {
      route: false,
      props: { modelValue: { 'tensor-split': '3,1' }, scope: 'instance' }
    })
    await flushPromises()

    const tensorCode = [...wrapper.element.querySelectorAll('code')].find(node => node.textContent?.trim() === '--tensor-split')
    expect(tensorCode).toBeTruthy()
    const tensorCard = tensorCode!.closest('[data-slot="root"]') || tensorCode!.parentElement?.parentElement?.parentElement
    expect(tensorCard?.textContent).not.toContain('Manager controlled')
    expect(tensorCard?.textContent).toContain('Remove override')

    const tensorInput = wrapper.findAllComponents({ name: 'Input' }).find(component => component.props('modelValue') === '3,1')
      || wrapper.findAllComponents({ name: 'UInput' }).find(component => component.props('modelValue') === '3,1')
    expect(tensorInput).toBeTruthy()
    expect(tensorInput!.props('disabled')).not.toBe(true)

    await wrapper.findAll('button').find(button => button.text() === 'Advanced')!.trigger('click')
    await flushPromises()
    const deviceCode = [...wrapper.element.querySelectorAll('code')].find(node => node.textContent?.trim() === '--device')
    expect(deviceCode).toBeTruthy()
    expect(wrapper.text()).toContain('Manager controlled')
  })
})
