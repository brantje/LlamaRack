import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import LlamaCppOptionsEditor from '~/components/LlamaCppOptionsEditor.vue'
import { useManager, type Profile } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

const profile = {
  path: '/app/llama-server',
  version: 'override-order-test',
  fingerprint: 'override-order',
  options: [
    { key: 'threads', value_hint: 'N', kind: 'integer' },
    { key: 'temperature', value_hint: 'FLOAT', kind: 'number' },
    { key: 'flash-attn', kind: 'boolean' },
    { key: 'ctx-size', value_hint: 'N', kind: 'integer' },
    { key: 'batch-size', value_hint: 'N', kind: 'integer' }
  ]
} satisfies Profile

function optionKeys(wrapper: any) {
  return [...wrapper.element.querySelectorAll('code')]
    .map((element: Element) => element.textContent?.trim() || '')
    .filter((value: string) => value.startsWith('--'))
}

beforeEach(() => {
  mocks.request.mockReset()
  mocks.request.mockResolvedValue({
    profile: { options: profile.options },
    effective: {
      global: {},
      model: {},
      instance: {},
      values: {},
      sources: {}
    }
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
  manager.profile.value = profile
})

describe('llama.cpp override ordering', () => {
  it.each(['instance', 'model'] as const)('sorts %s overrides first in basic and advanced views', async (scope) => {
    const wrapper = await mountSuspended(LlamaCppOptionsEditor, {
      route: false,
      props: {
        modelValue: {
          threads: '8',
          'batch-size': '256',
          temperature: '0.7'
        },
        scope
      }
    })
    await flushPromises()

    expect(optionKeys(wrapper)).toEqual([
      '--batch-size',
      '--threads',
      '--ctx-size',
      '--flash-attn'
    ])

    expect(wrapper.get('[data-testid="llamacpp-configured-overrides"]').text()).toContain('Configured overrides')
    expect(wrapper.get('[data-testid="llamacpp-inherited-options"]').text()).toContain('Available inherited options')
    expect(wrapper.findAll('[data-testid="llamacpp-option-row"][data-option-state="override"]')).toHaveLength(2)
    expect(wrapper.findAll('[data-testid="llamacpp-option-row"][data-option-state="inherited"]')).toHaveLength(2)
    const modeButtons = wrapper.findAll('button').filter(button => ['Basic', 'Advanced'].includes(button.text()))
    expect(modeButtons.find(button => button.text() === 'Basic')?.attributes('aria-pressed')).toBe('true')
    expect(modeButtons.find(button => button.text() === 'Advanced')?.attributes('aria-pressed')).toBe('false')

    await wrapper.findAll('button').find(button => button.text() === 'Advanced')!.trigger('click')
    await flushPromises()
    expect(optionKeys(wrapper)).toEqual([
      '--batch-size',
      '--temperature',
      '--threads',
      '--ctx-size',
      '--flash-attn'
    ])

    await wrapper.setProps({ modelValue: { 'ctx-size': '8192' } })
    await flushPromises()
    expect(optionKeys(wrapper)).toEqual([
      '--ctx-size',
      '--batch-size',
      '--flash-attn',
      '--temperature',
      '--threads'
    ])

    wrapper.unmount()
  })
})
