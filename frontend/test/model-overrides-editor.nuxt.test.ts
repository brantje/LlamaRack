import { describe, expect, it } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountSuspended } from '@nuxt/test-utils/runtime'
import ModelOverridesEditor from '~/components/ModelOverridesEditor.vue'

describe('Model overrides editor', () => {
  it('keeps excluded companion options while editing flat flag/value rows', async () => {
    const wrapper = await mountSuspended(ModelOverridesEditor, {
      props: {
        modelValue: { mmproj: '/models/mmproj.gguf', threads: '8' },
        excludeKeys: ['mmproj']
      }
    })
    await flushPromises()

    expect(wrapper.findAll('[data-testid="model-override-row"]')).toHaveLength(1)
    expect(wrapper.text()).not.toContain('/models/mmproj.gguf')

    const row = wrapper.get('[data-testid="model-override-row"]')
    const inputs = row.findAllComponents({ name: 'Input' }).length
      ? row.findAllComponents({ name: 'Input' })
      : row.findAllComponents({ name: 'UInput' })
    inputs[0]!.vm.$emit('update:modelValue', '--ctx-size')
    inputs[1]!.vm.$emit('update:modelValue', '8192')
    await flushPromises()

    expect(wrapper.emitted('update:modelValue')?.at(-1)?.[0]).toEqual({
      mmproj: '/models/mmproj.gguf',
      'ctx-size': '8192'
    })

    await wrapper.get('[data-testid="model-override-row"] button').trigger('click')
    await flushPromises()
    expect(wrapper.emitted('update:modelValue')?.at(-1)?.[0]).toEqual({ mmproj: '/models/mmproj.gguf' })
  })

  it('covers empty/default props, adding a blank row and exclusion resync', async () => {
    const wrapper = await mountSuspended(ModelOverridesEditor, { props: { modelValue: {} } })
    await flushPromises()
    expect(wrapper.text()).toContain('No Model-specific defaults.')

    await wrapper.get('[data-testid="add-model-option"]').trigger('click')
    expect(wrapper.findAll('[data-testid="model-override-row"]')).toHaveLength(1)

    const row = wrapper.get('[data-testid="model-override-row"]')
    const inputs = row.findAllComponents({ name: 'Input' }).length
      ? row.findAllComponents({ name: 'Input' })
      : row.findAllComponents({ name: 'UInput' })
    inputs[1]!.vm.$emit('update:modelValue', 'ignored without a flag')
    await flushPromises()
    expect(wrapper.emitted('update:modelValue')?.at(-1)?.[0]).toEqual({})

    await wrapper.setProps({ modelValue: { threads: '4', batch: '128' }, excludeKeys: ['threads'] })
    await flushPromises()
    expect(wrapper.findAll('[data-testid="model-override-row"]')).toHaveLength(1)
    expect(wrapper.text()).not.toContain('threads')
  })
})
