import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountSuspended } from '@nuxt/test-utils/runtime'
import AppCopyButton from '~/components/AppCopyButton.vue'

afterEach(() => {
  vi.useRealTimers()
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: undefined })
  Object.defineProperty(document, 'execCommand', { configurable: true, value: undefined })
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

  it('uses the legacy selection fallback when Clipboard is unavailable', async () => {
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: undefined })
    const execCommand = vi.fn().mockReturnValue(true)
    Object.defineProperty(document, 'execCommand', { configurable: true, value: execCommand })

    const wrapper = await mountSuspended(AppCopyButton, {
      props: { text: 'legacy-value', label: 'Copy value' }
    })
    const button = wrapper.get('button')
    expect(button.text()).toContain('Copy value')
    expect(button.attributes('aria-label')).toBe('Copy value')

    await button.trigger('click')
    await flushPromises()
    expect(execCommand).toHaveBeenCalledWith('copy')
    expect(wrapper.emitted('copied')?.[0]).toEqual(['legacy-value'])
    expect(button.attributes('title')).toBe('Copied')
    wrapper.unmount()
  })

  it('reports Clipboard rejection details when the legacy fallback also fails', async () => {
    const writeText = vi.fn().mockRejectedValue(new Error('Clipboard blocked'))
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    Object.defineProperty(document, 'execCommand', { configurable: true, value: vi.fn().mockReturnValue(false) })

    const wrapper = await mountSuspended(AppCopyButton, {
      props: { text: 'secret', errorMessage: 'Copy manually.' }
    })
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(wrapper.emitted('error')?.[0]).toEqual(['Clipboard blocked. Copy manually.'])
    expect(wrapper.emitted('copied')).toBeUndefined()
    wrapper.unmount()
  })

  it('falls back safely when clipboard capability lookup and execCommand throw', async () => {
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      get() { throw new Error('privacy mode') }
    })
    Object.defineProperty(document, 'execCommand', {
      configurable: true,
      value: vi.fn(() => { throw new Error('legacy blocked') })
    })

    const wrapper = await mountSuspended(AppCopyButton, { props: { text: 'secret' } })
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(wrapper.emitted('error')?.[0]).toEqual(['Unable to copy. Select the value and copy it manually.'])
    wrapper.unmount()
  })

  it('does nothing for empty text and resets copied state when text changes', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })

    const empty = await mountSuspended(AppCopyButton, { props: { text: '', iconOnly: true } })
    await empty.get('button').trigger('click')
    await flushPromises()
    expect(writeText).not.toHaveBeenCalled()
    expect(empty.emitted('copied')).toBeUndefined()
    empty.unmount()

    const wrapper = await mountSuspended(AppCopyButton, { props: { text: 'first', iconOnly: true } })
    const button = wrapper.get('button')
    await button.trigger('click')
    await flushPromises()
    expect(button.attributes('aria-label')).toBe('Copied first')

    await wrapper.setProps({ text: 'second' })
    await flushPromises()
    expect(button.attributes('aria-label')).toBe('Copy second')
    wrapper.unmount()
  })
})
