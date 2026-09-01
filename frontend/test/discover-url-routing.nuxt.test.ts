import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import { useState } from '#imports'
import DiscoverIndexPage from '~/pages/models/discover/index.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn(), navigateTo: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))
mockNuxtImport('navigateTo', () => mocks.navigateTo)

beforeEach(() => {
  mocks.request.mockReset()
  mocks.request.mockResolvedValue({ items: [], next_cursor: '' })
  mocks.navigateTo.mockReset()

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

  useState<string>('models-discover-query').value = ''
  useState<string>('models-discover-author').value = ''
  useState<string>('models-discover-sort').value = 'trending_score'
  useState<any[]>('models-discover-results').value = []
  useState<string>('models-discover-next-cursor').value = ''
  useState<boolean>('models-discover-has-searched').value = false
  useState<number>('models-discover-scroll-position').value = 0
})

describe('Discover pasted repository URL routing', () => {
  it('normalizes a Hugging Face model URL and navigates directly to repository detail', async () => {
    const wrapper = await mountSuspended(DiscoverIndexPage, { route: '/models/discover' })
    const search = wrapper.find('input[placeholder="Qwen, Llama, Gemma… or Hugging Face URL"]')

    await search.setValue('https://huggingface.co/unsloth/Qwen3-GGUF/tree/main')
    await flushPromises()

    expect(useState<string>('models-discover-query').value).toBe('unsloth/Qwen3-GGUF')
    expect(mocks.navigateTo).toHaveBeenCalledWith('/models/discover/unsloth/Qwen3-GGUF')
    wrapper.unmount()
  })

  it('does not treat Hugging Face dataset or space URLs as model detail routes', async () => {
    const wrapper = await mountSuspended(DiscoverIndexPage, { route: '/models/discover' })
    const search = wrapper.find('input[placeholder="Qwen, Llama, Gemma… or Hugging Face URL"]')

    await search.setValue('https://huggingface.co/datasets/acme/demo')
    await flushPromises()
    expect(mocks.navigateTo).not.toHaveBeenCalled()

    await search.setValue('https://huggingface.co/spaces/acme/demo')
    await flushPromises()
    expect(mocks.navigateTo).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
