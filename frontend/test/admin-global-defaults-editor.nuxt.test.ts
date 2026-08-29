import { describe, expect, it } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountSuspended } from '@nuxt/test-utils/runtime'
import AdminGlobalDefaultsEditor from '~/components/AdminGlobalDefaultsEditor.vue'

function rowInputs(row: any) {
  const inputs = row.findAllComponents({ name: 'Input' })
  return inputs.length ? inputs : row.findAllComponents({ name: 'UInput' })
}

describe('Administration global defaults editor', () => {
  it('normalizes flags, preserves values and uses profile descriptions', async () => {
    const wrapper = await mountSuspended(AdminGlobalDefaultsEditor, {
      props: {
        modelValue: { threads: '8' },
        profile: {
          path: '/llama-server',
          version: 'b1',
          fingerprint: 'fp',
          options: [{ key: 'ctx-size', description: 'Context size from the discovered binary.' }]
        }
      }
    })
    await flushPromises()

    const row = wrapper.get('[data-testid="admin-global-default-row"]')
    const inputs = rowInputs(row)
    inputs[0]!.vm.$emit('update:modelValue', '--ctx-size')
    inputs[1]!.vm.$emit('update:modelValue', '8192')
    await flushPromises()

    expect(wrapper.emitted('update:modelValue')?.at(-1)?.[0]).toEqual({ 'ctx-size': '8192' })
    expect(wrapper.text()).toContain('Context size from the discovered binary.')
  })

  it('covers value-hint and custom notes plus blank-row and remove behavior', async () => {
    const wrapper = await mountSuspended(AdminGlobalDefaultsEditor, {
      props: {
        modelValue: { batch: '128', custom: 'yes' },
        profile: {
          path: '/llama-server',
          fingerprint: 'fp',
          options: [{ key: 'batch', value_hint: 'N' }]
        }
      }
    })
    await flushPromises()

    expect(wrapper.text()).toContain('N')
    expect(wrapper.text()).toContain('Custom llama.cpp option')

    await wrapper.get('[data-testid="add-global-option"]').trigger('click')
    await flushPromises()
    const rows = wrapper.findAll('[data-testid="admin-global-default-row"]')
    const blankInputs = rowInputs(rows.at(-1)!)
    blankInputs[1]!.vm.$emit('update:modelValue', 'ignored without a flag')
    await flushPromises()
    expect(wrapper.emitted('update:modelValue')?.at(-1)?.[0]).toEqual({ batch: '128', custom: 'yes' })

    await rows[0]!.find('button').trigger('click')
    await flushPromises()
    expect(wrapper.emitted('update:modelValue')?.at(-1)?.[0]).toEqual({ custom: 'yes' })
  })

  it('renders the empty state and resyncs from external model changes', async () => {
    const wrapper = await mountSuspended(AdminGlobalDefaultsEditor, { props: { modelValue: {}, profile: null } })
    await flushPromises()
    expect(wrapper.text()).toContain('No global defaults.')

    await wrapper.setProps({ modelValue: { '--flash-attn': 'on', zeta: '2' } })
    await flushPromises()
    const rows = wrapper.findAll('[data-testid="admin-global-default-row"]')
    expect(rows).toHaveLength(2)
    expect(rows[0]!.text()).toContain('Custom llama.cpp option')

    const firstInputs = rowInputs(rows[0]!)
    firstInputs[0]!.vm.$emit('update:modelValue', '')
    await flushPromises()
    expect(wrapper.emitted('update:modelValue')?.at(-1)?.[0]).toEqual({ zeta: '2' })
  })

  it('treats a missing defaults model as an empty map during initial sync', async () => {
    const wrapper = await mountSuspended(AdminGlobalDefaultsEditor, {
      props: { modelValue: undefined as unknown as Record<string, string>, profile: null }
    })
    await flushPromises()
    expect(wrapper.text()).toContain('No global defaults.')
    expect(wrapper.findAll('[data-testid="admin-global-default-row"]')).toHaveLength(0)
    wrapper.unmount()
  })
})
