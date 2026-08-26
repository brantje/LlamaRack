import { describe, expect, it } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountSuspended } from '@nuxt/test-utils/runtime'
import AppConfirmationModal from '~/components/AppConfirmationModal.vue'

function button(kind: 'confirm' | 'cancel') {
  const buttons = [...document.body.querySelectorAll<HTMLButtonElement>(`[data-testid="confirmation-${kind}"]`)]
  const match = buttons.at(-1)
  if (!match) throw new Error(`Missing confirmation ${kind} button`)
  return match
}

describe('AppConfirmationModal', () => {
  it('uses default labels and color when optional values are omitted', async () => {
    const wrapper = await mountSuspended(AppConfirmationModal, { route: false })
    const result = (wrapper.vm as any).request({ title: 'Confirm action', description: 'Proceed with this action?' })
    await flushPromises()

    expect(document.body.textContent).toContain('Confirm action')
    expect(document.body.textContent).toContain('Proceed with this action?')
    expect(button('confirm').textContent).toContain('Confirm')
    expect(button('cancel').textContent).toContain('Cancel')

    button('confirm').click()
    await expect(result).resolves.toBe(true)
  })

  it('cancels an outstanding request when a new confirmation replaces it', async () => {
    const wrapper = await mountSuspended(AppConfirmationModal, { route: false })
    const first = (wrapper.vm as any).request({
      title: 'First action', description: 'First description', confirmLabel: 'First confirm', cancelLabel: 'First cancel', color: 'warning'
    })
    await flushPromises()
    const second = (wrapper.vm as any).request({
      title: 'Second action', description: 'Second description', confirmLabel: 'Second confirm', cancelLabel: 'Second cancel', color: 'error'
    })
    await flushPromises()

    await expect(first).resolves.toBe(false)
    expect(document.body.textContent).toContain('Second action')
    expect(button('confirm').textContent).toContain('Second confirm')

    button('cancel').click()
    await expect(second).resolves.toBe(false)
  })
})
