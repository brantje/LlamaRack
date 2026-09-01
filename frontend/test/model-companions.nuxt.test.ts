import { describe, expect, it } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountSuspended } from '@nuxt/test-utils/runtime'
import ModelCompanionFiles from '~/components/ModelCompanionFiles.vue'
import { isNativeMTP } from '~/utils/modelCompanions'

describe('model companion helpers', () => {
  it('treats inspect features as built-in MTP unless a draft sidecar exists', () => {
    expect(isNativeMTP({}, { features: { has_mtp: true, mtp_only: false } })).toBe(true)
    expect(isNativeMTP({}, { features: { has_mtp: true, mtp_only: true } })).toBe(false)
    expect(isNativeMTP({}, { features: { has_mtp: false } })).toBe(false)
    expect(isNativeMTP({}, { features: { has_mtp: true, mtp_only: false }, suggested_options: { 'spec-draft-model': '/models/draft.gguf' } })).toBe(false)
    expect(isNativeMTP({ 'spec-draft-model': '/models/draft.gguf' }, { features: { has_mtp: true, mtp_only: false } })).toBe(false)
    expect(isNativeMTP({}, { dependencies: [{ kind: 'mtp', name: 'draft.gguf', total_bytes: 1, files: [] }], features: { has_mtp: true, mtp_only: false } })).toBe(false)
  })

  it('falls back to suggested or saved spec-type when inspect omits features', () => {
    expect(isNativeMTP({}, { suggested_options: { 'spec-type': 'draft-mtp' } })).toBe(true)
    expect(isNativeMTP({ 'spec-type': 'draft-mtp' }, null)).toBe(true)
    expect(isNativeMTP({}, null, { 'spec-type': 'draft-mtp' })).toBe(true)
    expect(isNativeMTP({}, { suggested_options: { 'spec-type': 'draft-mtp', 'spec-draft-model': '/models/draft.gguf' } })).toBe(false)
    expect(isNativeMTP({}, null)).toBe(false)
  })

  it('keeps Enable visible after disabling built-in MTP when inspection is null', async () => {
    const nativeOptions = {
      'spec-type': 'draft-mtp',
      'spec-draft-n-max': '16',
      'spec-draft-p-min': '0.8'
    }
    const wrapper = await mountSuspended(ModelCompanionFiles, {
      route: false,
      props: {
        modelValue: { ...nativeOptions },
        inspection: null,
        fallbackSuggestedOptions: nativeOptions,
        'onUpdate:modelValue': (value: Record<string, string>) => {
          void wrapper.setProps({ modelValue: value })
        }
      }
    })

    const mtp = wrapper.get('[data-testid="companion-mtp"]')
    expect(mtp.text()).toContain('Built-in MTP')
    await mtp.findAll('button').find(button => button.text() === 'Disable')!.trigger('click')
    await flushPromises()
    expect(mtp.text()).toContain('Ignored')
    expect(mtp.findAll('button').some(button => button.text() === 'Enable')).toBe(true)
    expect(wrapper.props('modelValue')).toMatchObject({
      'spec-type': '',
      'spec-draft-n-max': '',
      'spec-draft-p-min': ''
    })

    await mtp.findAll('button').find(button => button.text() === 'Enable')!.trigger('click')
    await flushPromises()
    expect(mtp.text()).toContain('Built-in')
    expect(wrapper.props('modelValue')).toMatchObject(nativeOptions)
    wrapper.unmount()
  })
})
