import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import DiscoverPage from '~/components/ModelsDiscover.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

beforeEach(() => {
  vi.useFakeTimers()
  mocks.request.mockReset()
  mocks.request.mockImplementation(async (path: string) => {
    if (path.startsWith('/api/v1/huggingface/search?')) {
      return [{ id: 'acme/demo', downloads: 1, likes: 2, private: false, gated: false, tags: ['gguf'] }]
    }
    return []
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
  manager.profile.value = null
})

afterEach(() => {
  vi.useRealTimers()
})

describe('Discover automatic search', () => {
  it('loads trending results, debounces input, accepts Hugging Face URLs and sorts immediately', async () => {
    const wrapper = await mountSuspended(DiscoverPage, { route: false })

    expect(mocks.request).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(350)
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledTimes(1)
    expect(mocks.request.mock.calls[0][0]).toContain('sort=trending_score')
    expect(mocks.request.mock.calls[0][0]).toContain('q=')
    expect(wrapper.text()).toContain('acme/demo')

    mocks.request.mockClear()
    const search = wrapper.find('input[placeholder="Qwen, Llama, Gemma… or Hugging Face URL"]')
    await search.setValue('Qwen')
    await vi.advanceTimersByTimeAsync(349)
    expect(mocks.request).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1)
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledTimes(1)
    expect(mocks.request.mock.calls[0][0]).toContain('q=Qwen')

    mocks.request.mockClear()
    await search.setValue('https://huggingface.co/unsloth/Qwen3-GGUF/tree/main')
    await flushPromises()
    expect((search.element as HTMLInputElement).value).toBe('unsloth/Qwen3-GGUF')
    await vi.advanceTimersByTimeAsync(350)
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledTimes(1)
    expect(mocks.request.mock.calls[0][0]).toContain('q=unsloth%2FQwen3-GGUF')

    mocks.request.mockClear()
    const select = wrapper.findAllComponents({ name: 'USelect' })[0] || wrapper.findAllComponents({ name: 'Select' })[0]
    expect(select).toBeTruthy()
    select.vm.$emit('update:modelValue', 'likes')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledTimes(1)
    expect(mocks.request.mock.calls[0][0]).toContain('sort=likes')

    wrapper.unmount()
  })
})
