import { describe, expect, it } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountSuspended } from '@nuxt/test-utils/runtime'
import InstanceOverridesEditor from '~/components/InstanceOverridesEditor.vue'
import { useManager } from '~/composables/useManager'

describe('Instance overrides edge branches', () => {
  it('covers default props, empty state, missing profile notes and blank flags', async () => {
    const manager = useManager()
    manager.profile.value = null

    const wrapper = await mountSuspended(InstanceOverridesEditor, {
      props: { modelValue: {} }
    })
    await flushPromises()

    expect(wrapper.text()).toContain('No Instance-specific overrides.')
    expect(wrapper.find('[data-testid="add-instance-option"]').exists()).toBe(true)

    await wrapper.get('[data-testid="add-instance-option"]').trigger('click')
    const row = wrapper.get('[data-testid="instance-override-row"]')
    expect(row.text()).toContain('Instance override')

    const inputs = row.findAllComponents({ name: 'Input' }).length
      ? row.findAllComponents({ name: 'Input' })
      : row.findAllComponents({ name: 'UInput' })
    expect(inputs).toHaveLength(2)

    inputs[1]!.vm.$emit('update:modelValue', 'ignored-without-a-flag')
    await flushPromises()
    expect(wrapper.emitted('update:modelValue')?.at(-1)?.[0]).toEqual({})
  })

  it('resyncs rows when excluded keys change', async () => {
    const manager = useManager()
    manager.profile.value = {
      path: '/app/llama-server', version: 'test', fingerprint: 'abc',
      options: [{ key: 'threads', value_hint: 'N', kind: 'integer', description: 'CPU threads' }]
    }

    const wrapper = await mountSuspended(InstanceOverridesEditor, {
      props: { modelValue: { threads: '4', batch: '128' } }
    })
    await flushPromises()
    expect(wrapper.findAll('[data-testid="instance-override-row"]')).toHaveLength(2)

    await wrapper.setProps({ excludeKeys: ['threads'] })
    await flushPromises()
    expect(wrapper.findAll('[data-testid="instance-override-row"]')).toHaveLength(1)
    expect(wrapper.text()).not.toContain('CPU threads')
  })
})
