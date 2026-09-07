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

function components(wrapper: any, names: string[]) {
  const out: any[] = []
  const seen = new Set<Element>()
  for (const name of names) {
    for (const component of wrapper.findAllComponents({ name })) {
      if (component.element && !seen.has(component.element)) {
        seen.add(component.element)
        out.push(component)
      }
    }
  }
  return out
}

function inputNumber(wrapper: any) {
  const found = components(wrapper, ['InputNumber', 'UInputNumber'])[0]
  if (!found) throw new Error('Missing download limit input')
  return found
}

function unitSelect(wrapper: any) {
  const found = components(wrapper, ['Select', 'USelect']).find((item: any) => item.attributes('data-testid') === 'hf-max-download-unit' || item.props('aria-label') === 'Download size unit')
    || components(wrapper, ['Select', 'USelect'])[0]
  if (!found) throw new Error('Missing download limit unit select')
  return found
}

beforeEach(() => {
  mocks.request.mockReset()
  resetManager()
})

describe('Hugging Face administration', () => {
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
      if (path === '/api/v1/huggingface/settings') {
        return { max_download_bytes: { value: 1099511627776, source: 'default', editable: true } }
      }
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
      if (path !== '/api/v1/huggingface/token') {
        if (path === '/api/v1/huggingface/settings') {
          return { max_download_bytes: { value: 1099511627776, source: 'default', editable: true } }
        }
        return []
      }
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

  it('loads and saves the Hugging Face max download size in human units as bytes', async () => {
    let stored = 1099511627776
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/huggingface/token') return { configured: false }
      if (path === '/api/v1/huggingface/settings' && options?.method === 'PUT') {
        stored = options.body.max_download_bytes
        return { max_download_bytes: { value: stored, source: 'database', editable: true } }
      }
      if (path === '/api/v1/huggingface/settings') {
        return { max_download_bytes: { value: stored, source: stored === 1099511627776 ? 'default' : 'database', editable: true } }
      }
      return []
    })

    const wrapper = await mountSuspended(AdminHuggingFacePage, { route: false })
    await flushPromises()
    expect(wrapper.text()).toContain('Download limits')
    expect(wrapper.get('[data-testid="hf-max-download-bytes"]').text()).toContain('1,099,511,627,776 bytes')
    expect(wrapper.get('[data-testid="hf-max-download-save"]').attributes('disabled')).toBeDefined()

    inputNumber(wrapper).vm.$emit('update:modelValue', 2)
    await flushPromises()
    expect(wrapper.get('[data-testid="hf-max-download-bytes"]').text()).toContain('2,199,023,255,552 bytes')
    await button(wrapper, 'Save download limit').trigger('click')
    await flushPromises()
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/huggingface/settings', { method: 'PUT', body: { max_download_bytes: 2199023255552 } })
    expect(wrapper.text()).toContain('Download limit saved')
  })

  it('surfaces download-limit load and save errors and disables env-locked values', async () => {
    let mode: 'load' | 'save' | 'locked' = 'load'
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/huggingface/token') return { configured: false }
      if (path === '/api/v1/huggingface/settings' && options?.method === 'PUT') {
        if (mode === 'save') throw { data: { error: 'limit save denied' } }
        return { max_download_bytes: { value: options.body.max_download_bytes, source: 'database', editable: true } }
      }
      if (path === '/api/v1/huggingface/settings') {
        if (mode === 'load') throw { data: { error: 'limit load denied' } }
        if (mode === 'locked') return { max_download_bytes: { value: 1099511627776, source: 'environment', editable: false } }
        return { max_download_bytes: { value: 1099511627776, source: 'default', editable: true } }
      }
      return []
    })

    const loader = await mountSuspended(AdminHuggingFacePage, { route: false })
    await flushPromises()
    expect(loader.text()).toContain('limit load denied')
    expect(loader.find('[data-testid="hf-max-download-unavailable"]').exists()).toBe(true)
    expect(loader.find('[data-testid="hf-max-download-bytes"]').exists()).toBe(false)
    expect(loader.text()).not.toContain('Persisted as')
    loader.unmount()

    mode = 'save'
    const saver = await mountSuspended(AdminHuggingFacePage, { route: false })
    await flushPromises()
    inputNumber(saver).vm.$emit('update:modelValue', 3)
    await flushPromises()
    await button(saver, 'Save download limit').trigger('click')
    await flushPromises()
    expect(saver.text()).toContain('limit save denied')
    saver.unmount()

    mode = 'locked'
    const locked = await mountSuspended(AdminHuggingFacePage, { route: false })
    await flushPromises()
    expect(inputNumber(locked).props('disabled')).toBe(true)
    expect(unitSelect(locked).props('disabled')).toBe(true)
    expect(locked.get('[data-testid="hf-max-download-save"]').attributes('disabled')).toBeDefined()
    locked.unmount()
  })

  it('splits loaded byte ceilings into GiB and MiB and ignores unchanged or invalid saves', async () => {
    let stored = 2147483648
    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/huggingface/token') return { configured: false }
      if (path === '/api/v1/huggingface/settings' && options?.method === 'PUT') {
        throw new Error('limit save exploded')
      }
      if (path === '/api/v1/huggingface/settings') {
        return { max_download_bytes: { value: stored, source: 'database', editable: true } }
      }
      return []
    })

    const gib = await mountSuspended(AdminHuggingFacePage, { route: false })
    await flushPromises()
    expect(gib.get('[data-testid="hf-max-download-bytes"]').text()).toContain('2,147,483,648 bytes')
    await button(gib, 'Save download limit').trigger('click')
    await flushPromises()
    expect(mocks.request).not.toHaveBeenCalledWith('/api/v1/huggingface/settings', expect.objectContaining({ method: 'PUT' }))
    inputNumber(gib).vm.$emit('update:modelValue', 0)
    await flushPromises()
    expect(gib.get('[data-testid="hf-max-download-bytes"]').text()).toContain('0 B')
    await button(gib, 'Save download limit').trigger('click')
    await flushPromises()
    expect(mocks.request).not.toHaveBeenCalledWith('/api/v1/huggingface/settings', expect.objectContaining({ method: 'PUT' }))
    inputNumber(gib).vm.$emit('update:modelValue', 4)
    await flushPromises()
    await button(gib, 'Save download limit').trigger('click')
    await flushPromises()
    expect(gib.text()).toContain('limit save exploded')
    gib.unmount()

    stored = 536870912
    const mib = await mountSuspended(AdminHuggingFacePage, { route: false })
    await flushPromises()
    expect(mib.get('[data-testid="hf-max-download-bytes"]').text()).toContain('536,870,912 bytes')
    unitSelect(mib).vm.$emit('update:modelValue', 'GiB')
    await flushPromises()
    expect(mib.get('[data-testid="hf-max-download-bytes"]').text()).toContain('549,755,813,888 bytes')
    mib.unmount()

    mocks.request.mockImplementation(async (path: string, options?: any) => {
      if (path === '/api/v1/huggingface/token') return { configured: false }
      if (path === '/api/v1/huggingface/settings' && options?.method === 'PUT') throw {}
      if (path === '/api/v1/huggingface/settings') {
        return { max_download_bytes: { value: 0, source: 1, editable: true } }
      }
      return []
    })
    const fallback = await mountSuspended(AdminHuggingFacePage, { route: false })
    await flushPromises()
    expect(fallback.get('[data-testid="hf-max-download-bytes"]').text()).toContain('1,099,511,627,776 bytes')
    inputNumber(fallback).vm.$emit('update:modelValue', 2)
    await flushPromises()
    await button(fallback, 'Save download limit').trigger('click')
    await flushPromises()
    expect(fallback.text()).toContain('Unable to save download limit')
    fallback.unmount()
  })
})
