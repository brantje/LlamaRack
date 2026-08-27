import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import AdminHuggingFacePage from '~/pages/admin/huggingface.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

function resetManager() {
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.models.value = []
  manager.instances.value = []
  manager.runtimes.value = {}
  return manager
}

function button(wrapper: any, text: string) {
  const found = wrapper.findAll('button').find((item: any) => item.text().trim() === text)
  if (!found) throw new Error(`Missing button ${text}`)
  return found
}

beforeEach(() => {
  mocks.request.mockReset()
  resetManager()
})

describe('Phase 8 Hugging Face administration', () => {
  it('loads, saves, replaces and removes the encrypted provider token without returning plaintext', async () => {
    let configured = false
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/huggingface/token' && options?.method === 'PUT') {
        configured = true
        return { configured: true, prefix: 'hf_abc' }
      }
      if (path === '/api/v1/huggingface/token' && options?.method === 'DELETE') {
        configured = false
        return undefined
      }
      if (path === '/api/v1/huggingface/token') return configured ? { configured: true, prefix: 'hf_abc' } : { configured: false }
      return []
    })

    const wrapper = await mountSuspended(AdminHuggingFacePage, { route: false })
    await flushPromises()
    expect(wrapper.text()).toContain('Not configured')

    const token = wrapper.find('input[placeholder="hf_…"]')
    await token.setValue('hf_secret')
    await button(wrapper, 'Save token').trigger('click')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/huggingface/token', { method: 'PUT', body: { token: 'hf_secret' } })
    expect(wrapper.text()).toContain('Hugging Face token saved')
    expect(wrapper.text()).toContain('Configured')
    expect(wrapper.text()).not.toContain('hf_secret')

    await wrapper.find('input[placeholder="hf_…"]').setValue('hf_replacement')
    await button(wrapper, 'Replace').trigger('click')
    await flushPromises()
    expect(wrapper.text()).not.toContain('hf_replacement')

    await button(wrapper, 'Remove').trigger('click')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/huggingface/token', { method: 'DELETE' })
    expect(wrapper.text()).toContain('Not configured')
  })

  it('surfaces load, save and remove error variants', async () => {
    let mode: 'load-data' | 'load-message' | 'load-fallback' | 'save-data' | 'save-message' | 'save-fallback' | 'remove-data' | 'remove-message' | 'remove-fallback' | 'ok' = 'load-data'
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path !== '/api/v1/huggingface/token') return []
      if (options?.method === 'PUT') {
        if (mode === 'save-data') throw { data: { error: 'token save denied' } }
        if (mode === 'save-message') throw new Error('token save exploded')
        if (mode === 'save-fallback') throw {}
        return { configured: true, prefix: 'hf_abc' }
      }
      if (options?.method === 'DELETE') {
        if (mode === 'remove-data') throw { data: { error: 'token remove denied' } }
        if (mode === 'remove-message') throw new Error('token remove exploded')
        if (mode === 'remove-fallback') throw {}
        return undefined
      }
      if (mode === 'load-data') throw { data: { error: 'token load denied' } }
      if (mode === 'load-message') throw new Error('token load exploded')
      if (mode === 'load-fallback') throw {}
      return { configured: true, prefix: 'hf_abc' }
    })

    const wrapper = await mountSuspended(AdminHuggingFacePage, { route: false })
    await flushPromises()
    expect(wrapper.text()).toContain('token load denied')

    mode = 'ok'
    wrapper.vm.$forceUpdate()
    await flushPromises()
    await wrapper.unmount()

    for (const [next, expected] of [
      ['save-data', 'token save denied'],
      ['save-message', 'token save exploded'],
      ['save-fallback', 'Unable to save Hugging Face token']
    ] as const) {
      mode = 'ok'
      const candidate = await mountSuspended(AdminHuggingFacePage, { route: false })
      await flushPromises()
      mode = next
      await candidate.find('input[placeholder="hf_…"]').setValue('hf_test')
      await button(candidate, 'Replace').trigger('click')
      await flushPromises()
      expect(candidate.text()).toContain(expected)
      candidate.unmount()
    }

    for (const [next, expected] of [
      ['remove-data', 'token remove denied'],
      ['remove-message', 'token remove exploded'],
      ['remove-fallback', 'Unable to remove Hugging Face token']
    ] as const) {
      mode = 'ok'
      const candidate = await mountSuspended(AdminHuggingFacePage, { route: false })
      await flushPromises()
      mode = next
      await button(candidate, 'Remove').trigger('click')
      await flushPromises()
      expect(candidate.text()).toContain(expected)
      candidate.unmount()
    }
  })
})
