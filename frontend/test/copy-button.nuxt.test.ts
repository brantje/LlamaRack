import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountSuspended } from '@nuxt/test-utils/runtime'
import AppCopyButton from '~/components/AppCopyButton.vue'

afterEach(() => {
  vi.useRealTimers()
})

describe('AppCopyButton', () => {
  it('returns to the copy state after five seconds and resets the timer on another copy', async () => {
    vi.useFakeTimers()
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })

    const wrapper = await mountSuspended(AppCopyButton, {
      props: { text: 'gemma-4', iconOnly: true }
    })
    const button = wrapper.get('button')

    expect(button.attributes('aria-label')).toBe('Copy gemma-4')
    await button.trigger('click')
    await flushPromises()
    expect(button.attributes('aria-label')).toBe('Copied gemma-4')

    await vi.advanceTimersByTimeAsync(4999)
    expect(button.attributes('aria-label')).toBe('Copied gemma-4')

    await button.trigger('click')
    await flushPromises()
    await vi.advanceTimersByTimeAsync(4999)
    expect(button.attributes('aria-label')).toBe('Copied gemma-4')

    await vi.advanceTimersByTimeAsync(1)
    await wrapper.vm.$nextTick()
    expect(button.attributes('aria-label')).toBe('Copy gemma-4')
    expect(writeText).toHaveBeenCalledTimes(2)

    wrapper.unmount()
  })
})
